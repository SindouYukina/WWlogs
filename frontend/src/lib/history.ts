import type { DPS, Encounter, Timeline } from "./snapshot.ts"

// The Combat History contracts the Go host returns (internal/desktop/history.go) and the calendar arithmetic of the History
// view. Every count, state, DPS and identity is the backend's, and so is every date and clock: Go lays each stored UTC
// instant out in the zone the view names (hostTimeZone), so the heatmap, the day list and the detail always agree. This
// file only lays out a calendar grid of the dates Go returned and formats offsets.

export type HistoryState = "ready" | "read_only" | "unavailable"

export interface HistoryStatus {
  state: HistoryState
  /** A stable reason code when the history is not ready. */
  reason?: string
  schemaVersion: number
  latestSchemaVersion: number
  recoveredSessions: number
  recoveredEncounters: number
  /** True when the history cannot be opened and a new, empty one may replace it at the user's request (Go decides). */
  rebuildable?: boolean
}

/** The answer to a retry or a rebuild of a history that could not be opened. */
export interface HistoryRecoveryResult {
  status: HistoryStatus
  /** True when the history is open now. */
  open: boolean
  refused?: "not_rebuildable"
}

export interface HistoryDayCount {
  /** A date, YYYY-MM-DD, of the view's zone where the encounters started. */
  date: string
  encounters: number
}

/** The zone Go laid an answer out in: the IANA zone the view asked for, or Local (the Windows zone Go read at start). */
export interface HistoryZone {
  id: string
  /** The zone the view asked for could not be loaded, so Local was used. */
  fallback?: boolean
  /** The zone's UTC offset now. */
  offsetSeconds: number
}

/** One stored instant in the view's zone: its date, its clock (HH:MM:SS) and that zone's UTC offset at the instant. */
export interface HistoryLocalTime {
  date: string
  clock: string
  offsetSeconds: number
}

export interface HistoryYear {
  status: HistoryStatus
  zone: HistoryZone
  year: number
  days: HistoryDayCount[]
  total: number
  /** Years with any encounter; both 0 for an empty history. */
  firstYear: number
  lastYear: number
  /** The current date in the view's zone. */
  today: string
}

/**
 * A Master name reached from an observed ID. VERIFIED only for a relation the client itself resolves, in an encounter of the
 * game version the Master describes; CORRELATED for any other named link; UNKNOWN without a name. Go decides the status.
 */
export interface HistoryName {
  name?: string
  status: "VERIFIED" | "CORRELATED" | "UNKNOWN"
}

/**
 * The kind of play of an encounter: an Adventure Guide category named by the Master, the open world, or a content system the
 * game gives no system name (system), which the UI labels with a WWlogs classification and never as a game name.
 */
export interface HistoryMode {
  kind?: "open_world" | "category" | "content_system"
  name?: string
  status: "VERIFIED" | "CORRELATED" | "UNKNOWN"
  /** The content system that gave the mode. */
  system?: string
  /** Why a content system mode has no game name: none exists, it is not decided yet, or none in this language or English. */
  nameStatus?: "NO_OFFICIAL_NAME" | "UNRESOLVED" | "NAME_MISSING"
}

/** One dimension of the content Go read for an encounter's instance (Go historyContentLayer). */
export interface HistoryContentLayer {
  dimension: "activity" | "season" | "cycle" | "series" | "stage" | "difficulty" | "mode" | "role"
  /**
   * OFFICIAL with its name; NO_OFFICIAL_NAME (the game names it nowhere); UNRESOLVED (not known yet); NAME_MISSING (no name in
   * this language or English); RUNTIME_DEPENDENT (the encounter could be any of candidates, and nothing stored says which).
   */
  state: "OFFICIAL" | "NO_OFFICIAL_NAME" | "UNRESOLVED" | "NAME_MISSING" | "RUNTIME_DEPENDENT"
  name?: string
  status?: "VERIFIED" | "CORRELATED"
  candidates?: number
}

