import type { SkillRow } from "../lib/combat-data"
import type { Effect, Encounter, ListeningInfo, Role, Snapshot, UnassignedRole, Vehicle } from "../lib/snapshot"
import { masterEffects, masterRoles } from "./master-fixture.ts"

// Scripted numbers for visual review, shaped exactly like the engine's Snapshot so that the Showcase runs the same
// adapter and trust-gating code as production views. This is not a combat engine: nothing here parses, attributes or
// calculates real data, and it is never reachable from Public builds.

export const historySlots = 61
/** The one local player of the scripted feed. */
export const mockPlayerId = 1

/** Every status a window must be able to show. The values are scripted; the real ones come from internal/service. */
export const listeningScenarios = ["active", "establishing", "waiting", "traffic_without_key", "stale", "recovering", "error", "closed", "off"] as const
export type ListeningScenario = (typeof listeningScenarios)[number]

const scenarioStatus: Record<ListeningScenario, Pick<ListeningInfo, "state" | "reason">> = {
  active: { state: "active", reason: "receiving" },
  establishing: { state: "establishing", reason: "handshake_in_progress" },
  waiting: { state: "waiting", reason: "no_game_traffic" },
  traffic_without_key: { state: "waiting", reason: "traffic_without_key" },
  stale: { state: "stale", reason: "no_recent_frames" },
  recovering: { state: "active", reason: "transport_recovering" },
  error: { state: "error", reason: "decrypt_failing" },
  closed: { state: "closed", reason: "game_process_ended" },
  off: { state: "off", reason: "capture_off" },
}

export interface MockOutput {
  snapshot: Snapshot
  /** The Snapshot has no per-role time series yet; see the Phase 1 gap list. */
  history: ReadonlyMap<number, ReadonlyArray<number | null>>
  skillsOf: (roleId: number) => SkillRow[]
}

interface MockRole {
  roleId: number
  damage: number
  base: number
  burstUntil: number
  taken: number
  window: number[]
}

interface MockEffect {
  key: string
  effectId: number
  termNames?: Readonly<Record<string, string>>
  termId?: number
  duration?: number
  remaining?: number
  stacks?: number
  polarity: "buff" | "debuff" | "unclassified"
  holderRoleId?: number
  holderRelation: string
  firstObservedAt: string
  lastUpdatedAt: string
  unknownTimer: boolean
}

function seededRandom(seed: number) {
  let state = seed
  return () => {
    state = (state + 0x6d2b79f5) | 0
    let mixed = Math.imul(state ^ (state >>> 15), 1 | state)
    mixed = (mixed + Math.imul(mixed ^ (mixed >>> 7), 61 | mixed)) ^ mixed
    return ((mixed ^ (mixed >>> 14)) >>> 0) / 4294967296
  }
}

const baseDps = [14800, 8600, 5200, 11000]
const skillWeights = [0.38, 0.27, 0.18, 0.11, 0.06]
const skillHitSize = [6400, 3800, 11200, 2200, 15800]

export class MockFeed {
  private random = seededRandom(0x57_57)
  private sequence = 1
  private encounterSequence = 1
  private elapsed = 0
  private state: "running" | "ended" | "idle" = "running"
  private roles: MockRole[] = []
  private history = new Map<number, Array<number | null>>()
  private historyAccumulator = 0
  private effects = new Map<string, MockEffect>()
  private effectCounter = 0
  private vehicle: Vehicle | undefined
  private unassignedDamage = 0
  private untrusted = false
  private lastPublish = Date.now()
  effectAuto = true
  /** Display names follow this locale, as the production display projection follows the global locale. */
  locale = "en"
  listeningScenario: ListeningScenario = "active"
  private clockMs = Date.UTC(2026, 10, 6, 9, 0, 0)

  constructor() {
    this.resetEncounter()
    this.addEffect("timed")
    this.addEffect("timed")
    this.addEffect("timed")
    this.addEffect("unknown")
  }

  resetEncounter(): void {
    this.encounterSequence += 1
    this.elapsed = 0
    this.state = "running"
    this.vehicle = undefined
    this.unassignedDamage = 0
    this.roles = masterRoles.slice(0, 3).map((role, index) => ({ roleId: role.roleId, damage: 0, base: baseDps[index]!, burstUntil: 0, taken: 0, window: [] }))
    this.history = new Map(this.roles.map(role => [role.roleId, [] as Array<number | null>]))
    this.historyAccumulator = 0
  }

  endEncounter(): void {
    this.state = "ended"
  }

