import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { fitEffectStrip } from "../../frontend/src/lib/effects.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")

// The default 640 px window: a 10 px status slot beside 36 px icons with 4 px gaps.
const layout = (width = 616) => ({ width, icon: 36, gap: 4, more: 34 })
const icons = n => Array.from({ length: n }, (_, i) => ({ key: `i${i}`, hudIcon: true }))

test("the strip keeps the longest prefix that fits and counts every other eligible effect", () => {
  // 15 icons need 15 * 36 + 14 * 4 = 596 px.
  assert.deepEqual(fitEffectStrip(icons(15), 15, layout()), { visible: 15, overflow: 0 })
  // Sixteen would take 636 px: 14 icons leave room for the 34 px count (14 * 36 + 13 * 4 + 4 + 34 = 594).
  assert.deepEqual(fitEffectStrip(icons(16), 16, layout()), { visible: 14, overflow: 2 })
  for (const [count, eligible, width] of [[0, 0, 616], [1, 1, 616], [128, 500, 616], [9, 9, 60], [5, 5, 0], [40, 40, 220]]) {
    const { visible, overflow } = fitEffectStrip(icons(count), eligible, layout(width))
    assert.equal(visible + overflow, eligible, `${count}/${eligible}/${width}`)
    assert.ok(visible >= 0 && visible <= count)
  }
})

test("effects beyond the engine's bound need the count even when every listed effect would fit", () => {
  assert.deepEqual(fitEffectStrip(icons(3), 9, layout()), { visible: 3, overflow: 6 })
})

test("a strip that has not been measured yet shows no cells and loses no count", () => {
  assert.deepEqual(fitEffectStrip(icons(3), 3, undefined), { visible: 0, overflow: 3 })
})

test("the drawn effects do not change while only time passes", () => {
  const list = icons(25)
  const first = fitEffectStrip(list, 25, layout(300))
  for (let tick = 0; tick < 50; tick++) assert.deepEqual(fitEffectStrip(list, 25, layout(300)), first)
})

test("the empty state covers exactly the area both rows draw in, with its text in the middle", () => {
  const panel = read("frontend/src/views/EffectsOverlayPanel.tsx")
  const views = read("frontend/src/design/views.css")
  assert.match(views, /\.ww-effects-empty \{ grid-row: 1 \/ -1; min-width: 0; display: flex; align-items: center; justify-content: center;/)
  assert.match(panel, /ww-effects-empty"><span className="ww-truncate">\{t\("noEffects"\)\}<\/span>/, "a long label shortens inside the block instead of widening it")
})

test("a cell keeps its frame line unbroken and its art off the frame", () => {
  const components = read("frontend/src/design/components.css")
  assert.match(components, /\.ww-effect-face \{[^}]*border: 1px solid var\(--ww-line-soft\);/, "the fill is clipped inside a real border")
  assert.match(components, /\.ww-effect-face img \{ position: absolute; inset: 1px; width: calc\(100% - 2px\); height: calc\(100% - 2px\); object-fit: contain; \}/)
  assert.doesNotMatch(components, /\.ww-effect-fill \{[^}]*inset: 1px/, "the progress edge stays straight because the frame clips it, not a rounded fill")
})

test("the production strip orders the engine's set, never filters it, and never names an effect", () => {
  const panel = read("frontend/src/views/EffectsOverlayPanel.tsx")
  assert.ok(panel.includes("view?.effects.buffs") && panel.includes("view?.effects.debuffs"), "the rows come from the engine")
  assert.ok(!/\.sort\(|orderEffects|compareEffects|[.]polarity ===/.test(panel), "the panel neither sorts nor classifies")
  assert.ok(panel.includes("fitEffectStrip(effects, cells, layout)"), "each row is a prefix of its order")
  assert.ok(!/relevantEffect|\.filter\(relevant/.test(panel), "the engine, not the panel, decides display eligibility")
  assert.ok(!/ListeningStatus|useListeningLabels/.test(panel), "the listening status is shown once, on the Combat Overlay")
  assert.match(read("frontend/src/views/CombatOverlayPanel.tsx"), /ListeningStatus/, "the Combat Overlay keeps the listening status")
  assert.ok(!/effectGroupOf|EffectGroupMark|holderName|holderRoleId/.test(panel), "the strip does not group by holder")
  for (const forbidden of ["termName", "unknownEffect", "relatedTerm", "effectId ??", "title={"]) assert.ok(!panel.includes(forbidden), `the overlay shows no effect name or ID: ${forbidden}`)
  const components = read("frontend/src/design/components/combat.tsx")
  const cell = components.slice(components.indexOf("export const EffectCell"), components.indexOf("export const EffectMore"))
  assert.ok(!/title=/.test(cell), "no hover text on a cell")
  assert.ok(cell.includes('effectId !== undefined ? gameAssetUrl("buff", effectId)'), "only this effect's own game icon is requested")
  assert.ok(!cell.includes("ww-effect-mark"), "no neutral mark stands in for an icon")
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
