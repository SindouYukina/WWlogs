import type { DPS, Encounter, Timeline } from "./snapshot.ts"

// The Combat History contracts the Go host returns (internal/desktop/history.go) and the calendar arithmetic of the History
// view. Every count, state, DPS and identity is the backend's; this file only lays out dates and formats times.

export type HistoryState = "ready" | "read_only" | "unavailable"

export interface HistoryStatus {
  state: HistoryState
  /** A stable reason code when the history is not ready. */
  reason?: string
  schemaVersion: number
  latestSchemaVersion: number
  recoveredSessions: number
  recoveredEncounters: number
  /** Always false: the file is plain local SQLite. */
  encrypted: boolean
}

export interface HistoryDayCount {
  /** A local date, YYYY-MM-DD, where the encounters started. */
  date: string
  encounters: number
}

export interface HistoryYear {
  status: HistoryStatus
  year: number
  days: HistoryDayCount[]
  total: number
  /** Years with any encounter; both 0 for an empty history. */
  firstYear: number
  lastYear: number
}

/** A Master name reached through a versioned link from an observed ID: at most CORRELATED, otherwise UNKNOWN. */
export interface HistoryName {
  name?: string
  status: "CORRELATED" | "UNKNOWN"
}

export type HistoryEncounterState = "open" | "finished" | "abandoned"

export interface HistoryEncounterSummary {
  id: number
  /** UTC ISO time; utcOffsetSeconds is the offset where the encounter started. */
  startedAt: string
  utcOffsetSeconds: number
  localDate: string
  endedAt?: string
  durationSeconds?: number
  state: HistoryEncounterState
  outcome: string
  complete: boolean
  source: "live" | "replay"
  integrity?: "INTACT" | "DEGRADED" | "UNRELIABLE"
  dps: DPS
  damageDealt?: number
  scene: { recorded: boolean; known: boolean; instanceId?: number }
  mainEnemy?: { ordinal: number; monsterId?: number; templateId?: number; identityConflict?: boolean; damageTaken: number; sameType: number }
  enemies: number
  enemiesRecorded: boolean
  sceneName: HistoryName
  mainEnemyName: HistoryName
}

export interface HistoryDay {
  status: HistoryStatus
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

export interface HistorySession {
  source: "live" | "replay"
  status: string
  openedAt: string
  historyLevel: string
  journalDropped: number
  queueDropped: number
  writeFailed: number
  transportGaps: number
  appVersion: string
  appBuild: number
  appProfile: string
  appCommit: string
  protocolSchema: string
  masterSha256: string
}

export interface HistoryEncounterDetail {
  status: HistoryStatus
  summary: HistoryEncounterSummary
  session: HistorySession
  /** The engine's view of the finished encounter, as the Dashboard showed it; null for an unfinished one. */
  encounter: Encounter | null
  timeline: Timeline | null
  timelineValid: boolean
  enemies: HistoryEnemy[]
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

/** The local date of an instant in the host's time zone now, for "today". */
export function localDateOf(instant: Date): string {
  const y = instant.getFullYear(), m = instant.getMonth() + 1, d = instant.getDate()
  return `${y}-${String(m).padStart(2, "0")}-${String(d).padStart(2, "0")}`
}

/**
 * The wall-clock time an encounter started where it was recorded: its UTC instant shifted by the offset stored with it, so a
 * later change of the host's time zone never moves a fight to another hour or day.
 */
export function recordedClock(startedAt: string, offsetSeconds: number): { hours: number; minutes: number; seconds: number } {
  const shifted = new Date(Date.parse(startedAt) + offsetSeconds * 1000)
  return { hours: shifted.getUTCHours(), minutes: shifted.getUTCMinutes(), seconds: shifted.getUTCSeconds() }
}

export function clockText(clock: { hours: number; minutes: number; seconds: number }): string {
  return [clock.hours, clock.minutes, clock.seconds].map(value => String(value).padStart(2, "0")).join(":")
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
