import { useEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Button, ContentSwitch, EffectCell, EffectMore, Icon, NumberField, Segmented, Select, SettingsRow, SettingsSection, Sidebar, Slider, Stepper, ThemeBackdrop, ThemePicker, Toggle, Tooltip, resolveTheme, type SidebarGroupModel } from "@/design"
import { manifest } from "@/i18n"
import { buildNumberText, profileKey, revisionText, versionText, type IdentityText } from "@/lib/build-identity"
import { adapterProblem, environmentNotice } from "@/lib/capture-environment"
import { statusText } from "@/i18n"
import type { SettingsController } from "@/lib/settings-controller"
import { defaultUpdateInterval, normalizeUpdateInterval, updateIntervals, type OverlayKind } from "@/lib/snapshot"
import { useThemeDescriptions } from "@/lib/useListeningLabels"
import { indicatorText, type HistoryChange } from "@/lib/history"
import { HistoryIndicatorChip } from "./HistoryView"
import { HistoryRecovery } from "./HistoryRecovery"
import { LicensesPage } from "./LicensesPage"

type Page = "general" | "history" | "appearance" | "combatOverlay" | "effectsOverlay" | "about" | "licenses"

const pageOrder: Page[] = ["general", "history", "appearance", "combatOverlay", "effectsOverlay", "about", "licenses"]

function Hint({ text }: { text: string }) {
  return <Tooltip text={text}><Icon name="info" size={15} /></Tooltip>
}

/**
 * The capture interface with its recheck, as one row. A running capture keeps the interface it started on, so the choice
 * is locked until it stops; the saved interface is never replaced by another one automatically.
 */
function AdapterRow({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation(["settings", "dashboard"])
  const saved = controller.defaultAdapter
  const listed = controller.adapters.some(adapter => adapter.ipv4 === saved)
  const options = [
    ...(saved === "" ? [{ value: "", label: t(controller.environment === null ? "environment.checking" : "selectInterface", { ns: "dashboard" }) }] : []),
    ...(saved !== "" && !listed ? [{ value: saved, label: `${saved} · ${statusText("unavailable")}` }] : []),
    ...controller.adapters.map(adapter => ({ value: adapter.ipv4, label: `${adapter.ipv4} — ${adapter.description || t("network", { ns: "dashboard" })}` })),
  ]
  const notice = environmentNotice(controller.environment)
  const problem = adapterProblem(controller.environment, controller.adapters, saved)
  const description = controller.captureBusy ? t("adapterLocked")
    : notice ? t(`environment.${notice.key}.title`, { ns: "dashboard" })
    : problem === "unavailable" ? t("missingInterface", { ns: "dashboard" })
    : problem === "unset" ? t("adapterChoose")
    : t("adapterHint")
  return <SettingsRow title={t("defaultAdapter")} description={description}>
    <span className="ww-row-pair" data-field="adapter" data-problem={problem ?? ""} data-locked={controller.captureBusy}>
      <Select label={t("defaultAdapter")} value={saved} onChange={controller.setDefaultAdapter} options={options} disabled={controller.captureBusy || !controller.connected} />
      <Button size="sm" disabled={controller.checking} data-field="settings-environment-recheck" onClick={controller.checkEnvironment}>{t(controller.checking ? "environment.checking" : "environment.recheck", { ns: "dashboard" })}</Button>
    </span>
  </SettingsRow>
}

function GeneralPage({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const languageLabel = (locale: string) => manifest.languages[locale as keyof typeof manifest.languages]?.label ?? locale
  const options = [{ value: "auto", label: t("languageAuto") }, ...manifest.supportedLocales.map(locale => ({ value: locale, label: languageLabel(locale) }))]
  const followsWindows = controller.languageChoice === "auto"
  const interval = (ms: number) => ms % 1000 === 0 ? t("units.seconds", { value: ms / 1000 }) : t("units.milliseconds", { value: ms })
  return <>
    <SettingsSection title={t("sections.language")}>
      <SettingsRow title={t("language")} description={followsWindows ? t("languageAutoHint", { language: languageLabel(controller.locale) }) : t("languageHint")}><span data-field="locale" data-choice={controller.languageChoice}><Select label={t("language")} value={controller.languageChoice} onChange={controller.setLocale} options={options} /></span></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("sections.capture")}>
      <AdapterRow controller={controller} />
      <SettingsRow title={t("autoStartCapture")} description={t("autoStartCaptureHint")}><span data-field="auto-start-capture"><Toggle label={t("autoStartCapture")} disabled={!controller.connected} checked={controller.autoStartCapture} onChange={controller.setAutoStartCapture} /></span></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("sections.displayPerformance")}>
      <SettingsRow title={t("updateInterval")} description={t("updateIntervalNote", { value: interval(defaultUpdateInterval) })} hint={<Hint text={t("updateIntervalHint")} />}>
        <span data-field="update-interval"><Segmented equal label={t("updateInterval")} value={String(controller.updateIntervalMs)} onChange={value => controller.setUpdateInterval(normalizeUpdateInterval(Number(value)))} options={updateIntervals.map(ms => ({ value: String(ms), label: interval(ms) }))} /></span>
      </SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("sections.window")}>
      <SettingsRow title={t("mainWindow")} description={t("mainWindowNote")}><Button size="sm" disabled={!controller.connected} data-field="reset-main-window" onClick={controller.resetDesktopWindow}>{t("resetMainWindow")}</Button></SettingsRow>
    </SettingsSection>
  </>
}

