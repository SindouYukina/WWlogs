import { useCallback, useEffect, useRef, useState } from "react"
import { host, type Adapter, type OverlayChange } from "./host"
import type { OverlayKind, OverlayStates, Snapshot } from "./snapshot"

/**
 * What the Settings view reads and calls. The Live controller talks to the Go host, which owns every value; the
 * Development controller keeps local demo state. Settings apply at once, so there is no form and no Save step.
 */
export interface SettingsController {
  locale: string
  setLocale: (locale: string) => void
  theme: string | undefined
  reduceMotion: boolean
  setAppearance: (theme: string, reduceMotion: boolean) => void
  adapters: readonly Adapter[]
  defaultAdapter: string
  setDefaultAdapter: (ipv4: string) => void
  refreshAdapters: () => void
  refreshing: boolean
  overlays: OverlayStates | undefined
  updateOverlay: (kind: OverlayKind, change: OverlayChange) => void
  resetOverlay: (kind: OverlayKind) => void
  app: { version: string; commit: string; profile: string } | undefined
  /** A failed request. Values are unchanged because the host only reports what it applied. */
  failed: boolean
  /** A recoverable settings problem reported by the host, as a warning code. */
  warning: string
  connected: boolean
}

export function useLiveSettings(snapshot: Snapshot | null, connected: boolean, changeLocale: (locale: string) => Promise<void>) {
  const [adapters, setAdapters] = useState<Adapter[]>([])
  const [adaptersLoaded, setAdaptersLoaded] = useState(false)
  const [defaultAdapter, setStoredAdapter] = useState("")
  const [startupWarning, setStartupWarning] = useState("")
  const [refreshing, setRefreshing] = useState(false)
  const [failed, setFailed] = useState(false)
  const mounted = useRef(true)
  const guard = useCallback((request: Promise<unknown>) => {
    setFailed(false)
    request.catch(() => { if (mounted.current) setFailed(true) })
  }, [])
  useEffect(() => {
    mounted.current = true
    void Promise.allSettled([host.listAdapters(), host.getSettings()]).then(([interfaces, preferences]) => {
      if (!mounted.current) return
      setAdapters(interfaces.status === "fulfilled" ? interfaces.value ?? [] : [])
      if (interfaces.status === "rejected") setFailed(true)
      if (preferences.status === "fulfilled") {
        setStoredAdapter(preferences.value.settings.defaultAdapterIPv4)
        setStartupWarning(preferences.value.warning ?? "")
      } else setFailed(true)
      setAdaptersLoaded(true)
    })
    return () => { mounted.current = false }
  }, [])
  const refreshAdapters = useCallback(() => {
    setRefreshing(true)
    setFailed(false)
    host.listAdapters().then(list => { if (mounted.current) setAdapters(list ?? []) }, () => { if (mounted.current) setFailed(true) }).finally(() => { if (mounted.current) setRefreshing(false) })
  }, [])
  const controller: SettingsController = {
    locale: snapshot?.locale ?? "",
    setLocale: locale => guard(changeLocale(locale)),
    theme: snapshot?.theme,
    reduceMotion: snapshot?.reduceMotion === true,
    setAppearance: (theme, reduceMotion) => guard(host.setAppearance(theme, reduceMotion)),
    adapters,
    defaultAdapter,
    setDefaultAdapter: ipv4 => guard(host.setDefaultAdapter(ipv4).then(state => { if (mounted.current) setStoredAdapter(state.settings.defaultAdapterIPv4) })),
    refreshAdapters,
    refreshing,
    overlays: snapshot?.overlays,
    updateOverlay: (kind, change) => guard(host.updateOverlay(kind, change)),
    resetOverlay: kind => guard(host.resetOverlay(kind)),
    app: snapshot?.service.app,
    failed,
    warning: snapshot?.settingsWarning ?? startupWarning,
    connected,
  }
  return { controller, adaptersLoaded }
}
