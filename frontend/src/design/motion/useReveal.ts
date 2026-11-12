import { useCallback, useState } from "react"
import { curveEntrance, motionReduced } from "./tokens.ts"

/** One playing of a timeline reveal: a key that changes each time it plays, and when it started (performance.now()). */
export interface RevealClock {
  key: number
  at: number
}

/** A surface's part in a reveal: which playing, and how far into it the surface starts. */
export interface RevealPhase {
  key: number
  elapsedMs: number
}

/**
 * Where a surface mounting at `now` joins `clock`: at its start when they begin together, where it is when the surface
 * mounts while it plays, and not at all once it has ended or under reduced motion. Pure, so tests can check it.
 */
export function revealPhaseAt(clock: RevealClock | null, now: number, reduced: boolean): RevealPhase | null {
  if (!clock || reduced) return null
  const elapsed = now - clock.at
  return elapsed < curveEntrance.durationMs ? { key: clock.key, elapsedMs: Math.max(0, elapsed) } : null
}

/**
 * The reveal clock of what opens under `key` (an encounter). It starts once, in the first render in which the content of
 * that key is ready, and again when replay is called; hover, filters, ranges and mode switches never start it. Every
 * surface of the timeline follows the same clock (useRevealPhase), so the DPS lines and the tracks under them always show
 * the same moment of the encounter, even when one of them arrives later.
 */
export function useRevealClock(key: string | number, ready: boolean): { clock: RevealClock | null; replay: () => void } {
  const [clock, setClock] = useState<RevealClock | null>(null)
  const [opened, setOpened] = useState<string | number | null>(null)
  if (ready && opened !== key) {
    setOpened(key)
    setClock(current => ({ key: (current?.key ?? 0) + 1, at: performance.now() }))
  }
  const replay = useCallback(() => setClock(current => ({ key: (current?.key ?? 0) + 1, at: performance.now() })), [])
  return { clock, replay }
}

/**
 * Whether this surface plays the reveal of `clock`, decided when the surface mounts or a new playing starts, and kept
 * until `end` is called from the animation's own end. Re-renders never restart or cut it.
 */
export function useRevealPhase(clock: RevealClock | null): { phase: RevealPhase | null; end: () => void } {
  const [state, setState] = useState(() => ({ key: clock?.key ?? null, phase: revealPhaseAt(clock, performance.now(), motionReduced()) }))
  let current = state
  if ((clock?.key ?? null) !== state.key) {
    current = { key: clock?.key ?? null, phase: revealPhaseAt(clock, performance.now(), motionReduced()) }
    setState(current)
  }
  const end = useCallback(() => setState(previous => previous.phase ? { key: previous.key, phase: null } : previous), [])
  return { phase: current.phase, end }
}
