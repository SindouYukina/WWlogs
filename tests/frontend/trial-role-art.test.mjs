import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { combatRows } from "../../frontend/src/lib/combat-view.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const role = (roleId, extra = {}) => ({ playerId: 1, roleId, elementId: 6, name: "R", nameStatus: "VERIFIED", directDamage: 10, summonDamage: 0, damageDealt: 10, damageTaken: 0, rank: 0, ...extra })

test("a trial character's row shows the art Go resolved for it, and keeps its own RoleId and key", () => {
  const [trial] = combatRows({ roles: [role(13127004, { staticRoleId: 1312 })] })
  assert.equal(trial.roleId, 13127004)
  assert.equal(trial.staticRoleId, 1312)
  assert.equal(trial.key, "role-1-13127004")
  // Before the display projection fills it, a row shows the art of its own RoleId, as it always did.
  const [plain] = combatRows({ roles: [role(1505)] })
  assert.equal(plain.staticRoleId, 1505)
})

test("every character image is addressed by the static role, never by the runtime RoleId", () => {
  const sources = { dashboard: read("frontend/src/views/DashboardView.tsx"), combat: read("frontend/src/design/components/combat.tsx"), history: read("frontend/src/views/HistoryAnalysis.tsx") }
  assert.match(sources.dashboard, /<CharacterAvatar roleId=\{row\.staticRoleId\}/)
  assert.match(sources.dashboard, /<GameAssetImage kind="character-portrait" id=\{selected\.staticRoleId\} \/>/)
  assert.match(sources.combat, /<CharacterAvatar roleId=\{row\.staticRoleId\}/)
  assert.match(sources.history, /<CharacterAvatar roleId=\{who\?\.staticRoleId\}/)
  for (const [name, text] of Object.entries(sources)) {
    assert.doesNotMatch(text, /<CharacterAvatar roleId=\{[^}]*\.roleId\}/, name)
    assert.doesNotMatch(text, /kind="character-(avatar|portrait)" id=\{[^}]*\.roleId\}/, name)
  }
})
