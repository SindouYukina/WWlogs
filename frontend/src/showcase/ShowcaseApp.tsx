import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import "../design/tokens.css"
import "../design/components.css"
import "./showcase.css"
import i18n, { manifest } from "@/i18n"
import { host } from "@/lib/host"
import { Button, Chip, CombatPanel, ContentSwitch, Segmented, Select, Toggle, installMotionTokens } from "@/design"
import { useMockFeed } from "./useMockFeed"
import { CombatSection, useCombatPanelProps } from "./sections/CombatSection"
import { EffectsPanel, EffectsSection } from "./sections/EffectsSection"
import { DashboardSection } from "./sections/DashboardSection"
import { SettingsDemo } from "./sections/SettingsSection"
import { PerfPanel, type PerfBridge } from "./perf"
import en from "./locales/en.json"
import zhTW from "./locales/zh-TW.json"

// Development-only review surface. Its prose ships as a dev-only bundle; the production views use the official locales.
i18n.addResourceBundle("en", "showcase", en)
i18n.addResourceBundle("zh-TW", "showcase", zhTW)

type Section = "combat" | "effects" | "dashboard" | "settings"
const sections: Section[] = ["combat", "effects", "dashboard", "settings"]
type Accent = "crimson" | "tide" | "jade" | "gold"

/** Bare transparent surfaces for real frameless windows opened by the -showcase host mode. */
function OverlayWindow({ surface }: { surface: "combat" | "effects" }) {
  const controls = useMockFeed()
  const props = useCombatPanelProps(controls, "tween")
  useEffect(() => installMotionTokens(), [])
  return <div className="ww-root sc-window-surface" style={{ "--wails-draggable": "drag" } as React.CSSProperties}>
    {surface === "combat" ? <CombatPanel {...props} /> : <EffectsPanel controls={controls} />}
  </div>
}

function Shell() {
  const { t, i18n: instance } = useTranslation("showcase")
  const controls = useMockFeed()
  const [section, setSection] = useState<Section>(() => {
    const requested = new URLSearchParams(window.location.search).get("section") as Section | null
    return requested && sections.includes(requested) ? requested : "combat"
  })
  const [numberMode, setNumberMode] = useState<"tween" | "roll">("tween")
  const [speed, setSpeed] = useState("1")
  const [reduced, setReduced] = useState(false)
  const [accent, setAccent] = useState<Accent>("crimson")
  const [perfOpen, setPerfOpen] = useState(false)
  const uninstall = useRef<(() => void) | null>(null)
  useEffect(() => {
    uninstall.current?.()
    uninstall.current = installMotionTokens({ scale: Number(speed), preference: reduced ? "reduced" : undefined })
    return () => uninstall.current?.()
  }, [speed, reduced])
  useEffect(() => {
    document.documentElement.dataset.wwTheme = "dark"
    if (accent === "crimson") delete document.documentElement.dataset.wwAccent
    else document.documentElement.dataset.wwAccent = accent
  }, [accent])
  const direction = useRef<"forward" | "back">("forward")
  const previousSection = useRef(section)
  if (previousSection.current !== section) {
    direction.current = sections.indexOf(section) > sections.indexOf(previousSection.current) ? "forward" : "back"
    previousSection.current = section
  }
  // Automation hook for the performance runner; it exists only in this Development-only chunk.
  const bridge = useMemo<PerfBridge>(() => ({ setSection, setNumberMode, controls: () => controlsRef.current }), [])
  const controlsRef = useRef(controls)
  controlsRef.current = controls
  useEffect(() => {
    (window as unknown as { __wwShowcase?: PerfBridge }).__wwShowcase = bridge
  }, [bridge])
  const localeOptions = manifest.supportedLocales.map(locale => ({ value: locale, label: manifest.languages[locale as keyof typeof manifest.languages].label }))
  return <div className="ww-root sc-shell">
    <header className="sc-header">
      <div className="sc-brand"><span className="sc-mark" aria-hidden="true">W</span><div><strong>{t("shell.title")}</strong><span>{t("shell.subtitle")}</span></div></div>
      <Segmented variant="tabs" label={t("shell.sections")} value={section} onChange={setSection} options={sections.map(value => ({ value, label: t(`shell.section.${value}`) }))} />
      <div className="sc-header-tools"><Chip tone="warn" dot>{t("shell.mock")}</Chip>
        <Select label={t("shell.locale")} value={instance.language} onChange={locale => void instance.changeLanguage(locale)} options={localeOptions} />
      </div>
    </header>
    <div className="sc-toolbar">
      <label>{t("shell.speed")}<Segmented label={t("shell.speed")} value={speed} onChange={setSpeed} options={[{ value: "1", label: "1×" }, { value: "2", label: "½×" }, { value: "5", label: "⅕×" }]} /></label>
      <label>{t("shell.accent")}<Segmented label={t("shell.accent")} value={accent} onChange={setAccent} options={[{ value: "crimson", label: t("shell.crimson") }, { value: "tide", label: t("shell.tide") }, { value: "jade", label: t("shell.jade") }, { value: "gold", label: t("shell.gold") }]} /></label>
      <label className="sc-toolbar-toggle">{t("shell.reduced")}<Toggle label={t("shell.reduced")} checked={reduced} onChange={setReduced} /></label>
      <Button size="sm" variant="ghost" onClick={() => setPerfOpen(!perfOpen)}>{t(perfOpen ? "shell.hidePerf" : "shell.showPerf")}</Button>
    </div>
    <main className="sc-main">
      <ContentSwitch contentKey={section} direction={direction.current} className="sc-section">
        {section === "combat" && <CombatSection controls={controls} numberMode={numberMode} setNumberMode={setNumberMode} />}
        {section === "effects" && <EffectsSection controls={controls} />}
        {section === "dashboard" && <DashboardSection controls={controls} numberMode={numberMode} />}
        {section === "settings" && <div className="sc-settings-frame"><SettingsDemo /></div>}
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
  return surface === "combat" || surface === "effects" ? <OverlayWindow surface={surface} /> : <Shell />
}
