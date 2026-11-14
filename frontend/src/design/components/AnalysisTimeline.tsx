import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { useRevealPhase, type RevealClock } from "../motion/useReveal.ts"
import { draggedRange, plotRelease, pressMovedTo, type PlotPress } from "./timeline-press.ts"
import { useTimelineSync, type TimelineSync } from "./timeline-sync.ts"

// The time axis of the History analysis workspace. One encounter's DPS lines and its effect tracks are drawn against the
// same axis and share one left gutter (--ww-analysis-gutter) so a moment lines up across every track. A view window (zoom
// and pan) shows the same part of the encounter on every track at once; it is presentation only and never changes the
// selected range or a number. Values, lines and intervals are the engine's; this only places them and turns pointer input
// into a range.

export interface TimelinePoint {
  /** Milliseconds from the encounter start. */
  fromMs: number
  toMs: number
  /** The line's value over the interval; null is not drawn. */
  value: number | null
}

export interface TimelineLine {
  key: string
  label: string
  /** CSS color: an element color for a role, a neutral token for the team and unassigned lines. */
  color: string
  points: readonly TimelinePoint[]
  /** The team line: it dims less while another line is focused. */
  aggregate?: boolean
}

export interface TimelineMark {
  atMs: number
  amount: number
}

export interface TimelineSpan {
  fromMs: number
  toMs: number
}

export interface AnalysisTimelineProps {
  durationMs: number
  lines: readonly TimelineLine[]
  /** The line drawn in focus (the selected contributor), or null. */
  focused: string | null
  /** A click near a line selects it, or releases it when it is the selected one. */
  onSelectLine: (key: string | null) => void
  /** The selected range, or null for the whole encounter. */
  range: TimelineSpan | null
  /** A finished drag commits a range; the workspace decides what it means. */
  onRange: (fromMs: number, toMs: number) => void
  /** Back to the whole encounter: Escape, or a click away from every line while a range is selected. */
  onReset: () => void
  gaps: readonly TimelineSpan[]
  marks: readonly TimelineMark[]
  formatValue: (value: number) => string
  formatTime: (ms: number) => string
  labels: { aria: string; empty: string; gap: string; mark: string; legend: string; drag: string }
  height?: number
  minimumRangeMs: number
  /**
   * The opening reveal (useRevealClock): while it plays, the lines are shown from the encounter's start up to a moment
   * that sweeps to its end. The values, the axis and the capture gaps are drawn in full from the first frame.
   */
  reveal?: RevealClock | null
  /** The part of the encounter shown; the whole encounter without one. */
  view?: TimelineSpan
  /** Shares the drag in progress and the moment under the pointer with the card's other tracks. */
  sync?: TimelineSync
}

const plotWidth = 1000
const lineReach = 12

function niceMax(value: number): number {
  if (value <= 0) return 1
  const magnitude = 10 ** Math.floor(Math.log10(value))
  for (const step of [1, 1.5, 2, 3, 4, 5, 7.5, 10]) if (value <= step * magnitude) return step * magnitude
  return 10 * magnitude
}

const centerOf = (point: TimelinePoint) => (point.fromMs + point.toMs) / 2

/** A line's path through the centers of its intervals; a missing value starts a new piece. */
function linePath(points: readonly TimelinePoint[], durationMs: number, max: number, height: number): string {
  let path = ""
  let open = false
  for (const point of points) {
    if (point.value === null) { open = false; continue }
    const x = (centerOf(point) / durationMs) * plotWidth
    const y = height - (point.value / max) * height
    path += `${open ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`
    open = true
  }
  return path
}

const clamp01 = (value: number) => Math.max(0, Math.min(1, value))

/** Where a moment sits in a window, as a fraction of the window (outside it below 0 or above 1). */
export function viewFraction(ms: number, view: TimelineSpan): number {
  const width = view.toMs - view.fromMs
  return width > 0 ? (ms - view.fromMs) / width : 0
}

/** A moment as a CSS left position in a window, held at its edges. */
export const placeIn = (ms: number, view: TimelineSpan) => `${clamp01(viewFraction(ms, view)) * 100}%`

/** The part of [fromMs, toMs] inside a window as a CSS width. */
export const widthIn = (fromMs: number, toMs: number, view: TimelineSpan) => `${Math.max(0, clamp01(viewFraction(toMs, view)) - clamp01(viewFraction(fromMs, view))) * 100}%`

