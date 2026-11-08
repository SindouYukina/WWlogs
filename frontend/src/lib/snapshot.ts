// Types of the snapshot the Go host publishes (internal/service.Snapshot and internal/capture.ViewSnapshot).
// Every number here was computed by the Go statistics engine; the frontend only formats it.

import type { AppIdentity } from "./build-identity"

export type ServiceState = "stopped" | "starting" | "running" | "stopping" | "error"
export type IntegrityStatus = "INTACT" | "DEGRADED" | "UNRELIABLE"
export type DPSStatus = "available" | "lower_bound" | "unreliable" | "unavailable" | "insufficient_data" | "invalid_duration"

export interface Snapshot {
  locale: string
  localeRevision: number
  sequence: number
  publishedAt: string
  service: ServiceStatus
  view: ViewSnapshot | null
  /** Host state published with every snapshot. It can change while the core sequence stays the same. */
  listening?: ListeningInfo
  theme?: string
  reduceMotion?: boolean
  /** The one live combat data cadence of every window (Settings, General), in milliseconds. */
  updateIntervalMs?: number
  overlays?: OverlayStates
  settingsWarning?: string
}

/** The selectable live combat data cadences, in milliseconds. The host only reports one of these. */
export const updateIntervals = [250, 500, 750, 1000] as const
export type UpdateInterval = (typeof updateIntervals)[number]
export const defaultUpdateInterval: UpdateInterval = 250

export function normalizeUpdateInterval(value: unknown): UpdateInterval {
  return updateIntervals.includes(value as UpdateInterval) ? (value as UpdateInterval) : defaultUpdateInterval
}

export type OverlayKind = "combat" | "effects"

/** One Overlay window. Editing is transient: it is never saved, so a restart returns to Normal Mode. */
export interface OverlayState {
  visible: boolean
  /** Surface opacity in percent. Text stays fully readable. */
  opacity: number
  /** Display scale in percent, 75 to 200: the window and everything in it. Placement and manual resizing are kept. */
  scale: number
  alwaysOnTop: boolean
  clickThrough: boolean
  editing: boolean
}

export type OverlayStates = Record<OverlayKind, OverlayState>

export type ListeningStateName = "off" | "waiting" | "establishing" | "active" | "stale" | "error" | "closed"

/** Classification only. The host never sends key material or anything derived from it. */
export interface ListeningInfo {
  state: ListeningStateName
  reason: string
  since: string
}

type ShellFields = "listening" | "theme" | "reduceMotion" | "updateIntervalMs" | "overlays" | "settingsWarning"

/** True when only host state differs, so the window updates it without treating the snapshot as new core data. */
export function shellChanged(next: Pick<Snapshot, ShellFields>, previous: Pick<Snapshot, ShellFields>): boolean {
  return next.listening?.state !== previous.listening?.state || next.listening?.reason !== previous.listening?.reason || next.theme !== previous.theme || next.reduceMotion !== previous.reduceMotion || next.settingsWarning !== previous.settingsWarning
    || next.updateIntervalMs !== previous.updateIntervalMs
    || JSON.stringify(next.overlays) !== JSON.stringify(previous.overlays)
}

/** Copies host state onto the current core snapshot, for a heartbeat whose sequence did not move. */
export function withShell(current: Snapshot, next: Pick<Snapshot, ShellFields>): Snapshot {
  return { ...current, listening: next.listening, theme: next.theme, reduceMotion: next.reduceMotion, updateIntervalMs: next.updateIntervalMs, overlays: next.overlays, settingsWarning: next.settingsWarning }
}

// Locale changes may reproject the same core sequence; neither dimension may move backwards.
export function snapshotSupersedes(next: Pick<Snapshot, "sequence" | "localeRevision">, previous: Pick<Snapshot, "sequence" | "localeRevision">, minimumRevision = 0): boolean {
  return next.sequence >= previous.sequence && next.localeRevision >= Math.max(previous.localeRevision, minimumRevision)
    && (next.sequence > previous.sequence || next.localeRevision > previous.localeRevision)
}