/**
 * Combat History: the recording switch, what the history is doing now, how it is stored, and clearing it. The state and
 * every reason come from the host's indicator.
 */
function HistoryPage({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation(["settings", "history"])
  const h = (key: string, options?: Record<string, unknown>) => t(key, { ns: "history", ...options })
  const indicator = controller.history
  const text = indicatorText(indicator)
  const [asking, setAsking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [outcome, setOutcome] = useState<{ key: string; tone?: "critical"; n?: number } | null>(null)
  const clear = () => {
    setBusy(true)
    setOutcome(null)
    controller.clearHistory().then((change: HistoryChange) => {
      setOutcome(change.refused ? { key: `delete.refused.${change.refused}`, tone: "critical" } : { key: "clear.done", n: change.encounters })
    }).catch(() => setOutcome({ key: "delete.failed", tone: "critical" })).finally(() => { setBusy(false); setAsking(false) })
  }
  const writable = indicator !== undefined && indicator.state !== "unavailable" && indicator.state !== "read_only"
  const unavailable = indicator?.state === "unavailable"
  return <>
    <SettingsSection title={t("historySettings.sections.recording")}>
      <SettingsRow title={t("historySettings.recording")} description={t("historySettings.recordingHint")}>
        <span data-field="history-recording"><Toggle label={t("historySettings.recording")} disabled={!controller.connected} checked={controller.historyRecording} onChange={controller.setHistoryRecording} /></span>
      </SettingsRow>
      <SettingsRow title={t("historySettings.status")} description={text.detail ? h(text.detail) : undefined}><HistoryIndicatorChip indicator={indicator} /></SettingsRow>
      {indicator?.lost ? <p className="ww-settings-notice" data-tone="warn" data-field="history-lost">{h("indicator.lost", { n: indicator.lost })}</p> : null}
      <SettingsRow title={t("historySettings.coverage")} description={t("historySettings.coverageHint")}><span className="ww-history-unknown">{t("historySettings.coverageValue")}</span></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("historySettings.sections.storage")}>
      <SettingsRow title={t("historySettings.encryption")} description={t(indicator?.encrypted ? "historySettings.encryptedHint" : "historySettings.notEncryptedHint")}>
        <span data-field="history-encryption" data-encrypted={indicator?.encrypted === true}>{t(indicator?.encrypted ? "historySettings.encrypted" : "historySettings.notEncrypted")}</span>
      </SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("historySettings.sections.data")}>
      {unavailable && <div className="ww-row-block"><HistoryRecovery rebuildable={indicator?.rebuildable === true} retry={controller.retryHistory} rebuild={controller.rebuildHistory} onRecovered={() => setOutcome(null)} /></div>}
      {!unavailable && <SettingsRow title={t("historySettings.clear")} description={t("historySettings.clearHint")}>
        {!asking
          ? <Button size="sm" variant="ghost" disabled={!controller.connected || !writable || busy} data-field="history-clear-start" onClick={() => { setOutcome(null); setAsking(true) }}>{t("historySettings.clearAction")}</Button>
          : <span className="ww-row-pair" role="group" aria-label={t("historySettings.clearConfirm")} data-field="history-clear-confirm-group">
            <Button size="sm" variant="danger" disabled={busy} data-field="history-clear-confirm" onClick={clear}>{t("historySettings.clearConfirmAction")}</Button>
            <Button size="sm" disabled={busy} data-field="history-clear-cancel" onClick={() => setAsking(false)}>{h("delete.cancel")}</Button>
          </span>}
      </SettingsRow>}
      {asking && <p className="ww-settings-notice" data-tone="critical" role="alert" data-field="history-clear-warning">{t("historySettings.clearConfirm")}</p>}
      {outcome && <p className="ww-settings-notice" data-tone={outcome.tone} role="status" data-field="history-clear-outcome">{h(outcome.key, { n: outcome.n ?? 0 })}</p>}
    </SettingsSection>
  </>
}

