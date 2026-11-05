import { useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { host, type Adapter, type Settings } from "@/lib/host"
import { useOverlay } from "@/lib/useOverlay"
import { dataState, type Connection, type Snapshot } from "@/lib/snapshot"
import { CombatDashboard } from "./CombatDashboard"
import { StateBanner } from "./StateBanner"

const emptySettings: Settings = { version: 1, defaultAdapterIPv4: "", showOverlayOnStartup: false }

function AdapterSelect({ adapters, value, onChange, disabled, id, allowEmpty = false }: { adapters: Adapter[]; value: string; onChange: (value: string) => void; disabled?: boolean; id: string; allowEmpty?: boolean }) {
  const missing = value !== "" && !adapters.some((adapter) => adapter.ipv4 === value)
  return <select id={id} className="adapter-select" value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}>
    {(allowEmpty || value === "") && <option value="">{allowEmpty ? "Choose at each launch" : "Select a network interface"}</option>}
    {missing && <option value={value}>{value} · unavailable</option>}
    {adapters.map((adapter) => <option key={adapter.ipv4} value={adapter.ipv4}>{adapter.ipv4} — {adapter.description || "Network interface"}</option>)}
  </select>
}

export function DesktopView({ snapshot, connection }: { snapshot: Snapshot | null; connection: Connection }) {
  const [page, setPage] = useState<"dashboard" | "settings">("dashboard")
  const [adapters, setAdapters] = useState<Adapter[]>([])
  const [selected, setSelected] = useState("")
  const [settings, setSettings] = useState<Settings>(emptySettings)
  const [saved, setSaved] = useState<Settings | null>(null)
  const [settingsWarning, setSettingsWarning] = useState("")
  const [settingsError, setSettingsError] = useState("")
  const [saveMessage, setSaveMessage] = useState("")
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [adapterError, setAdapterError] = useState("")
  const [actionError, setActionError] = useState("")
  const [action, setAction] = useState<"start" | "stop" | null>(null)
  const actionLock = useRef(false)
  const saveLock = useRef(false)
  const overlay = useOverlay()
  const service = snapshot?.service
  const view = snapshot?.view ?? null
  const busy = service?.state === "starting" || service?.state === "running" || service?.state === "stopping"
  const connected = connection === "connected"
  const selectedAvailable = adapters.some((adapter) => adapter.ipv4 === selected)
  const dirty = saved !== null && (settings.defaultAdapterIPv4 !== saved.defaultAdapterIPv4 || settings.showOverlayOnStartup !== saved.showOverlayOnStartup)

  useEffect(() => {
    let cancelled = false
    void Promise.allSettled([host.listAdapters(), host.getSettings()]).then(([adapterResult, settingsResult]) => {
      if (cancelled) return
      const list = adapterResult.status === "fulfilled" ? adapterResult.value ?? [] : []
      setAdapters(list)
      if (adapterResult.status === "rejected") setAdapterError(String(adapterResult.reason))
      if (settingsResult.status === "fulfilled") {
        setSettings(settingsResult.value.settings)
        setSaved(settingsResult.value.settings)
        setSettingsWarning(settingsResult.value.warning ?? "")
        setSelected(settingsResult.value.settings.defaultAdapterIPv4)
      } else setSettingsError("Settings could not be loaded. Reopen the window to retry.")
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [])

  const captureAction = async (kind: "start" | "stop") => {
    if (actionLock.current) return
    actionLock.current = true
    setAction(kind)
    setActionError("")
    try {
      if (kind === "start") await host.startCapture(selected)
      else await host.stopCapture()
    } catch (error: unknown) { setActionError(String(error)) }
    finally { actionLock.current = false; setAction(null) }
  }

  const refreshAdapters = async () => {
    if (refreshing) return
    setRefreshing(true)
    setAdapterError("")
    try { setAdapters(await host.listAdapters() ?? []) }
    catch (error: unknown) { setAdapterError(String(error)) }
    finally { setRefreshing(false) }
  }

  const saveSettings = async () => {
    if (saveLock.current || !saved) return
    saveLock.current = true
    setSaving(true)
    setSettingsError("")
    setSaveMessage("")
    try {
      const result = await host.saveSettings(settings)
      setSettings(result.settings)
      setSaved(result.settings)
      setSettingsWarning(result.warning ?? "")
      setSaveMessage("Preferences saved. Startup changes apply on the next launch.")
    } catch (error: unknown) { setSettingsError(String(error)) }
    finally { saveLock.current = false; setSaving(false) }
  }

  return <div className="desktop-shell">
    <header className="app-header">
      <div className="flex items-center gap-3"><div className="app-mark" aria-hidden="true">W</div><div><h1 className="text-lg font-semibold tracking-tight">WWlogs</h1><div className="text-xs text-muted-foreground">Combat analysis</div></div></div>
      <div className="flex flex-wrap items-center justify-end gap-3"><span className="text-xs text-muted-foreground" data-field="app">{service ? `${service.app.version} · ${service.app.profile}` : "Connecting to host"}</span><StateBanner connection={connection} state={dataState(snapshot)} snapshot={snapshot} /></div>
    </header>
    <div className="workspace">
      <nav className="page-nav" aria-label="Main navigation"><Button variant={page === "dashboard" ? "secondary" : "ghost"} aria-current={page === "dashboard" ? "page" : undefined} onClick={() => setPage("dashboard")}>Dashboard</Button><Button variant={page === "settings" ? "secondary" : "ghost"} aria-current={page === "settings" ? "page" : undefined} onClick={() => setPage("settings")}>Settings{dirty ? " ·" : ""}</Button><div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground"><span className={`status-dot ${service?.state === "running" ? "bg-emerald-400" : service?.state === "error" ? "bg-destructive" : "bg-muted-foreground"}`} /><span className="capitalize" data-field="service-state">{service?.state ?? "Connecting"}</span></div></nav>

      {settingsWarning && <div className="notice text-warning" role="status">{settingsWarning}</div>}
      <section aria-label="Capture controls">
        <Card><CardContent className="pt-5"><div className="flex flex-wrap items-end gap-3"><div className="min-w-56 flex-1"><label className="eyebrow mb-2 block" htmlFor="capture-adapter">Network interface</label><AdapterSelect id="capture-adapter" adapters={adapters} value={selected} onChange={setSelected} disabled={busy || action !== null || loading || refreshing} /></div><Button variant="outline" disabled={!connected || refreshing || loading || busy || action !== null} onClick={() => void refreshAdapters()}>{refreshing ? "Refreshing…" : "Refresh"}</Button><Button className="min-w-32 bg-emerald-400 text-emerald-950 hover:bg-emerald-300" disabled={!connected || loading || busy || action !== null || !selectedAvailable} onClick={() => void captureAction("start")}>{action === "start" || service?.state === "starting" ? "Starting…" : "Start capture"}</Button><Button variant="secondary" className="min-w-28" disabled={!connected || service?.state !== "running" || action !== null} onClick={() => void captureAction("stop")}>{action === "stop" || service?.state === "stopping" ? "Stopping…" : "Stop capture"}</Button><Button variant="outline" className="min-w-32" disabled={!connected || overlay.pending || overlay.visible === null} onClick={() => void overlay.toggle()}>{overlay.pending ? "Updating…" : overlay.visible ? "Hide overlay" : "Show overlay"}</Button></div>
          <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-muted-foreground"><span data-field="session">{service?.session ? `Session ${service.session.captureSessionId} · ${service.session.source} · ${service.session.localIPv4}` : "No capture session"}</span><span data-field="overlay-state">Overlay: {overlay.visible === null ? "unknown" : overlay.visible ? "visible" : "hidden"}</span>{service?.lastResult && !busy && <span>Ended: {service.lastResult.stopReason.replaceAll("_", " ")}</span>}</div>
          {!loading && adapters.length === 0 && !adapterError && <p className="mt-3 text-sm text-warning">No IPv4 interfaces found. Check that Npcap is installed, then refresh.</p>}
          {selected !== "" && !selectedAvailable && !loading && <p className="mt-3 text-sm text-warning">The selected interface is unavailable. Choose a connected interface before starting capture.</p>}
          {[actionError, service?.lastError, adapterError, overlay.error].filter(Boolean).map((error, index) => <p key={index} role="alert" className="mt-3 text-sm text-destructive">{error}</p>)}
        </CardContent></Card>
      </section>

      <main>{page === "dashboard" ? <CombatDashboard view={view} stopped={service?.state === "stopped" || service?.state === "error"} /> : <div className="mx-auto max-w-3xl space-y-5"><div><h2 className="text-xl font-semibold">Settings</h2><p className="mt-1 text-sm text-muted-foreground">Basic preferences for this Windows user.</p></div><Card><CardHeader><CardTitle>Startup preferences</CardTitle></CardHeader><CardContent className="space-y-6"><div><label className="mb-2 block text-sm font-medium" htmlFor="default-adapter">Default network interface</label><AdapterSelect id="default-adapter" adapters={adapters} value={settings.defaultAdapterIPv4} onChange={(value) => { setSettings({ ...settings, defaultAdapterIPv4: value }); setSaveMessage("") }} disabled={loading || saving || saved === null} allowEmpty /><p className="mt-2 text-xs text-muted-foreground">Preselected on the next launch. Capture always starts manually.</p></div><label className="flex items-start gap-3 text-sm"><input type="checkbox" className="mt-1 size-4 accent-emerald-400" checked={settings.showOverlayOnStartup} disabled={loading || saving || saved === null} onChange={(event) => { setSettings({ ...settings, showOverlayOnStartup: event.target.checked }); setSaveMessage("") }} /><span>Show overlay on startup<span className="mt-1 block text-xs text-muted-foreground">Use the overlay button above to change its current visibility.</span></span></label><div className="flex items-center gap-3 border-t pt-5"><Button disabled={!connected || saving || loading || saved === null || (!dirty && !settingsWarning)} onClick={() => void saveSettings()}>{saving ? "Saving…" : "Save preferences"}</Button>{dirty && <Badge variant="outline">Unsaved changes</Badge>}</div>{saveMessage && <p role="status" className="text-sm text-emerald-300">{saveMessage}</p>}{settingsError && <p role="alert" className="text-sm text-destructive">{settingsError}</p>}</CardContent></Card><p className="text-xs leading-relaxed text-muted-foreground">Stored in %LOCALAPPDATA%\WWlogs\settings.json. Invalid settings fall back to defaults and can be replaced by saving preferences.</p></div>}</main>
      <footer className="flex flex-wrap justify-between gap-2 border-t pt-4 text-xs text-muted-foreground"><span>Server-confirmed damage · no estimated output</span><span data-field="sequence">Snapshot #{snapshot?.sequence ?? "—"}{service ? ` · ${service.app.commit.slice(0, 12)}` : ""}{view?.final ? " · final" : ""}</span></footer>
    </div>
  </div>
}
