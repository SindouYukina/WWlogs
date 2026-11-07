import { useEffect, useMemo, useState } from "react"
import { displayedEncounter, type CombatData } from "./combat-data"
import { skillBreakdowns } from "./combat-view"
import { normalizeUpdateInterval } from "./snapshot"
import { timelineFromView } from "./timeline"
import { useSnapshot } from "./useSnapshot"

/** A 1 Hz clock that stops while the window is hidden and catches up the moment it is shown again. */
export function useClock(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | undefined
    const sync = () => {
      clearInterval(timer)
      timer = undefined
      if (document.hidden) return
      setNow(Date.now())
      timer = setInterval(() => setNow(Date.now()), 1000)
    }
    sync()
    document.addEventListener("visibilitychange", sync)
    return () => {
      clearInterval(timer)
      document.removeEventListener("visibilitychange", sync)
    }
  }, [])
  return now
}

/**
 * The Live provider. It publishes only what the Go engine reports: the DPS timeline comes from the snapshot's engine
 * timeline (empty until the first encounter), and skill rows come from the engine's per-contributor skill split of
 * the same encounter (or capture interval) the ranking shows.
 */
export function useLiveCombatData() {
  const { snapshot, connection, changeLocale } = useSnapshot()
  const now = useClock()
  const view = snapshot?.view
  const timeline = useMemo(() => timelineFromView(view, displayedEncounter(view)?.encounterId), [view])
  const skillsOf = useMemo(() => {
    const source = displayedEncounter(view) ?? view
    const breakdowns = source ? skillBreakdowns(source) : new Map()
    return (rowKey: string) => breakdowns.get(rowKey)
  }, [view])
  // Countdown bars ease across one publish interval, so they follow the same cadence setting as the data.
  const tickMs = normalizeUpdateInterval(snapshot?.updateIntervalMs)
  const data = useMemo<CombatData>(() => ({ snapshot, connection, now, timeline, skillsOf, tickMs }), [snapshot, connection, now, timeline, skillsOf, tickMs])
  return { data, changeLocale }
}
