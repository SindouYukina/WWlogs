import type { DPS, Encounter, Healing, Timeline } from "./snapshot"
import type { HistoryName } from "./history"

// The contracts of the History analysis workspace (internal/desktop/history_analysis.go). Go rebuilds every number of a
// range, contributor and enemy from the stored results with the engine's own rules, classifies every effect interval by how
// it is known, and names every endpoint; this file only types the answers and holds the presentation arithmetic of the
// time axis (pixels, labels). It computes no statistic, coverage or attribution.

export type EffectScope = "hud" | "all"

export interface AnalysisQuery {
  /** Milliseconds from the encounter start; toMs 0 means the end. */
  fromMs: number
  toMs: number
  /** A contributor key (role-<player>-<role>, vehicle-<kind>) or "" for the team. */
  contributor: string
  /** An enemy instance ordinal, or -1 for every enemy. */
  enemy: number
  effects: EffectScope
  /** A skill row key whose hits the timeline marks: "<contributor>:<SkillId>" or "<contributor>:unassigned". */
  skillRow: string
}

export const fullQuery: AnalysisQuery = { fromMs: 0, toMs: 0, contributor: "", enemy: -1, effects: "hud", skillRow: "" }

export type AnalysisState = "ready" | "not_finished" | "no_events" | "too_large"

export interface AnalysisRange {
  fromMs: number
  toMs: number
  durationMs: number
  full: boolean
}

export interface SelectionTotals {
  contributor?: string
  dps: DPS
  damage: number
  hits: number
  taken: number
  takenHits: number
  healing: Healing
}

export interface SkillStats {
  contributor: string
  /** 0 is the contributor's row without a skill. */
  skillId: number
  damage: number
  hits: number
  maxHit: number
  averageHit: number
  /** The row's damage over the whole encounter in equal parts (enemy filter applied, range not). */
  bins: number[]
}

export interface EnemyRange {
  ordinal: number
  mapped: boolean
  damageTaken: number
  hitsTaken: number
  damageDealt: number
  hitsDealt: number
}

export interface GapRef {
  fromMs: number
  toMs: number
  direction: string
  failed?: boolean
  open?: boolean
}

export interface EffectCoverage {
  key: string
  confirmedMs: number
  partialMs: number
  unknownMs: number
  gapMs: number
  exact: boolean
  /** The exact share of the range; only with exact. */
  ratio?: number
  /** A lower bound, always given. */
  lower: number
  /** An upper bound, when one is known. */
  upper?: number
  maxStack?: number
  applies: number
  startUnconfirmed?: boolean
}

export interface SkillHit {
  atMs: number
  amount: number
}

export interface EncounterAnalysis {
  state: AnalysisState
  range: AnalysisRange
  query: AnalysisQuery
  consistent: boolean
  mismatch?: string
  /** The range rebuilt and projected exactly as the Dashboard projects an encounter. */
  encounter: Encounter | null
  timeline?: Timeline
  timelineValid: boolean
  selection: SelectionTotals
  skills: SkillStats[]
  skillBins: number
  enemies: EnemyRange[]
  gaps: GapRef[]
  untimedEvents: number
  effects: { available: boolean; scope: EffectScope; rejected: number; rows: EffectCoverage[] }
  hits: SkillHit[]
  hitsTruncated?: boolean
}

export type ActorKind = "role" | "summon" | "vehicle" | "team" | "team_unassigned" | "enemy" | "other" | "unknown"

/** An endpoint in product terms with its name in the UI locale; never a runtime entity ID. */
export interface ActorRef {
  kind: ActorKind
  contributor?: string
  roleId?: number
  enemy?: number
  monsterId?: number
  templateId?: number
  relation?: string
  name: { name?: string; status: string }
}

export type SegmentEvidence = "confirmed" | "stated" | "unknown"

export interface EffectSegment {
  fromMs: number
  toMs: number
  evidence: SegmentEvidence
  stack?: number
}

