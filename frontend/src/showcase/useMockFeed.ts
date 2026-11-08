import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import type { CombatData } from "@/lib/combat-data"
import { MockFeed, historySlots, mockPlayerId, type ListeningScenario, type MockOutput } from "./mock-feed.ts"

export interface FeedControls {
  /** The Mock provider's output, in the same contract the Live provider fills. */
  data: CombatData
  output: MockOutput
  feed: MockFeed
  publishMs: number
  setPublishMs: (ms: number) => void
  /** Stops publishing. Trust gating then treats the last snapshot as stale, exactly as for a stalled engine. */
  paused: boolean
  setPaused: (paused: boolean) => void
  /** Applies a scripted change and publishes it immediately, as a new engine snapshot would. */
  apply: (change: (feed: MockFeed) => void) => void
  /** Wall clock used by freshness checks. Updated at 1 Hz, as in production views. */
  now: number
  listeningScenario: ListeningScenario
  setListeningScenario: (scenario: ListeningScenario) => void
}

export function useMockFeed(initialPublishMs = 250): FeedControls {
  const { i18n } = useTranslation()
  const feed = useMemo(() => new MockFeed(), [])
  feed.locale = i18n.language
  const [output, setOutput] = useState<MockOutput>(() => feed.publish(0.25))
  const [publishMs, setPublishMs] = useState(initialPublishMs)
  const [paused, setPaused] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const [listeningScenario, setScenario] = useState<ListeningScenario>("active")
  const last = useRef(performance.now())
  useEffect(() => {
    // Names are resolved per locale, as the production display projection does.
    const onLanguage = () => { setOutput(feed.publish(0)) }
    i18n.on("languageChanged", onLanguage)
    return () => i18n.off("languageChanged", onLanguage)
  }, [feed, i18n])
  useEffect(() => {
    if (paused) return
    last.current = performance.now()
    const timer = setInterval(() => {
      // Hidden windows skip publishing, matching the production windows.
      if (document.hidden) {
        last.current = performance.now()
        return
      }
      const current = performance.now()
      setOutput(feed.publish((current - last.current) / 1000))
      last.current = current
    }, publishMs)
    return () => clearInterval(timer)
  }, [feed, paused, publishMs])
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  const apply = useCallback((change: (feed: MockFeed) => void) => {
    change(feed)
    setOutput(feed.publish(0))
    setNow(Date.now())
  }, [feed])
  const setListeningScenario = useCallback((scenario: ListeningScenario) => {
    feed.listeningScenario = scenario
    setScenario(scenario)
    setOutput(feed.publish(0))
  }, [feed])
  const data = useMemo<CombatData>(() => ({
    snapshot: output.snapshot,
    connection: "connected",
    now,
    paused,
    // Mock series are keyed like the live timeline: by the ranking row key of each role.
    timeline: { slots: historySlots, slotSeconds: 1, series: new Map([...output.history].map(([roleId, values]) => [`role-${mockPlayerId}-${roleId}`, values])) },
    skillsOf: output.skillsOf,
  }), [output, now, paused])
  return { data, output, feed, publishMs, setPublishMs, paused, setPaused, apply, now, listeningScenario, setListeningScenario }
}
