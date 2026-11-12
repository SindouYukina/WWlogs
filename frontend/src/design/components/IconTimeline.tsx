import { memo, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode } from "react"
import type { GameAssetKind } from "../assets"
import { useRevealPhase, type RevealClock } from "../motion/useReveal.ts"
import { overlaps, placeIn, TrackOverlay, viewFraction, widthIn, type TimelineSpan } from "./AnalysisTimeline"
import { GameAssetImage } from "./combat"
import { Icon } from "./Icon"
import type { TimelineSync } from "./timeline-sync.ts"
import { clusterHits, effectBars, segmentAt, type HitCluster } from "@/lib/history-analysis"

// The icon timeline of the History analysis workspace: the encounter read as skill icons where hits landed and effect bars
// with their own icons, on the same axis, gutter and view window as the DPS lines above it. Every hit, interval and
// marker comes from Go; this only places them. Icons that would pile up are grouped for drawing (clusterHits), and every
// group can be picked to list the stored hits it holds, so nothing is dropped to make room. A group is never a cast.

export interface IconImage {
  kind: GameAssetKind
  id: number
}

export interface HitTrackSeries {
  key: string
  /** The image the series is drawn with; null draws the neutral hit mark. */
  icon: IconImage | null
  atMs: readonly number[]
}

export interface HitTrack {
  kind: "hits"
  key: string
  label: ReactNode
  sub?: ReactNode
  /** The track's accent: the contributor's element color. */
  color?: string
  series: readonly HitTrackSeries[]
}

export interface EffectTrack {
  kind: "effect"
  key: string
  label: ReactNode
  sub?: ReactNode
  polarity: "buff" | "debuff" | "unclassified"
  /** The effect's own game image, drawn where an instance starts when there is room; null draws none. */
  icon: IconImage | null
  segments: ReadonlyArray<{ fromMs: number; toMs: number; evidence: "confirmed" | "stated" | "unknown"; stack?: number }>
  markers: ReadonlyArray<{ atMs: number; kind: string }>
}

export type IconTrack = HitTrack | EffectTrack

export interface IconTrackGroup {
  key: string
  label: ReactNode
  note?: ReactNode
  /** Starts collapsed (a long group); the user's own choice wins afterwards. */
  collapsed?: boolean
  tracks: readonly IconTrack[]
}

export type IconTimelinePick =
  | { kind: "effect"; track: string; segment: number | null }
  | { kind: "hits"; track: string; cluster: HitCluster }

export interface IconTimelineProps {
  durationMs: number
  view: TimelineSpan
  groups: readonly IconTrackGroup[]
  gaps: readonly TimelineSpan[]
  range: TimelineSpan | null
  sync?: TimelineSync
  /** The picked track, and the picked hit group of it. */
  selected: { track: string; cluster?: string } | null
  onPick: (pick: IconTimelinePick | null) => void
  reveal?: RevealClock | null
  describeCluster: (track: HitTrack, cluster: HitCluster) => string
  describeSegment: (track: EffectTrack, index: number) => string
  labels: { aria: string; expand: string; collapse: string; empty: string }
  /** Height of the scrolling area; rows beyond it scroll, and only the rows in view are mounted. */
  maxHeight?: number
  /** Its data is still on the way: an empty timeline says nothing yet. */
  loading?: boolean
}

const groupHeight = 28
const hitHeight = 56
const effectHeight = 28
const hitIcon = 22
const laneGap = 2
const effectIcon = 16
const overscanPx = 240

type Row =
  | { kind: "group"; key: string; group: IconTrackGroup; collapsed: boolean; top: number; height: number }
  | { kind: "track"; key: string; track: IconTrack; top: number; height: number }

/** The rows of the expanded groups, each with its offset; collapsed groups keep only their header. */
function layoutRows(groups: readonly IconTrackGroup[], collapsedOf: (group: IconTrackGroup) => boolean): { rows: Row[]; total: number } {
  const rows: Row[] = []
  let top = 0
  for (const group of groups) {
    const collapsed = collapsedOf(group)
    rows.push({ kind: "group", key: `g:${group.key}`, group, collapsed, top, height: groupHeight })
    top += groupHeight
    if (collapsed) continue
    for (const track of group.tracks) {
      const height = track.kind === "hits" ? hitHeight : effectHeight
      rows.push({ kind: "track", key: `t:${track.key}`, track, top, height })
      top += height
    }
  }
  return { rows, total: top }
}

