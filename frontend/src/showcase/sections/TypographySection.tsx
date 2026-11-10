import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type RefObject } from "react"
import { useTranslation } from "react-i18next"
import { Button, Card, Chip, DamageBar, Segmented, Select, SettingsRow, SettingsSection, Slider } from "@/design"
import { requiredFaces, schemeIds, schemeStacks, schemeUsable, uncoveredCharacters, type FontManifest, type SchemeId, type SchemeStatus } from "../typefaces"
import type { FontSchemes } from "../useFontSchemes"
import { masterRoles } from "../master-fixture.ts"

export interface TypeTuning {
  weight: number
  tracking: number
  leading: number
}

export const neutralTuning: TypeTuning = { weight: 0, tracking: 0, leading: 0 }

/** Variables that make a subtree render with one scheme. Applying them at the root compares a whole page. */
export function schemeStyle(scheme: SchemeId, locale: string, tuning: TypeTuning, numericFace: "scheme" | "bahnschrift"): Record<string, string> {
  const stacks = schemeStacks(scheme, locale)
  return {
    "--ww-font-latin": stacks.latin,
    "--ww-font-cjk": stacks.cjk,
    "--ww-font-numeric-face": numericFace === "bahnschrift" ? `"Bahnschrift"` : stacks.latin,
    "--ww-weight-shift": String(tuning.weight),
    "--ww-tracking-shift": `${tuning.tracking}em`,
    "--ww-leading-shift": String(tuning.leading),
  }
}

const SAMPLE_DPS = [21635, 12147, 6627, 2833]

/** One compact page holding the text roles that matter: display numbers, data columns, UI prose and mixed text. */
const Specimen = memo(function Specimen({ scheme, tuning, numericFace, textRef }: { scheme: SchemeId; tuning: TypeTuning; numericFace: "scheme" | "bahnschrift"; textRef: RefObject<HTMLDivElement | null> }) {
  const { t, i18n } = useTranslation("showcase")
  const locale = i18n.language
  const style = useMemo(() => schemeStyle(scheme, locale, tuning, numericFace), [scheme, locale, tuning, numericFace])
  const integer = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const roles = masterRoles.slice(0, 3)
  return <div ref={textRef} className="ww-typeface-scope ft-specimen ww-type-ui" style={style as CSSProperties} lang={locale}>
    <div className="ft-hero">
      <span className="ww-type-small ft-label">{t("combat.teamDps")}</span>
      <span className="ww-num ww-type-display ft-hero-number">{integer.format(SAMPLE_DPS[0]!)}</span>
    </div>
    <h3 className="ww-type-display ft-title">{t("dashboard.title")} · Combat Summary 3.7</h3>
    <div className="ft-rows">
      {roles.map((role, index) => {
        const names = role.names as Readonly<Record<string, string>>
        return <div className="ft-row" key={role.roleId} data-element={role.elementId}>
          <strong>{names[locale] ?? names.en}</strong>
          <span className="ww-num">{integer.format(SAMPLE_DPS[index + 1]!)}</span>
          <span className="ww-num ft-dim">{(((3 - index) / 6) * 100).toFixed(1)}%</span>
          <div className="ft-bar"><DamageBar share={(3 - index) / 6} /></div>
        </div>
      })}
    </div>
    <p className="ft-prose">{t("typography.sample")}</p>
    <div className="ft-setting"><span>{t("settings.releaseMemory")}</span><small>{t("settings.releaseMemoryHint")}</small></div>
    <div className="ft-glyphs" title={t("typography.glyphsNote")}>{t("typography.glyphs")}</div>
    <div className="ft-digits ww-num">0123456789 · 1,234,567.89 · 00:42 · 98.6%</div>
  </div>
})