/** The content Go read for an encounter's instance, when its content system gave the mode. */
export interface HistoryContent {
  system: string
  layers: HistoryContentLayer[]
}

/** The content layers the UI shows: the named ones, and the ones whose value the History cannot choose. */
export function shownContentLayers(content: HistoryContent | undefined): HistoryContentLayer[] {
  return (content?.layers ?? []).filter(layer => (layer.state === "OFFICIAL" && !!layer.name) || layer.state === "RUNTIME_DEPENDENT")
}

/** The named content layers, which identify an instance that has no stage name of its own. */
export function namedContentLayers(content: HistoryContent | undefined): HistoryContentLayer[] {
  return (content?.layers ?? []).filter(layer => layer.state === "OFFICIAL" && !!layer.name)
}

type Translate = (key: string, options?: Record<string, unknown>) => string

/** The text of a mode: the Master's category name, or the UI label of the open world or of a content system. */
export function modeText(mode: HistoryMode | undefined, t: Translate): string | undefined {
  if (!mode?.kind) return undefined
  if (mode.kind === "open_world") return t("modeOpenWorld")
  if (mode.kind === "content_system") return t(`contentSystem.${mode.system ?? ""}`, { defaultValue: t("contentSystemOther") })
  return mode.name
}

/** One content layer as text: its dimension and name, or its dimension and how many values the History cannot choose from. */
export function contentLayerText(layer: HistoryContentLayer, t: Translate): string {
  const label = t(`contentLayer.${layer.dimension}`)
  if (layer.state === "RUNTIME_DEPENDENT") return t("contentLayerOpen", { label, n: layer.candidates ?? 0 })
  return t("contentLayerValue", { label, name: layer.name ?? "" })
}

/**
 * The stage text of an instance: its own stage name, else its named content layers, each labelled with its dimension so
 * that no other layer reads as the stage; undefined when neither exists.
 */
export function stageText(name: HistoryName, content: HistoryContent | undefined, t: Translate): string | undefined {
  if (name.name) return name.name
  const layers = namedContentLayers(content)
  return layers.length ? layers.map(layer => contentLayerText(layer, t)).join(" · ") : undefined
}

export type HistoryEncounterState = "open" | "finished" | "abandoned"

export interface HistoryEncounterSummary {
  id: number
  /** UTC ISO time; start lays it out in the view's zone and localDate is start's date. */
  startedAt: string
  start: HistoryLocalTime
  localDate: string
  /** Where the encounter was recorded: provenance only, never the date it is listed on. */
  recorded: { date: string; offsetSeconds: number }
  endedAt?: string
  end?: HistoryLocalTime
  durationSeconds?: number
  /** The major.minor game version of the protocol session the encounter was fought in, as stored with it. */
  gameVersion: string
  /** False when the recording build did not count server-executed damage: the stored values may be lower than a current analysis. */
  notifiedDamageCounted?: boolean
  state: HistoryEncounterState
  outcome: string
  complete: boolean
  source: "live" | "replay"
  integrity?: "INTACT" | "DEGRADED" | "UNRELIABLE"
  dps: DPS
  damageDealt?: number
  scene: { known: boolean; instanceId?: number }
  mainEnemy?: { ordinal: number; monsterId?: number; templateId?: number; identityConflict?: boolean; damageTaken: number; sameType: number }
  enemies: number
  sceneName: HistoryName
  mode: HistoryMode
  /** The content of the encounter's instance, when its content system gave the mode. */
  content?: HistoryContent
  mainEnemyName: HistoryName
  /** Why the record is incomplete or unfinished: entries_lost, application_exit, recording_disabled or not_finished. */
  incompleteReason?: string
  /** UTC time the stored values of an unfinished encounter describe (its last checkpoint). */
  checkpointAt?: string
  checkpoint?: HistoryLocalTime
}

export interface HistoryDay {
  status: HistoryStatus
  zone: HistoryZone
  date: string
  total: number
  offset: number
  encounters: HistoryEncounterSummary[]
}

