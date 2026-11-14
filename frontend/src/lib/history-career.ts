import { dateOfDay, dayNumber, heatLevel, type HeatmapCell, type HistoryLocalTime, type HistoryMode, type HistoryName, type HistoryStatus, type HistoryZone } from "./history.ts"
import type { HistoryLibraryQuery } from "./history-library.ts"

// The Career Overview contracts the Go host returns (internal/desktop/history_career.go, internal/history/career.go). Every
// total, record, eligibility and date comes from Go; this file only names the query presets and lays out a calendar grid of
// the dates Go returned.

export type CareerPreset = "7d" | "30d" | "all" | "custom"

export const careerPresets: readonly CareerPreset[] = ["7d", "30d", "all", "custom"]

export interface HistoryCareerQuery {
  from?: string
  to?: string
  /** The last N dates of the view's zone up to today, as Go decides them. */
  lastDays?: number
  gameVersions?: string[]
  /** Mode keys ("open_world" or "category:<name>") and instance IDs. */
  modes?: string[]
  stages?: number[]
}

export interface CareerRef {
  encounterId: number
  startedAt: string
  start: HistoryLocalTime
  gameVersion: string
  instanceId?: number
  /** Static roles that dealt confirmed damage in the encounter. */
  roles: number[]
}

export interface CareerRecord extends CareerRef {
  value: number
  roleId?: number
  skillId?: number
  roleName?: HistoryName
  elementId?: number
  skillName?: HistoryName
  mode: HistoryMode
  stage: HistoryName
}

export interface CareerTotals {
  encounters: number
  counted: number
  classes: Record<string, number>
  damage: number
  damageLowerBound?: boolean
  /** Encounters of the scope recorded by a build that did not count server-executed damage. */
  withoutNotifiedDamage?: number
  seconds: number
  activeDays: number
  first?: CareerRef
  latest?: CareerRef
}

export interface CareerDay {
  date: string
  encounters: number
  counted: number
  damage: number
  /** Set when a counted encounter in the sum stored a lower bound (degraded or known_affected): the sum is a lower bound too. */
  damageLowerBound?: boolean
  seconds: number
  bestDps?: number
}

export interface CareerRole {
  roleId: number
  name: HistoryName
  elementId?: number
  encounters: number
  counted: number
  trialEncounters?: number
  damage: number
  /** Set when a counted encounter in the sum stored a lower bound (degraded or known_affected): the sum is a lower bound too. */
  damageLowerBound?: boolean
  bestDps?: CareerRecord
  bestDamage?: CareerRecord
  maxHit?: CareerRecord
  firstDate: string
  lastDate: string
  days: CareerRoleDay[]
}

/** One role on one date: participations, its damage in counted encounters, its best DPS in complete ones. */
export interface CareerRoleDay {
  date: string
  encounters: number
  damage: number
  damageLowerBound?: boolean
  bestDps?: number
}

export interface CareerCombo {
  roles: Array<{ roleId: number; elementId?: number; name: HistoryName }>
  encounters: number
  damage: number
  /** Set when a counted encounter in the sum stored a lower bound (degraded or known_affected): the sum is a lower bound too. */
  damageLowerBound?: boolean
  bestDps?: CareerRecord
}

export interface CareerPlace {
  key: string
  instanceId?: number
  mode: HistoryMode
  stage: HistoryName
  encounters: number
  counted: number
  damage: number
  /** Set when a counted encounter in the sum stored a lower bound (degraded or known_affected): the sum is a lower bound too. */
  damageLowerBound?: boolean
  bestDps?: CareerRecord
}

export interface HistoryCareer {
  status: HistoryStatus
  zone: HistoryZone
  today: string
  from: string
  to: string
  totals: CareerTotals
  days: CareerDay[]
  roles: CareerRole[]
  combos: CareerCombo[]
  modes: CareerPlace[]
  stages: CareerPlace[]
  records: { maxHit?: CareerRecord | null; bestDps?: CareerRecord | null; bestDamage?: CareerRecord | null; bestPeak?: CareerRecord | null }
  versions: string[]
  filters: {
    modes: Array<{ key: string; kind: "open_world" | "category"; name?: string; status: HistoryName["status"]; count: number }>
    stages: Array<{ instanceId: number; name: HistoryName; count: number }>
  }
}

