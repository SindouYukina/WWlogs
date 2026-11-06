import { useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, ContentSwitch, Icon, Segmented, Select, SettingsRow, SettingsSection, Sidebar, Slider, Stepper, ThemeBackdrop, ThemePicker, Toggle, Tooltip, type SidebarGroupModel, type ThemeId } from "@/design"
import { manifest } from "@/i18n"
import { useThemeDescriptions } from "@/lib/useListeningLabels"

type Page = "general" | "combatOverlay" | "effectsOverlay" | "capture" | "appearance" | "about"

const pageOrder: Page[] = ["general", "combatOverlay", "effectsOverlay", "capture", "appearance", "about"]

// All values here are local demo state. Nothing is read from or written to the user's real settings.
function useDemo<T>(initial: T) {
  return useState<T>(initial)
}

function Hint({ text }: { text: string }) {
  return <Tooltip text={text}><Icon name="info" size={15} /></Tooltip>
}

function GeneralPage() {
  const { t } = useTranslation("showcase")
  const [startup, setStartup] = useDemo(false)
  const [memory, setMemory] = useDemo(true)
  const [language, setLanguage] = useDemo("zh-TW")
  const [scale, setScale] = useDemo("100")
  const [display, setDisplay] = useDemo("primary")
  const [hoverDelay, setHoverDelay] = useDemo(0)
  const [animations, setAnimations] = useDemo(true)
  const [animationTime, setAnimationTime] = useDemo(360)
  return <>
    <SettingsSection title={t("settings.sections.startup")}>
      <SettingsRow title={t("settings.startWithWindows")}><Toggle label={t("settings.startWithWindows")} checked={startup} onChange={setStartup} /></SettingsRow>
      <SettingsRow title={t("settings.releaseMemory")} hint={<Hint text={t("settings.releaseMemoryHint")} />}><Toggle label={t("settings.releaseMemory")} checked={memory} onChange={setMemory} /></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("settings.sections.appearance")}>
      <SettingsRow title={t("settings.language")}><Select label={t("settings.language")} value={language} onChange={setLanguage} options={manifest.supportedLocales.map(locale => ({ value: locale, label: manifest.languages[locale as keyof typeof manifest.languages].label }))} /></SettingsRow>
      <SettingsRow title={t("settings.scale")}><Select label={t("settings.scale")} value={scale} onChange={setScale} options={["90", "100", "110", "125", "150"].map(value => ({ value, label: `${value}%` }))} /></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("settings.sections.display")}>
      <SettingsRow title={t("settings.showOn")}><Segmented label={t("settings.showOn")} value={display} onChange={setDisplay} options={[{ value: "primary", label: t("settings.primaryDisplay") }, { value: "custom", label: t("settings.customDisplay") }]} /></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("settings.sections.motion")} collapsible>
      <SettingsRow title={t("settings.hoverDelay")} hint={<Hint text={t("settings.hoverDelayHint")} />}>
        <Slider label={t("settings.hoverDelay")} min={0} max={1000} step={10} value={hoverDelay} onChange={setHoverDelay} />
        <Stepper value={hoverDelay} unit={t("settings.ms")} step={50} min={0} max={1000} onChange={setHoverDelay} decreaseLabel={t("settings.decrease")} increaseLabel={t("settings.increase")} />
      </SettingsRow>
      <SettingsRow title={t("settings.enableAnimations")}><Toggle label={t("settings.enableAnimations")} checked={animations} onChange={setAnimations} /></SettingsRow>
      <SettingsRow title={t("settings.animationTime")}>
        <Slider label={t("settings.animationTime")} min={120} max={800} step={10} value={animationTime} onChange={setAnimationTime} />
        <Stepper value={animationTime} unit={t("settings.ms")} step={20} min={120} max={800} onChange={setAnimationTime} decreaseLabel={t("settings.decrease")} increaseLabel={t("settings.increase")} />
      </SettingsRow>
    </SettingsSection>
  </>
}

function OverlayPage({ kind }: { kind: "combat" | "effects" }) {
  const { t } = useTranslation("showcase")
  const [visible, setVisible] = useDemo(true)
  const [locked, setLocked] = useDemo(false)
  const [clickThrough, setClickThrough] = useDemo(false)
  const [opacity, setOpacity] = useDemo(kind === "combat" ? 74 : 62)
  const [density, setDensity] = useDemo("comfortable")
  const [limit, setLimit] = useDemo(8)
  return <>
    <SettingsSection title={t("settings.sections.window")}>
      <SettingsRow title={t("settings.showWindow")}><Toggle label={t("settings.showWindow")} checked={visible} onChange={setVisible} /></SettingsRow>
      <SettingsRow title={t("settings.lockPosition")}><Toggle label={t("settings.lockPosition")} checked={locked} onChange={setLocked} /></SettingsRow>
      <SettingsRow title={t("settings.clickThrough")} hint={<Hint text={t("settings.clickThroughHint")} />}><Toggle label={t("settings.clickThrough")} checked={clickThrough} onChange={setClickThrough} /></SettingsRow>
      <SettingsRow title={t("settings.position")} description={t("settings.positionNote")}><Button size="sm">{t("settings.resetPosition")}</Button></SettingsRow>
    </SettingsSection>
    <SettingsSection title={t("settings.sections.look")}>
      <SettingsRow title={t("controls.opacity")}>
        <Slider label={t("controls.opacity")} min={20} max={100} value={opacity} onChange={setOpacity} />
        <Stepper value={opacity} unit="%" step={5} min={20} max={100} onChange={setOpacity} decreaseLabel={t("settings.decrease")} increaseLabel={t("settings.increase")} />
      </SettingsRow>
      <SettingsRow title={t("settings.density")}><Segmented label={t("settings.density")} value={density} onChange={setDensity} options={[{ value: "compact", label: t("settings.compact") }, { value: "comfortable", label: t("settings.comfortable") }]} /></SettingsRow>
      {kind === "effects" && <SettingsRow title={t("settings.maxEffects")}><Stepper value={limit} step={1} min={3} max={16} onChange={setLimit} decreaseLabel={t("settings.decrease")} increaseLabel={t("settings.increase")} /></SettingsRow>}
    </SettingsSection>
  </>
}