export type EffectMarkerKind = "applied" | "start_unconfirmed" | "removed" | "holder_removed" | "superseded" | "untracked" | "instant" | "continues"

export interface EffectMarker {
  atMs: number
  kind: EffectMarkerKind
}

export interface EffectRow {
  key: string
  effectId?: number
  definitionLinked: boolean
  polarity: "buff" | "debuff" | "unclassified"
  hudIcon: boolean
  elementId?: number
  hud: boolean
  /** The glossary term the definition links to (CORRELATED at most); UNKNOWN keeps the effect ID as the only identity. */
  name: HistoryName
  /**
   * Go found the game image of exactly this effect's Buff.Id in the image pack (/game-assets/buff/<id>). Only then does
   * any view draw an image for it; no other ID's image ever stands in.
   */
  icon: boolean
  holder: ActorRef
  sources: ActorRef[]
  instances: number
  segments: EffectSegment[]
  markers: EffectMarker[]
  startUnconfirmed?: boolean
}

export interface EffectLanes {
  state: AnalysisState
  available: boolean
  truncated?: boolean
  scope: EffectScope
  durationMs: number
  rows: EffectRow[]
  more?: number
  hidden: number
  stale: number
  rejected: number
  gaps: GapRef[]
}

/** The contributor key Go gives confirmed local outgoing damage the engine credited to no contributor. */
export const unassignedContributor = "unassigned"

/**
 * One skill row's hits over the whole encounter (internal/history/analysis_hits.go), as parallel lists in processing order.
 * They are damage hits, never casts.
 */
export interface HitRow {
  contributor: string
  /** The skill table's row key; empty for damage credited to no contributor, which joins no skill table. */
  skillRow: string
  /** 0 for a contributor's row without a skill; for damage credited to no contributor, the SkillId it carried (0 without). */
  skillId: number
  atMs: number[]
  amount: number[]
  /** The stored results' sequences: the event browser lists the same results as "c<sequence>". */
  sequence: number[]
}

export interface HitTimeline {
  state: AnalysisState
  durationMs: number
  query: AnalysisQuery
  rows: HitRow[]
  /** Every hit the filters select; listed ones are in rows; untimed ones have no time to place. */
  total: number
  listed: number
  untimed: number
  truncated?: boolean
}

export type EventKind = "damage" | "healing" | "effect"

export interface EventQuery extends AnalysisQuery {
  kinds: EventKind[]
  effectRow: string
  offset: number
  limit: number
}

export interface EventRow {
  key: string
  atMs?: number
  kind: EventKind
  flow?: string
  amount?: number
  effective?: number
  source?: ActorRef
  target?: ActorRef
  skillId?: number
  skillRow?: string
  confidence?: string
  downgraded?: boolean
  effectRow?: string
  effectId?: number
  change?: string
  observation?: string
  evidence?: string
  reason?: string
  stack?: number
}

export interface EventPage {
  state: AnalysisState
  total: number
  offset: number
  limit: number
  rows: EventRow[]
  effectsAvailable: boolean
}

export const eventPageSize = 100