  setIdle(): void {
    this.state = "idle"
  }

  /** Scripts a large hit, which is the case rank swaps and fast counters have to handle gracefully. */
  burst(roleId: number): void {
    const role = this.roles.find(candidate => candidate.roleId === roleId)
    if (!role) return
    role.damage += 28000 + this.random() * 42000
    role.burstUntil = this.elapsed + 2.5
  }

  /** Lifts the last-placed role above the leader so a rank swap is visible on demand. */
  swapRanks(): void {
    const ordered = [...this.roles].sort((a, b) => a.damage - b.damage)
    const low = ordered[0]
    const high = ordered[ordered.length - 1]
    if (low && high) low.damage = high.damage * 1.04 + 1000
  }

  replaceRole(): void {
    const present = new Set(this.roles.map(role => role.roleId))
    const next = masterRoles.find(role => !present.has(role.roleId))
    const removed = this.roles[this.roles.length - 1]
    if (!next || !removed) return
    this.history.delete(removed.roleId)
    this.roles[this.roles.length - 1] = { roleId: next.roleId, damage: 0, base: baseDps[3]!, burstUntil: 0, taken: 0, window: [] }
    this.history.set(next.roleId, [])
  }

  toggleVehicle(): void {
    this.vehicle = this.vehicle ? undefined : { kind: "motorcycle", evidence: "mock", damageDealt: 12000, outgoingEvents: 8 }
  }

  toggleUnassigned(): void {
    this.unassignedDamage = this.unassignedDamage > 0 ? 0 : 18400
  }

  toggleUntrusted(): void {
    this.untrusted = !this.untrusted
  }

  addEffect(kind: "timed" | "unknown"): string {
    this.effectCounter += 1
    const master = masterEffects[this.effectCounter % masterEffects.length]!
    const key = `mock-${this.effectCounter}`
    const stamp = new Date(this.clockMs).toISOString()
    const duration = kind === "timed" ? 8 + Math.round(this.random() * 16) : undefined
    const hasTerm = "termNames" in master
    this.effects.set(key, {
      key,
      effectId: master.effectId,
      termNames: hasTerm ? master.termNames : undefined,
      termId: hasTerm ? master.termId : undefined,
      duration,
      remaining: duration,
      stacks: master.stackLimit > 1 ? 1 : undefined,
      // Illustrative only: production shows a polarity only when the engine has proven it.
      polarity: (["buff", "debuff", "unclassified"] as const)[this.effectCounter % 3]!,
      holderRoleId: this.roles[this.effectCounter % this.roles.length]?.roleId,
      holderRelation: "local_role",
      firstObservedAt: stamp,
      lastUpdatedAt: stamp,
      unknownTimer: kind === "unknown",
    })
    return key
  }

  /** Oldest timed effect goes back to its full duration, as a server update would report. */
  refreshEffect(): void {
    const effect = [...this.effects.values()].find(candidate => candidate.remaining !== undefined)
    if (!effect) return
    effect.remaining = effect.duration
    effect.lastUpdatedAt = new Date(this.clockMs).toISOString()
  }

  stackEffect(): void {
    const effect = [...this.effects.values()].find(candidate => candidate.stacks !== undefined)
    if (effect) effect.stacks = (effect.stacks ?? 0) % 3 + 1
  }

  removeEffect(): void {
    const first = this.effects.keys().next()
    if (!first.done) this.effects.delete(first.value)
  }

  addMany(count: number): void {
    for (let i = 0; i < count; i++) this.addEffect("timed")
  }

  activeCount(): number {
    return this.effects.size
  }

  clearEffects(): void {
    this.effects.clear()
  }

  /** Advances the mock capture clock and returns a freshly published snapshot. */
  publish(dtSeconds: number): MockOutput {
    this.clockMs += dtSeconds * 1000
    if (this.state === "running") {
      this.elapsed += dtSeconds
      for (const role of this.roles) {
        // Each role swings on its own slow cycle, so ranks occasionally change without any button being pressed.
        const swing = 0.75 + 0.5 * Math.sin(this.elapsed / (3.2 + (role.roleId % 7) * 0.4) + role.roleId)
        const noise = 0.6 + this.random() * 0.8
        const boost = this.elapsed < role.burstUntil ? 2.1 : 1
        const gained = role.base * swing * noise * boost * dtSeconds
        role.damage += gained
        role.window.push(gained)
        role.taken += this.random() < 0.02 ? 900 + this.random() * 1600 : 0
      }
      this.historyAccumulator += dtSeconds
      if (this.historyAccumulator >= 1) {
        this.historyAccumulator -= 1
        for (const role of this.roles) {
          const series = this.history.get(role.roleId)
          if (series) {
            series.push(role.window.reduce((sum, value) => sum + value, 0))
            if (series.length > historySlots) series.shift()
          }
          role.window = []
        }
      }
      for (const effect of this.effects.values()) {
        if (effect.remaining === undefined) continue
        effect.remaining = Math.max(0, effect.remaining - dtSeconds)
        if (effect.remaining === 0) this.effects.delete(effect.key)
      }
      this.effectChurn(dtSeconds)
    }
    this.sequence += 1
    this.lastPublish = Date.now()
    const history = new Map([...this.history].map(([id, series]) => [id, [...series]]))
    return { snapshot: this.buildSnapshot(), history, skillsOf: roleId => this.skills(roleId) }
  }

