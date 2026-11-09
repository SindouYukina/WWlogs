import assert from "node:assert/strict"
import { existsSync, readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"
import test from "node:test"
import { dataChip, displayedEncounter, isFrozen, usableDps } from "../../frontend/src/lib/combat-data.ts"
import { combatRows, intervalRows, skillBreakdown, skillBreakdowns } from "../../frontend/src/lib/combat-view.ts"
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
  assert.equal(rows[0].directDamage, 75)
  assert.equal(rows[0].summonDamage, 25)
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

test("skill rows are the engine's split, keyed like the ranking rows, with official names only", () => {
  const skills = [
    { skillId: 1409011, name: "Strike", nameStatus: "VERIFIED", showSkillId: false, damageDealt: 300, damageShare: 0.6, outgoingEvents: 4 },
    { skillId: 1409012, name: "", nameStatus: "UNKNOWN", showSkillId: true, damageDealt: 100, damageShare: 0.2, outgoingEvents: 1 },
  ]
  const unassignedSkill = { damageDealt: 100, outgoingEvents: 2, missingSkillEvents: 2, overflowEvents: 0, damageShare: 0.2 }
  const role = { playerId: 1, roleId: 1409, name: "A", nameStatus: "VERIFIED", damageDealt: 500, directDamage: 500, summonDamage: 0, damageTaken: 0, skills, unassignedSkill }
  const breakdown = skillBreakdown(role)
  assert.deepEqual(breakdown.rows, [
    { skillId: 1409011, name: "Strike", showId: false, damage: 300, events: 4, share: 0.6 },
    { skillId: 1409012, name: undefined, showId: true, damage: 100, events: 1, share: 0.2 },
  ])
  assert.deepEqual(breakdown.unassigned, { damage: 100, events: 2, share: 0.2 })
  const total = breakdown.rows.reduce((sum, row) => sum + row.damage, breakdown.unassigned.damage)
  assert.equal(total, role.damageDealt, "rows plus unassigned add up to the role")
  assert.equal(skillBreakdown({ ...role, unassignedSkill: { ...unassignedSkill, damageDealt: 0, outgoingEvents: 0 } }).unassigned, undefined)
  assert.equal(skillBreakdown({ ...role, skills: undefined }), undefined, "an old host without a skill field has no split")
  const map = skillBreakdowns({ roles: [role], vehicle: { kind: "motorcycle", damageDealt: 9, skills: [{ skillId: 7, name: "", nameStatus: "UNKNOWN", damageDealt: 9, outgoingEvents: 1 }] } })
  const keys = intervalRows({ roles: [role], vehicle: { kind: "motorcycle", damageDealt: 9 } }).map(row => row.key)
  assert.deepEqual([...map.keys()].sort(), [...keys].sort(), "every ranking row finds its own split")
  assert.equal(map.get("vehicle-motorcycle").rows[0].skillId, 7)
})

test("derived skill names keep the engine's source and are marked", () => {
  const skills = [
    { skillId: 1312050, name: "Liberation", nameStatus: "DERIVED", nameSource: "damage_list", showSkillId: true, damageDealt: 50, outgoingEvents: 2 },
    { skillId: 1209400, name: "Intro", nameStatus: "DERIVED", nameSource: "skill_type", showSkillId: true, damageDealt: 30, outgoingEvents: 3 },
    { skillId: 280026010, name: "Echo", nameStatus: "DERIVED", nameSource: "echo", showSkillId: true, damageDealt: 15, outgoingEvents: 3 },
    { skillId: 1312011, name: "Basic", nameStatus: "VERIFIED", nameSource: "official", showSkillId: false, damageDealt: 5, outgoingEvents: 1 },
  ]
  const rows = skillBreakdown({ skills }).rows
  assert.deepEqual(rows.map(row => [row.skillId, row.name, row.derived]), [
    [1312050, "Liberation", "damage_list"], [1209400, "Intro", "skill_type"], [280026010, "Echo", "echo"], [1312011, "Basic", undefined],
  ])
  assert.equal(rows.reduce((sum, row) => sum + row.damage, 0), 100, "naming never changes a row's damage")
  const dashboard = readFileSync("frontend/src/views/DashboardView.tsx", "utf8")
  assert.match(dashboard, /data-name-source=\{row\.derived\}/)
  assert.match(dashboard, /derivedSkillNote/)
  assert.match(dashboard, /derivedSkillTypeNote/)
})

test("whether a row shows its ID is the engine's decision, and an unassigned row never shows one", () => {
  const skills = [
    { skillId: 1, name: "Basic", nameStatus: "VERIFIED", nameSource: "official", showSkillId: false, damageDealt: 4, outgoingEvents: 1 },
    { skillId: 2, name: "Liberation", nameStatus: "DERIVED", nameSource: "damage_list", showSkillId: true, damageDealt: 3, outgoingEvents: 1 },
    { skillId: 4, name: "", nameStatus: "UNKNOWN", showSkillId: true, damageDealt: 1, outgoingEvents: 1 },
  ]
  const rows = skillBreakdown({ skills, unassignedSkill: { damageDealt: 5, outgoingEvents: 2 } })
  assert.deepEqual(rows.rows.map(row => [row.skillId, row.showId]), [[1, false], [2, true], [4, true]])
  assert.equal(rows.unassigned.name, undefined, "an unassigned row without evidence stays the unconfirmed row")
  const view = readFileSync("frontend/src/views/DashboardView.tsx", "utf8")
  assert.match(view, /row\.showId && <span className="ww-dash-skill-id/, "the ID is drawn only when the engine says so")
  const unassignedRow = view.slice(view.indexOf('data-field="unassigned-skill"'), view.indexOf("</tbody>"))
  assert.doesNotMatch(unassignedRow, /ww-dash-skill-id|skillId/, "an unassigned row never shows an ID")
  const adapter = readFileSync("frontend/src/lib/combat-view.ts", "utf8")
  assert.doesNotMatch(adapter.slice(adapter.indexOf("export function skillBreakdown"), adapter.indexOf("export function skillBreakdowns")), /nameStatus === "VERIFIED"/, "the frontend does not grade skill evidence")
})

test("an unassigned row keeps the name and source the engine gave it", () => {
  const named = source => skillBreakdown({ skills: [], unassignedSkill: { damageDealt: 5, outgoingEvents: 2, name: "X", nameStatus: "DERIVED", nameSource: source } }).unassigned
  assert.deepEqual([named("echo").name, named("echo").derived], ["X", "echo"])
  assert.deepEqual([named("damage_list").name, named("damage_list").derived], ["X", "damage_list"])
})

test("the Live provider and the Dashboard never compute or invent skill rows", () => {
  const provider = readFileSync("frontend/src/lib/useLiveCombatData.ts", "utf8")
  assert.match(provider, /skillBreakdowns\(/)
  const dashboard = readFileSync("frontend/src/views/DashboardView.tsx", "utf8")
  assert.match(dashboard, /data\.skillsOf\(selected\.key\)/)
  assert.doesNotMatch(dashboard, /mock|Mock/)
  const view = readFileSync("frontend/src/lib/combat-view.ts", "utf8")
  assert.doesNotMatch(view, /devLabel|dev_label/, "developer labels are not official skill names")
})

test("a skill row keeps its name source and its own icon, and a row may have one without the other", () => {
  const breakdown = skillBreakdown({
    skills: [
      { skillId: 1, name: "諧度破壞·長刃", nameStatus: "DERIVED", nameSource: "tune_break", showSkillId: true, sourceId: 9, iconKind: "skill-config", iconId: 9, damageDealt: 10, outgoingEvents: 1 },
      { skillId: 2, name: "共鳴迴響", nameStatus: "DERIVED", nameSource: "echo", sourceId: 60, iconKind: "echo-skill", iconId: 280026, damageDealt: 8, outgoingEvents: 1 },
      { skillId: 3, name: "", nameStatus: "UNKNOWN", iconKind: "skill", iconId: 3, damageDealt: 6, outgoingEvents: 1 },
      { skillId: 4, name: "", nameStatus: "UNKNOWN", iconKind: "weapon", iconId: 5, damageDealt: 1, outgoingEvents: 1 },
    ],
    unassignedSkill: { damageDealt: 7, outgoingEvents: 2, missingSkillEvents: 2, overflowEvents: 0, name: "鳴式·利維亞坦", nameStatus: "DERIVED", nameSource: "echo", iconKind: "echo-skill", iconId: 280033 },
  })
  assert.deepEqual(breakdown.rows[0], { skillId: 1, name: "諧度破壞·長刃", showId: true, derived: "tune_break", iconKind: "skill-config", iconId: 9, damage: 10, events: 1, share: undefined })
  assert.equal(breakdown.rows[1].derived, "echo")
  assert.equal(breakdown.rows[1].iconKind, "echo-skill")
  assert.equal(breakdown.rows[2].name, undefined, "an unnamed row still asks for its own icon")
  assert.equal(breakdown.rows[2].iconKind, "skill")
  assert.equal(breakdown.rows[3].name, undefined, "only verified or derived names are shown")
  assert.equal(breakdown.rows[3].iconKind, undefined, "an icon kind the host does not serve is dropped")
  assert.equal(breakdown.unassigned.name, "鳴式·利維亞坦")
  assert.equal(breakdown.unassigned.iconId, 280033)
  assert.equal(breakdown.unassigned.damage, 7)
  const plain = skillBreakdown({ skills: [], unassignedSkill: { damageDealt: 7, outgoingEvents: 2, missingSkillEvents: 2, overflowEvents: 0 } })
  assert.equal(plain.unassigned.name, undefined)
})
