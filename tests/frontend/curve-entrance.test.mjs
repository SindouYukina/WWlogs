import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { curveEntrance, motionVariables } from "../../frontend/src/design/motion/tokens.ts"
import { revealPhaseAt } from "../../frontend/src/design/motion/useReveal.ts"

const source = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8")

// The progress of a CSS cubic-bezier easing at a time fraction, solved by bisection on x(u).
const bezier = text => {
  const [x1, y1, x2, y2] = text.match(/cubic-bezier\(([^)]+)\)/)[1].split(",").map(Number)
  return t => {
    let lo = 0, hi = 1, u = t
    for (let i = 0; i < 60; i++) { u = (lo + hi) / 2; const x = 3 * (1 - u) * (1 - u) * u * x1 + 3 * (1 - u) * u * u * x2 + u * u * u; if (x < t) lo = u; else hi = u }
    return { y: 3 * (1 - u) * (1 - u) * u * y1 + 3 * (1 - u) * u * u * y2 + u * u * u, y1, y2 }
  }
}

test("the DPS curve entrance shoots in almost at once and glides to a stop, for every encounter length", () => {
  // The owner asked for a quick, clearly decelerating sweep: under a second, the same for a 20-second and a 20-minute encounter.
  assert.ok(curveEntrance.durationMs >= 500 && curveEntrance.durationMs <= 1000, `${curveEntrance.durationMs} ms`)
  assert.equal(curveEntrance.easing, "cubic-bezier(0.16, 1, 0.3, 1)")
  const at = bezier(curveEntrance.easing)
  // Very fast at first: most of the curve is drawn as the encounter opens.
  assert.ok(at(100 / curveEntrance.durationMs).y >= 0.55, `${at(100 / curveEntrance.durationMs).y} at 100 ms`)
  assert.ok(at(200 / curveEntrance.durationMs).y >= 0.8, `${at(200 / curveEntrance.durationMs).y} at 200 ms`)
  // Yet not a jump: the first 60 Hz frame shows less than a fifth of it.
  assert.ok(at(16.7 / curveEntrance.durationMs).y < 0.2, `${at(16.7 / curveEntrance.durationMs).y} in the first frame`)
  // Then a long, slow deceleration: every later step is smaller than the one before, and it never passes the end.
  let previous = 0, step = Infinity
  for (let t = 0.05; t <= 1.0001; t += 0.05) {
    const { y, y1, y2 } = at(Math.min(1, t))
    assert.ok(y1 <= 1 && y2 <= 1 && y <= 1 + 1e-9, "no overshoot")
    assert.ok(y - previous <= step + 1e-9, `decelerating at ${t.toFixed(2)}`)
    step = y - previous
    previous = y
  }
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
  assert.deepEqual(revealPhaseAt(clock, 1000, false), { key: 3, elapsedMs: 0, waiting: false })
  assert.deepEqual(revealPhaseAt(clock, 1450, false), { key: 3, elapsedMs: 450, waiting: false }, "a surface that arrives later joins mid-way")
  assert.equal(revealPhaseAt(clock, 1000 + curveEntrance.durationMs, false), null, "a mode switch after the entrance does not replay it")
  assert.deepEqual(revealPhaseAt(clock, 990, false), { key: 3, elapsedMs: 0, waiting: false })
  // A playing waits at the start until the frame that mounted the page has been drawn; reduced motion never waits.
  assert.deepEqual(revealPhaseAt({ key: 4, at: null }, 5000, false), { key: 4, elapsedMs: 0, waiting: true })
  assert.equal(revealPhaseAt({ key: 4, at: null }, 5000, true), null)
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
  // While it waits, the window holds the start: nothing of the lines shows yet, and nothing animates.
  assert.ok(css.includes('.ww-atl-reveal[data-enter="waiting"] { transform: translateX(-100%); }'))
  assert.ok(css.includes('.ww-atl-reveal[data-enter="waiting"] > .ww-atl-reveal-inner { transform: translateX(100%); }'))
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
  assert.match(timeline, /const \{ phase, end \} = useRevealPhase\(reveal, hasData\)/)
  assert.match(timeline, /key=\{reveal\?\.key \?\? 0\}/)
  assert.match(timeline, /data-enter=\{phase \? \(phase\.waiting \? "waiting" : "true"\) : undefined\}/)
  // Ends from its own animation, never from a timer.
  assert.match(timeline, /event\.target === element && event\.animationName === "ww-reveal-window"/)
  assert.match(timeline, /addEventListener\("animationend", ended\)/)
  assert.match(timeline, /addEventListener\("animationcancel", ended\)/)
  assert.match(timeline, /<div ref=\{revealWindow\} className="ww-atl-reveal"/)
  const hook = source("frontend/src/design/motion/useReveal.ts")
  assert.doesNotMatch(hook, /setTimeout|setInterval/)
  assert.match(hook, /if \(ready && opened !== key\) \{/)
  // It begins after the page that shows it was drawn, at the first idle moment of the main thread (bounded), never on a fixed timer.
  assert.match(hook, /setClock\(current => \(\{ key: \(current\?\.key \?\? 0\) \+ 1, at: null \}\)\)/)
  assert.match(hook, /requestIdleCallback\(begin, \{ timeout: startWaitLimitMs \}\)/)
  assert.match(hook, /const startWaitLimitMs = 150/)
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  // Only a new encounter whose own analysis is ready starts the clock; ranges, filters, tabs and hover are not inputs.
  assert.match(view, /useRevealClock\(id, scope === id && analysis\?\.state === "ready"\)/)
  assert.match(view, /<AnalysisTimeline [^>]*reveal=\{reveal\}/)
  assert.match(view, /onClick=\{replay\}[^>]*data-field="replay-entrance"/)
  assert.ok(source("frontend/src/design/views.css").includes(':root[data-ww-motion="reduced"] .ww-analysis-replay { display: none; }'))
})
