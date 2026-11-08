import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { compareEffects, effectCellSize, effectExpiryMs, fitEffectStrip, orderEffects } from "../../frontend/src/lib/effects.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const base = Date.parse("2026-11-08T12:00:00Z")
const expires = seconds => new Date(base + seconds * 1000).toISOString()
const timed = (key, seconds, extra = {}) => ({ key, lifecycle: "active", remainingStatus: "estimated", expectedExpiry: expires(seconds), ...extra })
const untimed = (key, extra = {}) => ({ key, lifecycle: "active", remainingStatus: "unknown", ...extra })
const keys = list => list.map(effect => effect.key)

// The default 640 px window: a 10 px status slot beside 36 px icons and 22 px gauges with 4 px gaps.
const layout = (width = 616) => ({ width, icon: 36, gauge: 22, gap: 4, more: 34 })
const icons = n => Array.from({ length: n }, (_, i) => ({ key: `i${i}`, hudIcon: true }))
const gauges = n => Array.from({ length: n }, (_, i) => ({ key: `g${i}` }))

test("effects run from the soonest to end to the latest, then those without a reliable time", () => {
  const list = [timed("c", 12.3), untimed("u"), timed("a", 1.8), timed("d", 6), timed("b", 3.4)]
  assert.deepEqual(keys(orderEffects(list)), ["a", "b", "d", "c", "u"])
})

test("a new effect lands at the front, in the middle or at the end by its expiry", () => {
  const list = [timed("a", 2), timed("b", 4), timed("c", 6)]
  assert.deepEqual(keys(orderEffects([...list, timed("n", 1)])), ["n", "a", "b", "c"])
  assert.deepEqual(keys(orderEffects([...list, timed("n", 5)])), ["a", "b", "n", "c"])
  assert.deepEqual(keys(orderEffects([...list, timed("n", 9)])), ["a", "b", "c", "n"])
})

test("effects ending together keep one order whatever order the engine lists them in", () => {
  const list = [timed("z", 5), timed("m", 5), timed("a", 5)]
  const expected = ["a", "m", "z"]
  for (let turn = 0; turn < 20; turn++) {
    list.push(list.shift())
    assert.deepEqual(keys(orderEffects(list)), expected)
  }
})

test("time passing never reorders: the order follows the expiry, not the seconds left", () => {
  const snapshot = tick => [timed("a", 3, { remainingSeconds: 3 - tick / 4 }), timed("b", 3.4, { remainingSeconds: 3.4 - tick / 4 }), timed("c", 3.40001, { remainingSeconds: 3.40001 - tick / 4 }), untimed("u")]
  const first = keys(orderEffects(snapshot(0)))
  for (let tick = 1; tick < 12; tick++) assert.deepEqual(keys(orderEffects(snapshot(tick))), first)
})

test("a refreshed effect moves to where its new expiry belongs", () => {
  assert.deepEqual(keys(orderEffects([timed("a", 2), timed("b", 4)])), ["a", "b"])
  assert.deepEqual(keys(orderEffects([timed("a", 9), timed("b", 4)])), ["b", "a"])
})

test("an effect whose duration becomes known joins the timed effects", () => {
  assert.deepEqual(keys(orderEffects([untimed("a"), timed("b", 4)])), ["b", "a"])
  assert.deepEqual(keys(orderEffects([timed("a", 1), timed("b", 4)])), ["a", "b"])
})

test("only a reliable engine time sorts: stale, overdue and ended effects go last", () => {
  for (const unreliable of [timed("x", 1, { stale: true }), timed("x", 1, { remainingStatus: "overdue" }), timed("x", 1, { lifecycle: "estimated_expired" }), { ...timed("x", 1), expectedExpiry: "not a time" }, untimed("x")]) {
    assert.equal(effectExpiryMs(unreliable), undefined)
    assert.deepEqual(keys(orderEffects([unreliable, timed("b", 40)])), ["b", "x"])
  }
})

test("among effects without a time, those the game draws with an icon come first", () => {
  assert.deepEqual(keys(orderEffects([untimed("a"), untimed("b", { hudIcon: true }), untimed("c")])), ["b", "a", "c"])
  assert.ok(compareEffects(untimed("a"), untimed("a")) === 0)
})

test("the same effect on two characters stays two instances", () => {
  const list = orderEffects([timed("holder-1|1312010033", 5, { effectId: 1312010033 }), timed("holder-2|1312010033", 5, { effectId: 1312010033 })])
  assert.equal(new Set(keys(list)).size, 2)
})

test("only an icon of the effect's own makes a full cell", () => {
  assert.equal(effectCellSize({ hudIcon: true }), "icon")
  assert.equal(effectCellSize({ hudIcon: true, hudIconGeneric: true }), "gauge")
  assert.equal(effectCellSize({}), "gauge")
})

test("the strip keeps the longest prefix that fits and counts every other eligible effect", () => {
  // 15 icons need 15 * 36 + 14 * 4 = 596 px.
  assert.deepEqual(fitEffectStrip(icons(15), effectCellSize, 15, layout()), { visible: 15, overflow: 0 })
  // Sixteen would take 636 px: 14 icons leave room for the 34 px count (14 * 36 + 13 * 4 + 4 + 34 = 594).
  assert.deepEqual(fitEffectStrip(icons(16), effectCellSize, 16, layout()), { visible: 14, overflow: 2 })
  assert.deepEqual(fitEffectStrip(gauges(30), effectCellSize, 30, layout(610)), { visible: 22, overflow: 8 })
  for (const [count, eligible, width] of [[0, 0, 616], [1, 1, 616], [128, 500, 616], [9, 9, 60], [5, 5, 0], [40, 40, 220]]) {
    const { visible, overflow } = fitEffectStrip([...icons(Math.ceil(count / 2)), ...gauges(Math.floor(count / 2))], effectCellSize, eligible, layout(width))
    assert.equal(visible + overflow, eligible, `${count}/${eligible}/${width}`)
    assert.ok(visible >= 0 && visible <= count)
  }
})