  /** Keeps the review surface populated: expiring effects are replaced and some are refreshed before they end. */
  private effectChurn(dtSeconds: number): void {
    if (!this.effectAuto) return
    const timed = [...this.effects.values()].filter(effect => effect.remaining !== undefined)
    if (timed.length < 4 && this.random() < dtSeconds * 0.9) this.addEffect("timed")
    for (const effect of timed) {
      if (effect.stacks !== undefined && this.random() < dtSeconds * 0.08) this.stackEffectByKey(effect.key)
      if ((effect.remaining ?? 0) < 4 && this.random() < dtSeconds * 0.1) {
        effect.remaining = effect.duration
        effect.lastUpdatedAt = new Date(this.clockMs).toISOString()
      }
    }
  }

  private stackEffectByKey(key: string): void {
    const effect = this.effects.get(key)
    if (effect?.stacks !== undefined) effect.stacks = effect.stacks % 3 + 1
  }

  private localized(names: Readonly<Record<string, string>> | undefined): string | undefined {
    return names?.[this.locale] ?? names?.en
  }

  private skills(roleId: number): SkillRow[] {
    const master = masterRoles.find(role => role.roleId === roleId)
    const role = this.roles.find(candidate => candidate.roleId === roleId)
    if (!master || !role) return []
    return master.skills.map((skill, index) => {
      const damage = role.damage * (skillWeights[index] ?? 0)
      return { skillId: skill.skillId, name: this.localized(skill.names), damage, hits: Math.floor(damage / (skillHitSize[index] ?? 5000)), share: skillWeights[index] ?? 0 }
    })
  }

