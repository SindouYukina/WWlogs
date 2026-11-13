import { Call } from "@wailsio/runtime"
import type { CaptureEnvironment, NpcapDownload } from "./capture-environment"
import type { HistoryChange, HistoryDay, HistoryEncounterDetail, HistoryYear } from "./history"
import type { HistoryLibrary, HistoryLibraryQuery } from "./history-library"
import type { HistoryCareer, HistoryCareerQuery } from "./history-career"
import type { HistoryComparison } from "./history-compare"
import type { AnalysisQuery, EffectLanes, EncounterAnalysis, EventPage, EventQuery, HitTimeline } from "./history-analysis"
import type { OverlayKind, OverlayState, OverlayStates, Snapshot } from "./snapshot"

// Calls into the Go Bridge (internal/desktop.Bridge) by fully qualified name; no generated bindings are needed.
const bridge = "wwlogs/internal/desktop.Bridge."

function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(bridge + method, ...args) as Promise<T>
}

export type { Adapter } from "./capture-environment"

export interface Settings {
  version: number
  defaultAdapterIPv4: string
  locale: string
  theme?: string
  reduceMotion?: boolean
  updateIntervalMs?: number
  autoStartCapture?: boolean
  /** Combat History recording is on unless this is true. */
  historyRecordingDisabled?: boolean
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
  checkCaptureEnvironment: () => call<CaptureEnvironment>("CheckCaptureEnvironment"),
  npcapDownloadInfo: () => call<NpcapDownload>("NpcapDownloadInfo"),
  openNpcapDownload: () => call<NpcapDownload>("OpenNpcapDownload"),
  openLicenseLink: (url: string) => call<void>("OpenLicenseLink", url),
  startCapture: (localIPv4: string) => call<void>("StartCapture", { localIPv4 }),
  stopCapture: () => call<void>("StopCapture"),
  getSettings: () => call<SettingsState>("GetSettings"),
  setDefaultAdapter: (ipv4: string) => call<SettingsState>("SetDefaultAdapter", ipv4),
  setAutoStartCapture: (enabled: boolean) => call<SettingsState>("SetAutoStartCapture", enabled),
  setLocale: (locale: string) => call<SettingsState>("SetLocale", locale),
  setAppearance: (theme: string, reduceMotion: boolean) => call<SettingsState>("SetAppearance", theme, reduceMotion),
  setUpdateInterval: (ms: number) => call<SettingsState>("SetUpdateInterval", ms),
  updateOverlay: (kind: OverlayKind, change: OverlayChange) => call<OverlayStates>("UpdateOverlay", kind, change),
  resetOverlay: (kind: OverlayKind) => call<void>("ResetOverlay", kind),
  /** Forgets the Desktop window's remembered size and position and returns it to the first-launch default. */
  resetDesktopWindow: () => call<void>("ResetDesktopWindow"),
  /**
   * Combat History: encounter counts per date of a year (0: the current year), one page of a date, and one encounter. zone is
   * the IANA zone Go lays every date and clock out in (hostTimeZone).
   */
  historyYear: (year: number, zone: string) => call<HistoryYear>("HistoryYear", year, zone),
  historyDay: (date: string, offset: number, zone: string) => call<HistoryDay>("HistoryDay", date, offset, zone),
  historyEncounter: (id: number, zone: string) => call<HistoryEncounterDetail>("HistoryEncounter", id, zone),
  /** One page of the History Library: stored encounters across dates, selected, sorted and classified by Go. */
  historyLibrary: (query: HistoryLibraryQuery, zone: string) => call<HistoryLibrary>("HistoryLibrary", query, zone),
  /** The Career Overview of a range: totals, activity, roles, places and personal records, all computed by Go. */
  historyCareer: (query: HistoryCareerQuery, zone: string) => call<HistoryCareer>("HistoryCareer", query, zone),
  /** Two to four stored encounters side by side: Go lines them up, states their shared conditions and gives the changes. */
  historyCompare: (ids: number[], zone: string) => call<HistoryComparison>("HistoryCompare", ids, zone),
  /** The analysis workspace of a finished stored encounter: a range, contributor and enemy rebuilt by Go, its effect lanes and its events. */
  historyAnalysis: (id: number, query: AnalysisQuery) => call<EncounterAnalysis>("HistoryAnalysis", id, query),
  historyEffects: (id: number, query: AnalysisQuery) => call<EffectLanes>("HistoryEffects", id, query),
  historyEvents: (id: number, query: EventQuery) => call<EventPage>("HistoryEvents", id, query),
  /** Every skill hit of the whole encounter for the icon timeline, with the contributor and enemy filters; hits, never casts. */
  historyHits: (id: number, query: AnalysisQuery) => call<HitTimeline>("HistoryHits", id, query),
  /** Saves whether Combat History records and applies it to a running capture. */
  setHistoryRecording: (enabled: boolean) => call<SettingsState>("SetHistoryRecording", enabled),
  /** Deletes one stored encounter, or every one; the UI asks the user to confirm first. */
  deleteHistoryEncounter: (id: number) => call<HistoryChange>("DeleteHistoryEncounter", id),
  clearHistory: () => call<HistoryChange>("ClearHistory"),
}
