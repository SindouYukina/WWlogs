import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { curveEntrance, motionVariables } from "../../frontend/src/design/motion/tokens.ts"
import { holdLimitMs, holdWaitMs, revealPhaseAt, settledFrames, settleFrame, settleLimitMs, smoothFrameFactor, unsettled } from "../../frontend/src/design/motion/useReveal.ts"

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
  // While it waits, the content is drawn in place but invisible, in the composited layers the sweep then moves, so its first
  // rasterization (and any GPU program it is the first to need) is done before anything moves; nothing can be pointed at.
  assert.ok(css.includes('.ww-atl-reveal[data-enter], .ww-atl-reveal[data-enter] > * { will-change: transform; }'))
  assert.ok(css.includes('.ww-atl-reveal[data-enter="waiting"] { opacity: 0.004; }'))
  assert.ok(css.includes('.ww-atl-reveal[data-enter="waiting"], .ww-atl-reveal[data-enter="waiting"] * { pointer-events: none; }'))
  assert.ok(css.includes('.ww-atl-reveal[data-enter="waiting"] > .ww-atl-reveal-edge { opacity: 0.85; }'))
  assert.doesNotMatch(css, /\.ww-atl-reveal\[data-enter="waiting"\][^{]*\{[^}]*transform:/, "waiting no longer moves the content out of view")
  // The edge is there while waiting too, so its glow is drawn once before the sweep.
  for (const file of ["frontend/src/design/components/AnalysisTimeline.tsx", "frontend/src/design/components/IconTimeline.tsx"]) {
    assert.match(source(file), /\{phase && <i className="ww-atl-reveal-edge" aria-hidden="true" \/>\}/, file)
  }
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
  assert.doesNotMatch(hook, /setInterval/)
  // The only timer bounds a hold; it is never a delay of its own.
  assert.equal(hook.match(/setTimeout/g)?.length, 1)
  assert.match(hook, /else limit = window\.setTimeout\(waitForFrames, wait\)/)
  assert.match(hook, /if \(ready && opened !== key\) \{/)
  // It begins once nothing holds it and the frames since have settled (a first rasterization delays them), at the first idle
  // moment of the main thread (bounded).
  assert.match(hook, /setClock\(current => \(\{ key: \(current\?\.key \?\? 0\) \+ 1, at: null, hold \}\)\)/)
  assert.match(hook, /settle = settleFrame\(settle, now\)\s+if \(settle\.smooth >= settledFrames \|\| now - started >= settleLimitMs\) idle = requestIdleCallback\(begin, \{ timeout: startWaitLimitMs \}\)\s+else frame = requestAnimationFrame\(step\)/)
  assert.match(hook, /const startWaitLimitMs = 150/)
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  // Only a new encounter whose own analysis is ready starts the clock; ranges, filters, tabs and hover are not inputs.
  assert.match(view, /useRevealClock\(id, scope === id && analysis\?\.state === "ready"\)/)
  assert.match(view, /<AnalysisTimeline [^>]*reveal=\{reveal\}/)
  assert.match(view, /onClick=\{replay\}[^>]*data-field="replay-entrance"/)
  assert.ok(source("frontend/src/design/views.css").includes(':root[data-ww-motion="reduced"] .ww-analysis-replay { display: none; }'))
})

