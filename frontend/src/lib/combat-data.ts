import { snapshotStale } from "./effects.ts"
import { dataState, type Connection, type DPS, type Encounter, type Snapshot, type ViewSnapshot } from "./snapshot.ts"

// The contract between a data provider and the product views. The Live provider fills it from the Go host; the
// Development mock provider fills it from scripted data. The views never know which one they received.

export interface SkillRow {
  skillId: number
  /** Master display name; absent names show the raw ID. */
  name?: string
  damage: number
  hits: number
  share: number
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
  /** Absent means no backend source exists yet; the view shows that state instead of numbers. */
  skillsOf?: (roleId: number) => SkillRow[]
  /** Publish interval used to pace countdown bars, when the provider knows it. */
  tickMs?: number
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
