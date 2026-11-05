// Types of the snapshot the Go host publishes (internal/service.Snapshot and internal/capture.ViewSnapshot).
// Every number here was computed by the Go statistics engine; the frontend only formats it.

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
  app: { version: string; commit: string; profile: string }
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
  damageDealt: number
  damageShare?: number
  directDamage: number
  summonDamage: number
  outgoingEvents: number
  damageTaken: number
  incomingEvents: number
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
  roles: Role[]
  unassignedRole: UnassignedRole
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
  stackCount?: number
  lifecycle: string
  durationSeconds?: number
  remainingStatus: string
  remainingSeconds?: number
  stale?: boolean
  firstObservedAt: string
  lastUpdatedAt: string
  endedAt?: string
  evidence: string
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
  }
  encounters: { state: string; battleState: string; current?: Encounter; recent: Encounter[]; completedTotal: number }
  roles: Role[]
  unassignedRole: UnassignedRole
  effects: { status: string; reason?: string; active: Effect[]; recentEnded: Effect[]; omitted: number }
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