/** The first row that reaches below `y`. */
function firstRowBelow(rows: readonly Row[], y: number): number {
  let lo = 0, hi = rows.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (rows[mid]!.top + rows[mid]!.height <= y) lo = mid + 1
    else hi = mid
  }
  return lo
}

interface TipState {
  text: string
  x: number
  y: number
  flip: boolean
}

type ShowTip = (text: string | null, event?: ReactPointerEvent) => void

/** One contributor's hits in the window shown: grouped icons in two sub-lanes, each group a button that lists its hits. */
const HitRow = memo(function HitRow({ track, view, width, top, height, selected, onPick, describe, showTip }: { track: HitTrack; view: TimelineSpan; width: number; top: number; height: number; selected: string | undefined; onPick: IconTimelineProps["onPick"]; describe: IconTimelineProps["describeCluster"]; showTip: ShowTip }) {
  const clusters = useMemo(() => clusterHits(track.series, view, width, hitIcon + laneGap, 2), [track.series, view.fromMs, view.toMs, width])
  // An icon is centred on its first hit, held inside the track at both ends so it is never cut off.
  const half = hitIcon / 2
  return <div className="ww-itl-row" data-kind="hits" data-track={track.key} data-clusters={clusters.length} data-hits={clusters.reduce((count, cluster) => count + cluster.count, 0)}
    style={{ top, height, "--ww-series": track.color } as CSSProperties}>
    {clusters.map(cluster => {
      const lead = track.series[cluster.members[0]!.series]!
      const text = describe(track, cluster)
      const laneTop = 6 + cluster.lane * (hitIcon + 4)
      const center = Math.min(Math.max(cluster.x * width, half), Math.max(half, width - half))
      return <span key={cluster.key} className="ww-itl-hit-wrap" style={{ left: center, top: laneTop }}>
        {cluster.x2 > cluster.x && <i className="ww-itl-hit-tail" aria-hidden="true" style={{ width: Math.max(0, cluster.x2 * width - center) }} />}
        <button type="button" className="ww-itl-hit" data-cluster={cluster.key} data-count={cluster.count} data-mixed={cluster.members.length > 1} data-selected={selected === cluster.key}
          data-from={Math.round(cluster.fromMs)} data-to={Math.round(cluster.toMs)} aria-label={text} aria-pressed={selected === cluster.key}
          onClick={() => onPick(selected === cluster.key ? null : { kind: "hits", track: track.key, cluster })}
          onPointerEnter={event => showTip(text, event)} onPointerMove={event => showTip(text, event)} onPointerLeave={() => showTip(null)}>
          {lead.icon ? <GameAssetImage kind={lead.icon.kind} id={lead.icon.id} /> : null}
          <i className="ww-itl-hit-mark" aria-hidden="true" />
          {cluster.count > 1 && <b className="ww-itl-count ww-num" aria-hidden="true">{cluster.count}</b>}
        </button>
      </span>
    })}
  </div>
})

/**
 * One effect's intervals in the window shown: touching intervals of one evidence as one bar (effectBars), the stack count
 * once per run, the markers of every start and end, and the effect's own icon where an instance starts when there is room.
 * A pick or a hover finds the exact interval under the pointer.
 */