  private buildSnapshot(): Snapshot {
    const iso = (ms: number) => new Date(ms).toISOString()
    const durationSeconds = Math.max(this.elapsed, 0)
    const vehicleDamage = this.vehicle?.damageDealt ?? 0
    const roleDamage = this.roles.reduce((sum, role) => sum + role.damage, 0)
    const confirmed = roleDamage + vehicleDamage + this.unassignedDamage
    const masterOf = (roleId: number | undefined) => masterRoles.find(candidate => candidate.roleId === roleId)
    const roles: Role[] = this.roles.map(role => ({
      playerId: mockPlayerId,
      roleId: role.roleId,
      name: this.localized(masterOf(role.roleId)?.names) ?? "",
      nameStatus: "VERIFIED",
      elementId: masterOf(role.roleId)?.elementId,
      damageDealt: Math.round(role.damage),
      damageShare: confirmed > 0 ? role.damage / confirmed : undefined,
      directDamage: Math.round(role.damage * 0.92),
      summonDamage: Math.round(role.damage * 0.08),
      outgoingEvents: Math.round(role.damage / 4200),
      damageTaken: Math.round(role.taken),
      incomingEvents: Math.round(role.taken / 1200),
    }))
    const vehicle = this.vehicle ? { ...this.vehicle, damageShare: confirmed > 0 ? vehicleDamage / confirmed : undefined } : undefined
    const unassignedRole: UnassignedRole = { damageDealt: this.unassignedDamage, outgoingEvents: this.unassignedDamage > 0 ? 6 : 0, damageTaken: 0, incomingEvents: 0, overflowEvents: 0 }
    const integrity = { status: this.untrusted ? "UNRELIABLE" as const : "INTACT" as const, risks: [], transportLosses: 0, unrecoveredLosses: 0, contextDowngradedEvents: 0 }
    const dps = durationSeconds >= 1
      ? { status: "available" as const, damage: Math.round(confirmed), durationSeconds, value: confirmed / durationSeconds }
      : { status: "insufficient_data" as const, damage: Math.round(confirmed), durationSeconds, value: 0 }
    const encounter: Encounter = {
      elapsedSeconds: durationSeconds,
      encounterId: `mock-${this.encounterSequence}`,
      sequence: this.encounterSequence,
      protocolSession: 1,
      state: this.state === "running" ? "ACTIVE" : "ENDED",
      outcome: this.state === "running" ? "IN_PROGRESS" : "COMPLETED",
      startedAt: iso(this.clockMs - durationSeconds * 1000),
      endedAt: this.state === "running" ? "" : iso(this.clockMs),
      totals: { damageDealt: Math.round(confirmed), damageTaken: Math.round(roles.reduce((sum, role) => sum + role.damageTaken, 0)), unattributedOutgoingDamage: 0, unattributedIncomingDamage: 0 },
      integrity,
      dps,
      roles,
      vehicle,
      unassignedRole,
      unassignedRoleShare: confirmed > 0 && this.unassignedDamage > 0 ? this.unassignedDamage / confirmed : undefined,
    }
    const active: Effect[] = [...this.effects.values()].map(effect => ({
      key: effect.key,
      effectId: effect.effectId,
      termName: this.localized(effect.termNames),
      termId: effect.termId,
      termStatus: effect.termNames ? "CORRELATED" : undefined,
      holderRelation: effect.holderRelation,
      holderRoleId: effect.holderRoleId,
      holderName: this.localized(masterOf(effect.holderRoleId)?.names),
      holderNameStatus: "VERIFIED",
      sourceRelation: "unknown",
      polarity: effect.polarity,
      combatClass: "combat",
      stackCount: effect.stacks,
      lifecycle: "active",
      durationSeconds: effect.duration,
      remainingStatus: effect.unknownTimer ? "unknown" : "estimated",
      remainingSeconds: effect.remaining,
      firstObservedAt: effect.firstObservedAt,
      lastUpdatedAt: effect.lastUpdatedAt,
      evidence: "server_notify",
    }))
    const zeroDps = { status: "unavailable" as const, damage: 0, durationSeconds: 0, value: 0 }
    return {
      locale: this.locale,
      localeRevision: 0,
      sequence: this.sequence,
      publishedAt: new Date(this.lastPublish).toISOString(),
      listening: { ...scenarioStatus[this.listeningScenario], since: new Date(this.lastPublish).toISOString() },
      service: { state: "running", session: { captureSessionId: 1, source: "replay", localIPv4: "0.0.0.0", diagnostics: false }, app: { version: "showcase", buildNumber: 0, official: false, commit: "mock", dirty: "unknown", profile: "development" } },
      view: {
        schemaVersion: 1,
        captureSessionId: 1,
        statisticsIntervalId: this.encounterSequence,
        protocolSession: 1,
        captureObservedAt: iso(this.clockMs),
        final: false,
        health: {
          capture: { source: "mock", packetsRead: 0, kernelDropped: { status: "available", value: 0 } },
          transport: { state: "SYNCED" },
          context: { state: "VALID", downgradedEvents: 0, revalidatedEvents: 0 },
          dataIntegrity: integrity.status,
          session: { keyReady: true, handshakes: 1, handshakeErrors: 0, lastHandshakeFailed: false, framesObserved: 0, consecutiveDecryptFailures: 0 },
          rpcPairing: { requests: 0, paired: 0, pairedOutOfOrder: 0, unpaired: 0, heldResponses: 0 },
        },
        statistics: {
          damageDealt: Math.round(confirmed),
          damageTaken: encounter.totals.damageTaken,
          outgoingEvents: 0,
          incomingEvents: 0,
          unattributed: { outgoingDamage: 0, incomingDamage: 0, outgoingEvents: 0, incomingEvents: 0 },
          duplicateEvents: 0,
          integrity,
          encounterDps: dps,
          damageSpanDps: zeroDps,
          rolling5sDps: zeroDps,
          rolling10sDps: zeroDps,
        },
        encounters: { state: this.state === "idle" ? "IDLE" : "ACTIVE", battleState: "IN_BATTLE", current: this.state === "idle" ? undefined : encounter, recent: [], completedTotal: 0 },
        roles,
        vehicle,
        unassignedRole,
        effects: { status: "complete", active, recentEnded: [], omitted: 0 },
      },
    }
  }

  /** Static Master term lookups for a mock effect; UI gating still treats these as unconfirmed names. */
  termFor(key: string): Readonly<Record<string, string>> | undefined {
    return this.effects.get(key)?.termNames
  }
}