test("a surface still loading or rendering holds the reveal at the start, for a bounded time", () => {
  // Nothing held: the start frames are awaited at once. Held: until the holds go, at most holdLimitMs after the wait began.
  assert.equal(holdWaitMs(0, 0), 0)
  assert.equal(holdWaitMs(0, 5000), 0)
  assert.equal(holdWaitMs(1, 0), holdLimitMs)
  assert.equal(holdWaitMs(2, 400), holdLimitMs - 400)
  assert.equal(holdWaitMs(1, holdLimitMs), 0)
  assert.equal(holdWaitMs(1, holdLimitMs + 1), 0)
  assert.ok(holdLimitMs >= 1000 && holdLimitMs <= 2000, `${holdLimitMs} ms`)
  const hook = source("frontend/src/design/motion/useReveal.ts")
  // A hold or a release reschedules the start without rendering the page again, and a playing that began is never held.
  assert.match(hook, /holds\.current\.add\(token\)\s+reschedule\.current\(\)/)
  assert.match(hook, /if \(holds\.current\.delete\(token\)\) reschedule\.current\(\)/)
  assert.match(hook, /if \(!hold \|\| !waiting \|\| !pending\) return\s+return hold\(\)/)
  // The icon timeline holds while its tracks are on their way or not rendered yet (they render in a deferred pass).
  const icons = source("frontend/src/design/components/IconTimeline.tsx")
  assert.match(icons, /useRevealHold\(reveal, loading \|\| \(latestGroups\.length > 0 && groups !== latestGroups\)\)/)
  assert.match(icons, /const groups = useDeferredValue\(latestGroups, noGroups\)/)
})

test("the opening bars wait with the reveal and the hit icons have an opaque border", () => {
  const css = source("frontend/src/design/views.css")
  // While the reveal waits for the page's first drawing, the bars of the opening wait at their start too.
  assert.ok(css.includes('.ww-analysis[data-reveal="waiting"] .ww-bar[data-enter="true"] > * { animation-play-state: paused; }'))
  assert.match(source("frontend/src/views/HistoryAnalysis.tsx"), /className="ww-analysis" data-field="analysis" data-encounter-id=\{id\} data-reveal=\{reveal\?\.at === null \? "waiting" : undefined\}/)
  // A translucent rounded border over a background is drawn as a path; the icon border is the same colour, mixed opaque.
  const hit = css.split(/\r?\n/).find(line => line.startsWith(".ww-itl-hit {"))
  assert.match(hit, /border: 1px solid color-mix\(in srgb, var\(--ww-series, var\(--ww-ink-400\)\) 75%, color-mix\(in oklch, var\(--ww-series, var\(--ww-ink-400\)\) 30%, var\(--ww-surface-3\)\)\);/)
  assert.match(hit, /background: color-mix\(in oklch, var\(--ww-series, var\(--ww-ink-400\)\) 30%, var\(--ww-surface-3\)\);/)
  assert.doesNotMatch(hit, /transparent/)
})

test("the reveal starts only once the frames after the page's first drawing run smoothly", () => {
  const run = times => times.reduce((state, at) => settleFrame(state, at), unsettled)
  // 120 Hz without a stall: settled after settledFrames smooth intervals.
  assert.equal(settledFrames, 3)
  assert.equal(run([0, 8.3, 16.6, 24.9]).smooth, 3)
  // A stall (a first rasterization, a GPU program build) restarts the count, wherever it falls.
  assert.equal(run([0, 8.3, 16.6, 50, 58.3]).smooth, 1)
  assert.equal(run([0, 8.3, 16.6, 50, 58.3, 66.6, 74.9]).smooth, 3)
  // A first interval that is itself a stall is soon known as one: the period is the shortest interval seen.
  const late = run([0, 80, 88.3])
  assert.ok(Math.abs(late.period - 8.3) < 1e-9, `${late.period}`)
  assert.equal(run([0, 80, 88.3, 96.6, 104.9]).smooth >= settledFrames, true)
  // 60 Hz settles the same way; a frame of under 1.75 periods is still smooth, a dropped frame is not.
  assert.equal(run([0, 16.7, 33.4, 50.1]).smooth, 3)
  assert.equal(run([0, 16.7, 33.4, 50.1, 83.5]).smooth, 0)
  assert.ok(smoothFrameFactor > 1.5 && smoothFrameFactor < 2)
  assert.ok(settleLimitMs >= 300 && settleLimitMs <= 1000, `${settleLimitMs} ms`)
})
