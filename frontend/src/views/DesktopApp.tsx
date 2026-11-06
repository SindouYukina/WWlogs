import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, ContentSwitch, Icon, ListeningStatus, Segmented, Select, resolveTheme, type IconName } from "@/design"
import { statusText } from "@/i18n"
import { adapterUsable, environmentNotice } from "@/lib/capture-environment"
import { host } from "@/lib/host"
import { useLiveSettings } from "@/lib/settings-controller"
import type { OverlayKind } from "@/lib/snapshot"
import { useLiveCombatData } from "@/lib/useLiveCombatData"
import { useListeningLabels } from "@/lib/useListeningLabels"
import { CaptureEnvironmentNotice } from "./CaptureEnvironmentNotice"
import { DashboardView } from "./DashboardView"
import { SettingsView } from "./SettingsView"

type Page = "dashboard" | "settings"

const overlayToggles: ReadonlyArray<{ kind: OverlayKind; icon: IconName; label: string }> = [
  { kind: "combat", icon: "overlay", label: "pages.combatOverlay" },
  { kind: "effects", icon: "spark", label: "pages.effectsOverlay" },
]

/** The production Desktop window: one global header over the Dashboard and Settings views. */
export function DesktopApp() {
  const { t } = useTranslation(["dashboard", "common", "settings"])
  const c = (key: string) => t(key, { ns: "common" })
  const { data, changeLocale } = useLiveCombatData()
  const snapshot = data.snapshot
  const connected = data.connection === "connected"
  const controller = useLiveSettings(snapshot, connected, changeLocale)
  const describeListening = useListeningLabels()
  const [page, setPage] = useState<Page>("dashboard")
  const [selected, setSelected] = useState<string | null>(null)
  const [action, setAction] = useState<"start" | "stop" | null>(null)
  const [failed, setFailed] = useState(false)
  const actionLock = useRef(false)
  const adapter = selected ?? controller.defaultAdapter
  const service = snapshot?.service
  const busy = service?.state === "starting" || service?.state === "running" || service?.state === "stopping"
  const available = controller.adapters.some(item => item.ipv4 === adapter)
  const usable = adapterUsable(controller.environment, controller.checking, controller.adapters, adapter)
  const capture = async (kind: "start" | "stop") => {
    if (actionLock.current) return
    actionLock.current = true
    setAction(kind)
    setFailed(false)
    try {
      if (kind === "start") await host.startCapture(adapter)
      else await host.stopCapture()
    } catch {
      setFailed(true)
    } finally {
      actionLock.current = false
      setAction(null)
    }
  }
  const running = service?.state === "running"
  const listening = snapshot?.listening
  const listeningText = listening ? describeListening(listening) : null
  const adapterOptions = [
    ...(adapter === "" ? [{ value: "", label: controller.environment === null ? t("environment.checking") : t("selectInterface") }] : []),
    ...(adapter !== "" && !available ? [{ value: adapter, label: `${adapter} · ${statusText("unavailable")}` }] : []),
    ...controller.adapters.map(item => ({ value: item.ipv4, label: `${item.ipv4} — ${item.description || t("network")}` })),
  ]
  return <div className="ww-root ww-app">
    <header className="ww-app-header">
      <div className="ww-app-brand"><span className="ww-app-mark" aria-hidden="true">W</span><div><strong>WWlogs</strong><span>{c("appSubtitle")}</span></div></div>
      <Segmented variant="tabs" label={c("dashboard")} value={page} onChange={setPage} options={[{ value: "dashboard", label: c("dashboard") }, { value: "settings", label: c("settings") }]} />
      <div className="ww-app-tools">
        {listening && listeningText && <span data-field="listening" data-state={listening.state} data-reason={listening.reason}><ListeningStatus state={listening.state} label={listeningText.label} detail={listeningText.detail} /></span>}
        <div className="ww-app-overlays">
          {overlayToggles.map(({ kind, icon, label }) => {
            const visible = snapshot?.overlays?.[kind].visible === true
            return <Button key={kind} size="sm" variant={visible ? "secondary" : "ghost"} aria-pressed={visible} aria-label={t(label, { ns: "settings" })} title={t(label, { ns: "settings" })} disabled={!connected} data-field={`overlay-toggle-${kind}`} onClick={() => controller.updateOverlay(kind, { visible: !visible })}><Icon name={icon} size={15} /></Button>
          })}
        </div>
        <div className="ww-app-capture" data-field="service-state" data-state={service?.state}>
          <Select label={t("network")} value={adapter} onChange={setSelected} options={adapterOptions} />
          {running || service?.state === "stopping"
            ? <Button size="sm" disabled={!connected || !running || action !== null} data-field="capture-stop" onClick={() => void capture("stop")}><Icon name="pause" size={13} />{action === "stop" || service?.state === "stopping" ? statusText("stopping") : t("stop")}</Button>
            : <Button size="sm" variant="primary" disabled={!connected || busy || action !== null || !usable} data-field="capture-start" onClick={() => void capture("start")}><Icon name="play" size={13} />{action === "start" || service?.state === "starting" ? statusText("starting") : t("start")}</Button>}
        </div>
      </div>
    </header>
    {(failed || service?.lastError || environmentNotice(controller.environment)) && <div className="ww-app-notices">
      {failed && <p role="alert" data-tone="critical">{c("operationFailed")}</p>}
      {service?.lastError && <p role="alert" data-tone="critical">{c("captureFailed")}</p>}
      <CaptureEnvironmentNotice controller={controller} />
    </div>}
    <main className="ww-app-main">
      <ContentSwitch contentKey={page} direction={page === "settings" ? "forward" : "back"} className="ww-app-page">
        {page === "dashboard" ? <DashboardView data={data} theme={resolveTheme(snapshot?.theme)} /> : <SettingsView controller={controller} />}
      </ContentSwitch>
    </main>
  </div>
}