const EffectRow = memo(function EffectRow({ track, view, width, top, height, selected, onPick, describe, showTip }: { track: EffectTrack; view: TimelineSpan; width: number; top: number; height: number; selected: boolean; onPick: IconTimelineProps["onPick"]; describe: IconTimelineProps["describeSegment"]; showTip: ShowTip }) {
  const bars = useMemo(() => effectBars(track.segments), [track.segments])
  const starts = useMemo(() => {
    if (!track.icon || !(width > 0)) return []
    const at = track.markers.filter(marker => (marker.kind === "applied" || marker.kind === "start_unconfirmed") && overlaps(marker.atMs, marker.atMs, view)).map(marker => viewFraction(marker.atMs, view) * width)
    // An icon needs its own width before the next one starts.
    return at.filter((x, index) => index === at.length - 1 || at[index + 1]! - x >= effectIcon + 2)
  }, [track.icon, track.markers, view.fromMs, view.toMs, width])
  // A stack label needs room inside its run, and stays clear of the label before it.
  const stacks = useMemo(() => {
    if (!(width > 0)) return []
    let lastX = -Infinity
    return bars.flatMap(bar => bar.stacks.map(run => ({ ...run, evidence: bar.evidence }))).filter(run => {
      if (!overlaps(run.fromMs, run.toMs, view)) return false
      const x = Math.min(1, viewFraction(run.toMs, view)) * width
      const room = x - Math.max(0, viewFraction(run.fromMs, view)) * width
      if (room < 12 || x - lastX < 14) return false
      lastX = x
      return true
    })
  }, [bars, view.fromMs, view.toMs, width])
  const segmentUnder = (event: ReactPointerEvent | React.MouseEvent, indices: readonly number[]) => {
    const bounds = (event.currentTarget as HTMLElement).parentElement!.getBoundingClientRect()
    const ms = view.fromMs + Math.max(0, Math.min(1, (event.clientX - bounds.left) / Math.max(1, bounds.width))) * (view.toMs - view.fromMs)
    return segmentAt(track.segments, indices, ms)
  }
  return <div className="ww-itl-row ww-itl-effect" data-kind="effect" data-track={track.key} data-polarity={track.polarity} data-selected={selected} data-bars={bars.length} style={{ top, height }}>
    {bars.map((bar, index) => {
      if (!overlaps(bar.fromMs, bar.toMs, view)) return null
      return <span key={index} className="ww-lane-seg ww-itl-bar" data-evidence={bar.evidence} data-segments={bar.segments.join(" ")} style={{ left: placeIn(bar.fromMs, view), width: widthIn(bar.fromMs, bar.toMs, view) }}
        onClick={event => onPick({ kind: "effect", track: track.key, segment: segmentUnder(event, bar.segments) })}
        onPointerMove={event => showTip(describe(track, segmentUnder(event, bar.segments)), event)} onPointerLeave={() => showTip(null)} />
    })}
    {stacks.map(run => <b key={`s${run.fromMs}`} className="ww-itl-stack ww-num" data-evidence={run.evidence} style={{ left: placeIn(run.toMs, view) }}>{run.stack}</b>)}
    {track.markers.map((marker, index) => overlaps(marker.atMs, marker.atMs, view) && <span key={`m${index}`} className="ww-lane-mark" data-kind={marker.kind} style={{ left: placeIn(marker.atMs, view) }} />)}
    {track.icon && starts.map(x => <span key={x} className="ww-itl-effect-icon" aria-hidden="true" style={{ left: x }}><GameAssetImage kind={track.icon!.kind} id={track.icon!.id} /></span>)}
  </div>
})

/**
 * The icon timeline: collapsible groups of tracks (skill hits per contributor, then effects by holder) under the DPS lines,
 * zoomed and panned with them. Only the rows in view are mounted and only what lies inside the window is drawn. The opening
 * reveal plays with the same clock as the DPS lines, so the tracks and the lines always show the same moment.
 */
const noGroups: readonly IconTrackGroup[] = []

