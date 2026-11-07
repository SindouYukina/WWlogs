import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { effectCapacity, visibleEffects } from "../../frontend/src/lib/effects.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const layout = available => ({ available, head: 20, row: 38, rowGap: 5, sectionGap: 6, hint: 18 })

test("capacity counts whole rows only", () => {
  // 404 is the default window minus padding: 378 for rows fits 8 full rows (8*38 + 7*5 = 339), not a ninth (382).
  assert.deepEqual(effectCapacity(layout(404)), { all: 8, withHint: 8 })
  assert.deepEqual(effectCapacity(layout(408)), { all: 9, withHint: 8 })
  // Exactly enough for n rows is enough; one pixel less is not.
  const needed = n => 26 + n * 38 + (n - 1) * 5
  for (const n of [1, 2, 7, 25]) {
    assert.equal(effectCapacity(layout(needed(n))).all, n)
    assert.equal(effectCapacity(layout(needed(n) - 1)).all, n - 1)
  }
})

test("a window too small for any row still leaves the hidden count", () => {
  assert.deepEqual(effectCapacity(layout(0)), { all: 0, withHint: 0 })
  const tiny = effectCapacity(layout(60))
  assert.equal(tiny.withHint, 0)
  assert.deepEqual(visibleEffects(3, 0, tiny), { visible: 0, omitted: 3 })
})

test("every effect is shown or counted, and a hint appears only when needed", () => {
  const capacity = { all: 8, withHint: 7 }
  for (const [total, engine, visible, omitted] of [
    [0, 0, 0, 0], [1, 0, 1, 0], [5, 0, 5, 0], [8, 0, 8, 0],
    [9, 0, 7, 2], [12, 0, 7, 5], [13, 0, 7, 6], [20, 0, 7, 13], [500, 0, 7, 493],
    // The engine's own omissions always need the line, even when all its rows would fit.
    [3, 2, 3, 2], [8, 1, 7, 2], [0, 4, 0, 4],
  ]) {
    assert.deepEqual(visibleEffects(total, engine, capacity), { visible, omitted }, `${total}+${engine}`)
    assert.equal(visible + omitted, total + engine)
  }
})

test("the row count does not change while only time passes", () => {
  const capacity = { all: 8, withHint: 7 }
  const first = visibleEffects(13, 0, capacity)
  for (let tick = 0; tick < 100; tick++) assert.deepEqual(visibleEffects(13, 0, capacity), first)
})

test("the production window fits its rows; the panel keeps engine order and no fixed limit", () => {
  const panel = read("frontend/src/views/EffectsOverlayPanel.tsx")
  assert.ok(!panel.includes("maxVisibleEffects"))
  assert.ok(panel.includes("relevant.slice(0, visible)"), "rows are a prefix of the engine's order")
  assert.ok(!/\.sort\(/.test(panel), "this panel must not invent a priority")
  assert.match(read("frontend/src/views/OverlayWindow.tsx"), /<EffectsOverlayPanel data=\{data\} alpha=\{alpha\} fitHeight \/>/)
  assert.ok(!read("frontend/src/showcase/sections/EffectsSection.tsx").includes("fitHeight"), "the review stage grows with its content")
})
