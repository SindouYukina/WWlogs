import { useEffect, useMemo, useState } from "react"
import { displayedEncounter, type CombatData } from "./combat-data"
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
 * timeline (empty until the first encounter); per-skill rows have no backend field yet, so they stay absent and the
 * views show their unavailable state.
 */
export function useLiveCombatData() {
  const { snapshot, connection, changeLocale } = useSnapshot()
  const now = useClock()
  const view = snapshot?.view
  const timeline = useMemo(() => timelineFromView(view, displayedEncounter(view)?.encounterId), [view])
  const data = useMemo<CombatData>(() => ({ snapshot, connection, now, timeline }), [snapshot, connection, now, timeline])
  return { data, changeLocale }
}