export const IconTimeline = memo(function IconTimeline({ durationMs, view, groups: latestGroups, gaps, range, sync, selected, onPick, reveal = null, describeCluster, describeSegment, labels, maxHeight = 460, loading = false }: IconTimelineProps) {
  // The tracks render in an interruptible background pass: opening an encounter paints its numbers and DPS lines first
  // and never blocks a frame on hundreds of icons, and a new scope or filter keeps the page responsive while its tracks
  // are laid out. The window, the range and a drag are never deferred.
  const groups = useDeferredValue(latestGroups, noGroups)
  const [toggled, setToggled] = useState<Record<string, boolean>>({})
  const [scrollTop, setScrollTop] = useState(0)
  const [width, setWidth] = useState(0)
  const [tip, setTip] = useState<TipState | null>(null)
  const root = useRef<HTMLDivElement>(null)
  const tracks = useRef<HTMLDivElement>(null)
  // The tracks join the opening reveal once they have something to show, where the DPS lines are in it by then.
  const ready = groups.length > 0
  const { phase, end } = useRevealPhase(reveal, ready)
  const revealWindow = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const element = revealWindow.current
    if (!phase || !element) return
    const ended = (event: AnimationEvent) => { if (event.target === element && event.animationName === "ww-reveal-window") end() }
    element.addEventListener("animationend", ended)
    element.addEventListener("animationcancel", ended)
    return () => {
      element.removeEventListener("animationend", ended)
      element.removeEventListener("animationcancel", ended)
    }
  }, [phase, end])
  // Grouping hits needs the track width in pixels: icons are a fixed size, time is not.
  useLayoutEffect(() => {
    const element = tracks.current
    if (!element) return
    setWidth(element.getBoundingClientRect().width)
    const observer = new ResizeObserver(entries => { for (const entry of entries) setWidth(entry.contentRect.width) })
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  const { rows, total } = useMemo(() => layoutRows(groups, group => toggled[group.key] ?? group.collapsed ?? false), [groups, toggled])
  const viewport = Math.min(total, maxHeight)
  const first = Math.max(0, firstRowBelow(rows, scrollTop - overscanPx))
  const last = Math.min(rows.length, firstRowBelow(rows, scrollTop + viewport + overscanPx) + 1)
  const shownRows = rows.slice(first, last)
  const showTip: ShowTip = (text, event) => {
    const bounds = root.current?.getBoundingClientRect()
    if (!text || !event || !bounds) { setTip(null); return }
    const x = event.clientX - bounds.left
    setTip({ text, x, y: event.clientY - bounds.top, flip: x > bounds.width * 0.66 })
  }
  const duration = Math.max(1, durationMs)
  const more = total > viewport && scrollTop + viewport < total - 1
  // The structure is always there, so the track width is measured and the reveal can end before any data arrives.
  return <div ref={root} className="ww-itl" role="list" aria-label={labels.aria} data-field="icon-timeline" data-rows={rows.length} data-more={more} data-duration={Math.round(duration)} data-ready={ready}>
    {latestGroups.length === 0 && !loading && <p className="ww-dash-empty-note" data-field="icon-timeline-empty">{labels.empty}</p>}
    <div className="ww-itl-scroll" style={{ height: viewport }} onScroll={event => setScrollTop(event.currentTarget.scrollTop)}>
      <div className="ww-itl-body" style={{ height: total }}>
        <div className="ww-itl-labels">
          {shownRows.map(row => {
            if (row.kind === "group") return null
            const track = row.track
            const picked = selected?.track === track.key
            return <div key={row.key} className="ww-itl-label" role="listitem" data-kind={track.kind} data-track={track.key} data-selected={picked} style={{ top: row.top, height: row.height }}>
              {track.kind === "effect"
                ? <button type="button" className="ww-itl-label-button" aria-pressed={picked} onClick={() => onPick(picked && !selected?.cluster ? null : { kind: "effect", track: track.key, segment: null })}>
                  <span className="ww-itl-label-main">{track.label}</span>{track.sub && <span className="ww-itl-label-sub">{track.sub}</span>}</button>
                : <span className="ww-itl-label-static"><span className="ww-itl-label-main">{track.label}</span>{track.sub && <span className="ww-itl-label-sub">{track.sub}</span>}</span>}
            </div>
          })}
        </div>
        <div ref={tracks} className="ww-itl-tracks">
          <TrackOverlay className="ww-itl-overlay" view={view} range={range} gaps={gaps} sync={sync} />
          <div ref={revealWindow} className="ww-atl-reveal ww-itl-reveal" key={reveal?.key ?? 0} data-field="icon-reveal" data-enter={phase ? (phase.waiting ? "waiting" : "true") : undefined}
            style={phase && !phase.waiting ? { "--ww-reveal-at": `${-Math.round(phase.elapsedMs)}ms` } as CSSProperties : undefined}>
            <div className="ww-atl-reveal-inner">
              {shownRows.map(row => {
                if (row.kind === "group") return null
                const track = row.track
                return track.kind === "hits"
                  ? <HitRow key={row.key} track={track} view={view} width={width} top={row.top} height={row.height} selected={selected?.track === track.key ? selected.cluster : undefined} onPick={onPick} describe={describeCluster} showTip={showTip} />
                  : <EffectRow key={row.key} track={track} view={view} width={width} top={row.top} height={row.height} selected={selected?.track === track.key} onPick={onPick} describe={describeSegment} showTip={showTip} />
              })}
            </div>
            {phase && !phase.waiting && <i className="ww-atl-reveal-edge" aria-hidden="true" />}
          </div>
        </div>
        {shownRows.map(row => row.kind === "group" && <button key={row.key} type="button" className="ww-itl-group" data-group={row.group.key} aria-expanded={!row.collapsed}
          title={row.collapsed ? labels.expand : labels.collapse} style={{ top: row.top, height: row.height }} onClick={() => setToggled(current => ({ ...current, [row.group.key]: !row.collapsed }))}>
          <Icon name="chevron" size={12} /><span className="ww-itl-group-label">{row.group.label}</span>{row.group.note && <span className="ww-itl-group-note ww-num">{row.group.note}</span>}
        </button>)}
      </div>
    </div>
    {tip && <div className="ww-chart-tip ww-itl-tip" data-field="icon-tip" style={{ left: tip.x, top: tip.y }} data-flip={tip.flip}>{tip.text}</div>}
  </div>
})
