import test from "node:test"
import assert from "node:assert/strict"
import { activeFrameCallbacks, animateFrames, counterFrameRate, setFrameScheduler } from "../../frontend/src/design/motion/ticker.ts"

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
function measure(refreshMs, seconds) {
  const display = fakeDisplay(refreshMs)
  setFrameScheduler(display.scheduler)
  let steps = 0
  let elapsed = 0
  const stop = animateFrames(dt => { steps++; elapsed += dt; return true })
  display.run(seconds * 1000)
  stop()
  return { steps, elapsed, display }
}

test.afterEach(() => setFrameScheduler())

test("counters target 60 steps per second and follow a slower display", () => {
  assert.equal(counterFrameRate, 60)
  for (const [hz, expected] of [[144, 60], [120, 60], [60, 60], [50, 50], [30, 30]]) {
    const { steps } = measure(1000 / hz, 10)
    const rate = steps / 10
    assert.ok(Math.abs(rate - expected) <= expected * 0.03, `${hz} Hz display: ${rate} steps/s, want about ${expected}`)
  }
})

test("an animation lasts the same wall time on every display", () => {
  for (const hz of [144, 120, 60, 30]) {
    const { elapsed } = measure(1000 / hz, 2)
    // Elapsed time is reported to the callback, so a time-based animation reaches the same point at any step rate.
    assert.ok(Math.abs(elapsed - 2) < 1 / 30 + 1e-9, `${hz} Hz: callbacks saw ${elapsed}s of 2s`)
  }
})

test("between steps the clock sleeps on a timer instead of waking for every display refresh", () => {
  const { steps, display } = measure(1000 / 144, 10)
  // A clock driven only by requestAnimationFrame would ask for all 1,440 refreshes; this one asks about twice per step.
  assert.ok(display.counts.frames <= steps * 2.2, `${display.counts.frames} frame requests for ${steps} steps`)
})

test("a settled clock schedules nothing, and restarts never pile up work", () => {
  const display = fakeDisplay(1000 / 120)
  setFrameScheduler(display.scheduler)
  let remaining = 5
  animateFrames(() => --remaining > 0)
  display.run(1000)
  assert.equal(remaining, 0)
  assert.equal(activeFrameCallbacks(), 0)
  assert.equal(display.pending(), 0, "nothing stays scheduled once every animation has finished")

  // Many counters starting and stopping (mounts, unmounts, new values) share one wakeup.
  const stops = []
  let steps = 0
  for (let i = 0; i < 12; i++) stops.push(animateFrames(() => { steps++; return true }))
  for (let i = 0; i < 6; i++) stops[i]()
  assert.ok(display.pending() <= 1, `${display.pending()} pending wakeups for one clock`)
  display.run(2000)
  assert.ok(Math.abs(steps - 6 * 60) <= 6 * 2, `${steps} callback steps for 6 counters in 1s`)
  for (const stop of stops.slice(6)) stop()
  assert.equal(activeFrameCallbacks(), 0)
  assert.equal(display.pending(), 0, "stopping the last animation cancels its wakeup")
})
