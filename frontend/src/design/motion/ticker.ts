// One clock shared by every JavaScript-driven animation, currently the text counters. Measured in WebView2 on a 120 Hz
// display, a requestAnimationFrame loop forced a main-thread frame and a layout on every refresh; text does not read
// better above about 30 updates per second, so the clock is a fixed timer. It stops as soon as nothing is animating, so
// a settled window schedules no work, and the browser throttles it in hidden windows.

export type FrameCallback = (dtSeconds: number) => boolean

export const counterIntervalMs = 33

const active = new Set<FrameCallback>()
let handle: ReturnType<typeof setTimeout> | undefined
let last = 0

function tick() {
  const now = performance.now()
  // A long gap (hidden window, debugger) must not look like one giant animation step.
  const dt = Math.min((now - last) / 1000, 0.1)
  last = now
  for (const callback of active) {
    if (!callback(dt)) active.delete(callback)
  }
  handle = active.size > 0 ? setTimeout(tick, counterIntervalMs) : undefined
}

/** Runs callback on the shared clock until it returns false or the returned function is called. */
export function animateFrames(callback: FrameCallback): () => void {
  active.add(callback)
  if (handle === undefined) {
    last = performance.now()
    handle = setTimeout(tick, counterIntervalMs)
  }
  return () => {
    active.delete(callback)
  }
}

export function activeFrameCallbacks(): number {
  return active.size
}
