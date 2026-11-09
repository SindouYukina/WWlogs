import type { GameAssetKind } from "../design/assets.ts"
import type { SkillBreakdown, SkillRow } from "./combat-data"
import type { Effect, Encounter, Role, Skill, UnassignedSkill, Vehicle, ViewSnapshot } from "./snapshot"

// Adapts engine snapshot types to the row models the shared components render. Nothing here adds information: a value
// the engine did not report stays undefined, and unresolved identities keep their raw IDs. Damage, share, DPS and rank are
// the engine's (Go rankViewContributors), all from the one scope the rows came from.

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
  /** The engine's DPS of this row; undefined unless its scope has a DPS the UI may show. */
  dps?: number
  damageTaken: number
  /** The engine's rank, 0 is the most damage. */
  rank: number
}

function roleRow(role: Role): CombatRowModel {
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
    dps: role.dps,
    damageTaken: role.damageTaken,
    rank: role.rank,
  }
}

function vehicleRow(vehicle: Vehicle): CombatRowModel {
  return {
    key: `vehicle-${vehicle.kind}`,
    kind: "vehicle",
    name: null,
    damage: vehicle.damageDealt,
    share: vehicle.damageShare,
    dps: vehicle.dps,
    damageTaken: 0,
    rank: vehicle.rank,
  }
}

/**
 * The rows of one scope, an encounter or the capture interval, in the engine's order. Rows keep a stable identity while their
 * rank changes, which is what lets a rank swap animate instead of re-mount.
 */
export function combatRows(scope: Pick<Encounter, "roles" | "vehicle">): CombatRowModel[] {
  const rows = scope.roles.map(roleRow)
  if (scope.vehicle) rows.push(vehicleRow(scope.vehicle))
  return rows.sort((a, b) => a.rank - b.rank)
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

/** The Echo or UI skill the engine named an unassigned row after, from its DamageIds. */
function unassignedSkillSource(unassigned: UnassignedSkill): Pick<NonNullable<SkillBreakdown["unassigned"]>, "name" | "derived" | "iconKind" | "iconId"> {
  return {
    ...(unassigned.name ? { name: unassigned.name, derived: unassigned.nameSource as NonNullable<SkillBreakdown["unassigned"]>["derived"] } : {}),
    ...(isSkillIconKind(unassigned.iconKind) && unassigned.iconId !== undefined ? { iconKind: unassigned.iconKind, iconId: unassigned.iconId } : {}),
  }
}

/**
 * The engine's skill split of one contributor. Names, evidence grades and whether a row shows its ID are the engine's
 * decisions (ViewSkill); this only adapts the shape the table draws.
 */
export function skillBreakdown(contributor: { skills?: Skill[]; unassignedSkill?: UnassignedSkill }): SkillBreakdown | undefined {
  if (contributor.skills === undefined) return undefined
  const rows = contributor.skills.map(skill => ({
    skillId: skill.skillId,
    name: skill.name === "" ? undefined : skill.name,
    showId: skill.showSkillId === true,
    ...(skill.nameStatus === "DERIVED" ? { derived: skill.nameSource as SkillRow["derived"] } : {}),
    ...(isSkillIconKind(skill.iconKind) && skill.iconId !== undefined ? { iconKind: skill.iconKind, iconId: skill.iconId } : {}),
    damage: skill.damageDealt,
    events: skill.outgoingEvents,
    share: skill.damageShare,
  }))
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
  /** The element the effect's own definition states; undefined keeps the neutral color. */
  elementId?: number
  /** Whom the effect is on, from the engine's display decision. */
  target?: "team" | "enemy" | "unresolved"
  stackCount?: number
  countdown: CountdownState
  stale: boolean
}

export function effectRow(effect: Effect, remaining: number | undefined): EffectRowModel {
  return {
    key: effect.key,
    effectId: effect.effectId,
    hudIcon: effect.hudIcon === true,
    elementId: effect.elementId,
    target: effect.display.target,
    stackCount: effect.stackCount,
    countdown: countdownState(effect, remaining),
    stale: effect.stale === true,
  }
}