export interface ServiceStatus {
  state: ServiceState
  session?: SessionInfo
  startedAt?: string
  lastError?: string
  lastResult?: SessionResult
  app: AppIdentity
}

export interface SessionInfo {
  captureSessionId: number
  source: "live" | "replay"
  localIPv4: string
  handle?: string
  diagnosticsWarning?: string
  diagnostics: boolean
}

export interface SessionResult {
  stopReason: string
  udpPackets: number
  diagnosticsSummary?: string
}

export interface DataIntegrity {
  status: IntegrityStatus
  risks: string[]
  transportLosses: number
  unrecoveredLosses: number
  contextDowngradedEvents: number
}

export interface DPS {
  status: DPSStatus
  reason?: string
  damage: number
  durationSeconds: number
  value: number
  confidence?: string
}

export interface Role {
  playerId: number
  roleId: number
  name: string
  nameStatus: string
  /** Static Master element, from the display projection. Absent means unknown. */
  elementId?: number
  damageDealt: number
  damageShare?: number
  directDamage: number
  summonDamage: number
  outgoingEvents: number
  damageTaken: number
  incomingEvents: number
  /** Damage split by DamageContext.SkillId, ordered by damage; absent from hosts older than skill breakdown. */
  skills?: Skill[]
  unassignedSkill?: UnassignedSkill
}

/** One DamageContext.SkillId of a contributor. The share uses the contributor's own confirmed damage. */
export interface Skill {
  skillId: number
  /**
   * Master name in the selected locale. nameStatus VERIFIED is the skill's own official name; DERIVED is the official
   * name of the related record nameSource names (a UI skill whose damage list holds the hits, or the casting Echo);
   * UNKNOWN leaves it empty.
   */
  name: string
  nameStatus: string
  /** "official", "damage_list", "tune_break" or "echo"; absent when there is no name. */
  nameSource?: string
  /** The row's image, resolved separately from its name. */
  iconKind?: string
  iconId?: number
  damageDealt: number
  damageShare?: number
  outgoingEvents: number
}

/** Confirmed damage of a contributor without a SkillId (or beyond the engine's row bound). */
export interface UnassignedSkill {
  damageDealt: number
  outgoingEvents: number
  missingSkillEvents: number
  overflowEvents: number
  damageShare?: number
  /** The Echo these hits belong to, DERIVED from their DamageIds; the hits still have no skill row of their own. */
  name?: string
  nameStatus?: string
  nameSource?: string
  iconKind?: string
  iconId?: number
}

export interface UnassignedRole {
  damageDealt: number
  outgoingEvents: number
  damageTaken: number
  incomingEvents: number
  overflowEvents: number
}

export interface Encounter {
  elapsedSeconds?: number
  encounterId: string
  sequence: number
  protocolSession: number
  state: string
  outcome: string
  startedAt: string
  endedAt: string
  totals: { damageDealt: number; damageTaken: number; unattributedOutgoingDamage: number; unattributedIncomingDamage: number }
  integrity: DataIntegrity
  dps: DPS
  /** The highest 1-second interval of the encounter's DPS timeline, per 1 second. */
  peakDps?: DPS
  /** The local team's healing within the encounter. */
  healing?: Healing
  roles: Role[]
  vehicle?: Vehicle
  unassignedRole: UnassignedRole
  unassignedRoleShare?: number
}

/**
 * The local team's healing: healing results from a local source to a local target. Effective is the HP the server
 * applied and is what the UI shows; Status follows data integrity as DPS does.
 */
export interface Healing {
  status: DPS["status"]
  reason?: string
  effective: number
  reported: number
  overheal: number
  events: number
  unknownHealerEffective: number
  unknownHealerEvents: number
}

/** Healing a user may see: available, or a lower bound when the data is degraded. */
export function usableHealing(healing: Healing | undefined): healing is Healing {
  return healing?.status === "available" || healing?.status === "lower_bound"
}