function AppearancePage({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const describe = useThemeDescriptions()
  const theme = resolveTheme(controller.theme)
  return <>
    <SettingsSection title={t("appearance")}>
      <div className="ww-row-block" data-field="theme" data-theme={theme}><ThemePicker label={t("theme")} value={theme} onChange={next => controller.setAppearance(next, controller.reduceMotion)} describe={describe} /></div>
    </SettingsSection>
    <SettingsSection title={t("sections.motion")}>
      <SettingsRow title={t("reduceMotion")} description={t("reduceMotionHint")}><Toggle label={t("reduceMotion")} checked={controller.reduceMotion} onChange={value => controller.setAppearance(theme, value)} /></SettingsRow>
    </SettingsSection>
  </>
}

/** A slider moves faster than a round trip, so a live value shows what was set until the host has caught up. */
function useLiveValue(current: number | undefined, fallback: number, apply: (value: number) => void) {
  const [draft, setDraft] = useState<number | null>(null)
  const settle = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(settle.current), [])
  const set = (value: number) => {
    setDraft(value)
    apply(value)
    clearTimeout(settle.current)
    settle.current = setTimeout(() => setDraft(null), 500)
  }
  return [draft ?? current ?? fallback, set] as const
}

/** The display scale range in percent; the host clamps to the same range. */
const minOverlayScale = 75
const maxOverlayScale = 200