/** What the Career page shows: a range preset with its custom dates, and at most one game version, mode and stage. */
export interface CareerState {
  preset: CareerPreset
  from?: string
  to?: string
  gameVersion?: string
  /** A mode key of the filters ("open_world" or "category:<name>"). */
  mode?: string
  stage?: number
}

export const defaultCareerState: CareerState = { preset: "all" }

/** The query of the page's state. */
export function careerStateQuery(state: CareerState): HistoryCareerQuery {
  const filters: Pick<HistoryCareerQuery, "gameVersions" | "modes" | "stages"> = {}
  if (state.gameVersion) filters.gameVersions = [state.gameVersion]
  if (state.mode) filters.modes = [state.mode]
  if (state.stage !== undefined) filters.stages = [state.stage]
  return careerQuery(state.preset, { from: state.from, to: state.to }, filters)
}

/**
 * The History Library query that lists the encounters behind a career answer: the dates Go answered for, the page's
 * filters, and optionally one role, mode or stage the user picked. A picked mode or stage replaces the page's own.
 */
export function careerLibraryQuery(career: Pick<HistoryCareer, "from" | "to">, state: CareerState, focus: { role?: number; mode?: string; stage?: number } = {}): HistoryLibraryQuery {
  const query: HistoryLibraryQuery = { sort: "start_desc" }
  if (career.from) query.from = career.from
  if (career.to) query.to = career.to
  if (state.gameVersion) query.gameVersions = [state.gameVersion]
  const stage = focus.stage ?? (focus.mode === undefined ? state.stage : undefined)
  const mode = focus.mode ?? (focus.stage === undefined ? state.mode : undefined)
  if (mode) query.modes = [mode]
  if (stage !== undefined) query.stages = [stage]
  if (focus.role !== undefined) query.roles = [focus.role]
  return query
}

/** The query of a preset: the last 7 or 30 dates, everything, or the custom dates. */
export function careerQuery(preset: CareerPreset, custom: { from?: string; to?: string }, filters: Pick<HistoryCareerQuery, "gameVersions" | "modes" | "stages">): HistoryCareerQuery {
  switch (preset) {
    case "7d": return { ...filters, lastDays: 7 }
    case "30d": return { ...filters, lastDays: 30 }
    case "all": return { ...filters }
    case "custom": {
      const query: HistoryCareerQuery = { ...filters }
      if (custom.from) query.from = custom.from
      if (custom.to) query.to = custom.to
      return query
    }
  }
}

export interface RangeHeatmapLayout {
  weeks: Array<Array<HeatmapCell | null>>
  /** The first week column of each month that starts in the range, in order; months names which month each one is (0-11). */
  monthStarts: number[]
  months: number[]
  total: number
  max: number
  days: number
}

/** The longest range one calendar shows: 53 weeks. A longer range shows its last 371 dates. */
export const maxCalendarDays = 371

/**
 * The dates from..to (Go's dates) as calendar weeks, Sunday first, each shaded by its encounter count. Only lays out what Go
 * returned; a date without a stored encounter is shown as such, never as a day without play.
 */
export function rangeHeatmapLayout(from: string, to: string, days: ReadonlyArray<{ date: string; encounters: number }>): RangeHeatmapLayout {
  const last = dayNumber(to)
  const first = Math.max(dayNumber(from), last - maxCalendarDays + 1)
  const counts = new Map(days.map(day => [day.date, day.encounters]))
  let max = 0
  for (let day = first; day <= last; day++) max = Math.max(max, counts.get(dateOfDay(day)) ?? 0)
  // 1970-01-01 was a Thursday (4).
  const weekdayOf = (day: number) => ((day % 7) + 7 + 4) % 7
  const start = first - weekdayOf(first)
  const weeks: Array<Array<HeatmapCell | null>> = []
  const monthStarts: number[] = []
  const months: number[] = []
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
      if (date.endsWith("-01") || day === first) {
        monthStarts.push(week)
        months.push(Number(date.slice(5, 7)) - 1)
      }
      column.push({ date, count, level: heatLevel(count, max), weekday })
    }
    weeks.push(column)
  }
  return { weeks, monthStarts, months, total, max, days: last - first + 1 }
}