/** Whether [fromMs, toMs] shows in a window at all. */
export const overlaps = (fromMs: number, toMs: number, view: TimelineSpan) => toMs >= view.fromMs && fromMs <= view.toMs

/** Turns a pointer position into milliseconds of the encounter, inside the window shown. */
function msAt(element: HTMLElement, clientX: number, view: TimelineSpan): number {
  const bounds = element.getBoundingClientRect()
  const share = bounds.width > 0 ? clamp01((clientX - bounds.left) / bounds.width) : 0
  return view.fromMs + share * (view.toMs - view.fromMs)
}

/** The SVG viewBox that shows a window of an encounter drawn over plotWidth units. */
const viewBoxOf = (view: TimelineSpan, durationMs: number, height: number) => `${((view.fromMs / durationMs) * plotWidth).toFixed(3)} 0 ${(((view.toMs - view.fromMs) / durationMs) * plotWidth).toFixed(3)} ${height}`

/**
 * The DPS lines of an encounter on its own time axis. Hover reads the values at a moment; a drag selects a range, released
 * once to commit it; a click in place selects the nearest line within reach, and a click away from every line returns to
 * the whole fight and keeps the selected line (timeline-press.ts tells the two apart). Arrow keys move the reading point,
 * Shift+Arrow grows a range from where it started, Enter commits it and Escape returns to the whole fight.
 * The plot is a single SVG layer per line; nothing animates while the values stay the same, and zooming changes only the
 * viewBox of the same paths.
 */
