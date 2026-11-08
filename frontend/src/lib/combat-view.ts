import type { GameAssetKind } from "../design/assets.ts"
import type { SkillBreakdown, SkillRow } from "./combat-data"
import type { Effect, Encounter, Role, Skill, UnassignedSkill, Vehicle, ViewSnapshot } from "./snapshot"

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

/** A ranking row the user picked, tied to the encounter (or capture interval) it was picked in. */
export interface DetailSelection {
  scope: string
  key: string
}

/**
 * The identity the detail selection belongs to: the engine's encounter ID (unique per capture session and statistics
 * interval), or the capture interval itself before any encounter exists. Damage totals and snapshot sequence play no part.
 */
export function selectionScope(view: Pick<ViewSnapshot, "captureSessionId" | "statisticsIntervalId"> | null | undefined, encounter: Pick<Encounter, "encounterId"> | undefined): string {
  if (encounter) return `encounter:${encounter.encounterId}`
  return view ? `interval:${view.captureSessionId}-${view.statisticsIntervalId}` : "none"
}

/** The highest-ranked character with confirmed damage. Ties already follow the stable rank order; no damage, no row. */
export function topRole(rows: readonly CombatRowModel[]): CombatRowModel | undefined {
  let top: CombatRowModel | undefined
  for (const row of rows) if (row.kind === "role" && row.damage > 0 && (top === undefined || row.rank < top.rank)) top = row
  return top
}

/**
 * The row the detail shows. A pick made in this scope stays while its row exists, whatever the ranking does; otherwise
 * the detail follows the current top character. A pick from an earlier encounter never carries over.
 */
export function detailRow(rows: readonly CombatRowModel[], scope: string, manual: DetailSelection | null): { row: CombatRowModel | undefined; mode: "manual" | "auto" } {
  if (manual && manual.scope === scope) {
    const picked = rows.find(row => row.key === manual.key)
    if (picked) return { row: picked, mode: "manual" }
  }
  return { row: topRole(rows), mode: "auto" }
}

const skillIconKinds: readonly GameAssetKind[] = ["skill", "skill-config", "echo-skill"]
function isSkillIconKind(kind: string | undefined): kind is GameAssetKind {
  return skillIconKinds.includes(kind as GameAssetKind)
}

/** The Echo an unassigned row's hits belong to, when the engine could name it from their DamageIds. */
function unassignedSkillSource(unassigned: UnassignedSkill): Pick<NonNullable<SkillBreakdown["unassigned"]>, "name" | "iconKind" | "iconId"> {
  const named = unassigned.nameStatus === "DERIVED" && unassigned.nameSource === "echo" && unassigned.name
  return {
    ...(named ? { name: unassigned.name } : {}),
    ...(isSkillIconKind(unassigned.iconKind) && unassigned.iconId !== undefined ? { iconKind: unassigned.iconKind, iconId: unassigned.iconId } : {}),
  }
}

/**
 * The engine's skill split of one contributor. A name is shown only when Master reports the skill's own VERIFIED name
 * or a DERIVED name of a related record; a DERIVED row keeps its source so the table can mark it.
 */
export function skillBreakdown(contributor: { skills?: Skill[]; unassignedSkill?: UnassignedSkill }): SkillBreakdown | undefined {
  if (contributor.skills === undefined) return undefined
  const rows = contributor.skills.map(skill => {
    // A DERIVED name without a known source is not shown: the table could not say what it is.
    const source = skill.nameSource
    const derived: SkillRow["derived"] = skill.nameStatus === "DERIVED" && (source === "damage_list" || source === "tune_break" || source === "echo") ? source : undefined
    const named = skill.name !== "" && (skill.nameStatus === "VERIFIED" || derived !== undefined)
    return {
      skillId: skill.skillId,
      name: named ? skill.name : undefined,
      ...(named && derived ? { derived } : {}),
      ...(isSkillIconKind(skill.iconKind) && skill.iconId !== undefined ? { iconKind: skill.iconKind, iconId: skill.iconId } : {}),
      damage: skill.damageDealt,
      events: skill.outgoingEvents,
      share: skill.damageShare,
    }
  })
  const unassigned = contributor.unassignedSkill
  return { rows, unassigned: unassigned && unassigned.outgoingEvents > 0 ? { ...unassignedSkillSource(unassigned), damage: unassigned.damageDealt, events: unassigned.outgoingEvents, share: unassigned.damageShare } : undefined }
}

/** Skill splits by ranking-row key, from the same encounter (or interval) the ranking rows come from. */
export function skillBreakdowns(source: Pick<Encounter, "roles" | "vehicle">): Map<string, SkillBreakdown> {
  const result = new Map<string, SkillBreakdown>()
  for (const role of source.roles) {
    const breakdown = skillBreakdown(role)
    if (breakdown) result.set(`role-${role.playerId}-${role.roleId}`, breakdown)
  }
  const vehicle = source.vehicle && skillBreakdown(source.vehicle)
  if (source.vehicle && vehicle) result.set(`vehicle-${source.vehicle.kind}`, vehicle)
  return result
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
  /** The game draws this effect with an icon of its own (Go ViewEffect.HUDIcon); only then is an icon requested. */
  hudIcon: boolean
  /** The icon is shared by many definitions (Go ViewEffect.HUDIconGeneric): it is drawn at a lower visual weight. */
  hudIconGeneric: boolean
  /** The element the effect's own definition states; undefined keeps the neutral color. */
  elementId?: number
  /** The engine's EffectKind; it chooses the neutral mark of an effect without a game icon. */
  kind: string
  stackCount?: number
  countdown: CountdownState
  stale: boolean
}

export function effectRow(effect: Effect, remaining: number | undefined): EffectRowModel {
  return {
    key: effect.key,
    effectId: effect.effectId,
    hudIcon: effect.hudIcon === true,
    hudIconGeneric: effect.hudIconGeneric === true,
    elementId: effect.elementId,
    kind: effect.kind,
    stackCount: effect.stackCount,
    countdown: countdownState(effect, remaining),
    stale: effect.stale === true,
  }
}
