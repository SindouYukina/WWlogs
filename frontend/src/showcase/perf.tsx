import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { activeFrameCallbacks } from "../design/motion/ticker.ts"
import type { ThemeId } from "@/design"
import type { FeedControls } from "./useMockFeed"
import type { SchemeId } from "./typefaces"

export interface PerfBridge {
  setSection: (section: "combat" | "effects" | "dashboard" | "settings" | "typography") => void
  setTheme: (theme: ThemeId) => void
  setScheme: (scheme: SchemeId) => void
  setLocale: (locale: string) => void
  setNumberMode: (mode: "tween" | "roll") => void
  controls: () => FeedControls
}

interface PerfSample {
  fps: number
  p95: number
  worst: number
  longTasks: number
  animations: number
  frameCallbacks: number
  dom: number
}

/** A review aid, closed by default. While open it schedules frames, so idle-cost measurements run with it closed. */
export function PerfPanel() {
  const { t } = useTranslation("showcase")
  const [sample, setSample] = useState<PerfSample | null>(null)
  useEffect(() => {
    let frame = 0
    let last = performance.now()
    let durations: number[] = []
    let longTasks = 0
    const observer = "PerformanceObserver" in window ? new PerformanceObserver(list => { longTasks += list.getEntries().length }) : null
    try { observer?.observe({ entryTypes: ["longtask"] }) } catch { /* Long task timing is not available. */ }
    const tick = (now: number) => {
      durations.push(now - last)
      last = now
      frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    const timer = setInterval(() => {
      const sorted = [...durations].sort((a, b) => a - b)
      const total = durations.reduce((sum, value) => sum + value, 0)
      setSample({
        fps: total > 0 ? (durations.length * 1000) / total : 0,
        p95: sorted[Math.floor(sorted.length * 0.95)] ?? 0,
        worst: sorted[sorted.length - 1] ?? 0,
        longTasks,
        animations: document.getAnimations().length,
        frameCallbacks: activeFrameCallbacks(),
        dom: document.getElementsByTagName("*").length,
      })
      durations = []
      longTasks = 0
    }, 1000)
    return () => {
      cancelAnimationFrame(frame)
      clearInterval(timer)
      observer?.disconnect()
    }
  }, [])
  const rows: Array<[string, string]> = sample ? [
    [t("perf.fps"), sample.fps.toFixed(0)],
    [t("perf.p95"), `${sample.p95.toFixed(1)} ms`],
    [t("perf.worst"), `${sample.worst.toFixed(1)} ms`],
    [t("perf.longTasks"), String(sample.longTasks)],
    [t("perf.animations"), String(sample.animations)],
    [t("perf.frameCallbacks"), String(sample.frameCallbacks)],
    [t("perf.dom"), String(sample.dom)],
  ] : []
  return <div className="sc-perf" role="status">{rows.map(([label, value]) => <div key={label}><span>{label}</span><b className="ww-num">{value}</b></div>)}</div>
}
