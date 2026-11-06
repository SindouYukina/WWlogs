import type { Effect, Encounter, Role, Vehicle, ViewSnapshot } from "./snapshot"

// Adapts engine snapshot types to the row models the shared components render. Nothing here adds information: a value
// the engine did not report stays undefined, and unresolved identities keep their raw IDs.

export interface CombatRowModel {
  key: string
  kind: "role" | "vehicle"
  roleId?: number
  /** Static Master element; undefined means unknown. */
  elementId?: number
  /** Null when the Master lookup is not VERIFIED; the UI must then show UNKNOWN with roleId. */
  name: string | null
  /** Engine split of damage by direct hits and summons. Vehicles have no split. */
  directDamage?: number
  summonDamage?: number
  damage: number
  /** 0..1, or undefined when the engine did not report a share. */
  share?: number
  /** Display-derived from damage and the engine's encounter duration; undefined unless encounter DPS is usable. */
  dps?: number
  damageTaken: number
  /** Rank by damage, 0 is highest. Ties keep a stable order through the key. */
  rank: number
}

function usableDuration(encounter: Encounter): number | undefined {
  const { status, durationSeconds } = encounter.dps
  return (status === "available" || status === "lower_bound") && durationSeconds > 0 ? durationSeconds : undefined
}

function roleRow(role: Role, duration: number | undefined): Omit<CombatRowModel, "rank"> {
  return {
    key: `role-${role.playerId}-${role.roleId}`,
    kind: "role",
    roleId: role.roleId,
    elementId: role.elementId,
    name: role.nameStatus === "VERIFIED" && role.name !== "" ? role.name : null,
    directDamage: role.directDamage,
    summonDamage: role.summonDamage,
    damage: role.damageDealt,
    share: role.damageShare,
    dps: duration === undefined ? undefined : role.damageDealt / duration,
    damageTaken: role.damageTaken,
  }
}

function vehicleRow(vehicle: Vehicle, duration: number | undefined): Omit<CombatRowModel, "rank"> {
  return {
    key: `vehicle-${vehicle.kind}`,
    kind: "vehicle",
    name: null,
    damage: vehicle.damageDealt,
    share: vehicle.damageShare,
    dps: duration === undefined ? undefined : vehicle.damageDealt / duration,
    damageTaken: 0,
  }
}

/** Rows keep a stable identity while their rank changes, which is what lets a rank swap animate instead of re-mount. */
export function combatRows(encounter: Encounter): CombatRowModel[] {
  return ranked(encounter.roles, encounter.vehicle, usableDuration(encounter))
}

/** The capture interval's rows, for when no encounter exists yet. There is no encounter duration, so no per-role DPS. */
export function intervalRows(view: Pick<ViewSnapshot, "roles" | "vehicle">): CombatRowModel[] {
  return ranked(view.roles, view.vehicle, undefined)
}

function ranked(roles: readonly Role[], vehicle: Vehicle | undefined, duration: number | undefined): CombatRowModel[] {
  const rows = roles.map(role => roleRow(role, duration))
  if (vehicle) rows.push(vehicleRow(vehicle, duration))
  const ordered = [...rows].sort((a, b) => b.damage - a.damage || a.key.localeCompare(b.key))
  return ordered.map((row, rank) => ({ ...row, rank }))
}

/** Direct and summon shares of one row, or undefined when the engine reported no damage to split. */
export function contributionShares(row: Pick<CombatRowModel, "directDamage" | "summonDamage">): { direct: number; summon: number } | undefined {
  const direct = row.directDamage ?? 0
  const summon = row.summonDamage ?? 0
  const total = direct + summon
  return total > 0 ? { direct: direct / total, summon: summon / total } : undefined
}

export type CountdownState =
  /** The engine reports an estimated remaining time and a total duration, so a progress bar is truthful. */
  | { kind: "timed"; remaining: number; duration: number }
  /** The engine reports a remaining time but no total: show the seconds without a proportion. */
  | { kind: "remaining-only"; remaining: number }
  /** No trustworthy time: show no countdown at all. */
  | { kind: "unknown" }

/** Only the engine's value is displayed. The caller passes effectRemaining() output, which applies trust gating. */
export function countdownState(effect: Pick<Effect, "durationSeconds">, remaining: number | undefined): CountdownState {
  if (remaining === undefined) return { kind: "unknown" }
  const duration = effect.durationSeconds
  if (duration === undefined || !Number.isFinite(duration) || duration <= 0) return { kind: "remaining-only", remaining }
  return { kind: "timed", remaining, duration }
}

/** Remaining fraction for the progress bar. A remaining time above the known duration is shown as full, never above. */
export function countdownFraction(state: CountdownState): number | undefined {
  return state.kind === "timed" ? Math.min(1, Math.max(0, state.remaining / state.duration)) : undefined
}

export interface EffectRowModel {
  key: string
  effectId?: number
  polarity: string
  stackCount?: number
  countdown: CountdownState
  /** Changes when the engine reports the effect updated; drives the refresh animation only. */
  refreshToken: string
  stale: boolean
}

export function effectRow(effect: Effect, remaining: number | undefined): EffectRowModel {
  return {
    key: effect.key,
    effectId: effect.effectId,
    polarity: effect.polarity,
    stackCount: effect.stackCount,
    countdown: countdownState(effect, remaining),
    refreshToken: effect.lastUpdatedAt === effect.firstObservedAt ? "" : effect.lastUpdatedAt,
    stale: effect.stale === true,
  }
}
