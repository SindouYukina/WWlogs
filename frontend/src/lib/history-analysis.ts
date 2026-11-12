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