function OverlayPage({ kind, controller }: { kind: OverlayKind; controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const state = controller.overlays?.[kind]
  const change = controller.updateOverlay
  const [opacity, setOpacity] = useLiveValue(state?.opacity, 100, value => change(kind, { opacity: value }))
  const [scale, setScale] = useLiveValue(state?.scale, 100, value => change(kind, { scale: value }))
  const disabled = !controller.connected || !state
  return <div data-field={`overlay-settings-${kind}`}>
    <SettingsSection title={t("sections.window")}>
      <SettingsRow title={t("showWindow")}><Toggle label={t("showWindow")} disabled={disabled} checked={state?.visible === true} onChange={visible => change(kind, { visible })} /></SettingsRow>
      <SettingsRow title={t("alwaysOnTop")}><Toggle label={t("alwaysOnTop")} disabled={disabled} checked={state?.alwaysOnTop === true} onChange={alwaysOnTop => change(kind, { alwaysOnTop })} /></SettingsRow>
      <SettingsRow title={t("clickThrough")} description={t("clickThroughHint")}><Toggle label={t("clickThrough")} disabled={disabled} checked={state?.clickThrough === true} onChange={clickThrough => change(kind, { clickThrough })} /></SettingsRow>
      <SettingsRow title={t("editMode")} description={t("editModeHint")}><Button size="sm" variant={state?.editing ? "primary" : "secondary"} disabled={disabled} data-field="edit-toggle" onClick={() => change(kind, { editing: !state?.editing })}>{t(state?.editing ? "finishEditing" : "startEditing")}</Button></SettingsRow>
      <SettingsRow title={t("position")} description={t("positionNote")}><Button size="sm" disabled={disabled} onClick={() => controller.resetOverlay(kind)}>{t("resetPosition")}</Button></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("sections.look")}>
      <SettingsRow title={t("opacity")} description={t("opacityHint")}>
        <Slider label={t("opacity")} min={20} max={100} value={opacity} onChange={setOpacity} />
        <Stepper value={opacity} unit="%" step={5} min={20} max={100} onChange={setOpacity} decreaseLabel={t("decrease")} increaseLabel={t("increase")} />
      </SettingsRow>
      <SettingsRow title={t("scale")} description={t("scaleHint", { min: minOverlayScale, max: maxOverlayScale })}>
        <span className="ww-row-control" data-field="overlay-scale" data-scale={scale}>
          <Slider label={t("scale")} min={minOverlayScale} max={maxOverlayScale} step={5} value={scale} onChange={setScale} />
          <NumberField value={scale} unit="%" step={5} min={minOverlayScale} max={maxOverlayScale} onChange={setScale} label={t("scaleInput")} decreaseLabel={t("decrease")} increaseLabel={t("increase")} />
        </span>
      </SettingsRow>
    </SettingsSection>
    {kind === "effects" && <EffectsLegend />}
  </div>
}

/** What the Effects Overlay's cells mean. The overlay carries no text per effect, so it is explained here. */
function EffectsLegend() {
  const { t } = useTranslation("settings")
  const describe = () => ""
  const cell = (key: string, props: Partial<ComponentProps<typeof EffectCell>> & Pick<ComponentProps<typeof EffectCell>, "effectId">) => <span key={key} className="ww-effects-legend-sample"><EffectCell fill={0.6} untimed={false} seconds="8" low={false} stale={false} describe={describe} {...props} /></span>
  const samples = (...content: ReactNode[]) => <span className="ww-effects-legend-samples" aria-hidden="true">{content}</span>
  // The sample cell uses the game's own Aero Erosion icon.
  const aero = 10041000
  const items: { key: string; swatch: ReactNode }[] = [
    { key: "gameIcon", swatch: samples(cell("icon", { effectId: aero, elementId: 4, stackCount: 3 })) },
    { key: "target", swatch: samples(cell("enemy", { effectId: aero, elementId: 4, target: "enemy" })) },
    { key: "color", swatch: samples(cell("element", { effectId: aero, elementId: 4 }), cell("neutral", { effectId: aero })) },
    { key: "untimed", swatch: samples(cell("untimed", { effectId: aero, fill: undefined, untimed: true, seconds: undefined })) },
    { key: "more", swatch: <span className="ww-effects-legend-samples ww-effects-legend-more" aria-hidden="true"><EffectMore count={12} label="" /></span> },
  ]
  return <SettingsSection title={t("effectsLegend.title")}>
    <ul className="ww-effects-legend" data-field="effects-legend">
      {items.map(item => <li key={item.key}><span className="ww-effects-legend-mark">{item.swatch}</span><span>{t(`effectsLegend.${item.key}`)}</span></li>)}
    </ul>
    <p className="ww-effects-legend-note">{t("effectsLegend.rows")}</p>
    <p className="ww-effects-legend-note">{t("effectsLegend.order")}</p>
    <p className="ww-effects-legend-note">{t("effectsLegend.onlyHud")}</p>
    <p className="ww-effects-legend-note">{t("effectsLegend.noNames")}</p>
  </SettingsSection>
}

