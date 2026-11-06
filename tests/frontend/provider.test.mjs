import assert from "node:assert/strict"
import { existsSync, readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"
import test from "node:test"
import { dataChip, displayedEncounter, isFrozen, usableDps } from "../../frontend/src/lib/combat-data.ts"
import { combatRows, contributionShares, intervalRows } from "../../frontend/src/lib/combat-view.ts"
import { shellChanged } from "../../frontend/src/lib/snapshot.ts"

const now = Date.parse("2026-11-06T10:00:10Z")
const health = (dataIntegrity = "INTACT", context = "VALID") => ({ dataIntegrity, context: { state: context } })
const snapshot = (service, view) => ({ service: { state: service }, publishedAt: "2026-11-06T10:00:09Z", view })

test("the trust chip never upgrades data", () => {
  const view = { health: health(), encounters: { recent: [] } }
  const chip = (s, extra = {}) => dataChip({ snapshot: s, connection: "connected", now, ...extra })
  assert.equal(dataChip({ snapshot: null, connection: "connecting", now }).key, "connecting")
  assert.equal(dataChip({ snapshot: null, connection: "disconnected", now }).tone, "critical")
  assert.equal(chip(snapshot("stopped", null)).key, "notCapturing")
  assert.equal(chip(snapshot("running", null)).key, "waiting")
  assert.equal(chip(snapshot("running", view)).key, "noEncounter")
  assert.equal(chip(snapshot("running", { ...view, encounters: { current: {}, recent: [] } })).key, "live")
  assert.equal(chip(snapshot("running", { ...view, health: health("DEGRADED") })).key, "degraded")
  assert.equal(chip(snapshot("running", { ...view, health: health("UNRELIABLE") })).tone, "critical")
  assert.equal(chip(snapshot("running", { ...view, health: health("INTACT", "SUSPECT") })).key, "untrusted")
  assert.equal(chip(snapshot("running", { ...view, encounters: { current: {}, recent: [] } }), { paused: true }).key, "stale")
  const old = { ...snapshot("running", { ...view, encounters: { current: {}, recent: [] } }), publishedAt: "2026-11-06T09:59:00Z" }
  assert.equal(chip(old).key, "stale")
  assert.equal(chip({ ...old, view: { ...old.view, health: health("UNRELIABLE") } }).key, "untrusted", "stale must not hide an untrusted judgement")
})

test("a stalled, paused or disconnected provider freezes motion", () => {
  const fresh = snapshot("running", {})
  assert.equal(isFrozen({ snapshot: fresh, connection: "connected", now }), false)
  assert.equal(isFrozen({ snapshot: fresh, connection: "disconnected", now }), true)
  assert.equal(isFrozen({ snapshot: fresh, connection: "connected", now, paused: true }), true)
  assert.equal(isFrozen({ snapshot: fresh, connection: "connected", now: now + 60000 }), true)
})

test("the current encounter wins, then the last finished one, and nothing is invented", () => {
  assert.equal(displayedEncounter(null), undefined)
  assert.equal(displayedEncounter({ encounters: { recent: [] } }), undefined)
  assert.equal(displayedEncounter({ encounters: { current: "now", recent: ["old"] } }), "now")
  assert.equal(displayedEncounter({ encounters: { recent: ["older", "old"] } }), "old")
})

test("only an engine-approved DPS status is usable", () => {
  for (const status of ["available", "lower_bound"]) assert.equal(usableDps({ status }), true)
  for (const status of ["unreliable", "unavailable", "insufficient_data", "invalid_duration"]) assert.equal(usableDps({ status }), false)
  assert.equal(usableDps(undefined), false)
})

test("rows carry Master element and engine split without adding information", () => {
  const role = { playerId: 1, roleId: 7, name: "A", nameStatus: "VERIFIED", elementId: 3, damageDealt: 100, directDamage: 75, summonDamage: 25, damageTaken: 0 }
  const unknown = { ...role, roleId: 8, name: "Guess", nameStatus: "CORRELATED", elementId: undefined, damageDealt: 50, directDamage: 0, summonDamage: 0 }
  const rows = combatRows({ roles: [unknown, role], dps: { status: "available", durationSeconds: 10 } })
  assert.deepEqual(rows.map(row => [row.roleId, row.rank, row.elementId, row.name, row.dps]), [[7, 0, 3, "A", 10], [8, 1, undefined, null, 5]])
  assert.deepEqual(contributionShares(rows[0]), { direct: 0.75, summon: 0.25 })
  assert.equal(contributionShares(rows[1]), undefined, "no damage means no split, not a 0% claim")
  assert.equal(contributionShares({}), undefined, "a vehicle has no split")
})

test("interval rows have no per-role DPS, because there is no encounter duration", () => {
  const rows = intervalRows({ roles: [{ playerId: 1, roleId: 7, name: "A", nameStatus: "VERIFIED", damageDealt: 100, directDamage: 100, summonDamage: 0, damageTaken: 0 }], vehicle: { kind: "motorcycle", damageDealt: 200 } })
  assert.deepEqual(rows.map(row => [row.kind, row.rank, row.dps]), [["vehicle", 0, undefined], ["role", 1, undefined]])
})

test("window state reaches other windows without a new core sequence", () => {
  const base = { listening: { state: "active", reason: "receiving" }, theme: "electro", reduceMotion: false, overlays: { combat: { visible: true, opacity: 74 }, effects: { visible: false, opacity: 62 } } }
  assert.equal(shellChanged(base, structuredClone(base)), false)
  const moved = structuredClone(base)
  moved.overlays.effects.opacity = 50
  assert.equal(shellChanged(moved, base), true)
  assert.equal(shellChanged({ ...base, settingsWarning: "overlay_save_failed" }, base), true)
})

const sources = dir => readdirSync(dir, { recursive: true, withFileTypes: true }).filter(entry => entry.isFile() && /\.tsx?$/.test(entry.name)).map(entry => join(entry.parentPath, entry.name))

test("each product view has one implementation, and the Showcase reuses it", () => {
  for (const view of ["DashboardView", "SettingsView", "CombatOverlayPanel", "EffectsOverlayPanel"]) {
    assert.ok(existsSync(`frontend/src/views/${view}.tsx`), view)
    const definitions = sources("frontend/src").filter(file => new RegExp(`export function ${view}\\b`).test(readFileSync(file, "utf8")))
    assert.equal(definitions.length, 1, `${view}: ${definitions}`)
  }
  const showcase = sources("frontend/src/showcase").map(file => readFileSync(file, "utf8")).join("\n")
  for (const view of ["DashboardView", "SettingsView", "CombatOverlayPanel", "EffectsOverlayPanel", "OverlaySurface"]) {
    assert.match(showcase, new RegExp(`import \\{[^}]*\\b${view}\\b[^}]*\\} from "@/views/`), `${view} must be imported by the Showcase`)
  }
  for (const copy of ["DashboardSection", "SettingsSection", "PublicDashboard", "DevDashboard"]) {
    assert.equal(sources("frontend/src").some(file => file.includes(copy)), false, copy)
  }
})

test("no product source depends on a build profile", () => {
  for (const file of [...sources("frontend/src/views"), ...sources("frontend/src/lib"), ...sources("frontend/src/design")]) {
    assert.doesNotMatch(readFileSync(file, "utf8"), /__WWLOGS_SHOWCASE__|WWLOGS_FRONTEND_PROFILE/, file)
  }
})
