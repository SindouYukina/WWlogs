import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import type { ThemeId } from "@/design"
import type { SettingsController } from "@/lib/settings-controller"
import { defaultUpdateInterval, type OverlayKind, type OverlayStates, type UpdateInterval } from "@/lib/snapshot"

const initialOverlays: OverlayStates = {
  combat: { visible: true, opacity: 74, scale: 100, alwaysOnTop: true, clickThrough: true, editing: false },
  effects: { visible: true, opacity: 62, scale: 100, alwaysOnTop: true, clickThrough: true, editing: false },
}

/**
 * Local demo state for the production Settings view. Nothing is read from or written to the user's real settings, and
 * the update interval only changes this value: the Showcase keeps its own mock publish rate and never drives the
 * product's update interval.
 */
export function useMockSettings(theme: ThemeId, setTheme: (theme: ThemeId) => void, reduced: boolean, setReduced: (reduced: boolean) => void): SettingsController {
  const { i18n } = useTranslation()
  const [overlays, setOverlays] = useState(initialOverlays)
  const [defaultAdapter, setDefaultAdapter] = useState("")
  const [autoStartCapture, setAutoStartCapture] = useState(false)
  const [updateIntervalMs, setUpdateInterval] = useState<UpdateInterval>(defaultUpdateInterval)
  return useMemo(() => ({
    locale: i18n.language,
    setLocale: locale => void i18n.changeLanguage(locale),
    theme,
    reduceMotion: reduced,
    setAppearance: (nextTheme, nextReduced) => { setTheme(nextTheme as ThemeId); setReduced(nextReduced) },
    updateIntervalMs,
    setUpdateInterval,
    adapters: [{ ipv4: "192.0.2.10", description: "Mock adapter" }],
    defaultAdapter,
    setDefaultAdapter,
    autoStartCapture,
    setAutoStartCapture,
    captureBusy: false,
    environment: "ready",
    checking: false,
    checkEnvironment: () => undefined,
    npcapDownload: { info: null, resolving: false, opening: false, opened: false, openFailed: false },
    openNpcapDownload: () => undefined,
    openLicenseLink: () => Promise.resolve(),
    overlays,
    updateOverlay: (kind: OverlayKind, change) => setOverlays(current => ({ ...current, [kind]: { ...current[kind], ...change } })),
    resetOverlay: () => undefined,
    resetDesktopWindow: () => undefined,
    // Mock identity: unmistakably not a real build (no version, no commit, never an official number).
    app: { version: "showcase", buildNumber: 0, official: false, commit: "mock", dirty: "unknown", profile: "development" },
    failed: false,
    loadFailed: false,
    warning: "",
    connected: true,
  }), [i18n, i18n.language, theme, setTheme, reduced, setReduced, overlays, defaultAdapter, autoStartCapture, updateIntervalMs])
}
