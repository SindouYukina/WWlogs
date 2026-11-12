import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { barEntrance, curveEntrance, motionVariables } from "../../frontend/src/design/motion/tokens.ts"
import { revealPhaseAt } from "../../frontend/src/design/motion/useReveal.ts"

const source = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8")

test("the DPS curve entrance is one short, decelerating sweep, the same in every theme and for every encounter length", () => {
  // Well under the 1.2 s ceiling, so a 20-minute encounter is drawn as quickly as a 20-second one.
  assert.ok(curveEntrance.durationMs >= 700 && curveEntrance.durationMs <= 1100, `${curveEntrance.durationMs} ms`)
  // An ease-out cubic without a control point that could overshoot the end of the encounter, like the bar entrance.
  assert.equal(curveEntrance.easing, "cubic-bezier(0.33, 1, 0.68, 1)")
  assert.equal(curveEntrance.easing, barEntrance.easing)
  const full = motionVariables()
  assert.equal(full["--ww-curve-enter-dur"], `${curveEntrance.durationMs}ms`)
  assert.equal(full["--ww-ease-curve-enter"], curveEntrance.easing)
  for (const flavor of ["electro", "aero", "havoc", "glacio", "fusion"]) assert.equal(motionVariables({ flavor })["--ww-curve-enter-dur"], `${curveEntrance.durationMs}ms`, flavor)
  assert.equal(motionVariables({ preference: "reduced" })["--ww-curve-enter-dur"], "0ms")
  assert.equal(motionVariables({ scale: 2 })["--ww-curve-enter-dur"], `${curveEntrance.durationMs * 2}ms`)
})

test("a surface joins the reveal where it is, never after it ended and never under reduced motion", () => {
  const clock = { key: 3, at: 1000 }
  assert.equal(revealPhaseAt(null, 1000, false), null)
  assert.equal(revealPhaseAt(clock, 1000, true), null, "reduced motion shows the whole curve at once")
  assert.deepEqual(revealPhaseAt(clock, 1000, false), { key: 3, elapsedMs: 0 })
  assert.deepEqual(revealPhaseAt(clock, 1450, false), { key: 3, elapsedMs: 450 }, "a surface that arrives later joins mid-way")
  assert.equal(revealPhaseAt(clock, 1000 + curveEntrance.durationMs, false), null, "a mode switch after the entrance does not replay it")
  assert.deepEqual(revealPhaseAt(clock, 990, false), { key: 3, elapsedMs: 0 })
})

test("the reveal widens a window over the lines with transforms only and holds nothing afterwards", () => {
  const css = source("frontend/src/design/views.css")
  assert.ok(css.includes("@keyframes ww-reveal-window { from { transform: translateX(-100%); } }"))
  assert.ok(css.includes("@keyframes ww-reveal-content { from { transform: translateX(100%); } }"))
  // The window clips and reaches past the plot so no stroke is cut; its content sits back on the plot.
  assert.ok(css.includes(".ww-atl-reveal { position: absolute; inset: -6px 0; overflow: hidden; pointer-events: none; }"))
  assert.ok(css.includes(".ww-atl-reveal-inner { position: absolute; inset: 6px 0; }"))
  const rules = css.split("\n").filter(line => line.startsWith(".ww-atl-reveal[data-enter=\"true\"]") && line.includes("animation:"))
  assert.equal(rules.length, 3)
  for (const rule of rules) {
    // Window and content share duration, delay and fill, so the lines never move; the edge fades linearly in time.
    assert.match(rule, /var\(--ww-curve-enter-dur\) (var\(--ww-ease-curve-enter\)|linear) var\(--ww-reveal-at, 0ms\) backwards;/)
    assert.doesNotMatch(rule, /\bboth\b|\bforwards\b|infinite/)
  }
  assert.ok(css.includes(':root[data-ww-motion="reduced"] .ww-atl-reveal[data-enter="true"], :root[data-ww-motion="reduced"] .ww-atl-reveal[data-enter="true"] > * { animation: none; }'))
})

test("only the lines are revealed: capture gaps, the axis and the values are drawn in full from the first frame", () => {
  const timeline = source("frontend/src/design/components/AnalysisTimeline.tsx")
  const plot = timeline.slice(timeline.indexOf('data-field="analysis-plot"'), timeline.indexOf("<TimeAxis"))
  // The gaps and the range are plot children ahead of the reveal; the hover tip reads every value of every line.
  assert.ok(plot.indexOf('data-field="analysis-gap"') < plot.indexOf('className="ww-atl-reveal"'))
  assert.ok(plot.indexOf('data-field="analysis-range"') < plot.indexOf('className="ww-atl-reveal"'))
  const reveal = plot.slice(plot.indexOf('className="ww-atl-reveal"'), plot.indexOf('className="ww-chart-empty"'))
  assert.match(reveal, /className="ww-chart-layer"/)
  assert.doesNotMatch(reveal, /analysis-gap|ww-chart-tip|ww-atl-marks/)
  // The paths are the same strings whether or not the reveal plays: nothing is interpolated or rebuilt per frame.
  assert.match(timeline, /d=\{paths\.get\(line\.key\)\}/)
  assert.match(timeline, /const paths = useMemo\(\(\) => new Map\(lines\.map\(line => \[line\.key, linePath\(/)
})

test("the curve reveal plays when an encounter opens or on replay, and nothing else restarts or cuts it", () => {
  const timeline = source("frontend/src/design/components/AnalysisTimeline.tsx")
  assert.match(timeline, /const \{ phase, end \} = useRevealPhase\(reveal\)/)
  assert.match(timeline, /key=\{reveal\?\.key \?\? 0\}/)
  assert.match(timeline, /data-enter=\{phase \? "true" : undefined\}/)
  // Ends from its own animation, never from a timer.
  assert.match(timeline, /event\.target === element && event\.animationName === "ww-reveal-window"/)
  assert.match(timeline, /addEventListener\("animationend", ended\)/)
  assert.match(timeline, /addEventListener\("animationcancel", ended\)/)
  assert.match(timeline, /<div ref=\{revealWindow\} className="ww-atl-reveal"/)
  const hook = source("frontend/src/design/motion/useReveal.ts")
  assert.doesNotMatch(hook, /setTimeout|setInterval/)
  assert.match(hook, /if \(ready && opened !== key\) \{/)
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  // Only a new encounter whose own analysis is ready starts the clock; ranges, filters, tabs and hover are not inputs.
  assert.match(view, /useRevealClock\(id, scope === id && analysis\?\.state === "ready"\)/)
  assert.match(view, /<AnalysisTimeline [^>]*reveal=\{reveal\}/)
  assert.match(view, /onClick=\{replay\}[^>]*data-field="replay-entrance"/)
  assert.ok(source("frontend/src/design/views.css").includes(':root[data-ww-motion="reduced"] .ww-analysis-replay { display: none; }'))
})
