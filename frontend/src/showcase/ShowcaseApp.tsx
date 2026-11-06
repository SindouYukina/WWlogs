import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import "./showcase.css"
import i18n, { manifest } from "@/i18n"
import { host } from "@/lib/host"
import { Button, Chip, ContentSwitch, Icon, ListeningStatus, Segmented, Select, Toggle, applyAppearance, themeIds, type ThemeId } from "@/design"
import type { OverlayKind } from "@/lib/snapshot"
import { DashboardView } from "@/views/DashboardView"
import { OverlaySurface } from "@/views/OverlayWindow"
import { SettingsView } from "@/views/SettingsView"
import { useMockFeed, type FeedControls } from "./useMockFeed"
import { useMockSettings } from "./useMockSettings"
import { listeningScenarios, type ListeningScenario } from "./mock-feed.ts"
import { CombatSection } from "./sections/CombatSection"
import { EffectsSection } from "./sections/EffectsSection"
import { TypographySection, neutralTuning, schemeStyle, type TypeTuning } from "./sections/TypographySection"
import { useFontSchemes, type SchemeId } from "./useFontSchemes"
import { useListeningLabels } from "@/lib/useListeningLabels"
import { PerfPanel, type PerfBridge } from "./perf"
import en from "./locales/en.json"
import zhTW from "./locales/zh-TW.json"
import zhCN from "./locales/zh-CN.json"
import ja from "./locales/ja.json"
import ko from "./locales/ko.json"

// Development-only review surface. Its prose ships as a dev-only bundle; the production views use the official locales.
i18n.addResourceBundle("en", "showcase", en)
i18n.addResourceBundle("zh-TW", "showcase", zhTW)
i18n.addResourceBundle("zh-CN", "showcase", zhCN)
i18n.addResourceBundle("ja", "showcase", ja)
i18n.addResourceBundle("ko", "showcase", ko)

type Section = "combat" | "effects" | "dashboard" | "settings" | "typography"
const sections: Section[] = ["dashboard", "settings", "typography", "combat", "effects"]

function StatusBadge({ controls }: { controls: FeedControls }) {
  const describe = useListeningLabels()
  const listening = controls.output.snapshot.listening
  if (!listening) return null
  const { label, detail } = describe(listening)
  return <ListeningStatus state={listening.state} label={label} detail={detail} />
}

/** The production Overlay surface in a real frameless window, fed by the Mock provider. */
function ReviewOverlayWindow({ surface }: { surface: OverlayKind }) {
  const controls = useMockFeed()
  useEffect(() => {
    applyAppearance(new URLSearchParams(window.location.search).get("theme") ?? undefined, false)
  }, [])
  return <OverlaySurface surface={surface} data={controls.data} draggable />
}

