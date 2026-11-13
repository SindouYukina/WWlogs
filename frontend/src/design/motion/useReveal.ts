import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { curveEntrance, motionReduced } from "./tokens.ts"

/**
 * One playing of a timeline reveal: a key that changes each time it plays, and when it started (performance.now()). A
 * playing waits at the encounter's start (at null) until every surface that shows it has its content drawn and the main
 * thread is idle, so neither the fastest part of the sweep nor the bars growing beside it is ever lost behind the work of
 * mounting and first drawing the page. A surface whose content is still on its way holds the start (useRevealHold).
 */
export interface RevealClock {
  key: number
  at: number | null
  /** Holds the start of a waiting playing until the returned release is called. */
  hold: () => () => void
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
export function revealPhaseAt(clock: Pick<RevealClock, "key" | "at"> | null, now: number, reduced: boolean): RevealPhase | null {
  if (!clock || reduced) return null
  if (clock.at === null) return { key: clock.key, elapsedMs: 0, waiting: true }
  const elapsed = now - clock.at
  return elapsed < curveEntrance.durationMs ? { key: clock.key, elapsedMs: Math.max(0, elapsed), waiting: false } : null
}

/** The longest a playing waits for an idle main thread, once its frames have settled, before it starts anyway. */
const startWaitLimitMs = 150

/**
 * Frames are settled once settledFrames frame callbacks in a row came at most smoothFrameFactor frame periods apart (the
 * shortest interval seen is the period). A first rasterization, and above all a GPU program it is the first to need,
 * holds up the next frames, so a long interval restarts the count: the sweep never starts in the middle of drawing the page.
 */
export const settledFrames = 3
export const smoothFrameFactor = 1.75
/** The longest a playing waits for its frames to settle before it starts anyway. */
export const settleLimitMs = 500

export interface FrameSettle {
  /** The previous frame callback's time; null before the first. */
  last: number | null
  period: number
  smooth: number
}

export const unsettled: FrameSettle = { last: null, period: Infinity, smooth: 0 }

/** The settle state after a frame callback at `now`. Pure, so tests can check it. */
export function settleFrame(state: FrameSettle, now: number): FrameSettle {
  if (state.last === null) return { last: now, period: state.period, smooth: 0 }
  const interval = now - state.last
  const period = Math.min(state.period, interval)
  return { last: now, period, smooth: interval <= period * smoothFrameFactor + 1 ? state.smooth + 1 : 0 }
}
/**
 * The longest surfaces may hold a playing. Content that is not there by then joins the sweep where it is, so the DPS lines
 * never wait longer for an answer that may not come. It bounds a hold; it is never a delay of its own.
 */
export const holdLimitMs = 1500

/**
 * How long a waiting playing still waits for its holds before its start frames are awaited: 0 when nothing holds it or
 * when it has waited holdLimitMs. Pure, so tests can check it.
 */
export function holdWaitMs(holds: number, waitedMs: number): number {
  return holds === 0 ? 0 : Math.max(0, holdLimitMs - waitedMs)
}

/**
 * The reveal clock of what opens under `key` (an encounter). It starts once, in the first render in which the content of
 * that key is ready, and again when replay is called; hover, filters, ranges and mode switches never start it. A playing
 * first waits at the start while its surfaces draw their content invisibly in place (the waiting state of
 * .ww-atl-reveal), so the first rasterization of everything the sweep will show, and every GPU program it is the first to
 * need, happens while nothing moves. It begins once no surface holds it (useRevealHold) and the frames since have settled
 * (settleFrame): a first rasterization delays the frames after it, so the count of smooth frames only completes once the
 * drawing of that content is done. Then it takes the first idle moment of the main thread (at most startWaitLimitMs
 * later). Every surface of the
 * timeline follows the same clock (useRevealPhase), so the DPS lines and the tracks under them always show the same moment
 * of the encounter.
 */
export function useRevealClock(key: string | number, ready: boolean): { clock: RevealClock | null; replay: () => void } {
  // Holds and their scheduling live outside React state: a hold or a release reschedules the start without rendering the
  // page that owns the clock again.
  const holds = useRef(new Set<symbol>())
  const reschedule = useRef<() => void>(() => {})
  const hold = useCallback(() => {
    const token = Symbol("reveal hold")
    holds.current.add(token)
    reschedule.current()
    return () => {
      if (holds.current.delete(token)) reschedule.current()
    }
  }, [])
  const [clock, setClock] = useState<RevealClock | null>(null)
  const [opened, setOpened] = useState<string | number | null>(null)
  if (ready && opened !== key) {
    setOpened(key)
    setClock(current => ({ key: (current?.key ?? 0) + 1, at: null, hold }))
  }
  useEffect(() => {
    if (!clock || clock.at !== null) return
    const since = performance.now()
    let frame = 0
    let idle = 0
    let limit = 0
    const begin = () => setClock(current => current && current.key === clock.key && current.at === null ? { ...current, at: performance.now() } : current)
    const cancel = () => {
      cancelAnimationFrame(frame)
      cancelIdleCallback(idle)
      window.clearTimeout(limit)
    }
    const waitForFrames = () => {
      const started = performance.now()
      let settle = unsettled
      const step = (now: number) => {
        settle = settleFrame(settle, now)
        if (settle.smooth >= settledFrames || now - started >= settleLimitMs) idle = requestIdleCallback(begin, { timeout: startWaitLimitMs })
        else frame = requestAnimationFrame(step)
      }
      frame = requestAnimationFrame(step)
    }
    const schedule = () => {
      cancel()
      const wait = holdWaitMs(holds.current.size, performance.now() - since)
      if (wait === 0) waitForFrames()
      else limit = window.setTimeout(waitForFrames, wait)
    }
    reschedule.current = schedule
    schedule()
    return () => {
      cancel()
      reschedule.current = () => {}
    }
  }, [clock])
  const replay = useCallback(() => setClock(current => ({ key: (current?.key ?? 0) + 1, at: null, hold })), [hold])
  return { clock, replay }
}

/**
 * Holds the start of `clock` while `pending`: a surface whose content has not arrived, or is not rendered yet, keeps a
 * waiting playing at the start, so that content is drawn (invisibly, in place) before the sweep moves. A playing that has
 * begun is never held; content that arrives while it plays joins it where it is.
 */
export function useRevealHold(clock: RevealClock | null, pending: boolean): void {
  const waiting = clock !== null && clock.at === null
  const hold = clock?.hold
  useLayoutEffect(() => {
    if (!hold || !waiting || !pending) return
    return hold()
  }, [hold, clock?.key, waiting, pending])
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
