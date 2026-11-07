import test from "node:test"
import assert from "node:assert/strict"
import { activeFrameCallbacks, animateFrames, defaultFrameRateCap, frameRateCap, normalizeFrameRateCap, setFrameRateCap, setFrameScheduler } from "../../frontend/src/design/motion/ticker.ts"

/**
 * A display that refreshes every `refreshMs` and a timer queue, both on one virtual clock. Frame requests run at the
 * next refresh, as requestAnimationFrame does; timers run at their time. Counts show how much work the clock asked for.
 */
function fakeDisplay(refreshMs) {
  let now = 0
  let nextId = 1
  const frames = new Map()
  const timers = new Map()
  const counts = { frames: 0, timers: 0 }
  const scheduler = {
    now: () => now,
    requestFrame: callback => { counts.frames++; frames.set(nextId, callback); return nextId++ },
    cancelFrame: id => { frames.delete(id) },
    setTimer: (callback, ms) => { counts.timers++; timers.set(nextId, { at: now + Math.max(0, ms), callback }); return nextId++ },
    clearTimer: id => { timers.delete(id) },
  }
  const nextRefresh = () => (Math.floor(now / refreshMs + 1e-9) + 1) * refreshMs
  /** Advances the clock to `until`, running timers and display refreshes in time order. */
  const run = until => {
    for (;;) {
      const timer = [...timers.entries()].sort((a, b) => a[1].at - b[1].at)[0]
      const refresh = frames.size > 0 ? nextRefresh() : Infinity
      const at = Math.min(timer ? timer[1].at : Infinity, refresh)
      if (at > until) break
      now = at
      if (timer && timer[1].at === at) {
        timers.delete(timer[0])
        timer[1].callback()
        continue
      }
      const due = [...frames.values()]
      frames.clear()
      for (const callback of due) callback(now)
    }
    now = until
  }
  return { scheduler, run, counts, pending: () => frames.size + timers.size }
}

/** Drives one animation for `seconds` and returns its step count and the total time it was told had passed. */
function measure(refreshMs, cap, seconds) {
  const display = fakeDisplay(refreshMs)
  setFrameScheduler(display.scheduler)
  setFrameRateCap(cap)
  let steps = 0
  let elapsed = 0
  const stop = animateFrames(dt => { steps++; elapsed += dt; return true })
  display.run(seconds * 1000)
  stop()
  return { steps, elapsed, display }
}

test.afterEach(() => setFrameScheduler())

test("the cap limits steps per second and never exceeds the display's refresh rate", () => {
  for (const [refreshMs, cap, expected] of [[1000 / 120, 30, 30], [1000 / 120, 60, 60], [1000 / 120, 120, 120], [1000 / 60, 120, 60], [1000 / 60, 30, 30], [1000 / 144, 60, 60], [1000 / 144, 120, 120]]) {
    const { steps } = measure(refreshMs, cap, 10)
    const rate = steps / 10
    assert.ok(Math.abs(rate - expected) <= expected * 0.03, `${Math.round(1000 / refreshMs)} Hz display at cap ${cap}: ${rate} steps/s, want about ${expected}`)
  }
})

test("an animation lasts the same wall time at every cap", () => {
  for (const cap of [30, 60, 120]) {
    const { elapsed } = measure(1000 / 120, cap, 2)
    // Elapsed time is reported to the callback, so a time-based animation reaches the same point at any cap.
    assert.ok(Math.abs(elapsed - 2) < 1000 / cap / 1000 + 1e-9, `cap ${cap}: callbacks saw ${elapsed}s of 2s`)
  }
})

test("between steps the clock sleeps on a timer instead of waking for every display refresh", () => {
  const { steps, display } = measure(1000 / 120, 30, 10)
  // A clock driven only by requestAnimationFrame would ask for all 1,200 refreshes; this one asks about twice per step.
  assert.ok(display.counts.frames <= steps * 2.2, `${display.counts.frames} frame requests for ${steps} steps`)
})

test("a settled clock schedules nothing, and a cap change or restart does not pile up work", () => {
  const display = fakeDisplay(1000 / 120)
  setFrameScheduler(display.scheduler)
  let remaining = 5
  animateFrames(() => --remaining > 0)
  display.run(1000)
  assert.equal(remaining, 0)
  assert.equal(activeFrameCallbacks(), 0)
  assert.equal(display.pending(), 0, "nothing stays scheduled once every animation has finished")

  let steps = 0
  const stop = animateFrames(() => { steps++; return true })
  for (const cap of [120, 30, 60, 120, 30, 60, 120]) setFrameRateCap(cap)
  assert.ok(display.pending() <= 1, `${display.pending()} pending wakeups after repeated cap changes`)
  const before = steps
  display.run(2000)
  assert.ok(Math.abs((steps - before) - 120) <= 4, `after switching, ${steps - before} steps in 1s at cap 120`)
  stop()
  assert.equal(display.pending(), 0, "stopping the last animation cancels its wakeup")
})

test("only the selectable caps are accepted; anything else keeps the default", () => {
  for (const value of [30, 60, 120]) assert.equal(normalizeFrameRateCap(value), value)
  for (const value of [undefined, null, 0, 59, 144, "60", Number.NaN]) assert.equal(normalizeFrameRateCap(value), defaultFrameRateCap)
  setFrameRateCap(30)
  setFrameRateCap(240)
  assert.equal(frameRateCap(), defaultFrameRateCap)
})