export const AnalysisTimeline = memo(function AnalysisTimeline({ durationMs, lines, focused, onSelectLine, range, onRange, onReset, gaps, marks, formatValue, formatTime, labels, height = 200, minimumRangeMs, reveal = null, view: shownView, sync }: AnalysisTimelineProps) {
  const hasData = lines.some(line => line.points.some(point => point.value !== null && point.value > 0))
  const { phase, end } = useRevealPhase(reveal, hasData)
  const revealWindow = useRef<HTMLDivElement>(null)
  // The reveal ends with its own animation (or its cancellation by reduced motion), never on a timer.
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
  const plot = useRef<HTMLDivElement>(null)
  const pressed = useRef<PlotPress | null>(null)
  const [drag, setDrag] = useState<TimelineSpan | null>(null)
  const [hover, setHover] = useState<number | null>(null)
  const [keyAnchor, setKeyAnchor] = useState<number | null>(null)
  const duration = Math.max(1, durationMs)
  const view = shownView ?? { fromMs: 0, toMs: duration }
  const reference = lines.find(line => line.aggregate) ?? lines[0]
  const points = reference?.points ?? []
  const max = useMemo(() => niceMax(Math.max(0, ...lines.flatMap(line => line.points.map(point => point.value ?? 0)))), [lines])
  const paths = useMemo(() => new Map(lines.map(line => [line.key, linePath(line.points, duration, max, height)])), [lines, duration, max, height])
  const nearestPoint = (ms: number) => {
    let best = -1
    let distance = Infinity
    points.forEach((point, index) => {
      const d = Math.abs(centerOf(point) - ms)
      if (d < distance) { distance = d; best = index }
    })
    return best
  }
  // The line within reach of a click, measured where the lines are drawn.
  const lineAt = (element: HTMLElement, clientX: number, clientY: number): string | null => {
    const bounds = element.getBoundingClientRect()
    const index = nearestPoint(msAt(element, clientX, view))
    let hit: string | null = null
    let distance = lineReach
    if (index >= 0 && hasData) {
      for (const line of lines) {
        const value = line.points[index]?.value
        if (value === null || value === undefined) continue
        const y = bounds.top + (1 - value / max) * bounds.height
        const d = Math.abs(clientY - y)
        // The team line only wins when no contributor line is as close.
        if (d < distance || (d === distance && !line.aggregate)) { distance = d; hit = line.key }
      }
    }
    return hit
  }
  const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || !plot.current) return
    pressed.current = { x: event.clientX, y: event.clientY, ms: msAt(plot.current, event.clientX, view), wandered: false }
    plot.current.setPointerCapture(event.pointerId)
  }
  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!plot.current) return
    const ms = msAt(plot.current, event.clientX, view)
    setHover(nearestPoint(ms))
    const press = pressed.current
    if (!press) return
    pressed.current = pressMovedTo(press, event.clientX, event.clientY)
    setDrag(draggedRange(press, event.clientX, ms))
  }
  const onPointerUp = (event: React.PointerEvent<HTMLDivElement>) => {
    const press = pressed.current
    pressed.current = null
    const element = plot.current
    if (!press || !element) return
    if (element.hasPointerCapture(event.pointerId)) element.releasePointerCapture(event.pointerId)
    setDrag(null)
    const release = plotRelease(press, event.clientX, event.clientY, msAt(element, event.clientX, view), () => lineAt(element, event.clientX, event.clientY), { focused, range, minimumRangeMs })
    if (release?.kind === "range") onRange(release.fromMs, release.toMs)
    else if (release?.kind === "line") onSelectLine(release.key)
    else if (release?.kind === "whole") onReset()
  }
  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (points.length === 0) return
    const current = hover ?? 0
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault()
      const next = Math.max(0, Math.min(points.length - 1, current + (event.key === "ArrowRight" ? 1 : -1)))
      if (event.shiftKey) {
        const anchor = keyAnchor ?? current
        setKeyAnchor(anchor)
        const a = points[Math.min(anchor, next)], b = points[Math.max(anchor, next)]
        if (a && b) setDrag({ fromMs: a.fromMs, toMs: b.toMs })
      } else {
        setKeyAnchor(null)
        setDrag(null)
      }
      setHover(next)
    } else if (event.key === "Enter" && drag) {
      event.preventDefault()
      if (drag.toMs - drag.fromMs >= minimumRangeMs) onRange(drag.fromMs, drag.toMs)
      setDrag(null)
      setKeyAnchor(null)
    } else if (event.key === "Escape") {
      setDrag(null)
      setKeyAnchor(null)
      onReset()
    }
  }
  const shown = drag ?? range
  const hoverPoint = hover !== null ? points[hover] : undefined
  const hoverMs = hoverPoint && hasData ? centerOf(hoverPoint) : null
  const hoverX = hoverMs === null ? -1 : viewFraction(hoverMs, view) * 100
  // The card's other tracks draw the same drag and the same moment, in the same frame.
  useLayoutEffect(() => { sync?.set({ preview: drag }) }, [sync, drag])
  useLayoutEffect(() => { sync?.set({ hoverMs }) }, [sync, hoverMs])
  useEffect(() => () => { sync?.set({ preview: null, hoverMs: null }) }, [sync])
  const box = viewBoxOf(view, duration, height)
  const ticks = [1, 0.5, 0]
  return <div className="ww-atl" data-focusing={focused !== null} data-dragging={drag !== null}>
    <div className="ww-atl-legend" role="group" aria-label={labels.legend}>
      {lines.map(line => <button key={line.key} type="button" className="ww-chart-legend-item" data-key={line.key} data-focused={line.key === focused} aria-pressed={line.key === focused}
        onClick={() => onSelectLine(line.key === focused ? null : line.key)}><i style={{ background: line.color }} />{line.label}</button>)}
    </div>
    <div className="ww-atl-body" style={{ height }}>
      <div className="ww-atl-axis ww-num" aria-hidden="true">{ticks.map(t => <span key={t} style={{ top: `${(1 - t) * 100}%` }}>{formatValue(max * t)}</span>)}</div>
      <div ref={plot} className="ww-atl-plot" role="application" tabIndex={0} aria-label={`${labels.aria}. ${labels.drag}`} data-field="analysis-plot" data-view-from={Math.round(view.fromMs)} data-view-to={Math.round(view.toMs)}
        onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={() => { pressed.current = null; setDrag(null) }}
        onPointerLeave={() => { if (!pressed.current) setHover(null) }} onKeyDown={onKeyDown} onBlur={() => { setKeyAnchor(null) }}>
        {ticks.map(t => <div key={t} className="ww-chart-grid" style={{ top: `${(1 - t) * 100}%` }} />)}
        {gaps.map((gap, index) => overlaps(gap.fromMs, gap.toMs, view) && <div key={index} className="ww-atl-gap" title={labels.gap} data-field="analysis-gap" style={{ left: placeIn(gap.fromMs, view), width: widthIn(gap.fromMs, gap.toMs, view) }} />)}
        {shown && <>
          <div className="ww-atl-dim" style={{ left: 0, width: placeIn(shown.fromMs, view) }} />
          <div className="ww-atl-dim" style={{ left: placeIn(shown.toMs, view), right: 0 }} />
          <div className="ww-atl-range" data-field="analysis-range" data-from={Math.round(shown.fromMs)} data-to={Math.round(shown.toMs)} data-pending={drag !== null} style={{ left: placeIn(shown.fromMs, view), width: widthIn(shown.fromMs, shown.toMs, view) }} />
        </>}
        {hasData
          // The reveal window widens over the time axis while its content moves back by the same amount, so the lines stay
          // where they are and only how much of them shows changes; both are transforms. Keyed by the playing, so a replay
          // starts afresh and the end of one never re-mounts the lines.
          ? <div ref={revealWindow} className="ww-atl-reveal" key={reveal?.key ?? 0} data-field="analysis-reveal" data-enter={phase ? (phase.waiting ? "waiting" : "true") : undefined}
            style={phase && !phase.waiting ? { "--ww-reveal-at": `${-Math.round(phase.elapsedMs)}ms` } as CSSProperties : undefined}>
            <div className="ww-atl-reveal-inner">
              {lines.map(line => <svg key={line.key} className="ww-chart-layer" data-key={line.key} data-focused={line.key === focused} data-aggregate={line.aggregate === true} viewBox={box} preserveAspectRatio="none" aria-hidden="true">
                <path className="ww-atl-line" stroke={line.color} d={paths.get(line.key)} data-focused={line.key === focused} />
              </svg>)}
            </div>
            {phase && <i className="ww-atl-reveal-edge" aria-hidden="true" />}
          </div>
          : <div className="ww-chart-empty">{labels.empty}</div>}
        {marks.length > 0 && <svg className="ww-atl-marks" viewBox={viewBoxOf(view, duration, 10)} preserveAspectRatio="none" aria-label={labels.mark} data-field="analysis-hits" data-count={marks.length}>
          {marks.map((mark, index) => { const x = (mark.atMs / duration) * plotWidth; return <line key={index} x1={x} x2={x} y1={0} y2={10} /> })}
        </svg>}
        {hoverPoint && hasData && hoverX >= 0 && hoverX <= 100 && <>
          <div className="ww-chart-cross" style={{ left: `${hoverX}%` }} />
          <div className="ww-chart-tip" data-flip={hoverX > 66} style={{ left: `${hoverX}%` }} data-field="analysis-tip">
            <div className="ww-chart-tip-title ww-num">{formatTime(hoverPoint.fromMs)} – {formatTime(hoverPoint.toMs)}</div>
            {lines.map(line => {
              const value = line.points[hover ?? -1]?.value
              return <div key={line.key} className="ww-chart-tip-row" data-key={line.key} data-focused={focused === null ? undefined : line.key === focused}><i style={{ background: line.color }} /><span className="ww-truncate">{line.label}</span><b className="ww-num">{value === null || value === undefined ? "—" : formatValue(value)}</b></div>
            })}
          </div>
        </>}
      </div>
    </div>
    <TimeAxis view={view} formatTime={formatTime} />
  </div>
})

