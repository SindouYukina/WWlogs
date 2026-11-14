import type { HistoryEncounterSummary, HistoryName, HistoryStatus, HistoryZone } from "./history.ts"
import type { RecordClass } from "./history-library.ts"

// The Encounter Comparison contracts the Go host returns (internal/desktop/history_compare.go). Go reads, projects and
// lines up the encounters, states their shared conditions and gives each change; this file only keeps the chosen set.

/** How many encounters one comparison holds (Go's maxCompared; the host also returns it as limit). */
export const compareLimit = 4

/**
 * What a compared value is: a complete result, a lower bound (degraded or known_affected), part of a result (incomplete or
 * unfinished), unreliable (unknown boundaries) or no value.
 */
export type CompareStatus = "available" | "lower_bound" | "partial" | "unreliable" | "unavailable"

export interface CompareValue {
  value?: number
  status: CompareStatus
  /** The change from the first encounter as a ratio (0.12 is 12 % more); only between complete records. */
  delta?: number
}

export type CompareMetricKey = "duration" | "damage" | "dps" | "peak_dps"

export interface CompareMetric {
  key: CompareMetricKey
  values: CompareValue[]
}

export interface CompareContribution {
  damage: number
  /** Of the encounter's confirmed damage. */
  share?: number
  dps?: number
  status: CompareStatus
  /** The damage came from a trial role shown as its parent. */
  trial?: boolean
  dpsDelta?: number
}

export interface CompareSkillValue {
  damage: number
  /** Of the contributor's damage in that encounter. */
  share?: number
  hits: number
}

export interface CompareSkill {
  skillId?: number
  unassigned?: boolean
  /** Set on a role's negative-status row: its damage of this DamageId (name is the status's glossary term). */
  statusDamageId?: number
  name: HistoryName
  nameSource?: string
  showSkillId?: boolean
  iconKind?: string
  iconId?: number
  /** One entry per encounter; null where the contributor dealt no damage with the skill. */
  values: Array<CompareSkillValue | null>
}

export interface CompareContributor {
  kind: "role" | "vehicle" | "unassigned"
  /** The role the contributor shows as (a trial role's parent). */
  roleId?: number
  elementId?: number
  name: HistoryName
  /** One entry per encounter; null where it dealt no confirmed damage or no result was stored. */
  values: Array<CompareContribution | null>
  skills: CompareSkill[]
}

export interface CompareCondition {
  key: "game_version" | "mode" | "stage" | "main_enemy"
  state: "same" | "different" | "unknown"
}

export interface CompareEncounter extends HistoryEncounterSummary {
  class: RecordClass
  /** A complete record: its values may be measured against another complete record. */
  reliable: boolean
  /** A result (or a checkpoint) was stored. */
  results: boolean
}

export interface HistoryComparison {
  status: HistoryStatus
  zone: HistoryZone
  /** Encounters deleted meanwhile; the view drops them from its set. */
  missing: number[]
  limit: number
  encounters: CompareEncounter[]
  metrics: CompareMetric[]
  contributors: CompareContributor[]
  context: {
    conditions: CompareCondition[]
    openWorld?: boolean
    durationRatio?: number
    reliable: boolean
  }
}

/** One encounter chosen for comparison, with the date and time its chip shows. */
export interface ComparedEntry {
  id: number
  date: string
  clock: string
}

/** Adds an encounter to the set or removes it; a full set stays as it is. */
export function toggleCompared(entries: readonly ComparedEntry[], entry: ComparedEntry, limit = compareLimit): ComparedEntry[] {
  if (entries.some(chosen => chosen.id === entry.id)) return entries.filter(chosen => chosen.id !== entry.id)
  if (entries.length >= limit) return [...entries]
  return [...entries, entry]
}