/** "01:05.250" for milliseconds from the encounter start. */
export function formatMs(ms: number): string {
  const total = Math.max(0, Math.round(ms))
  const minutes = Math.floor(total / 60000)
  const seconds = Math.floor((total % 60000) / 1000)
  const millis = total % 1000
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}.${String(millis).padStart(3, "0")}`
}

/** The query the workspace sends for a range picked on the timeline: a range that covers the encounter is the full one. */
export function rangeQuery(query: AnalysisQuery, fromMs: number, toMs: number, durationMs: number): AnalysisQuery {
  const from = Math.max(0, Math.min(fromMs, toMs, durationMs))
  const to = Math.min(durationMs, Math.max(fromMs, toMs))
  if (from <= 0 && to >= durationMs) return { ...query, fromMs: 0, toMs: 0 }
  return { ...query, fromMs: Math.round(from), toMs: Math.round(to) }
}

/**
 * The range a query commits, in milliseconds from the encounter start, or null for the whole encounter (toMs 0 is the
 * end). The timeline draws this at once, before Go answers for it, so releasing a drag never shows the previous range
 * again for a frame; the numbers of the range still come only from that answer.
 */
export function querySpan(query: AnalysisQuery, durationMs: number): { fromMs: number; toMs: number } | null {
  const to = query.toMs > 0 ? Math.min(query.toMs, durationMs) : durationMs
  if (query.fromMs <= 0 && to >= durationMs) return null
  return { fromMs: Math.max(0, query.fromMs), toMs: to }
}

/** The window of the encounter the timeline shows: zooming and panning change it, never the selected range or a number. */
export interface TimeView {
  fromMs: number
  toMs: number
}

/** The narrowest window the timeline zooms to: one second, or the whole encounter when it is shorter. */
export const minimumViewMs = 1000

export function fullView(durationMs: number): TimeView {
  return { fromMs: 0, toMs: Math.max(1, durationMs) }
}

export function isFullView(view: TimeView, durationMs: number): boolean {
  return view.fromMs <= 0 && view.toMs >= durationMs
}

/** Keeps a window inside the encounter and between the narrowest window and the whole encounter. */
export function clampView(view: TimeView, durationMs: number): TimeView {
  const duration = Math.max(1, durationMs)
  const span = Math.min(duration, Math.max(Math.min(minimumViewMs, duration), view.toMs - view.fromMs))
  const from = Math.min(Math.max(0, view.fromMs), duration - span)
  return { fromMs: from, toMs: from + span }
}

/** Zooms by factor (below 1 zooms in) around anchorMs, which stays where it is on screen. */
export function zoomView(view: TimeView, factor: number, anchorMs: number, durationMs: number): TimeView {
  const span = view.toMs - view.fromMs
  const next = span * factor
  const ratio = span > 0 ? Math.min(1, Math.max(0, (anchorMs - view.fromMs) / span)) : 0.5
  const from = anchorMs - ratio * next
  return clampView({ fromMs: from, toMs: from + next }, durationMs)
}

export function panView(view: TimeView, deltaMs: number, durationMs: number): TimeView {
  return clampView({ fromMs: view.fromMs + deltaMs, toMs: view.toMs + deltaMs }, durationMs)
}

/** A selected range shown with a little context on each side. */
export function viewAround(span: { fromMs: number; toMs: number }, durationMs: number): TimeView {
  const margin = (span.toMs - span.fromMs) * 0.08
  return clampView({ fromMs: span.fromMs - margin, toMs: span.toMs + margin }, durationMs)
}

/** One track's hits of one skill row (or observed SkillId), for drawing; times in ms from the encounter start. */
export interface HitSeries {
  key: string
  atMs: readonly number[]
}

/** One drawn group of hits: its members are hit indices of each series it holds. */
export interface HitCluster {
  key: string
  /** Where its first and last hit sit in the window, as fractions of the window. */
  x: number
  x2: number
  lane: number
  fromMs: number
  toMs: number
  count: number
  members: Array<{ series: number; hits: number[] }>
}

/**
 * Groups one track's hits for drawing in a window, so icons never pile up unreadably and none is left out:
 * consecutive hits of one skill row closer than iconPx to the group's first hit become one icon with a count; a group
 * that would overlap the previous one goes to the next of `lanes` sub-lanes; one that still overlaps joins the group it
 * collides with, which then holds several skill rows. Every hit inside the window belongs to exactly one group, and a
 * group is a drawing aid only: it is never a cast.
 */
export function clusterHits(series: readonly HitSeries[], view: TimeView, widthPx: number, iconPx: number, lanes = 2): HitCluster[] {
  const span = view.toMs - view.fromMs
  if (!(span > 0) || !(widthPx > 0)) return []
  const reach = (iconPx / widthPx) * span
  const flat: Array<{ at: number; series: number; hit: number }> = []
  series.forEach((one, index) => one.atMs.forEach((at, hit) => { if (at >= view.fromMs && at <= view.toMs) flat.push({ at, series: index, hit }) }))
  flat.sort((a, b) => a.at - b.at || a.series - b.series || a.hit - b.hit)
  const open = new Map<number, HitCluster>()
  const groups: HitCluster[] = []
  for (const entry of flat) {
    const current = open.get(entry.series)
    if (current && entry.at - current.fromMs < reach) {
      current.members[0]!.hits.push(entry.hit)
      current.toMs = entry.at
      current.count++
      continue
    }
    const group: HitCluster = { key: "", x: 0, x2: 0, lane: 0, fromMs: entry.at, toMs: entry.at, count: 1, members: [{ series: entry.series, hits: [entry.hit] }] }
    open.set(entry.series, group)
    groups.push(group)
  }
  groups.sort((a, b) => a.fromMs - b.fromMs || a.members[0]!.series - b.members[0]!.series)
  // Each lane remembers where its last icon ends; an icon needs the width of one icon from its start.
  const laneEnd: Array<{ end: number; group: HitCluster | null }> = Array.from({ length: Math.max(1, lanes) }, () => ({ end: -Infinity, group: null }))
  const placed: HitCluster[] = []
  for (const group of groups) {
    const free = laneEnd.findIndex(lane => group.fromMs >= lane.end)
    if (free >= 0) {
      group.lane = free
      laneEnd[free] = { end: group.fromMs + reach, group }
      placed.push(group)
      continue
    }
    // Every lane is taken here: the group joins the one that ends first, which now holds more than one skill row.
    let target = 0
    for (let lane = 1; lane < laneEnd.length; lane++) if (laneEnd[lane]!.end < laneEnd[target]!.end) target = lane
    const host = laneEnd[target]!.group!
    for (const member of group.members) {
      const same = host.members.find(existing => existing.series === member.series)
      if (same) same.hits.push(...member.hits)
      else host.members.push({ series: member.series, hits: [...member.hits] })
    }
    host.count += group.count
    host.fromMs = Math.min(host.fromMs, group.fromMs)
    host.toMs = Math.max(host.toMs, group.toMs)
  }
  for (const group of placed) {
    group.x = (group.fromMs - view.fromMs) / span
    group.x2 = (group.toMs - view.fromMs) / span
    group.key = `${group.lane}:${group.fromMs}:${group.members.map(member => `${member.series}.${member.hits[0]}`).join(",")}`
  }
  return placed
}

/** Touching intervals of one evidence drawn as one bar of the icon timeline; markers still show every start and end. */
export interface EffectBar {
  fromMs: number
  toMs: number
  evidence: SegmentEvidence
  /** The intervals the bar draws, by index, in order: a pick finds the exact interval under the pointer. */
  segments: number[]
  /** Runs of one stack count above 1 inside the bar, each labelled once. */
  stacks: Array<{ fromMs: number; toMs: number; stack: number }>
}

/**
 * Joins touching intervals of the same evidence into bars, so an effect refreshed many times reads as one bar instead of
 * a row of pieces, and labels a stack count once per run. Intervals of different evidence never join, so confirmed,
 * stated and unknown time stay apart; every interval belongs to exactly one bar. Drawing only: no interval changes.
 */
export function effectBars(segments: ReadonlyArray<Pick<EffectSegment, "fromMs" | "toMs" | "evidence" | "stack">>): EffectBar[] {
  const bars: EffectBar[] = []
  segments.forEach((segment, index) => {
    let bar = bars[bars.length - 1]
    if (!bar || bar.evidence !== segment.evidence || Math.abs(bar.toMs - segment.fromMs) > 0.5) {
      bar = { fromMs: segment.fromMs, toMs: segment.toMs, evidence: segment.evidence, segments: [], stacks: [] }
      bars.push(bar)
    }
    bar.toMs = Math.max(bar.toMs, segment.toMs)
    bar.segments.push(index)
    if (segment.stack !== undefined && segment.stack > 1) {
      const run = bar.stacks[bar.stacks.length - 1]
      if (run && run.stack === segment.stack && Math.abs(run.toMs - segment.fromMs) <= 0.5) run.toMs = segment.toMs
      else bar.stacks.push({ fromMs: segment.fromMs, toMs: segment.toMs, stack: segment.stack })
    }
  })
  return bars
}

/** The interval of a bar under a moment: the one holding it, else the nearest one. */
export function segmentAt(segments: ReadonlyArray<Pick<EffectSegment, "fromMs" | "toMs">>, indices: readonly number[], ms: number): number {
  let best = indices[0] ?? -1
  let distance = Infinity
  for (const index of indices) {
    const segment = segments[index]
    if (!segment) continue
    if (ms >= segment.fromMs && ms < segment.toMs) return index
    const away = ms < segment.fromMs ? segment.fromMs - ms : ms - segment.toMs
    if (away < distance) { distance = away; best = index }
  }
  return best
}

/** How the icon timeline shows an effect: its own game image, the neutral mark with its Master term, or the unknown mark with its ID. */
export type EffectFace = "icon" | "named" | "unknown"

export function effectFace(row: Pick<EffectRow, "icon" | "name">): EffectFace {
  if (row.icon) return "icon"
  return row.name.name ? "named" : "unknown"
}

/** The icon timeline's effect groups, by who holds the effect as Go resolved it; an unknown holder stays apart. */
export type EffectGroupKind = "team" | "role" | "enemy" | "unknown"

export function effectGroupOf(holder: Pick<ActorRef, "kind" | "roleId" | "contributor">): { kind: EffectGroupKind; key: string } {
  switch (holder.kind) {
    case "team":
    case "team_unassigned": return { kind: "team", key: "team" }
    case "role":
    case "summon": return { kind: "role", key: `role:${holder.contributor || `id-${holder.roleId ?? 0}`}` }
    case "enemy": return { kind: "enemy", key: "enemy" }
    default: return { kind: "unknown", key: "unknown" }
  }
}

/** The smallest range a drag selects, so a click never selects a sliver. */
export const minimumRangeMs = 200

/** Time-axis ticks at round steps that keep at most maxTicks labels. */
export function timeTicks(durationMs: number, maxTicks = 8): number[] {
  if (!(durationMs > 0)) return [0]
  const steps = [1000, 2000, 5000, 10000, 15000, 30000, 60000, 120000, 300000, 600000, 900000, 1800000, 3600000]
  const step = steps.find(candidate => durationMs / candidate <= maxTicks) ?? 3600000
  const ticks: number[] = []
  for (let at = 0; at <= durationMs + 1; at += step) ticks.push(at)
  return ticks
}

/** The skill row key a table row selects. */
export function skillRowKey(contributor: string, skillId: number | undefined): string {
  return `${contributor}:${skillId === undefined || skillId === 0 ? "unassigned" : skillId}`
}

/** Splits a skill row key into its contributor and SkillId (0 for the unassigned row). */
export function parseSkillRow(key: string): { contributor: string; skillId: number } | null {
  const index = key.lastIndexOf(":")
  if (index <= 0) return null
  const skill = key.slice(index + 1)
  return { contributor: key.slice(0, index), skillId: skill === "unassigned" ? 0 : Number(skill) }
}

/** The windowed slice of fixed-height rows a scroller shows, with a few rows of overscan. */
export function visibleRows(total: number, rowHeight: number, scrollTop: number, viewport: number, overscan = 6): { first: number; last: number } {
  if (total <= 0 || rowHeight <= 0) return { first: 0, last: -1 }
  const first = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan)
  const last = Math.min(total - 1, Math.ceil((scrollTop + viewport) / rowHeight) + overscan)
  return { first, last }
}