export interface HistoryEnemy {
  ordinal: number
  monsterId?: number
  templateId?: number
  identityConflict?: boolean
  name: HistoryName
  damageTaken: number
  hitsTaken: number
  damageDealt: number
  hitsDealt: number
}

/** The recorded span an encounter belongs to. The history also keeps which WWlogs analysed it; the view does not show that. */
export interface HistorySession {
  source: "live" | "replay"
  status: string
  openedAt: string
  journalDropped: number
  queueDropped: number
  writeFailed: number
  transportGaps: number
  startReason: string
  stopReason?: string
  effectsRejected: number
}

/** The effect lifecycle changes stored during one encounter. */
export interface HistoryEffects {
  changes: number
  instances: number
  observed: number
  derived: number
  untracked: number
}

export interface HistoryEncounterDetail {
  status: HistoryStatus
  zone: HistoryZone
  summary: HistoryEncounterSummary
  session: HistorySession
  /** The engine's view of the finished encounter, as the Dashboard showed it; null for an unfinished one. */
  encounter: Encounter | null
  timeline: Timeline | null
  timelineValid: boolean
  enemies: HistoryEnemy[]
  effects: HistoryEffects
}

/** The Combat History indicator the host decides (internal/desktop/history_status.go). */
export type HistoryIndicatorState = "disabled" | "waiting" | "recording" | "starting" | "unavailable" | "read_only" | "write_failed" | "not_recorded"

export interface HistoryIndicator {
  state: HistoryIndicatorState
  reason?: string
  enabled: boolean
  capturing: boolean
  /** Journal entries of the running capture that never reached storage. */
  lost?: number
  /** True when the unavailable history may be replaced by a new one at the user's request. */
  rebuildable?: boolean
}

/** The answer to a deletion: what it removed, or why it was refused. */
export interface HistoryChange {
  encounters: number
  spans: number
  compacted: boolean
  refused?: "encounter_recording" | "recording_active" | "not_writable" | "not_found"
}

export type IndicatorTone = "neutral" | "good" | "warn" | "critical"

/**
 * The label and tone of the host's indicator. This only maps the state the host decided to text keys of the history
 * namespace; it never decides a state itself.
 */
export function indicatorText(indicator: HistoryIndicator | undefined): { key: string; tone: IndicatorTone; detail?: string } {
  if (!indicator) return { key: "indicator.unknown", tone: "neutral" }
  switch (indicator.state) {
    case "recording": return { key: "indicator.recording", tone: "good" }
    case "waiting": return { key: indicator.capturing ? "indicator.waitingCombat" : "indicator.ready", tone: "neutral" }
    case "starting": return { key: "indicator.starting", tone: "warn" }
    case "disabled": return { key: "indicator.disabled", tone: "neutral" }
    case "read_only": return { key: "indicator.readOnly", tone: "warn", detail: `unavailable.${indicator.reason ?? "open_failed"}` }
    case "write_failed": return { key: "indicator.writeFailed", tone: "critical", detail: "unavailable.storage_stopped" }
    case "not_recorded": return { key: "indicator.notRecorded", tone: "warn", detail: `unavailable.${indicator.reason ?? "open_failed"}` }
    // Why a history cannot be opened is the host's to keep: every cause reads the same to the user (HistoryRecovery).
    case "unavailable": return { key: "indicator.unavailable", tone: "critical", detail: "recovery.detail" }
  }
}

const dayMs = 24 * 60 * 60 * 1000

/** Days since 1970-01-01 of a YYYY-MM-DD date; calendar arithmetic only, no time zone involved. */
export function dayNumber(date: string): number {
  const [year = 1970, month = 1, day = 1] = date.split("-").map(Number)
  return Math.round(Date.UTC(year, month - 1, day) / dayMs)
}

/** The YYYY-MM-DD date of a day number. */
export function dateOfDay(day: number): string {
  return new Date(day * dayMs).toISOString().slice(0, 10)
}

