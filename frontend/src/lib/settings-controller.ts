import { useCallback, useEffect, useRef, useState } from "react"
import type { AppIdentity } from "./build-identity"
import { captureBusy, environmentNotice, type Adapter, type EnvironmentState, type NpcapDownload } from "./capture-environment"
import { host, type OverlayChange } from "./host"
import { normalizeUpdateInterval, type OverlayKind, type OverlayStates, type Snapshot, type UpdateInterval } from "./snapshot"

/** The official Npcap download as far as it is known. `info` stays null until the host has answered. */
export interface NpcapDownloadState {
  info: NpcapDownload | null
  resolving: boolean
  opening: boolean
  /** The browser was asked to open, so the next step is installing. */
  opened: boolean
  openFailed: boolean
}

/**
 * What the Settings view reads and calls. The Live controller talks to the Go host, which owns every value; the
 * Development controller keeps local demo state. Settings apply at once, so there is no form and no Save step.
 */
export interface SettingsController {
  locale: string
  /** The value of the language selector: "auto" while the language follows Windows, otherwise the chosen locale. */
  languageChoice: string
  setLocale: (locale: string) => void
  theme: string | undefined
  reduceMotion: boolean
  setAppearance: (theme: string, reduceMotion: boolean) => void
  /** The one live combat data cadence for the Dashboard and both Overlays. */
  updateIntervalMs: UpdateInterval
  setUpdateInterval: (ms: UpdateInterval) => void
  adapters: readonly Adapter[]
  /** The capture interface. Empty means none is chosen yet. */
  defaultAdapter: string
  setDefaultAdapter: (ipv4: string) => void
  /** A launch starts one capture by itself on `defaultAdapter`. Takes effect at the next launch. */
  autoStartCapture: boolean
  setAutoStartCapture: (enabled: boolean) => void
  /** A capture is starting, running or stopping, so its interface cannot be changed now. */
  captureBusy: boolean
  /** The last finished check, or null before the first one. */
  environment: EnvironmentState | null
  /** A check is running. Starting another while this is true does nothing. */
  checking: boolean
  checkEnvironment: () => void
  npcapDownload: NpcapDownloadState
  openNpcapDownload: () => void
  /** Opens one of the Licenses page's official links in the browser. The host accepts only those addresses. */
  openLicenseLink: (url: string) => Promise<void>
  overlays: OverlayStates | undefined
  updateOverlay: (kind: OverlayKind, change: OverlayChange) => void
  resetOverlay: (kind: OverlayKind) => void
  /** Returns the Desktop window to its default size and position; the remembered ones are forgotten. */
  resetDesktopWindow: () => void
  app: AppIdentity | undefined
  /** A setting could not be saved. Values are unchanged because the host only reports what it applied. */
  failed: boolean
  /** Saved preferences could not be read, so defaults are showing. Capture problems never set this or `failed`. */
  loadFailed: boolean
  /** A recoverable settings problem reported by the host, as a warning code. */
  warning: string
  connected: boolean
}