/**
 * The whole encounter under a zoomed timeline, with the window shown as a thumb: drag the thumb to pan, press elsewhere to
 * centre the window there, arrow keys move it by a tenth. It only reports windows; the card keeps them inside the encounter.
 */
export const TimelineNavigator = memo(function TimelineNavigator({ durationMs, view, onView, label }: { durationMs: number; view: TimelineSpan; onView: (view: TimelineSpan) => void; label: string }) {
  const bar = useRef<HTMLDivElement>(null)
  const drag = useRef<{ x: number; from: number } | null>(null)
  const duration = Math.max(1, durationMs)
  const width = view.toMs - view.fromMs
  const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    const element = bar.current
    if (!element || event.button !== 0) return
    const bounds = element.getBoundingClientRect()
    const at = (clamp01((event.clientX - bounds.left) / Math.max(1, bounds.width))) * duration
    let from = view.fromMs
    if (at < view.fromMs || at > view.toMs) {
      from = at - width / 2
      onView({ fromMs: from, toMs: from + width })
    }
    drag.current = { x: event.clientX, from }
    element.setPointerCapture(event.pointerId)
  }
  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const start = drag.current, element = bar.current
    if (!start || !element) return
    const from = start.from + ((event.clientX - start.x) / Math.max(1, element.getBoundingClientRect().width)) * duration
    onView({ fromMs: from, toMs: from + width })
  }
  const release = (event: React.PointerEvent<HTMLDivElement>) => {
    drag.current = null
    if (bar.current?.hasPointerCapture(event.pointerId)) bar.current.releasePointerCapture(event.pointerId)
  }
  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return
    event.preventDefault()
    const step = (event.key === "ArrowRight" ? 1 : -1) * width * 0.1
    onView({ fromMs: view.fromMs + step, toMs: view.toMs + step })
  }
  return <div className="ww-atl-nav" data-field="timeline-navigator"><div />
    <div ref={bar} className="ww-atl-nav-bar" role="scrollbar" aria-orientation="horizontal" aria-label={label} title={label} tabIndex={0} aria-valuemin={0} aria-valuemax={Math.round(duration - width)} aria-valuenow={Math.round(view.fromMs)}
      onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={release} onPointerCancel={release} onKeyDown={onKeyDown}>
      <div className="ww-atl-nav-thumb" style={{ left: `${(view.fromMs / duration) * 100}%`, width: `${(width / duration) * 100}%` }} />
    </div>
  </div>
})

