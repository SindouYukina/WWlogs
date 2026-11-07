import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { combatRows, detailRow, selectionScope, topRole } from "../../frontend/src/lib/combat-view.ts"
import { motionVariables } from "../../frontend/src/design/motion/tokens.ts"

const read = path => readFileSync(path, "utf8")
const role = (roleId, damageDealt, playerId = 1) => ({ playerId, roleId, elementId: 1, name: `R${roleId}`, nameStatus: "VERIFIED", directDamage: damageDealt, summonDamage: 0, damageDealt, damageShare: undefined, damageTaken: 0 })
const encounter = (id, roles, vehicle) => ({ encounterId: id, roles, vehicle, dps: { status: "available", durationSeconds: 10 } })
const rowsOf = (...roles) => combatRows(encounter("1-1-1", roles))

test("the scope is the engine's encounter identity, or the capture interval before any encounter", () => {
  const view = { captureSessionId: 3, statisticsIntervalId: 2 }
  assert.equal(selectionScope(view, { encounterId: "3-2-5" }), "encounter:3-2-5")
  assert.equal(selectionScope(view, undefined), "interval:3-2")
  assert.equal(selectionScope({ captureSessionId: 4, statisticsIntervalId: 1 }, undefined), "interval:4-1", "a new capture session is a new scope")
  assert.equal(selectionScope({ captureSessionId: 3, statisticsIntervalId: 3 }, undefined), "interval:3-3", "a statistics reset is a new scope")
  assert.equal(selectionScope(null, undefined), "none")
})

test("auto mode shows the top character and follows a new leader", () => {
  const scope = "encounter:1-1-1"
  let rows = rowsOf(role(1001, 500), role(1002, 900))
  assert.equal(detailRow(rows, scope, null).row.roleId, 1002)
  assert.equal(detailRow(rows, scope, null).mode, "auto")
  rows = rowsOf(role(1001, 1500), role(1002, 900))
  assert.equal(detailRow(rows, scope, null).row.roleId, 1001, "the leader changed")
})

test("no confirmed character damage selects nothing, and a vehicle is never picked automatically", () => {
  assert.equal(topRole([]), undefined)
  assert.equal(topRole(rowsOf(role(1001, 0), role(1002, 0))), undefined, "an encounter without damage")
  const withVehicle = combatRows(encounter("1-1-1", [role(1001, 100)], { kind: "motorcycle", damageDealt: 900, damageShare: undefined }))
  assert.equal(withVehicle[0].kind, "vehicle")
  assert.equal(topRole(withVehicle).roleId, 1001)
  assert.equal(topRole(combatRows(encounter("1-1-1", [], { kind: "motorcycle", damageDealt: 900 }))), undefined)
})

test("equal damage resolves through the stable rank order", () => {
  const a = topRole(rowsOf(role(1003, 700, 2), role(1001, 700, 1)))
  const b = topRole(rowsOf(role(1001, 700, 1), role(1003, 700, 2)))
  assert.equal(a.key, b.key)
  assert.equal(a.key, "role-1-1001")
})

test("a manual pick holds within its encounter, even when overtaken, and ends with it", () => {
  const scope = "encounter:1-1-1"
  const manual = { scope, key: "role-1-1001" }
  let rows = rowsOf(role(1001, 500), role(1002, 900))
  assert.deepEqual([detailRow(rows, scope, manual).row.roleId, detailRow(rows, scope, manual).mode], [1001, "manual"])
  rows = rowsOf(role(1001, 500), role(1002, 5000))
  assert.equal(detailRow(rows, scope, manual).row.roleId, 1001, "ranking moved, the pick stays")
  const next = "encounter:1-1-2"
  const nextRows = combatRows(encounter("1-1-2", [role(1001, 100), role(1002, 300)]))
  assert.deepEqual([detailRow(nextRows, next, manual).row.roleId, detailRow(nextRows, next, manual).mode], [1002, "auto"], "a new encounter is automatic again")
  assert.equal(detailRow([], next, manual).row, undefined, "a new encounter before its first damage")
})

test("a pick whose row is gone falls back to the top character", () => {
  const scope = "interval:1-1"
  const rows = rowsOf(role(1002, 900))
  assert.deepEqual([detailRow(rows, scope, { scope, key: "role-1-1001" }).row.roleId, detailRow(rows, scope, { scope, key: "role-1-1001" }).mode], [1002, "auto"])
})

test("the Dashboard keeps the pick as view state and never writes it to the engine", () => {
  const view = read("frontend/src/views/DashboardView.tsx")
  assert.ok(view.includes("selectionScope(view, encounter)") && view.includes("detailRow(rows, detailScope, manual)"))
  assert.ok(view.includes("onSelect={pick}"))
  assert.ok(!/host\./.test(view), "the Dashboard does not call the host")
  assert.ok(!view.includes("rows[0]"), "the first row is no longer an implicit default")
})

test("both Overlay pages explain click-through under the setting name", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  assert.ok(view.includes('<SettingsRow title={t("clickThrough")} description={t("clickThroughHint")}>'))
  assert.ok(!view.includes('hint={<Hint text={t("clickThroughHint")}'))
  for (const locale of JSON.parse(read("frontend/locales/manifest.json")).supportedLocales) {
    const settings = JSON.parse(read(`frontend/locales/${locale}/settings.json`))
    assert.ok(settings.clickThroughHint && settings.clickThroughHint.length <= 100, `${locale}: one short description`)
  }
})

test("reduced motion stops counters and the low-time pulse, never the data", () => {
  const reduced = motionVariables({ preference: "reduced" })
  for (const [name, value] of Object.entries(reduced)) if (name.endsWith("-dur") || name.startsWith("--ww-dur-")) assert.equal(value, "0ms", name)
  const number = read("frontend/src/design/components/AnimatedNumber.tsx")
  assert.equal((number.match(/planned === "animate" && motionReduced\(\) \? "snap" : planned/g) ?? []).length, 2, "tween and roll counters snap")
  assert.ok(read("frontend/src/design/components.css").includes(':root[data-ww-motion="reduced"] .ww-effect[data-low="true"] .ww-effect-icon { animation: none; }'))
  // Only the user switch or the Windows preference decides; the update interval is a separate setting.
  const tokens = read("frontend/src/design/motion/tokens.ts")
  assert.ok(tokens.includes('options.preference ?? (query.matches ? "reduced" : "full")'))
  assert.ok(read("frontend/src/design/appearance.ts").includes('preference: reduceMotion ? "reduced" : undefined'))
})

test("the Desktop holds the pick across page switches and a new scope ends it for good", () => {
  const hook = read("frontend/src/lib/useDetailPick.ts")
  assert.ok(hook.includes("current && current.scope !== scope ? null : current"), "a scope change clears the pick instead of only hiding it")
  const app = read("frontend/src/views/DesktopApp.tsx")
  assert.ok(app.includes("useDetailPick(selectionScope(snapshot?.view, displayedEncounter(snapshot?.view)))") && app.includes("detailPick={detailPick}"))
  const view = read("frontend/src/views/DashboardView.tsx")
  assert.ok(view.includes("const [manual, pick] = detailPick ?? ownPick"), "a Dashboard without a holder (the Showcase) keeps its own")
})