function AboutPage({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const app = controller.app
  const show = (text: IdentityText) => text.kind === "value" ? text.value : t(text.key)
  const profile = app ? profileKey(app) : undefined
  const revision = app ? revisionText(app) : undefined
  return <SettingsSection title={t("sections.about")}>
    <SettingsRow title={t("buildVersion")} description={t("storage")}><span className="ww-num ww-selectable" data-field="app-version">{app ? show(versionText(app)) : "—"}</span></SettingsRow>
    <SettingsRow title={t("buildNumber")}><span className="ww-num ww-selectable" data-field="app-build-number" data-official={app?.official ? "true" : "false"}>{app ? show(buildNumberText(app)) : "—"}</span></SettingsRow>
    <SettingsRow title={t("buildProfile")}><span data-field="app-profile">{app ? (profile ? t(profile) : app.profile) : "—"}</span></SettingsRow>
    <SettingsRow title={t("buildRevision")} description={revision?.note ? t(revision.note) : undefined}><span className="ww-num ww-selectable" data-field="app-revision" data-dirty={app?.dirty}>{revision ? show(revision.revision) : "—"}</span></SettingsRow>
  </SettingsSection>
}

/** The one Settings view. Everything it shows is a working setting: a row without a backend does not exist here. */
export function SettingsView({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const [page, setPage] = useState<Page>("general")
  const previous = useRef<Page>("general")
  const direction = pageOrder.indexOf(page) >= pageOrder.indexOf(previous.current) ? "forward" : "back"
  const groups = useMemo<ReadonlyArray<SidebarGroupModel<Page>>>(() => [
    { label: t("groups.basic"), items: [{ value: "general", label: t("pages.general"), icon: "sliders" }, { value: "history", label: t("pages.history"), icon: "clock" }, { value: "appearance", label: t("pages.appearance"), icon: "palette" }] },
    { label: t("groups.overlays"), items: [{ value: "combatOverlay", label: t("pages.combatOverlay"), icon: "overlay" }, { value: "effectsOverlay", label: t("pages.effectsOverlay"), icon: "spark" }] },
    { label: t("groups.about"), items: [{ value: "about", label: t("pages.about"), icon: "about" }] },
    { label: "", pinned: true, items: [{ value: "licenses", label: t("pages.licenses"), icon: "license" }] },
  ], [t])
  const change = (next: Page) => {
    previous.current = page
    setPage(next)
  }
  const warning = controller.warning === "overlay_save_failed" || controller.warning === "window_save_failed" ? "saveFailed" : controller.warning === "overlay_geometry_reset" ? "geometryReset" : controller.warning ? "invalid" : ""
  return <div className="ww-settings" data-field="settings" data-page={page}>
    <ThemeBackdrop theme={resolveTheme(controller.theme)} intensity="ambient" />
    <Sidebar label={t("title")} value={page} groups={groups} onChange={change} />
    <div className="ww-detail">
      <ContentSwitch contentKey={page} direction={direction} className="ww-detail-inner">
        <div className="ww-detail-title"><h2>{t(`pages.${page}`)}</h2></div>
        {warning && <p className="ww-settings-notice" role="status">{t(warning)}</p>}
        {controller.loadFailed && <p className="ww-settings-notice" data-tone="critical" role="alert" data-field="settings-load-failed">{t("loadFailed")}</p>}
        {controller.failed && <p className="ww-settings-notice" data-tone="critical" role="alert" data-field="settings-save-failed">{t("saveFailed")}</p>}
        {page === "general" && <GeneralPage controller={controller} />}
        {page === "history" && <HistoryPage controller={controller} />}
        {page === "appearance" && <AppearancePage controller={controller} />}
        {page === "combatOverlay" && <OverlayPage kind="combat" controller={controller} />}
        {page === "effectsOverlay" && <OverlayPage kind="effects" controller={controller} />}
        {page === "about" && <AboutPage controller={controller} />}
        {page === "licenses" && <LicensesPage openLink={controller.openLicenseLink} />}
      </ContentSwitch>
    </div>
  </div>
}