export function isLeapYear(year: number): boolean {
  return (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0
}

export interface HeatmapCell {
  date: string
  count: number
  /** 0 for no encounter, 1–4 by the share of the year's busiest day. */
  level: 0 | 1 | 2 | 3 | 4
  /** 0 is Sunday. */
  weekday: number
}

export interface HeatmapLayout {
  year: number
  /** Week columns from the one holding January 1 to the one holding December 31. A cell outside the year is null. */
  weeks: Array<Array<HeatmapCell | null>>
  /** The first week column of each month (0–11). */
  monthStarts: number[]
  total: number
  max: number
  days: number
}

/** The level of a day: the year's busiest day is 4, any encounter at all is at least 1. */
export function heatLevel(count: number, max: number): HeatmapCell["level"] {
  if (count <= 0 || max <= 0) return 0
  return Math.min(4, Math.max(1, Math.ceil((count / max) * 4))) as HeatmapCell["level"]
}

/** Lays out a year like a contribution calendar: one column per week, Sunday on top. */
export function heatmapLayout(year: number, days: readonly HistoryDayCount[]): HeatmapLayout {
  const counts = new Map(days.filter(day => day.date.startsWith(`${year}-`)).map(day => [day.date, day.encounters]))
  const first = dayNumber(`${year}-01-01`)
  const last = dayNumber(`${year}-12-31`)
  const max = Math.max(0, ...counts.values())
  // 1970-01-01 was a Thursday (4).
  const weekdayOf = (day: number) => ((day % 7) + 7 + 4) % 7
  const start = first - weekdayOf(first)
  const weeks: Array<Array<HeatmapCell | null>> = []
  const monthStarts: number[] = []
  let total = 0
  for (let week = 0; start + week * 7 <= last; week++) {
    const column: Array<HeatmapCell | null> = []
    for (let weekday = 0; weekday < 7; weekday++) {
      const day = start + week * 7 + weekday
      if (day < first || day > last) {
        column.push(null)
        continue
      }
      const date = dateOfDay(day)
      const count = counts.get(date) ?? 0
      total += count
      if (date.endsWith("-01")) monthStarts[Number(date.slice(5, 7)) - 1] = week
      column.push({ date, count, level: heatLevel(count, max), weekday })
    }
    weeks.push(column)
  }
  return { year, weeks, monthStarts, total, max, days: last - first + 1 }
}

/**
 * The IANA name of the zone this WebView runs in, which is the zone Windows is set to. History queries pass it to Go, which
 * lays every date out in it; the view itself never converts an instant. An empty name makes Go use the zone it read from
 * Windows at start and say so.
 */
export function hostTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? ""
  } catch {
    return ""
  }
}

/** "UTC+09:00" for an offset in seconds. */
export function offsetText(offsetSeconds: number): string {
  const sign = offsetSeconds < 0 ? "-" : "+"
  const minutes = Math.abs(Math.round(offsetSeconds / 60))
  return `UTC${sign}${String(Math.floor(minutes / 60)).padStart(2, "0")}:${String(minutes % 60).padStart(2, "0")}`
}

/** The default date to open: today when it has encounters, otherwise the newest date of the year that has some. */
export function defaultDate(view: Pick<HistoryYear, "year" | "days">, today: string): string | undefined {
  if (view.days.some(day => day.date === today)) return today
  const dates = view.days.filter(day => day.encounters > 0).map(day => day.date).sort()
  return dates.at(-1)
}

/** The years the year switcher can reach: from the first recorded year to the later of the last one and this year. */
export function yearBounds(view: Pick<HistoryYear, "firstYear" | "lastYear">, currentYear: number): { first: number; last: number } {
  if (view.firstYear === 0) return { first: currentYear, last: currentYear }
  return { first: Math.min(view.firstYear, currentYear), last: Math.max(view.lastYear, currentYear) }
}
