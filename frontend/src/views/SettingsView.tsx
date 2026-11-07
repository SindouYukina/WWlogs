import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, ContentSwitch, Icon, Segmented, Select, SettingsRow, SettingsSection, Sidebar, Slider, Stepper, ThemeBackdrop, ThemePicker, Toggle, Tooltip, resolveTheme, type SidebarGroupModel } from "@/design"
import { manifest } from "@/i18n"
import { buildNumberText, profileKey, revisionText, versionText, type IdentityText } from "@/lib/build-identity"
import { adapterProblem, environmentNotice } from "@/lib/capture-environment"
import { statusText } from "@/i18n"
import type { SettingsController } from "@/lib/settings-controller"
import { defaultUpdateInterval, normalizeUpdateInterval, updateIntervals, type OverlayKind } from "@/lib/snapshot"
import { useThemeDescriptions } from "@/lib/useListeningLabels"
import { LicensesPage } from "./LicensesPage"

type Page = "general" | "appearance" | "combatOverlay" | "effectsOverlay" | "about" | "licenses"

const pageOrder: Page[] = ["general", "appearance", "combatOverlay", "effectsOverlay", "about", "licenses"]

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
  const options = manifest.supportedLocales.map(locale => ({ value: locale, label: manifest.languages[locale as keyof typeof manifest.languages].label }))
  const interval = (ms: number) => ms % 1000 === 0 ? t("units.seconds", { value: ms / 1000 }) : t("units.milliseconds", { value: ms })
  return <>
    <SettingsSection title={t("sections.language")}>
      <SettingsRow title={t("language")} description={t("languageHint")}><span data-field="locale"><Select label={t("language")} value={controller.locale} onChange={controller.setLocale} options={options} /></span></SettingsRow>
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

function OverlayPage({ kind, controller }: { kind: OverlayKind; controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const state = controller.overlays?.[kind]
  const change = controller.updateOverlay
  // A slider moves faster than a round trip, so it shows its own value until the host has caught up.
  const [draft, setDraft] = useState<number | null>(null)
  const settle = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(settle.current), [])
  const setOpacity = (opacity: number) => {
    setDraft(opacity)
    change(kind, { opacity })
    clearTimeout(settle.current)
    settle.current = setTimeout(() => setDraft(null), 500)
  }
  const opacity = draft ?? state?.opacity ?? 100
  const disabled = !controller.connected || !state
  return <div data-field={`overlay-settings-${kind}`}>
    <SettingsSection title={t("sections.window")}>
      <SettingsRow title={t("showWindow")}><Toggle label={t("showWindow")} disabled={disabled} checked={state?.visible === true} onChange={visible => change(kind, { visible })} /></SettingsRow>
      <SettingsRow title={t("alwaysOnTop")}><Toggle label={t("alwaysOnTop")} disabled={disabled} checked={state?.alwaysOnTop === true} onChange={alwaysOnTop => change(kind, { alwaysOnTop })} /></SettingsRow>
      <SettingsRow title={t("clickThrough")} hint={<Hint text={t("clickThroughHint")} />}><Toggle label={t("clickThrough")} disabled={disabled} checked={state?.clickThrough === true} onChange={clickThrough => change(kind, { clickThrough })} /></SettingsRow>
      <SettingsRow title={t("editMode")} description={t("editModeHint")}><Button size="sm" variant={state?.editing ? "primary" : "secondary"} disabled={disabled} data-field="edit-toggle" onClick={() => change(kind, { editing: !state?.editing })}>{t(state?.editing ? "finishEditing" : "startEditing")}</Button></SettingsRow>
      <SettingsRow title={t("position")} description={t("positionNote")}><Button size="sm" disabled={disabled} onClick={() => controller.resetOverlay(kind)}>{t("resetPosition")}</Button></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("sections.look")}>
      <SettingsRow title={t("opacity")} description={t("opacityHint")}>
        <Slider label={t("opacity")} min={20} max={100} value={opacity} onChange={setOpacity} />
        <Stepper value={opacity} unit="%" step={5} min={20} max={100} onChange={setOpacity} decreaseLabel={t("decrease")} increaseLabel={t("increase")} />
      </SettingsRow>
    </SettingsSection>
  </div>
}

function AboutPage({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const app = controller.app
  const show = (text: IdentityText) => text.kind === "value" ? text.value : t(text.key)
  const profile = app ? profileKey(app) : undefined
  const revision = app ? revisionText(app) : undefined
  return <SettingsSection title={t("sections.about")}>
    <SettingsRow title={t("buildVersion")} description={t("storage")}><span className="ww-num" data-field="app-version">{app ? show(versionText(app)) : "—"}</span></SettingsRow>
    <SettingsRow title={t("buildNumber")}><span className="ww-num" data-field="app-build-number" data-official={app?.official ? "true" : "false"}>{app ? show(buildNumberText(app)) : "—"}</span></SettingsRow>
    <SettingsRow title={t("buildProfile")}><span data-field="app-profile">{app ? (profile ? t(profile) : app.profile) : "—"}</span></SettingsRow>
    <SettingsRow title={t("buildRevision")} description={revision?.note ? t(revision.note) : undefined}><span className="ww-num" data-field="app-revision" data-dirty={app?.dirty}>{revision ? show(revision.revision) : "—"}</span></SettingsRow>
  </SettingsSection>
}

/** The one Settings view. Everything it shows is a working setting: a row without a backend does not exist here. */
export function SettingsView({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation("settings")
  const [page, setPage] = useState<Page>("general")
  const previous = useRef<Page>("general")
  const direction = pageOrder.indexOf(page) >= pageOrder.indexOf(previous.current) ? "forward" : "back"
  const groups = useMemo<ReadonlyArray<SidebarGroupModel<Page>>>(() => [
    { label: t("groups.basic"), items: [{ value: "general", label: t("pages.general"), icon: "sliders" }, { value: "appearance", label: t("pages.appearance"), icon: "palette" }] },
    { label: t("groups.overlays"), items: [{ value: "combatOverlay", label: t("pages.combatOverlay"), icon: "overlay" }, { value: "effectsOverlay", label: t("pages.effectsOverlay"), icon: "spark" }] },
    { label: t("groups.about"), items: [{ value: "about", label: t("pages.about"), icon: "about" }] },
    { label: "", pinned: true, items: [{ value: "licenses", label: t("pages.licenses"), icon: "license" }] },
  ], [t])
  const change = (next: Page) => {
    previous.current = page
    setPage(next)
  }
  const warning = controller.warning === "overlay_save_failed" ? "saveFailed" : controller.warning === "overlay_geometry_reset" ? "geometryReset" : controller.warning ? "invalid" : ""
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
        {page === "appearance" && <AppearancePage controller={controller} />}
        {page === "combatOverlay" && <OverlayPage kind="combat" controller={controller} />}
        {page === "effectsOverlay" && <OverlayPage kind="effects" controller={controller} />}
        {page === "about" && <AboutPage controller={controller} />}
        {page === "licenses" && <LicensesPage openLink={controller.openLicenseLink} />}
      </ContentSwitch>
    </div>
  </div>
}
