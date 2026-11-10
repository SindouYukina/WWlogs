import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { lineHitReach, lineVertices, nearestLine } from "../../frontend/src/design/components/chart-hit.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const chart = read("frontend/src/design/components/LiveChart.tsx")

const plotWidth = 1000
const height = 100
const line = (key, values, extra = {}) => ({ key, ...extra, vertices: lineVertices(values, values.length, 100, height, plotWidth) })

test("a line's vertices match what its path draws, with missing slots holding the last known point", () => {
  assert.deepEqual(lineVertices([50, null, null, 100], 4, 100, 100, 1000), [[0, 50], [0, 50], [0, 50], [1000, 0]])
  assert.deepEqual(lineVertices([null, 25], 2, 100, 100, 1000), [[0, 100], [1000, 75]])
})

test("a click selects the nearest line within reach, wherever it is drawn in the stack", () => {
  const low = line("low", [10, 10, 10])
  const high = line("high", [90, 90, 90])
  // y grows downward: a value of 10 is at y=90, a value of 90 is at y=10.
  assert.equal(nearestLine([low, high], 500, 88, 1, 1), "low")
  assert.equal(nearestLine([high, low], 500, 88, 1, 1), "low", "the order of the layers does not decide")
  assert.equal(nearestLine([low, high], 500, 12, 1, 1), "high")
})

test("two crossing lines: the line nearest the pointer wins on each side of the crossing", () => {
  const rising = line("rising", [0, 100])
  const falling = line("falling", [100, 0])
  assert.equal(nearestLine([rising, falling], 250, 25, 1, 1), "falling")
  assert.equal(nearestLine([rising, falling], 250, 75, 1, 1), "rising")
  assert.equal(nearestLine([rising, falling], 750, 25, 1, 1), "rising")
  assert.equal(nearestLine([rising, falling], 750, 75, 1, 1), "falling")
})

test("the reach is measured in screen pixels however the plot is stretched", () => {
  const flat = line("flat", [50, 50])
  assert.equal(nearestLine([flat], 500, 50 + lineHitReach, 1, 1), "flat", "exactly at the reach")
  assert.equal(nearestLine([flat], 500, 50 + lineHitReach + 1, 1, 1), null, "just beyond the reach")
  // The plot is drawn at 2.4 CSS pixels per drawing unit vertically: the line is now at y=120.
  assert.equal(nearestLine([flat], 500, 120 + lineHitReach, 1, 2.4), "flat")
  assert.equal(nearestLine([flat], 500, 120 + lineHitReach + 1, 1, 2.4), null)
})

test("a click away from every line selects nothing", () => {
  assert.equal(nearestLine([line("a", [10, 10])], 500, 5, 1, 1), null)
  assert.equal(nearestLine([], 500, 50, 1, 1), null)
})

test("the distance reaches a steep segment between two slots, not only its vertices", () => {
  const spike = line("spike", [0, 100, 0])
  // The middle of the climb from (0, 100) to (500, 0).
  assert.equal(nearestLine([spike], 250, 50, 1, 1), "spike")
})

test("a team total lying exactly on a character's line never takes the click from it", () => {
  const values = [20, 40, 60]
  assert.equal(nearestLine([line("team", values, { aggregate: true }), line("role", values)], 500, 60, 1, 1), "role")
  assert.equal(nearestLine([line("role", values), line("team", values, { aggregate: true })], 500, 60, 1, 1), "role")
  // A team line clearly closer than the character line is still the one selected.
  assert.equal(nearestLine([line("team", [70, 70], { aggregate: true }), line("role", [20, 20])], 500, 31, 1, 1), "team")
})

test("the chart decides selection itself on a click, and a press that moved is not a click", () => {
  assert.match(chart, /onPointerDown=\{onChartPointerDown\} onClick=\{onChartClick\}/, "one click handler serves both a line and the empty chart")
  assert.match(chart, /Math\.hypot\(event\.clientX - press\.x, event\.clientY - press\.y\) > clickMoveLimit\) return/)
  assert.match(chart, /setLocked\(current => hit === null \? null : toggleLegendLock\(current, hit\)\)/, "a hit toggles, a miss releases")
  // The lock is cleared when a new encounter starts or the series leaves, not on a data update.
  assert.match(chart, /if \(locked !== null && !series\.some\(s => s\.key === locked\)\) setLocked\(null\)/)
  assert.doesNotMatch(chart, /Wails|invoke\(|requestAnimationFrame|setInterval/, "selection stays in the component")
})