function Shell() {
  const { t, i18n: instance } = useTranslation("showcase")
  const controls = useMockFeed()
  const [section, setSection] = useState<Section>(() => {
    const requested = new URLSearchParams(window.location.search).get("section") as Section | null
    return requested && sections.includes(requested) ? requested : "dashboard"
  })
  const [numberMode, setNumberMode] = useState<"tween" | "roll">("tween")
  const [speed, setSpeed] = useState("1")
  const [reduced, setReduced] = useState(false)
  const [theme, setTheme] = useState<ThemeId>(() => (new URLSearchParams(window.location.search).get("theme") as ThemeId | null) ?? "electro")
  const mockSettings = useMockSettings(theme, setTheme, reduced, setReduced)
  const [perfOpen, setPerfOpen] = useState(false)
  const [scheme, setScheme] = useState<SchemeId>("inter-noto")
  const [numericFace, setNumericFace] = useState<"scheme" | "bahnschrift">("scheme")
  const [tuning, setTuning] = useState<TypeTuning>(neutralTuning)
  const fonts = useFontSchemes(instance.language)
  useEffect(() => {
    applyAppearance(theme, reduced, Number(speed))
  }, [theme, reduced, speed])
  const { ensure: ensureScheme } = fonts
  useEffect(() => { ensureScheme(scheme) }, [ensureScheme, scheme])
  // A candidate that did not load must not be applied: the page would show a fallback font under its name.
  const schemeUsable = fonts.usable(scheme)
  useEffect(() => {
    // The whole-page scheme overrides the root variables; clearing them returns to the bundled typography.css rules.
    const root = document.documentElement
    const applied = schemeUsable ? scheme : "inter-noto"
    const style = schemeStyle(applied, instance.language, tuning, numericFace)
    const neutral = applied === "inter-noto" && numericFace === "scheme" && tuning === neutralTuning
    for (const [name, value] of Object.entries(style)) {
      if (neutral) root.style.removeProperty(name)
      else root.style.setProperty(name, value)
    }
    root.classList.toggle("ww-typeface-scope", !neutral)
  }, [scheme, schemeUsable, numericFace, tuning, instance.language])
  useEffect(() => {
    document.documentElement.lang = instance.language
    const onChange = (language: string) => { document.documentElement.lang = language }
    instance.on("languageChanged", onChange)
    return () => instance.off("languageChanged", onChange)
  }, [instance])
  const paneSetter = useRef<((left: SchemeId, right: SchemeId) => void) | null>(null)
  const direction = useRef<"forward" | "back">("forward")
  const previousSection = useRef(section)
  if (previousSection.current !== section) {
    direction.current = sections.indexOf(section) > sections.indexOf(previousSection.current) ? "forward" : "back"
    previousSection.current = section
  }
  const controlsRef = useRef(controls)
  controlsRef.current = controls
  // Automation hook for the performance runner; it exists only in this Development-only chunk.
  const fontsRef = useRef(fonts)
  fontsRef.current = fonts
  const bridge = useMemo<PerfBridge>(() => ({ setSection, setNumberMode, setTheme, setScheme, setLocale: locale => void instance.changeLanguage(locale), controls: () => controlsRef.current, fontStatus: () => fontsRef.current.statuses, setPanes: (left, right) => paneSetter.current?.(left, right) }), [instance])
  useEffect(() => {
    (window as unknown as { __wwShowcase?: PerfBridge }).__wwShowcase = bridge
  }, [bridge])
  const localeOptions = manifest.supportedLocales.map(locale => ({ value: locale, label: manifest.languages[locale as keyof typeof manifest.languages].label }))
  return <div className="ww-root sc-shell">
    <header className="sc-header">
      <div className="sc-brand"><span className="sc-mark" aria-hidden="true">W</span><div><strong>{t("shell.title")}</strong><span>{t("shell.subtitle")}</span></div></div>
      <Segmented variant="tabs" label={t("shell.sections")} value={section} onChange={setSection} options={sections.map(value => ({ value, label: t(`shell.section.${value}`) }))} />
      <div className="sc-header-tools"><StatusBadge controls={controls} /><Chip tone="warn" dot>{t("shell.mock")}</Chip>
        <Select label={t("shell.locale")} value={instance.language} onChange={locale => void instance.changeLanguage(locale)} options={localeOptions} />
      </div>
    </header>
    <div className="sc-toolbar">
      <span className="sc-toolbar-tag">{t("shell.devTools")}</span>
      <label>{t("shell.theme")}<Segmented label={t("shell.theme")} value={theme} onChange={setTheme} options={themeIds.map(id => ({ value: id, label: t(`shell.themes.${id}`) }))} /></label>
      <label>{t("shell.speed")}<Segmented label={t("shell.speed")} value={speed} onChange={setSpeed} options={[{ value: "1", label: "1×" }, { value: "2", label: "½×" }, { value: "5", label: "⅕×" }]} /></label>
      <label className="sc-toolbar-toggle">{t("shell.reduced")}<Toggle label={t("shell.reduced")} checked={reduced} onChange={setReduced} /></label>
      <label className="sc-toolbar-select">{t("shell.listening")}<Select label={t("shell.listening")} value={controls.listeningScenario} onChange={controls.setListeningScenario as (value: ListeningScenario) => void} options={listeningScenarios.map(id => ({ value: id, label: t(`shell.scenario.${id}`) }))} /></label>
      <Button size="sm" variant="ghost" onClick={() => setPerfOpen(!perfOpen)}>{t(perfOpen ? "shell.hidePerf" : "shell.showPerf")}</Button>
    </div>
    <main className="sc-main">
      <ContentSwitch contentKey={section} direction={direction.current} className="sc-section">
        {section === "combat" && <CombatSection controls={controls} numberMode={numberMode} setNumberMode={setNumberMode} />}
        {section === "effects" && <EffectsSection controls={controls} />}
        {section === "dashboard" && <DashboardView data={controls.data} numberMode={numberMode} theme={theme} actions={<Button size="sm" onClick={() => controls.apply(feed => feed.resetEncounter())}><Icon name="reset" size={13} />{t("controls.reset")}</Button>} />}
        {section === "settings" && <div className="sc-settings-frame"><SettingsView controller={mockSettings} /></div>}
        {section === "typography" && <TypographySection fonts={fonts} paneSetter={paneSetter} globalScheme={scheme} setGlobalScheme={setScheme} numericFace={numericFace} setNumericFace={setNumericFace} tuning={tuning} setTuning={setTuning} />}
      </ContentSwitch>
    </main>
    {perfOpen && <PerfPanel />}
  </div>
}

export function ShowcaseApp() {
  const surface = new URLSearchParams(window.location.search).get("surface")
  useEffect(() => {
    // Read the saved language once for convenience; the Showcase never writes settings.
    host.getSettings().then(state => i18n.changeLanguage(state.settings.locale)).catch(() => i18n.changeLanguage("zh-TW"))
  }, [])
  return surface === "combat" || surface === "effects" ? <ReviewOverlayWindow surface={surface} /> : <Shell />
}