/** Round time steps for a window of this width that keep at most maxTicks labels. */
export function tickStep(widthMs: number, maxTicks = 8): number {
  const steps = [100, 200, 250, 500, 1000, 2000, 5000, 10000, 15000, 30000, 60000, 120000, 300000, 600000, 900000, 1800000, 3600000]
  return steps.find(candidate => widthMs / candidate <= maxTicks) ?? 3600000
}

/** Round time labels under a plot that starts after the shared gutter, for the window shown. */
export function TimeAxis({ view, formatTime, maxTicks = 8 }: { view: TimelineSpan; formatTime: (ms: number) => string; maxTicks?: number }) {
  const ticks = useMemo(() => {
    const step = tickStep(view.toMs - view.fromMs, maxTicks)
    const out: number[] = []
    for (let at = Math.ceil(view.fromMs / step) * step; at <= view.toMs + 1; at += step) out.push(at)
    return out
  }, [view.fromMs, view.toMs, maxTicks])
  return <div className="ww-atl-time ww-num" aria-hidden="true"><div />{<div className="ww-atl-time-track">{ticks.map(at => <span key={at} style={{ left: placeIn(at, view) }} data-edge={at <= view.fromMs ? "start" : undefined}>{formatTime(at)}</span>)}</div>}</div>
}

export interface LaneRowModel {
  key: string
  label: ReactNode
  /** A second line under the label, such as the holder. */
  sub?: ReactNode
  polarity: "buff" | "debuff" | "neutral" | "unclassified"
  segments: ReadonlyArray<{ fromMs: number; toMs: number; evidence: "confirmed" | "stated" | "unknown"; stack?: number }>
  markers: ReadonlyArray<{ atMs: number; kind: string }>
}

export interface EffectLanesProps {
  rows: readonly LaneRowModel[]
  durationMs: number
  range: TimelineSpan | null
  gaps: readonly TimelineSpan[]
  selected: string | null
  onSelect: (key: string | null) => void
  rowHeight?: number
  /** At most this many rows are visible at once; more scroll, and only the visible ones are mounted. */
  visibleRows?: number
  labels: { aria: string; markers: Record<string, string>; evidence: Record<string, string> }
  /** The part of the encounter shown; the whole encounter without one. */
  view?: TimelineSpan
  /** The card's drag in progress and pointer moment, drawn over the lanes. */
  sync?: TimelineSync
}

const laneOverscan = 6

/**
 * The selected range (or the one being dragged on the DPS plot) and the moment under the pointer, over a set of tracks.
 * Only this subscribes to the drag, so moving the pointer re-renders these few elements and never the tracks.
 */
