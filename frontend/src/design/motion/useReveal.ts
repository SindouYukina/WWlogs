import { useCallback, useEffect, useState } from "react"
import { curveEntrance, motionReduced } from "./tokens.ts"

/**
 * One playing of a timeline reveal: a key that changes each time it plays, and when it started (performance.now()). A
 * playing waits at the encounter's start (at null) until the page that shows it has been drawn and the main thread is
 * idle, so the fastest part of the sweep is never lost behind the work of mounting the page.
 */
export interface RevealClock {
  key: number
  at: number | null
}

/** A surface's part in a reveal: which playing, how far into it the surface starts, or still waiting at its start. */
export interface RevealPhase {
  key: number
  elapsedMs: number
  waiting: boolean
}

/**
 * Where a surface at `now` joins `clock`: waiting at the start while the playing waits for its first frame, at its start
 * when they begin together, where it is when the surface arrives while it plays, and not at all once it has ended or under
 * reduced motion. Pure, so tests can check it.
 */
export function revealPhaseAt(clock: RevealClock | null, now: number, reduced: boolean): RevealPhase | null {
  if (!clock || reduced) return null
  if (clock.at === null) return { key: clock.key, elapsedMs: 0, waiting: true }
  const elapsed = now - clock.at
  return elapsed < curveEntrance.durationMs ? { key: clock.key, elapsedMs: Math.max(0, elapsed), waiting: false } : null
}

/** The longest a playing waits for an idle main thread before it starts anyway. */
const startWaitLimitMs = 150

/**
 * The reveal clock of what opens under `key` (an encounter). It starts once, in the first render in which the content of
 * that key is ready, and again when replay is called; hover, filters, ranges and mode switches never start it. A playing
 * first waits at the start: after the frame that mounted the page has been drawn, it begins at the first idle moment of
 * the main thread (at most startWaitLimitMs later), so the deferred tracks and the first layout are done before the sweep
 * moves. Every surface of the timeline follows the same clock (useRevealPhase), so the DPS lines and the tracks under
 * them always show the same moment of the encounter, even when one of them arrives later.
 */
export function useRevealClock(key: string | number, ready: boolean): { clock: RevealClock | null; replay: () => void } {
  const [clock, setClock] = useState<RevealClock | null>(null)
  const [opened, setOpened] = useState<string | number | null>(null)
  if (ready && opened !== key) {
    setOpened(key)
    setClock(current => ({ key: (current?.key ?? 0) + 1, at: null }))
  }
  useEffect(() => {
    if (!clock || clock.at !== null) return
    const begin = () => setClock(current => current && current.key === clock.key && current.at === null ? { key: current.key, at: performance.now() } : current)
    let idle = 0
    const frame = requestAnimationFrame(() => { idle = requestIdleCallback(begin, { timeout: startWaitLimitMs }) })
    return () => {
      cancelAnimationFrame(frame)
      cancelIdleCallback(idle)
    }
  }, [clock])
  const replay = useCallback(() => setClock(current => ({ key: (current?.key ?? 0) + 1, at: null })), [])
  return { clock, replay }
}

/**
 * Whether this surface plays the reveal of `clock`, decided in the first render in which the surface has its content
 * (`ready`) for that playing and again when the playing begins, and kept until `end` is called from the animation's own
 * end. A surface whose data arrives while the reveal plays joins it where it is; one whose data arrives after it ended
 * shows at once. Re-renders never restart or cut it.
 */
export function useRevealPhase(clock: RevealClock | null, ready = true): { phase: RevealPhase | null; end: () => void } {
  const decide = () => ({ key: clock?.key ?? null, at: clock?.at ?? null, decided: ready, phase: ready ? revealPhaseAt(clock, performance.now(), motionReduced()) : null })
  const [state, setState] = useState(decide)
  let current = state
  if ((clock?.key ?? null) !== state.key || (clock?.at ?? null) !== state.at || (ready && !state.decided)) {
    current = decide()
    setState(current)
  }
  const end = useCallback(() => setState(previous => previous.phase ? { ...previous, phase: null } : previous), [])
  return { phase: current.phase, end }
}
