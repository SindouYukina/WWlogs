// One clock shared by every JavaScript-driven animation, currently the text counters (AnimatedNumber "tween"). CSS
// transitions and keyframes are not driven by it: the browser and its compositor run those at the display's rate.
//
// The clock targets `counterFrameRate` steps per second. It is the logical update rate of the counters, not a limit
// on WebView2 or on what the display presents. Each step is aligned to a display frame through requestAnimationFrame,
// so a step never lands between two frames, and between steps the clock sleeps on a timer instead of waking for every
// display refresh. Callbacks receive the real elapsed time, so an animation lasts the same wall time whatever rate is
// reached; a display slower than the target simply steps once per frame. The clock stops as soon as nothing is
// animating, so a settled window schedules no work, and requestAnimationFrame does not run in hidden windows.

export type FrameCallback = (dtSeconds: number) => boolean

/** Steps per second the counters aim for. Fixed: it is a product choice, not a user setting. */
export const counterFrameRate = 60

/** The browser services the clock uses. Tests replace them with a controllable clock. */
export interface FrameScheduler {
  now(): number
  requestFrame(callback: (now: number) => void): number
  cancelFrame(handle: number): void
  setTimer(callback: () => void, ms: number): unknown
  clearTimer(handle: unknown): void
}

const browserScheduler: FrameScheduler = {
  now: () => performance.now(),
  requestFrame: callback => requestAnimationFrame(callback),
  cancelFrame: handle => cancelAnimationFrame(handle),
  setTimer: (callback, ms) => setTimeout(callback, ms),
  clearTimer: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
}

/** A step is taken on the first display frame within this many milliseconds of its due time. */
const earlyToleranceMs = 4
/** While more than this remains, the clock sleeps on a timer and only then asks for a display frame. */
const sleepMarginMs = 10
/** A long gap (hidden window, debugger) must not look like one giant animation step. */
const maxStepSeconds = 0.1

let scheduler = browserScheduler
const active = new Set<FrameCallback>()
let last = 0
let due = 0
let frame: number | undefined
let timer: unknown

const stepMs = 1000 / counterFrameRate

function cancelPending() {
  if (frame !== undefined) scheduler.cancelFrame(frame)
  if (timer !== undefined) scheduler.clearTimer(timer)
  frame = undefined
  timer = undefined
}

function wake() {
  const wait = due - scheduler.now()
  if (wait > sleepMarginMs) {
    timer = scheduler.setTimer(() => {
      timer = undefined
      frame = scheduler.requestFrame(onFrame)
    }, wait - sleepMarginMs)
  } else {
    frame = scheduler.requestFrame(onFrame)
  }
}

function onFrame(now: number) {
  frame = undefined
  if (active.size === 0) return
  if (now < due - earlyToleranceMs) {
    wake()
    return
  }
  const dt = Math.min(Math.max(0, now - last) / 1000, maxStepSeconds)
  last = now
  // Due times advance by whole steps, so the average rate is the cap even when frames do not divide it evenly.
  due += stepMs
  if (due <= now) due = now + stepMs
  for (const callback of active) {
    if (!callback(dt)) active.delete(callback)
  }
  if (active.size > 0) wake()
}

/** Runs callback on the shared clock until it returns false or the returned function is called. */
export function animateFrames(callback: FrameCallback): () => void {
  active.add(callback)
  if (frame === undefined && timer === undefined) {
    last = scheduler.now()
    due = last + stepMs
    wake()
  }
  return () => {
    active.delete(callback)
    if (active.size === 0) cancelPending()
  }
}

export function activeFrameCallbacks(): number {
  return active.size
}

/** Test seam: replaces the browser services and resets the clock. Passing nothing restores the browser. */
export function setFrameScheduler(next?: FrameScheduler): void {
  cancelPending()
  active.clear()
  scheduler = next ?? browserScheduler
}