export function useLiveSettings(snapshot: Snapshot | null, connected: boolean, changeLocale: (locale: string) => Promise<void>): SettingsController {
  const [adapters, setAdapters] = useState<Adapter[]>([])
  const [environment, setEnvironment] = useState<EnvironmentState | null>(null)
  const [checking, setChecking] = useState(true)
  const [defaultAdapter, setStoredAdapter] = useState("")
  const [autoStartCapture, setStoredAutoStart] = useState(false)
  const [startupWarning, setStartupWarning] = useState("")
  const [failed, setFailed] = useState(false)
  const [loadFailed, setLoadFailed] = useState(false)
  const [download, setDownload] = useState<NpcapDownloadState>({ info: null, resolving: false, opening: false, opened: false, openFailed: false })
  const mounted = useRef(true)
  const checkInFlight = useRef(false)
  const preferencesMissing = useRef(true)
  const openInFlight = useRef(false)
  const guard = useCallback((request: Promise<unknown>) => {
    setFailed(false)
    request.catch(() => { if (mounted.current) setFailed(true) })
  }, [])
  const loadPreferences = useCallback(() => {
    host.getSettings().then(preferences => {
      if (!mounted.current) return
      preferencesMissing.current = false
      setStoredAdapter(preferences.settings.defaultAdapterIPv4)
      setStoredAutoStart(preferences.settings.autoStartCapture === true)
      setStartupWarning(preferences.warning ?? "")
      setLoadFailed(false)
    }, () => { if (mounted.current) setLoadFailed(true) })
  }, [])
  const checkEnvironment = useCallback(() => {
    if (checkInFlight.current) return
    checkInFlight.current = true
    setChecking(true)
    if (preferencesMissing.current) loadPreferences()
    host.checkCaptureEnvironment().then(result => {
      if (!mounted.current) return
      setEnvironment(result.state)
      setAdapters(result.adapters ?? [])
    }, () => {
      if (!mounted.current) return
      setEnvironment("check_failed")
      setAdapters([])
    }).finally(() => {
      checkInFlight.current = false
      if (mounted.current) setChecking(false)
    })
  }, [loadPreferences])
  useEffect(() => {
    mounted.current = true
    checkEnvironment()
    return () => { mounted.current = false }
  }, [checkEnvironment])

  // The official sources are contacted only once Npcap needs installing, and a failure leaves the page link usable.
  const wantsDownload = environmentNotice(environment)?.offersDownload === true
  const resolveStarted = useRef(false)
  useEffect(() => {
    if (!wantsDownload || resolveStarted.current) return
    resolveStarted.current = true
    setDownload(current => ({ ...current, resolving: true }))
    host.npcapDownloadInfo().then(info => {
      if (mounted.current) setDownload(current => ({ ...current, info, resolving: false }))
    }, () => {
      resolveStarted.current = false
      if (mounted.current) setDownload(current => ({ ...current, resolving: false }))
    })
  }, [wantsDownload])
  const openNpcapDownload = useCallback(() => {
    if (openInFlight.current) return
    openInFlight.current = true
    setDownload(current => ({ ...current, opening: true, openFailed: false }))
    host.openNpcapDownload().then(info => {
      if (mounted.current) setDownload(current => ({ ...current, info, opening: false, opened: true }))
    }, () => {
      if (mounted.current) setDownload(current => ({ ...current, opening: false, openFailed: true }))
    }).finally(() => { openInFlight.current = false })
  }, [])

  return {
    locale: snapshot?.locale ?? "",
    languageChoice: snapshot?.languagePreference === "auto" ? "auto" : snapshot?.locale ?? "",
    setLocale: locale => guard(changeLocale(locale)),
    theme: snapshot?.theme,
    reduceMotion: snapshot?.reduceMotion === true,
    setAppearance: (theme, reduceMotion) => guard(host.setAppearance(theme, reduceMotion)),
    updateIntervalMs: normalizeUpdateInterval(snapshot?.updateIntervalMs),
    setUpdateInterval: ms => guard(host.setUpdateInterval(ms)),
    adapters,
    defaultAdapter,
    setDefaultAdapter: ipv4 => guard(host.setDefaultAdapter(ipv4).then(state => { if (mounted.current) setStoredAdapter(state.settings.defaultAdapterIPv4) })),
    autoStartCapture,
    setAutoStartCapture: enabled => guard(host.setAutoStartCapture(enabled).then(state => { if (mounted.current) setStoredAutoStart(state.settings.autoStartCapture === true) })),
    captureBusy: captureBusy(snapshot?.service.state),
    environment,
    checking,
    checkEnvironment,
    npcapDownload: download,
    openNpcapDownload,
    openLicenseLink: host.openLicenseLink,
    overlays: snapshot?.overlays,
    updateOverlay: (kind, change) => guard(host.updateOverlay(kind, change)),
    resetOverlay: kind => guard(host.resetOverlay(kind)),
    resetDesktopWindow: () => guard(host.resetDesktopWindow()),
    app: snapshot?.service.app,
    failed,
    loadFailed,
    warning: snapshot?.settingsWarning ?? startupWarning,
    connected,
  }
}
