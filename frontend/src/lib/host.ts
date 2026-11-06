import { Call } from "@wailsio/runtime"
import type { OverlayKind, OverlayState, OverlayStates, Snapshot } from "./snapshot"

// Calls into the Go Bridge (internal/desktop.Bridge) by fully qualified name; no generated bindings are needed.
const bridge = "wwlogs/internal/desktop.Bridge."

function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(bridge + method, ...args) as Promise<T>
}

export interface Adapter {
  ipv4: string
  description: string
}

export interface Settings {
  version: number
  defaultAdapterIPv4: string
  locale: string
  theme?: string
  reduceMotion?: boolean
}

export interface SettingsState {
  settings: Settings
  warning?: string
  localeRevision: number
}

export type OverlayChange = Partial<OverlayState>

export const host = {
  nextSnapshot: (after: number) => call<Snapshot>("NextSnapshot", after),
  currentSnapshot: () => call<Snapshot>("CurrentSnapshot"),
  listAdapters: () => call<Adapter[]>("ListAdapters"),
  startCapture: (localIPv4: string) => call<void>("StartCapture", { localIPv4 }),
  stopCapture: () => call<void>("StopCapture"),
  getSettings: () => call<SettingsState>("GetSettings"),
  setDefaultAdapter: (ipv4: string) => call<SettingsState>("SetDefaultAdapter", ipv4),
  setLocale: (locale: string) => call<SettingsState>("SetLocale", locale),
  setAppearance: (theme: string, reduceMotion: boolean) => call<SettingsState>("SetAppearance", theme, reduceMotion),
  updateOverlay: (kind: OverlayKind, change: OverlayChange) => call<OverlayStates>("UpdateOverlay", kind, change),
  resetOverlay: (kind: OverlayKind) => call<void>("ResetOverlay", kind),
}