function CapturePage() {
  const { t } = useTranslation("showcase")
  const [adapter, setAdapter] = useDemo("a")
  const [auto, setAuto] = useDemo(false)
  return <SettingsSection title={t("settings.sections.capture")}>
    <SettingsRow title={t("settings.adapter")} description={t("settings.demoOnly")}><Select label={t("settings.adapter")} value={adapter} onChange={setAdapter} options={[{ value: "a", label: t("settings.adapterA") }, { value: "b", label: t("settings.adapterB") }]} /></SettingsRow>
    <SettingsRow title={t("settings.showOnStart")}><Toggle label={t("settings.showOnStart")} checked={auto} onChange={setAuto} /></SettingsRow>
  </SettingsSection>
}

function AppearancePage({ theme, setTheme, reduced, setReduced }: AppearanceProps) {
  const { t } = useTranslation(["showcase", "settings"])
  const describe = useThemeDescriptions()
  return <>
    <SettingsSection title={t("settings:appearance")}>
      <div className="ww-row-block"><ThemePicker label={t("settings:theme")} value={theme} onChange={setTheme} describe={describe} /></div>
    </SettingsSection>
    <SettingsSection title={t("settings.sections.motionPolicy")}>
      <SettingsRow title={t("settings:reduceMotion")} description={t("settings:reduceMotionHint")}><Toggle label={t("settings:reduceMotion")} checked={reduced} onChange={setReduced} /></SettingsRow>
    </SettingsSection>
  </>
}

function AboutPage() {
  const { t } = useTranslation("showcase")
  return <SettingsSection title={t("settings.about")}>
    <SettingsRow title="WWlogs" description={t("settings.aboutNote")}><Button size="sm" variant="ghost">{t("settings.demoAction")}</Button></SettingsRow>
  </SettingsSection>
}

interface AppearanceProps {
  theme: ThemeId
  setTheme: (theme: ThemeId) => void
  reduced: boolean
  setReduced: (reduced: boolean) => void
}

export function SettingsDemo(props: AppearanceProps) {
  const { t } = useTranslation("showcase")
  const [page, setPage] = useState<Page>("general")
  const previous = useRef<Page>("general")
  const direction = pageOrder.indexOf(page) >= pageOrder.indexOf(previous.current) ? "forward" : "back"
  const groups = useMemo<ReadonlyArray<SidebarGroupModel<Page>>>(() => [
    { label: t("settings.groups.basic"), items: [{ value: "general", label: t("settings.pages.general"), icon: "sliders" }, { value: "appearance", label: t("settings.pages.appearance"), icon: "palette" }] },
    { label: t("settings.groups.overlays"), items: [{ value: "combatOverlay", label: t("settings.pages.combatOverlay"), icon: "overlay" }, { value: "effectsOverlay", label: t("settings.pages.effectsOverlay"), icon: "spark" }] },
    { label: t("settings.groups.capture"), items: [{ value: "capture", label: t("settings.pages.capture"), icon: "capture" }] },
    { label: t("settings.groups.about"), items: [{ value: "about", label: t("settings.pages.about"), icon: "about" }] },
  ], [t])
  const change = (next: Page) => {
    previous.current = page
    setPage(next)
  }
  return <div className="ww-settings">
    <ThemeBackdrop theme={props.theme} intensity="ambient" />
    <Sidebar label={t("settings.title")} value={page} groups={groups} onChange={change} />
    <div className="ww-detail">
      <ContentSwitch contentKey={page} direction={direction} className="ww-detail-inner">
        <div className="ww-detail-title"><h2>{t(`settings.pages.${page}`)}</h2><Button size="sm" variant="ghost">{t("settings.restoreDefaults")}</Button></div>
        {page === "general" && <GeneralPage />}
        {page === "combatOverlay" && <OverlayPage kind="combat" />}
        {page === "effectsOverlay" && <OverlayPage kind="effects" />}
        {page === "capture" && <CapturePage />}
        {page === "appearance" && <AppearancePage {...props} />}
        {page === "about" && <AboutPage />}
      </ContentSwitch>
    </div>
  </div>
}
