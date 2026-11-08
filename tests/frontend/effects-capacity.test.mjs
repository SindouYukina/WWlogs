import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { compareEffectGroups, effectGridColumns, effectGroupOf, fitEffectGrid, unfittedEffectLimit } from "../../frontend/src/lib/effects.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
// The default 320 x 420 Effects window: 32 px tiles with 4 px gaps, 14 px chips with 3 px gaps, a 26 px holder mark.
const layout = (available, width = 312) => ({ available, width, head: 20, sectionGap: 6, hint: 18, tile: 32, tileGap: 4, chip: 14, chipGap: 3, blockGap: 4, groupHead: 26, groupHeadGap: 6, groupGap: 6 })
const role = (roleId, n, hudIcon = false) => Array.from({ length: n }, (_, i) => ({ key: `${roleId}-${hudIcon ? "t" : "c"}${i}`, display: { target: "team" }, holderRoleId: roleId, hudIcon }))
const enemy = (n, hudIcon = false) => Array.from({ length: n }, (_, i) => ({ key: `e-${i}`, display: { target: "enemy" }, hudIcon }))
const isTile = item => item.hudIcon === true
const fit = (listed, eligible, unclassified, size) => fitEffectGrid(listed, effectGroupOf, isTile, eligible, unclassified, size)
const drawn = grid => grid.groups.reduce((sum, group) => sum + group.tiles.length + group.chips.length, 0)

test("a group line holds as many whole tiles or chips as its width allows", () => {
  // 312 - 26 - 6 = 280 px: 7 tiles (248 px; an eighth would need 284) or 16 chips (269 px; a seventeenth 286).
  assert.equal(effectGridColumns(layout(400)), 7)
  assert.equal(effectGridColumns(layout(400), 14, 3), 16)
  assert.equal(effectGridColumns(layout(400, 316)), 8)
  assert.equal(effectGridColumns(layout(400, 40)), 1, "a very narrow window still lays out one item per line")
})

test("holders group in a fixed order: roles by RoleId, other team holders, enemies, unresolved", () => {
  assert.deepEqual(effectGroupOf({ display: { target: "team" }, holderRoleId: 1312 }), { key: "role-1312", kind: "role", roleId: 1312 })
  assert.deepEqual(effectGroupOf({ display: { target: "team" } }), { key: "team", kind: "team" })
  assert.deepEqual(effectGroupOf({ display: { target: "enemy" }, holderRoleId: 9 }), { key: "enemy", kind: "enemy" })
  assert.deepEqual(effectGroupOf({ display: { target: "unresolved" } }), { key: "unresolved", kind: "unresolved" })
  const groups = [effectGroupOf({ display: { target: "enemy" } }), effectGroupOf({ display: { target: "team" }, holderRoleId: 1505 }), effectGroupOf({ display: {} }), effectGroupOf({ display: { target: "team" }, holderRoleId: 1209 }), effectGroupOf({ display: { target: "team" } })]
  assert.deepEqual(groups.sort(compareEffectGroups).map(group => group.key), ["role-1209", "role-1505", "team", "enemy", "unresolved"])
})

test("game-icon effects become tiles and the rest chips, each in the engine's order within its holder", () => {
  const listed = [...role(1505, 2, true), ...role(1505, 3), ...enemy(2, true), ...role(1312, 2), ...role(1505, 1, true)]
  const grid = fit(listed, listed.length, 0, layout(420))
  assert.equal(grid.visible, listed.length)
  assert.deepEqual(grid.groups.map(group => [group.group.key, group.tiles.map(item => item.key), group.chips.map(item => item.key)]), [
    ["role-1312", [], ["1312-c0", "1312-c1"]],
    ["role-1505", ["1505-t0", "1505-t1", "1505-t0"], ["1505-c0", "1505-c1", "1505-c2"]],
    ["enemy", ["e-0", "e-1"], []],
  ])
})

test("every eligible effect is drawn or counted, and the hint line is kept only when needed", () => {
  // Height for effects: available - head - gap (- hint - gap). One tile line is 32 px, each further one 36 px.
  const exact = lines => 26 + lines * 32 + (lines - 1) * 4
  // One holder: 7 tiles per line. Exactly two lines fit 14 tiles without a hint.
  assert.equal(fit(role(1312, 14, true), 14, 0, layout(exact(2))).visible, 14)
  // Fifteen tiles need a third line; with the hint only one line remains: 7 drawn, 8 counted.
  const crowded = fit(role(1312, 15, true), 15, 0, layout(exact(2)))
  assert.deepEqual([crowded.visible, crowded.overflow], [7, 8])
  // Unclassified effects never take a place but always need the line.
  const withUnclassified = fit(role(1312, 14, true), 14, 40, layout(exact(2)))
  assert.deepEqual([withUnclassified.visible, withUnclassified.overflow], [7, 7])
  // Eligible effects beyond the engine's bound need the line even when every listed effect would fit.
  const bounded = fit(role(1312, 3, true), 9, 0, layout(420))
  assert.deepEqual([bounded.visible, bounded.overflow], [3, 6])
  for (const [count, eligible, unclassified, available] of [[0, 0, 0, 420], [1, 1, 0, 420], [40, 40, 0, 420], [128, 500, 0, 420], [9, 9, 3, 60], [5, 5, 0, 0]]) {
    const grid = fit([...role(1102, Math.ceil(count / 2), true), ...enemy(Math.floor(count / 2))], eligible, unclassified, layout(available))
    assert.equal(grid.visible + grid.overflow, eligible, `${count}/${eligible}/${unclassified}/${available}`)
    assert.equal(drawn(grid), grid.visible)
  }
})

