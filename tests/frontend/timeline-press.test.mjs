import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { clickMoveLimit, draggedRange, plotRelease, pressMovedTo } from "../../frontend/src/design/components/timeline-press.ts"

const source = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8").replace(/\r\n/g, "\n")

// A press at x=400, y=100 over 20 s of the encounter, on a plot showing 50 ms per pixel.
const press = { x: 400, y: 100, ms: 20000, wandered: false }
const msAt = x => 20000 + (x - 400) * 50
const none = () => null
const whole = { focused: null, range: null, minimumRangeMs: 200 }
const ranged = { focused: null, range: { fromMs: 10000, toMs: 30000 }, minimumRangeMs: 200 }
const moved = (path, start = press) => path.reduce((current, [x, y]) => pressMovedTo(current, x, y), start)

test("the click limit is a hand's jitter: a few pixels, never zero", () => {
  assert.ok(clickMoveLimit >= 3 && clickMoveLimit <= 8, String(clickMoveLimit))
})

test("a click away from every line returns a selected range to the whole encounter", () => {
  assert.deepEqual(plotRelease(press, 400, 100, msAt(400), none, ranged), { kind: "whole" })
  // The line stays selected: the click changes the range only.
  assert.deepEqual(plotRelease(press, 400, 100, msAt(400), none, { ...ranged, focused: "role-7-1505" }), { kind: "whole" })
})

test("a click away from every line without a range does nothing, so nothing is asked again", () => {
  assert.equal(plotRelease(press, 400, 100, msAt(400), none, whole), null)
  assert.equal(plotRelease(press, 400, 100, msAt(400), none, { ...whole, focused: "role-7-1505" }), null)
})

test("a click on a line selects it or releases it, and keeps the range", () => {
  assert.deepEqual(plotRelease(press, 400, 100, msAt(400), () => "role-7-1505", ranged), { kind: "line", key: "role-7-1505" })
  assert.deepEqual(plotRelease(press, 400, 100, msAt(400), () => "role-7-1505", { ...ranged, focused: "role-7-1505" }), { kind: "line", key: null })
  assert.deepEqual(plotRelease(press, 400, 100, msAt(400), () => "role-7-1205", { ...whole, focused: "role-7-1505" }), { kind: "line", key: "role-7-1205" })
})

test("jitter within the limit in any direction is still a click", () => {
  for (const [dx, dy] of [[clickMoveLimit, 0], [0, clickMoveLimit], [-3, 4], [3, -3], [-clickMoveLimit, 0]]) {
    const jittered = moved([[400 + dx, 100 + dy]])
    assert.equal(jittered.wandered, false, `${dx},${dy}`)
    assert.equal(draggedRange(jittered, 400 + dx, msAt(400 + dx)), null, `${dx},${dy}: no range is drawn`)
    assert.deepEqual(plotRelease(jittered, 400 + dx, 100 + dy, msAt(400 + dx), none, ranged), { kind: "whole" }, `${dx},${dy}`)
  }
})

test("a drag along the time axis selects the range it covers, from the first pixel past the limit", () => {
  const x = 400 + clickMoveLimit + 1
  assert.deepEqual(draggedRange(press, x, msAt(x)), { fromMs: 20000, toMs: msAt(x) })
  assert.deepEqual(plotRelease(moved([[x, 100]]), x, 100, msAt(x), () => "role-7-1505", ranged), { kind: "range", fromMs: 20000, toMs: msAt(x) })
  // Leftwards, and with the pointer off the line vertically: the range is the same either way round.
  assert.deepEqual(plotRelease(moved([[200, 160]]), 200, 160, msAt(200), none, whole), { kind: "range", fromMs: msAt(200), toMs: 20000 })
  // A drag never asks for a line.
  assert.notEqual(plotRelease(moved([[480, 100]]), 480, 100, msAt(480), () => { throw new Error("asked") }, whole), undefined)
})

test("a drag shorter than the minimum range selects nothing and clears nothing", () => {
  const fine = { ...press, ms: 20000 }
  const x = 400 + clickMoveLimit + 1
  // At 10 ms per pixel six pixels are 60 ms, below the 200 ms minimum.
  assert.equal(plotRelease(moved([[x, 100]], fine), x, 100, 20000 + (x - 400) * 10, none, ranged), null)
})

test("a press that wandered off is never a click, even when it comes back", () => {
  // Out along the axis and back within the limit: no range is drawn once it is back, and none is committed.
  const back = moved([[440, 100], [402, 100]])
  assert.equal(back.wandered, true)
  assert.equal(draggedRange(back, 402, msAt(402)), null)
  assert.equal(plotRelease(back, 402, 100, msAt(402), () => "role-7-1505", ranged), null)
  // Straight up or down: no range, and neither the line nor the range changes.
  const vertical = moved([[401, 130]])
  assert.equal(draggedRange(vertical, 401, msAt(401)), null)
  assert.equal(plotRelease(vertical, 401, 130, msAt(401), () => "role-7-1505", ranged), null)
  // Moved only between the last move and the release.
  assert.equal(plotRelease(press, 401, 112, msAt(401), none, ranged), null)
})

test("the DPS lines read every release through plotRelease, and a click away from the lines resets the range", () => {
  const timeline = source("frontend/src/design/components/AnalysisTimeline.tsx")
  assert.match(timeline, /const release = plotRelease\(press, event\.clientX, event\.clientY, msAt\(element, event\.clientX, view\), \(\) => lineAt\(element, event\.clientX, event\.clientY\), \{ focused, range, minimumRangeMs \}\)/)
  assert.match(timeline, /if \(release\?\.kind === "range"\) onRange\(release\.fromMs, release\.toMs\)\n    else if \(release\?\.kind === "line"\) onSelectLine\(release\.key\)\n    else if \(release\?\.kind === "whole"\) onReset\(\)/)
  assert.match(timeline, /pressed\.current = pressMovedTo\(press, event\.clientX, event\.clientY\)\n    setDrag\(draggedRange\(press, event\.clientX, ms\)\)/)
  assert.doesNotMatch(timeline, /clickMoveLimit/, "one click limit, in timeline-press.ts")
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  // The click and the reset button are one action: the range only, every filter and the window kept.
  assert.match(view, /<AnalysisTimeline [^>]*onReset=\{resetRange\}/)
  assert.match(view, /data-field="range-reset">/)
  assert.match(view, /onClick=\{resetRange\} data-field="range-reset"/)
  assert.match(view, /const resetRange = useCallback\(\(\) => setQuery\(q => q\.fromMs === 0 && q\.toMs === 0 \? q : \{ \.\.\.q, fromMs: 0, toMs: 0 \}\), \[\]\)/)
})