function SchemeFacts({ scheme, manifest }: { scheme: SchemeId; manifest: FontManifest | null }) {
  const { t } = useTranslation("showcase")
  const info = manifest?.[scheme]
  if (scheme === "system") return <p className="ft-facts">{t("typography.systemNote")}</p>
  if (!info) return null
  const total = info.faces.reduce((sum, face) => sum + face.bytes, 0)
  const first = info.faces[0]
  return <div className="ft-facts">
    <div><Chip tone={info.bundled ? "good" : "neutral"}>{info.bundled ? t("typography.bundled") : t("typography.candidate")}</Chip> <span>{info.license}</span></div>
    <div>{t("typography.size", { value: (total / 1048576).toFixed(2), count: info.faces.length })}</div>
    {first && <div>{first.family}: UPM {first.unitsPerEm} · {t("typography.ascent")} {first.ascent} · {t("typography.descent")} {first.descent}{first.xHeight ? ` · x ${first.xHeight}` : ""}{first.capHeight ? ` · Cap ${first.capHeight}` : ""}{Object.keys(first.axes ?? {}).length > 0 ? ` · ${Object.keys(first.axes).join(", ")}` : ""}</div>}
    <details><summary>{t("typography.faces")}</summary><ul>{info.faces.map(face => <li key={face.file}>{face.family} · {face.weight} · {face.scope} · {(face.bytes / 1024).toFixed(0)} KB · {face.glyphs} glyphs</li>)}</ul></details>
  </div>
}

const statusTone: Record<SchemeStatus["kind"], "good" | "warn" | "critical" | "neutral"> = { baseline: "neutral", loading: "neutral", loaded: "good", "no-root": "critical", missing: "critical", failed: "critical" }

/** States what is really on screen for this pane, so a fallback font can never pass for the scheme named above it. */
function SchemeStatusLine({ scheme, status, uncovered }: { scheme: SchemeId; status: SchemeStatus; uncovered: readonly string[] | undefined }) {
  const { t } = useTranslation("showcase")
  const partial = status.kind === "loaded" && uncovered !== undefined && uncovered.length > 0
  const label = partial ? t("typography.status.partial") : t(`typography.status.${status.kind}`)
  const files = [...status.missing, ...status.failed]
  return <div className="ft-status" data-field={`font-status-${scheme}`} data-status={partial ? "partial" : status.kind}>
    <Chip tone={partial ? "warn" : statusTone[status.kind]} dot>{label}</Chip>
    {files.length > 0 && <code>{files.join(", ")}</code>}
    {partial && <span>{t("typography.status.partialNote", { count: uncovered.length })} <span className="ft-fallback-chars">{uncovered.slice(0, 24).join("")}</span></span>}
    {status.kind === "loaded" && uncovered === undefined && scheme !== "inter-noto" && <span>{t("typography.status.coverageUnknown")}</span>}
  </div>
}

function ComparePane({ paneKey, scheme, setScheme, options, fonts, tuning, numericFace }: { paneKey: string; scheme: SchemeId; setScheme: (scheme: SchemeId) => void; options: ReadonlyArray<{ value: SchemeId; label: string }>; fonts: FontSchemes; tuning: TypeTuning; numericFace: "scheme" | "bahnschrift" }) {
  const { t, i18n } = useTranslation("showcase")
  const status = fonts.statuses[scheme]
  const { ensure } = fonts
  useEffect(() => { ensure(scheme) }, [ensure, scheme])
  const specimen = useRef<HTMLDivElement>(null)
  const [uncovered, setUncovered] = useState<string[] | undefined>(undefined)
  const usable = schemeUsable(status)
  const faces = useMemo(() => requiredFaces(fonts.manifest, scheme, i18n.language), [fonts.manifest, scheme, i18n.language])
  useLayoutEffect(() => {
    const text = specimen.current?.textContent ?? ""
    setUncovered(usable && scheme !== "system" ? uncoveredCharacters(text, faces) : undefined)
  }, [usable, scheme, faces, i18n.language])
  return <Card className="ft-pane">
    <div className="ft-pane-head"><strong className="ft-pane-key">{paneKey}</strong><Select label={t("typography.schemeLabel", { pane: paneKey })} value={scheme} onChange={setScheme} options={options} /></div>
    <SchemeStatusLine scheme={scheme} status={status} uncovered={uncovered} />
    {usable
      ? <Specimen scheme={scheme} tuning={tuning} numericFace={numericFace} textRef={specimen} />
      : <div className="ft-unavailable">{t(`typography.status.${status.kind}Note`)}</div>}
    <SchemeFacts scheme={scheme} manifest={fonts.manifest} />
  </Card>
}

