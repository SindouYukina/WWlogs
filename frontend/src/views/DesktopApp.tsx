import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { BrandEmblem, Button, ContentSwitch, Icon, ListeningStatus, Segmented, Tooltip, resolveTheme, type IconName } from "@/design"
import { statusText } from "@/i18n"
import { adapterProblem, adapterUsable, captureBusy, environmentNotice } from "@/lib/capture-environment"
import { displayedEncounter } from "@/lib/combat-data"
import { selectionScope } from "@/lib/combat-view"
import { useDetailPick } from "@/lib/useDetailPick"
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

/**
 * The production Desktop window: one global header over the Dashboard and Settings views. The header reads, from left to
 * right, capture status, the two Overlay switches and the capture button; the capture interface is chosen in Settings.
 */
export function DesktopApp() {
  const { t } = useTranslation(["dashboard", "common", "settings"])
  const c = (key: string) => t(key, { ns: "common" })
  const { data, changeLocale } = useLiveCombatData()
  const snapshot = data.snapshot
  const connected = data.connection === "connected"
  const controller = useLiveSettings(snapshot, connected, changeLocale)
  const describeListening = useListeningLabels()
  const [page, setPage] = useState<Page>("dashboard")
  // The Dashboard unmounts while Settings shows, so the window holds the ranking pick and ends it with its encounter.
  const detailPick = useDetailPick(selectionScope(snapshot?.view, displayedEncounter(snapshot?.view)))
  const [action, setAction] = useState<"start" | "stop" | null>(null)
  const [failed, setFailed] = useState(false)
  const actionLock = useRef(false)
  const adapter = controller.defaultAdapter
  const service = snapshot?.service
  const busy = captureBusy(service?.state)
  const problem = busy ? null : adapterProblem(controller.environment, controller.adapters, adapter)
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
  // Settings shows the same problem on the interface row, so the bar with its shortcut belongs to the Dashboard.
  const adapterNotice = problem !== null && page === "dashboard"
  const listening = snapshot?.listening
  const listeningText = listening ? describeListening(listening) : null
  return <div className="ww-root ww-app">
    <header className="ww-app-header">
      <div className="ww-app-brand"><BrandEmblem /><div><strong>WWlogs</strong><span>{c("appSubtitle")}</span></div></div>
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
          {running || service?.state === "stopping"
            ? <Button size="sm" disabled={!connected || !running || action !== null} data-field="capture-stop" onClick={() => void capture("stop")}><Icon name="pause" size={13} />{action === "stop" || service?.state === "stopping" ? statusText("stopping") : t("stop")}</Button>
            // A capture that begins after the game login cannot decode that connection, so the start button says so.
            : <Tooltip text={t("startHint")} trigger="child" placement="bottom"><Button size="sm" variant="primary" disabled={!connected || busy || action !== null || !usable} data-field="capture-start" onClick={() => void capture("start")}><Icon name="play" size={13} />{action === "start" || service?.state === "starting" ? statusText("starting") : t("start")}</Button></Tooltip>}
        </div>
      </div>
    </header>
    {(failed || service?.lastError || environmentNotice(controller.environment) || adapterNotice) && <div className="ww-app-notices">
      {failed && <p role="alert" data-tone="critical">{c("operationFailed")}</p>}
      {service?.lastError && <p role="alert" data-tone="critical">{c("captureFailed")}</p>}
      <CaptureEnvironmentNotice controller={controller} />
      {adapterNotice && <div className="ww-env-notice" role="status" data-field="adapter-notice" data-problem={problem}>
        <div className="ww-env-notice-text"><span>{t(problem === "unset" ? "interfaceUnset" : "interfaceUnavailable")}</span></div>
        <div className="ww-env-notice-actions"><Button size="sm" data-field="open-general-settings" onClick={() => setPage("settings")}>{t("openSettings")}</Button></div>
      </div>}
    </div>}
    <main className="ww-app-main">
      <ContentSwitch contentKey={page} direction={page === "settings" ? "forward" : "back"} className="ww-app-page">
        {page === "dashboard" ? <DashboardView data={data} theme={resolveTheme(snapshot?.theme)} detailPick={detailPick} /> : <SettingsView controller={controller} />}
      </ContentSwitch>
    </main>
  </div>
}