export interface Effect {
  key: string
  effectId?: number
  termName?: string
  termId?: number
  termStatus?: string
  holderRelation: string
  holderRoleId?: number
  holderName?: string
  holderNameStatus?: string
  sourceRelation: string
  polarity: string
  combatClass: string
  /** What the definition says the effect is for (Go EffectKind). */
  kind: string
  /** The game draws this effect in a HUD buff bar with an icon of its own; it says nothing about its meaning. */
  hudIcon?: boolean
  /** The game shares this icon across many definitions, so it does not identify the effect. */
  hudIconGeneric?: boolean
  /** The element the effect's own definition states (ElementInfo id). Absent means none is established. */
  elementId?: number
  /** The engine's display decision; listed effects are always eligible. */
  display: { eligibility: string; target?: "team" | "enemy" | "unresolved" }
  stackCount?: number
  lifecycle: string
  durationSeconds?: number
  remainingStatus: string
  remainingSeconds?: number
  /** Capture time the engine expects the effect to end. It changes only when the engine reports a new time. */
  expectedExpiry?: string
  stale?: boolean
  /** How many tracker instances this cell stands for: one effect on several holders, or one bonus per element. */
  instances?: number
  /** The game keeps refreshing this modifier, so its remaining time is a refresh period rather than a time left. */
  maintained?: boolean
  firstObservedAt: string
  lastUpdatedAt: string
  endedAt?: string
  evidence: string
}

/**
 * How the engine partitioned its tracked effects (Go ViewEffectCounts). `eligible` counts instances and `cells` the
 * overlay cells they form: active lists `listedCells` and `truncatedCells` are the rest. Every other count is a reason an
 * effect was not eligible.
 */
export interface EffectCounts {
  tracked: number
  estimatedExpired: number
  active: number
  notLocal: number
  local: number
  nonCombat: number
  internal: number
  unclassified: number
  passive: number
  predictedExpired: number
  eligible: number
  listed: number
  truncated: number
  cells: number
  listedCells: number
  truncatedCells: number
}

export interface ViewSnapshot {
  schemaVersion: number
  captureSessionId: number
  statisticsIntervalId: number
  protocolSession: number
  captureObservedAt: string
  final: boolean
  health: {
    capture: { source: string; packetsRead: number; kernelDropped: { status: string; value: number }; diagnosticDropped?: { status: string; value: number } }
    transport: { state: string }
    context: { state: "VALID" | "SUSPECT"; downgradedEvents: number; revalidatedEvents: number }
    dataIntegrity: IntegrityStatus
    rpcPairing: { requests: number; paired: number; pairedOutOfOrder: number; unpaired: number; heldResponses: number }
    session: { keyReady: boolean; handshakes: number; handshakeErrors: number; lastHandshakeFailed: boolean; framesObserved: number; consecutiveDecryptFailures: number; firstFrameAgeSeconds?: number; frameIdleSeconds?: number }
  }
  statistics: {
    damageDealt: number
    damageTaken: number
    outgoingEvents: number
    incomingEvents: number
    unattributed: { outgoingDamage: number; incomingDamage: number; outgoingEvents: number; incomingEvents: number }
    duplicateEvents: number
    integrity: DataIntegrity
    encounterDps: DPS
    damageSpanDps: DPS
    rolling5sDps: DPS
    rolling10sDps: DPS
    healing?: Healing
  }
  encounters: { state: string; battleState: string; current?: Encounter; recent: Encounter[]; completedTotal: number }
  roles: Role[]
  vehicle?: Vehicle
  unassignedRole: UnassignedRole
  unassignedRoleShare?: number
  effects: { status: string; reason?: string; active: Effect[]; recentEnded: Effect[]; counts: EffectCounts; clockStalled?: boolean }
  /** DPS over time of the encounter shown (encounters.current, else the newest recent one); absent before any. */
  timeline?: Timeline
}

