// Pure motion math with no DOM access. Node's test runner imports this file directly.

export interface SpringParams {
  stiffness: number
  damping: number
  mass: number
}

export interface SpringCurve {
  /** Time until the spring stays within the settle threshold, in milliseconds. */
  durationMs: number
  /** A CSS linear() easing function that reproduces the spring over durationMs. */
  easing: string
}

const settleThreshold = 0.002
const maxSeconds = 3

/** Position of a unit-step spring released from rest, for all damping regimes. */
export function springPosition({ stiffness, damping, mass }: SpringParams, t: number): number {
  const omega = Math.sqrt(stiffness / mass)
  const zeta = damping / (2 * Math.sqrt(stiffness * mass))
  if (zeta < 1) {
    const damped = omega * Math.sqrt(1 - zeta * zeta)
    return 1 - Math.exp(-zeta * omega * t) * (Math.cos(damped * t) + (zeta * omega / damped) * Math.sin(damped * t))
  }
  if (zeta === 1) return 1 - Math.exp(-omega * t) * (1 + omega * t)
  const root = omega * Math.sqrt(zeta * zeta - 1)
  const r1 = -zeta * omega + root
  const r2 = -zeta * omega - root
  return 1 - (r2 * Math.exp(r1 * t) - r1 * Math.exp(r2 * t)) / (r2 - r1)
}

function settleSeconds(params: SpringParams): number {
  const step = 1 / 240
  let settled = maxSeconds
  for (let t = maxSeconds; t >= 0; t -= step) {
    if (Math.abs(springPosition(params, t) - 1) > settleThreshold) break
    settled = t
  }
  return settled
}

/** Drops samples that a straight line reproduces within tolerance, so the CSS value stays short. */
function simplify(points: ReadonlyArray<readonly [number, number]>, tolerance: number): Array<readonly [number, number]> {
  if (points.length < 3) return [...points]
  const first = points[0]!
  const last = points[points.length - 1]!
  let worst = 0
  let index = 0
  for (let i = 1; i < points.length - 1; i++) {
    const point = points[i]!
    const expected = first[1] + (last[1] - first[1]) * ((point[0] - first[0]) / (last[0] - first[0]))
    const error = Math.abs(point[1] - expected)
    if (error > worst) {
      worst = error
      index = i
    }
  }
  if (worst <= tolerance) return [first, last]
  return [...simplify(points.slice(0, index + 1), tolerance).slice(0, -1), ...simplify(points.slice(index), tolerance)]
}

/**
 * Converts a spring into a CSS linear() curve. The compositor then runs real spring motion for transform and opacity
 * with no JavaScript per frame, which is what lets WWlogs avoid a runtime animation library.
 */
export function springCurve(params: SpringParams, tolerance = 0.004): SpringCurve {
  const seconds = settleSeconds(params)
  const samples: Array<readonly [number, number]> = []
  const count = 96
  for (let i = 0; i <= count; i++) {
    const progress = i / count
    samples.push([progress, i === count ? 1 : springPosition(params, progress * seconds)])
  }
  const stops = simplify(samples, tolerance).map(([progress, value], i, all) => {
    const text = String(Number(value.toFixed(4)))
    return i === 0 || i === all.length - 1 ? text : `${text} ${Number((progress * 100).toFixed(2))}%`
  })
  return { durationMs: Math.round(seconds * 1000), easing: `linear(${stops.join(", ")})` }
}

export interface NumberState {
  position: number
  velocity: number
}

/**
 * Advances a critically damped follower toward target. The closed form is stable at any frame time and a follower
 * released from behind a rising target never passes it, so an animated counter cannot display a value the engine has
 * not reported.
 */
export function stepCritical(state: NumberState, target: number, omega: number, dt: number): NumberState {
  const offset = state.position - target
  const c = state.velocity + omega * offset
  const decay = Math.exp(-omega * dt)
  return {
    position: target + (offset + c * dt) * decay,
    velocity: (c - omega * (offset + c * dt)) * decay,
  }
}

export type NumberPlan = "hide" | "snap" | "animate"

/**
 * Decides how a displayed number reaches a new engine value. Encounter resets, values that go backwards and the first
 * value after missing data are facts to show immediately, not transitions to animate.
 */
export function planNumberUpdate(previous: number | undefined, next: number | undefined, sameScope: boolean, stale: boolean): NumberPlan {
  if (next === undefined || !Number.isFinite(next)) return "hide"
  if (previous === undefined || !sameScope || stale || next < previous) return "snap"
  return "animate"
}

/**
 * Decides how a number that describes a selection (a History range, contributor or enemy) reaches the answer for a new
 * selection. Unlike a live counter, a lower value is not a reset: it is the answer for another selection, so it moves the
 * same way a higher one does. A new scope (another encounter), stale data and the first value are shown at once.
 */
export function planSelectionUpdate(previous: number | undefined, next: number | undefined, sameScope: boolean, stale: boolean): NumberPlan {
  if (next === undefined || !Number.isFinite(next)) return "hide"
  if (previous === undefined || !sameScope || stale) return "snap"
  return "animate"
}

/**
 * A fixed-duration ease-out cubic from one value to another. It moves monotonically, so it never shows a value outside
 * [from, to], and at durationMs it is exactly `to`.
 */
export function easeNumber(from: number, to: number, elapsedMs: number, durationMs: number): number {
  if (!(durationMs > 0) || elapsedMs >= durationMs) return to
  const progress = Math.max(0, elapsedMs) / durationMs
  return from + (to - from) * (1 - (1 - progress) ** 3)
}

export type PresencePhase = "enter" | "idle" | "exit"

export interface PresenceEntry<T> {
  key: string
  item: T
  phase: PresencePhase
  /** Set while exiting: the time at which the entry leaves the list. */
  removeAt?: number
}

/**
 * Merges the latest items with entries still playing their exit animation. Exiting entries keep their previous slot so
 * that neighbours do not jump before the exit has been seen. A key that returns while exiting comes back to life.
 */
export function reconcilePresence<T>(previous: ReadonlyArray<PresenceEntry<T>>, items: readonly T[], keyOf: (item: T) => string, now: number, exitMs: number): Array<PresenceEntry<T>> {
  const incoming = new Map(items.map(item => [keyOf(item), item]))
  const seen = new Set<string>()
  const result: Array<PresenceEntry<T>> = []
  for (const entry of previous) {
    const item = incoming.get(entry.key)
    if (item !== undefined) {
      seen.add(entry.key)
      result.push({ key: entry.key, item, phase: entry.phase === "exit" ? "enter" : entry.phase })
    } else if (entry.phase !== "exit") {
      result.push({ key: entry.key, item: entry.item, phase: "exit", removeAt: now + exitMs })
    } else if (entry.removeAt !== undefined && entry.removeAt > now) {
      result.push(entry)
    }
  }
  for (const [key, item] of incoming) {
    if (!seen.has(key)) result.push({ key, item, phase: "enter" })
  }
  return result
}