test("chips are compact: many effects without a game icon still fit next to their holder", () => {
  // Sixteen chips share one line, as tall as the holder mark; 40 chips take three chip lines (14 + 17 + 17 = 48 px).
  assert.equal(fit(role(1312, 16), 16, 0, layout(26 + 32)).visible, 16)
  assert.equal(fit(role(1312, 40), 40, 0, layout(26 + 48)).visible, 40)
  // One pixel less and the 40 chips need the hint line, which leaves less than a holder's height: all are counted.
  assert.deepEqual([fit(role(1312, 40), 40, 0, layout(26 + 47)).visible, fit(role(1312, 40), 40, 0, layout(26 + 47)).overflow], [0, 40])
  // A holder with tiles and chips stacks a tile line, the block gap and a chip line: 32 + 4 + 14 = 50 px.
  assert.equal(fit([...role(1312, 7, true), ...role(1312, 16)], 23, 0, layout(26 + 50)).visible, 23)
})

test("each new holder costs a group gap, so holders change how many effects fit", () => {
  const exact = lines => 26 + 18 + 6 + lines * 32 + (lines - 1) * 4
  // Room for two lines with the hint: one holder fills both (14 tiles); two holders of seven need the 6 px group gap too.
  assert.equal(fit(role(1312, 20, true), 20, 0, layout(exact(2))).visible, 14)
  assert.equal(fit([...role(1312, 7, true), ...role(1505, 7, true), ...role(1102, 1, true)], 15, 0, layout(exact(2))).visible, 7)
  assert.equal(fit([...role(1312, 7, true), ...role(1505, 7, true), ...role(1102, 1, true)], 15, 0, layout(exact(2) + 6)).visible, 14)
})

test("a panel without a measured window shows a bounded prefix", () => {
  const grid = fit(role(1312, 80), 80, 0, undefined)
  assert.equal(grid.visible, unfittedEffectLimit)
  assert.equal(grid.overflow, 80 - unfittedEffectLimit)
})

test("the drawn effects do not change while only time passes", () => {
  const listed = [...role(1312, 9, true), ...enemy(4), ...role(1505, 12)]
  const first = fit(listed, 25, 4, layout(200))
  for (let tick = 0; tick < 50; tick++) assert.deepEqual(fit(listed, 25, 4, layout(200)), first)
})

test("the production window fits its effects from the engine's order and never names one", () => {
  const panel = read("frontend/src/views/EffectsOverlayPanel.tsx")
  assert.ok(panel.includes("fitEffectGrid(listed, effectGroupOf, isTile, eligible, unclassified"), "effects are a prefix of the engine's order")
  assert.ok(panel.includes("const isTile = (effect: Effect) => effect.hudIcon === true"), "only a game icon makes a tile")
  assert.ok(!/relevantEffect|\.filter\(relevant/.test(panel), "the engine, not the panel, decides display eligibility")
  assert.match(panel, /counts\?\.eligible/, "the title counts the engine's display set")
  for (const forbidden of ["termName", "unknownEffect", "relatedTerm", "effectId ??", "title={"]) assert.ok(!panel.includes(forbidden), `the overlay shows no effect name or ID: ${forbidden}`)
  const tile = read("frontend/src/design/components/combat.tsx")
  assert.ok(tile.includes('effect.hudIcon && effect.effectId !== undefined ? gameAssetUrl("buff", effect.effectId)'), "only a game icon of this effect is requested")
  const components = tile.slice(tile.indexOf("export const EffectTile"), tile.indexOf("export const EffectGroupMark"))
  assert.ok(!/title=/.test(components), "no hover text on a tile or chip")
  assert.ok(!/gameAssetUrl/.test(tile.slice(tile.indexOf("export const EffectChip"), tile.indexOf("export const EffectGroupMark"))), "a chip never borrows an icon")
  assert.match(read("frontend/src/views/OverlayWindow.tsx"), /<EffectsOverlayPanel data=\{data\} alpha=\{alpha\} fitHeight \/>/)
  assert.ok(!read("frontend/src/showcase/sections/EffectsSection.tsx").includes("fitHeight"), "the review stage grows with its content")
  for (const locale of ["en", "ja", "ko", "zh-CN", "zh-TW"]) {
    const overlay = JSON.parse(read(`frontend/locales/${locale}/overlay.json`))
    assert.equal(overlay.unknownEffect, undefined, `${locale} has no unknown-effect label`)
  }
})