/** Who confirmed local damage was credited to (Go TimelineContributor). */
export interface TimelineContributor {
  kind: "role" | "vehicle" | "unassigned"
  playerId?: number
  roleId?: number
  vehicleKind?: string
}

/** One interval of the engine's DPS timeline (Go TimelinePoint). dps is null when the duration is zero. */
export interface TimelinePoint {
  startSeconds: number
  durationSeconds: number
  state: "complete" | "in_progress" | "partial"
  damage: number
  dps: number | null
  /** Aligned with Timeline.contributors. */
  contributorDamage: number[]
}

/** An encounter's DPS timeline (Go EncounterTimelineSnapshot); its points add up to the encounter's confirmed damage. */
export interface Timeline {
  encounterId: string
  final: boolean
  intervalSeconds: number
  startedAt: string
  endAt: string
  durationSeconds: number
  damageDealt: number
  contributors: TimelineContributor[]
  points: TimelinePoint[]
}

// The connection to the Go host, judged by the frontend. "disconnected" means calls to the host fail.
export type Connection = "connecting" | "connected" | "disconnected"

// The display state of a snapshot, from most to least usable.
export type DataState = "live" | "degraded" | "untrusted" | "no_encounter" | "not_capturing" | "no_data"

// dataState classifies what the UI may show. It never upgrades data: an untrusted interval stays untrusted.
export function dataState(snapshot: Snapshot | null): DataState {
  if (!snapshot || !snapshot.view) {
    return snapshot && snapshot.service.state !== "running" ? "not_capturing" : "no_data"
  }
  if (snapshot.service.state !== "running" && snapshot.service.state !== "stopping") {
    return "not_capturing"
  }
  const view = snapshot.view
  if (view.health.dataIntegrity === "UNRELIABLE" || view.health.context.state !== "VALID") {
    return "untrusted"
  }
  if (view.health.dataIntegrity === "DEGRADED") return "degraded"
  if (!view.encounters.current && view.encounters.recent.length === 0) {
    return "no_encounter"
  }
  return "live"
}

// dpsText shows a DPS value only when the engine says it may be shown; otherwise it shows the status.
export function dpsText(dps: DPS | undefined, locale = "en-US"): string {
  if (!dps) return "—"
  switch (dps.status) {
    case "available":
      return Number.isFinite(dps.value) ? Math.round(dps.value).toLocaleString(locale) : "unavailable"
    case "lower_bound":
      return Number.isFinite(dps.value) ? `≥ ${Math.round(dps.value).toLocaleString(locale)}` : "unavailable"
    default:
      return dps.status
  }
}

export function numberText(value: number | undefined, locale = "en-US"): string {
  return value === undefined ? "—" : value.toLocaleString(locale)
}

// roleLabel never invents a name: an unresolved RoleId is shown as UNKNOWN with its raw id.
export function roleLabel(role: Role): string {
  return role.nameStatus === "VERIFIED" && role.name !== "" ? role.name : `UNKNOWN (${role.roleId})`
}

// Vehicle rows are contributors, never synthetic Role IDs or character identities.
export interface Vehicle {
 kind: string
 evidence: string
 damageDealt: number
 outgoingEvents: number
 damageShare?: number
 skills?: Skill[]
 unassignedSkill?: UnassignedSkill
}

export interface DamageContributor {
 key: string
 role?: Role
 vehicle?: Vehicle
 damageDealt: number
 damageTaken: number
 damageShare?: number
}

export function damageContributors(roles: Role[], vehicle?: Vehicle): DamageContributor[] {
 const rows: DamageContributor[] = roles.map(role=>({key:`role-${role.playerId}-${role.roleId}`,role,damageDealt:role.damageDealt,damageTaken:role.damageTaken,damageShare:role.damageShare}))
 if(vehicle)rows.push({key:`vehicle-${vehicle.kind}`,vehicle,damageDealt:vehicle.damageDealt,damageTaken:0,damageShare:vehicle.damageShare})
 return rows.sort((a,b)=>b.damageDealt-a.damageDealt)
}
