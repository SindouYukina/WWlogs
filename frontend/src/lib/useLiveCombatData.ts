import { useEffect, useMemo, useState } from "react"
import { displayedEncounter, type CombatData } from "./combat-data"
import { skillBreakdowns } from "./combat-view"
import { liveClockActive } from "./effects"
import { timelineFromView } from "./timeline"
import { useSnapshot } from "./useSnapshot"

/**
 * A 1 Hz clock that ticks only while it is active and the window is shown. It keeps its last value otherwise and
 * catches up the moment both hold again.
 */
export function useClock(active: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
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
  }, [active])
  return now
}

/**
 * The Live provider. It publishes only what the Go engine reports: the DPS timeline comes from the snapshot's engine
 * timeline (empty until the first encounter), and skill rows come from the engine's per-contributor skill split of
 * the same encounter (or capture interval) the ranking shows.
 */
export function useLiveCombatData() {
  const { snapshot, connection, changeLocale } = useSnapshot()
  const now = useClock(liveClockActive(snapshot))
  const view = snapshot?.view
  const timeline = useMemo(() => timelineFromView(view, displayedEncounter(view)?.encounterId), [view])
  const skillsOf = useMemo(() => {
    const source = displayedEncounter(view) ?? view
    const breakdowns = source ? skillBreakdowns(source) : new Map()
    return (rowKey: string) => breakdowns.get(rowKey)
  }, [view])
  const data = useMemo<CombatData>(() => ({ snapshot, connection, now, timeline, skillsOf }), [snapshot, connection, now, timeline, skillsOf])
  return { data, changeLocale }
}
