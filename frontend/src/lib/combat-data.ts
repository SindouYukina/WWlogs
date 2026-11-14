import type { GameAssetKind } from "../design/assets.ts"
import { snapshotStale } from "./effects.ts"
import { dataState, type Connection, type DPS, type Encounter, type Snapshot, type ViewSnapshot } from "./snapshot.ts"

// The contract between a data provider and the product views. The Live provider fills it from the Go host; the
// Development mock provider fills it from scripted data. The views never know which one they received.

export interface SkillRow {
  skillId: number
  /** The engine's display name; absent means the skill is unnamed. */
  name?: string
  /** The engine's decision to print the raw SkillId beside the row. */
  showId: boolean
  /** Set when name is not the skill's own official name but that of a related record; the table marks such rows. */
  derived?: "damage_list" | "skill_type" | "tune_break" | "echo"
  iconKind?: GameAssetKind
  iconId?: number
  damage: number
  /** Server-confirmed, deduplicated damage events. Not a cast count. */
  events: number
  /** 0..1 of the same contributor's confirmed damage; undefined when that total is zero. */
  share?: number
}

/** A role's negative-status damage of one DamageId (Go ViewStatus); the engine names it by the status's glossary term. */
export interface StatusRow {
  damageId: number
  /** The status's official name; absent means the engine found no single term and the table shows the DamageId. */
  name?: string
  iconKind?: GameAssetKind
  iconId?: number
  damage: number
  events: number
  /** Executions the role forced (InvokePeriod); the rest ran on the status instance the role owns. */
  triggerEvents: number
  share?: number
}

/** A contributor's skill split. Rows, statuses and unassigned add up to the contributor's confirmed damage. */
export interface SkillBreakdown {
  rows: SkillRow[]
  /** A role's negative-status damage by DamageId; absent or empty when there is none. */
  statuses?: StatusRow[]
  /** Confirmed damage of the contributor without a skill source; absent when there is none. */
  unassigned?: { damage: number; events: number; share?: number; name?: string; derived?: "echo" | "damage_list" | "damage_type"; iconKind?: GameAssetKind; iconId?: number }
}

export interface CombatTimeline {
  slots: number
  /** Seconds one slot covers; slot n starts n * slotSeconds after the encounter start. */
  slotSeconds: number
  /** DPS per slot by series key: "team", a ranking row key (role-<playerId>-<roleId>, vehicle-<kind>), or "unassigned". Null slots are not drawn. */
  series: ReadonlyMap<string, ReadonlyArray<number | null>>
  /** The encounter the series belong to, when the provider knows it. */
  encounterId?: string
  /** The encounter finished, so the series are final. */
  final?: boolean
}

export interface CombatData {
  snapshot: Snapshot | null
  connection: Connection
  /** Wall clock for freshness checks, updated at 1 Hz. */
  now: number
  /** The provider stopped publishing on purpose. Trust gating treats it exactly like a stalled engine. */
  paused?: boolean
  /** Absent means no backend source exists yet; the view shows that state instead of numbers. */
  timeline?: CombatTimeline
  /**
   * Skill split of a ranking row (by its key). Absent means the provider has no skill source; undefined from the
   * function means the engine reported no split for that row.
   */
  skillsOf?: (rowKey: string) => SkillBreakdown | undefined
}

export function displayedEncounter(view: ViewSnapshot | null | undefined): Encounter | undefined {
  return view?.encounters.current ?? view?.encounters.recent.at(-1)
}

/** Stale, stalled or disconnected: values stay visible but must not move or count down. */
export function isFrozen(data: Pick<CombatData, "snapshot" | "connection" | "now" | "paused">): boolean {
  return data.connection === "disconnected" || data.paused === true || snapshotStale(data.snapshot, data.now)
}

export function usableDps(dps: DPS | undefined): boolean {
  return dps?.status === "available" || dps?.status === "lower_bound"
}

export type ChipTone = "neutral" | "good" | "warn" | "critical"

/** Classifies what the data can be trusted for. The key is in the common namespace. It never upgrades data. */
export function dataChip(data: Pick<CombatData, "snapshot" | "connection" | "now" | "paused">): { key: string; tone: ChipTone } {
  if (data.connection === "disconnected") return { key: "disconnected", tone: "critical" }
  if (!data.snapshot) return { key: "connecting", tone: "neutral" }
  const state = dataState(data.snapshot)
  if (state === "untrusted") return { key: "untrusted", tone: "critical" }
  if (isFrozen(data)) return { key: "stale", tone: "warn" }
  switch (state) {
    case "degraded": return { key: "degraded", tone: "warn" }
    case "not_capturing": return { key: "notCapturing", tone: "neutral" }
    case "no_data": return { key: "waiting", tone: "neutral" }
    case "no_encounter": return { key: "noEncounter", tone: "neutral" }
    default: return { key: "live", tone: "good" }
  }
}