test("effects beyond the engine's bound need the count even when every listed effect would fit", () => {
  assert.deepEqual(fitEffectStrip(icons(3), effectCellSize, 9, layout()), { visible: 3, overflow: 6 })
})

test("a strip that has not been measured yet shows no cells and loses no count", () => {
  assert.deepEqual(fitEffectStrip(icons(3), effectCellSize, 3, undefined), { visible: 0, overflow: 3 })
})

test("the drawn effects do not change while only time passes", () => {
  const list = [...icons(9), ...gauges(16)]
  const first = fitEffectStrip(list, effectCellSize, 25, layout(300))
  for (let tick = 0; tick < 50; tick++) assert.deepEqual(fitEffectStrip(list, effectCellSize, 25, layout(300)), first)
})

test("the production strip orders the engine's set, never filters it, and never names an effect", () => {
  const panel = read("frontend/src/views/EffectsOverlayPanel.tsx")
  assert.ok(panel.includes("orderEffects(listed)"), "the panel orders the engine's list")
  assert.ok(panel.includes("fitEffectStrip(ordered, effectCellSize, eligible, layout)"), "the strip is a prefix of that order")
  assert.ok(!/relevantEffect|\.filter\(relevant/.test(panel), "the engine, not the panel, decides display eligibility")
  assert.match(panel, /counts\?\.eligible/, "the count comes from the engine's display set")
  assert.ok(!/effectGroupOf|EffectGroupMark|holderName|holderRoleId/.test(panel), "the strip does not group by holder")
  for (const forbidden of ["termName", "unknownEffect", "relatedTerm", "effectId ??", "title={"]) assert.ok(!panel.includes(forbidden), `the overlay shows no effect name or ID: ${forbidden}`)
  const components = read("frontend/src/design/components/combat.tsx")
  const cell = components.slice(components.indexOf("export const EffectCell"), components.indexOf("export const EffectMore"))
  assert.ok(!/title=/.test(cell), "no hover text on a cell")
  assert.ok(cell.includes('icon !== "none" && effectId !== undefined ? gameAssetUrl("buff", effectId)'), "only this effect's own game icon is requested")
  assert.match(read("frontend/src/views/OverlayWindow.tsx"), /<EffectsOverlayPanel data=\{data\} alpha=\{alpha\} \/>/)
  for (const locale of ["en", "ja", "ko", "zh-CN", "zh-TW"]) {
    const overlay = JSON.parse(read(`frontend/locales/${locale}/overlay.json`))
    assert.equal(overlay.unknownEffect, undefined, `${locale} has no unknown-effect label`)
  }
})

test("the fill sits behind the icon and is colored by the effect's own element, or neutral", () => {
  const css = read("frontend/src/design/components.css")
  const cell = read("frontend/src/design/components/combat.tsx")
  assert.ok(cell.indexOf('className="ww-effect-fill"') < cell.indexOf("<img src={iconUrl}"), "the fill is drawn before the icon")
  assert.match(css, /\.ww-effect-face \{[^}]*--ww-effect-color: var\(--ww-series, var\(--ww-element-unknown\)\)/)
  assert.ok(cell.includes("data-element={elementId}"), "the color comes from the effect's element")
  assert.ok(!/\.ww-effect-[a-z-]*\[data-polarity/.test(css), "polarity never colors an effect")
  const fill = css.match(/\.ww-effect-fill \{[^}]*\}/)[0]
  assert.ok(fill.includes("scaleX(var(--ww-remaining))"), "the time moves by transform only")
  assert.ok(!/transition|animation|filter|box-shadow|width:/.test(fill), "a countdown runs no continuous animation")
})

test("the strip reuses the ranking's motion and sets no timer of its own per cell", () => {
  const layoutSource = read("frontend/src/design/components/layout.tsx")
  const row = layoutSource.slice(layoutSource.indexOf("export function StackRow"), layoutSource.indexOf("export interface SettingsSectionProps"))
  assert.ok(row.includes("translateX("), "cells are positioned by transform")
  assert.ok(row.includes("usePresence(items, keyOf)"), "the strip shares the ranking's presence handling")
  assert.ok(!/getBoundingClientRect|requestAnimationFrame|setInterval/.test(row), "a reorder measures nothing and runs no frame loop")
  const css = read("frontend/src/design/components.css")
  assert.match(css, /\.ww-stack-item \{[^}]*transition: transform var\(--ww-spring-rank-dur\) var\(--ww-spring-rank\)/)
  const strip = css.slice(css.indexOf("Effects strip"), css.indexOf("------ Settings"))
  assert.ok(!/infinite/.test(strip), "no looping animation")
  const cell = read("frontend/src/design/components/combat.tsx")
  assert.ok(!/setInterval|setTimeout|requestAnimationFrame/.test(cell.slice(cell.indexOf("export const EffectCell"))), "a cell owns no clock")
})