export const TrackOverlay = memo(function TrackOverlay({ view, range, gaps, sync, className }: { view: TimelineSpan; range: TimelineSpan | null; gaps: readonly TimelineSpan[]; sync?: TimelineSync; className: string }) {
  const preview = useTimelineSync(sync, "preview")
  const hoverMs = useTimelineSync(sync, "hoverMs")
  const shown = preview ?? range
  const guide = hoverMs === null ? -1 : viewFraction(hoverMs, view)
  return <div className={className} aria-hidden="true">
    {gaps.map((gap, index) => overlaps(gap.fromMs, gap.toMs, view) && <div key={index} className="ww-atl-gap" style={{ left: placeIn(gap.fromMs, view), width: widthIn(gap.fromMs, gap.toMs, view) }} />)}
    {shown && <>
      <div className="ww-atl-dim" data-field="track-dim" style={{ left: 0, width: placeIn(shown.fromMs, view) }} />
      <div className="ww-atl-dim" style={{ left: placeIn(shown.toMs, view), right: 0 }} />
      {preview && <div className="ww-atl-range" data-pending="true" style={{ left: placeIn(preview.fromMs, view), width: widthIn(preview.fromMs, preview.toMs, view) }} />}
    </>}
    {guide >= 0 && guide <= 1 && <div className="ww-track-guide" style={{ left: `${guide * 100}%` }} />}
  </div>
})

/**
 * Effect intervals as horizontal lanes on the encounter's time axis. Each interval states how it is known: confirmed
 * (solid), stated by the server but not confirmed (lighter), unknown (hatched); markers show observed starts and ends,
 * unconfirmed starts and instant effects. Only the rows in view are mounted and only the intervals inside the window shown
 * are drawn, so a long list never becomes thousands of elements.
 */
export const EffectLanes = memo(function EffectLanes({ rows, durationMs, range, gaps, selected, onSelect, rowHeight = 40, visibleRows: visible = 12, labels, view: shownView, sync }: EffectLanesProps) {
  const [scrollTop, setScrollTop] = useState(0)
  const duration = Math.max(1, durationMs)
  const view = shownView ?? { fromMs: 0, toMs: duration }
  const viewport = Math.min(rows.length, visible) * rowHeight
  const first = Math.max(0, Math.floor(scrollTop / rowHeight) - laneOverscan)
  const last = Math.min(rows.length - 1, Math.ceil((scrollTop + viewport) / rowHeight) + laneOverscan)
  const style = { "--ww-lane-height": `${rowHeight}px` } as CSSProperties
  // More rows below the visible ones: the list fades at its bottom edge until it is scrolled to the end.
  const more = rows.length * rowHeight > viewport && scrollTop + viewport < rows.length * rowHeight - 1
  return <div className="ww-lanes" style={style} role="list" aria-label={labels.aria} data-field="effect-lanes" data-rows={rows.length} data-more={more}>
    <div className="ww-lanes-scroll" style={{ height: viewport }} onScroll={event => setScrollTop(event.currentTarget.scrollTop)}>
      <div className="ww-lanes-inner" style={{ height: rows.length * rowHeight }}>
        <TrackOverlay className="ww-lanes-overlay" view={view} range={range} gaps={gaps} sync={sync} />
        {rows.slice(first, last + 1).map((row, offset) => {
          const index = first + offset
          return <div key={row.key} className="ww-lane" role="listitem" data-key={row.key} data-selected={row.key === selected} data-polarity={row.polarity}
            style={{ transform: `translateY(${index * rowHeight}px)` }}>
            <button type="button" className="ww-lane-label" aria-pressed={row.key === selected} onClick={() => onSelect(row.key === selected ? null : row.key)}>
              <span className="ww-lane-name">{row.label}</span>{row.sub && <span className="ww-lane-sub">{row.sub}</span>}
            </button>
            <div className="ww-lane-track" onClick={() => onSelect(row.key === selected ? null : row.key)}>
              {row.segments.map((segment, at) => overlaps(segment.fromMs, segment.toMs, view) && <span key={at} className="ww-lane-seg" data-evidence={segment.evidence} title={labels.evidence[segment.evidence]}
                style={{ left: placeIn(segment.fromMs, view), width: widthIn(segment.fromMs, segment.toMs, view) }}>{segment.stack !== undefined && segment.stack > 1 ? <b className="ww-num">{segment.stack}</b> : null}</span>)}
              {row.markers.map((marker, at) => overlaps(marker.atMs, marker.atMs, view) && <span key={`m${at}`} className="ww-lane-mark" data-kind={marker.kind} title={labels.markers[marker.kind] ?? marker.kind} style={{ left: placeIn(marker.atMs, view) }} />)}
            </div>
          </div>
        })}
      </div>
    </div>
  </div>
})