export function TypographySection({ fonts, paneSetter, globalScheme, setGlobalScheme, numericFace, setNumericFace, tuning, setTuning }: {
  fonts: FontSchemes
  paneSetter: { current: ((left: SchemeId, right: SchemeId) => void) | null }
  globalScheme: SchemeId
  setGlobalScheme: (scheme: SchemeId) => void
  numericFace: "scheme" | "bahnschrift"
  setNumericFace: (face: "scheme" | "bahnschrift") => void
  tuning: TypeTuning
  setTuning: (tuning: TypeTuning) => void
}) {
  const { t } = useTranslation("showcase")
  const [left, setLeft] = useState<SchemeId>("inter-noto")
  const [right, setRight] = useState<SchemeId>("misans")
  useEffect(() => {
    paneSetter.current = (nextLeft, nextRight) => { setLeft(nextLeft); setRight(nextRight) }
    return () => { paneSetter.current = null }
  }, [paneSetter])
  const options = schemeIds.map(id => ({ value: id, label: t(`typography.scheme.${id}`) }))
  const globalStatus = fonts.statuses[globalScheme]
  return <div className="ft-layout">
    <div className="ft-compare">
      <ComparePane paneKey="A" scheme={left} setScheme={setLeft} options={options} fonts={fonts} tuning={tuning} numericFace={numericFace} />
      <ComparePane paneKey="B" scheme={right} setScheme={setRight} options={options} fonts={fonts} tuning={tuning} numericFace={numericFace} />
    </div>
    <aside className="sc-controls ft-controls">
      <SettingsSection title={t("typography.tuning")}>
        <SettingsRow title={t("typography.weight", { value: tuning.weight })}><Slider label={t("typography.weightLabel")} min={-120} max={120} step={10} value={tuning.weight} onChange={weight => setTuning({ ...tuning, weight })} /></SettingsRow>
        <SettingsRow title={t("typography.tracking", { value: tuning.tracking.toFixed(3) })}><Slider label={t("typography.trackingLabel")} min={-30} max={30} step={2} value={Math.round(tuning.tracking * 1000)} onChange={value => setTuning({ ...tuning, tracking: value / 1000 })} /></SettingsRow>
        <SettingsRow title={t("typography.leading", { value: tuning.leading.toFixed(2) })}><Slider label={t("typography.leadingLabel")} min={-20} max={30} step={2} value={Math.round(tuning.leading * 100)} onChange={value => setTuning({ ...tuning, leading: value / 100 })} /></SettingsRow>
        <SettingsRow title={t("typography.numericFace")}><Segmented label={t("typography.numericFace")} value={numericFace} onChange={setNumericFace} options={[{ value: "scheme", label: t("typography.followScheme") }, { value: "bahnschrift", label: "Bahnschrift" }]} /></SettingsRow>
        <div className="ft-reset"><Button size="sm" variant="ghost" onClick={() => setTuning(neutralTuning)}>{t("typography.resetTuning")}</Button></div>
      </SettingsSection>
      <SettingsSection title={t("typography.wholePage")}>
        <p className="sc-note">{t("typography.wholePageNote")}</p>
        <div className="ft-global"><Select label={t("typography.wholePage")} value={globalScheme} onChange={setGlobalScheme} options={options} /></div>
        {!schemeUsable(globalStatus) && globalStatus.kind !== "loading" && <p className="sc-note" data-field="font-global-blocked">{t("typography.status.notApplied")}</p>}
      </SettingsSection>
      <p className="sc-note">{t("typography.renderedNote")}</p>
      <Button size="sm" className="ft-recheck" onClick={fonts.recheck} data-field="font-recheck">{t("typography.status.recheck")}</Button>
    </aside>
  </div>
}
