import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { MockFeed, type MockOutput } from "./mock-feed.ts"

export interface FeedControls {
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
}

export function useMockFeed(initialPublishMs = 250): FeedControls {
  const feed = useMemo(() => new MockFeed(), [])
  const [output, setOutput] = useState<MockOutput>(() => feed.publish(0.25))
  const [publishMs, setPublishMs] = useState(initialPublishMs)
  const [paused, setPaused] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const last = useRef(performance.now())
  useEffect(() => {
    if (paused) return
    last.current = performance.now()
    const timer = setInterval(() => {
      // Hidden windows skip publishing, matching the policy proposed for production Overlay windows.
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
  return { output, feed, publishMs, setPublishMs, paused, setPaused, apply, now }
}
