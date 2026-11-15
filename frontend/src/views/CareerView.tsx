import { useCallback, useEffect, useMemo, useState, type CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { ActivityHeatmap, Button, Card, CharacterAvatar, Icon, Reveal, Segmented, Select, ThemeBackdrop, Tooltip, type HeatmapDay, type ThemeId } from "@/design"
import { elementColor, useFormatters } from "@/lib/format"
import { dateOfDay, dayNumber, type HistoryIndicator, type HistoryMode, type HistoryName } from "@/lib/history"
import { careerLibraryQuery, careerPresets, careerStateQuery, maxCalendarDays, rangeHeatmapLayout, type CareerPlace, type CareerPreset, type CareerRecord, type CareerRole, type CareerState, type HistoryCareer } from "@/lib/history-career"
import { recordClasses, type HistoryIntent } from "@/lib/history-library"
import { host } from "@/lib/host"
import { useHostTimeZone } from "@/lib/useHostTimeZone"
import { useSettledKey } from "@/lib/useSettledKey"
import { StatCard } from "./DashboardView"
import { LinkedName } from "./HistoryAnalysis"
import { ClassChip } from "./HistoryLibrary"
import { HistoryIndicatorChip, statusNote, useDateFormats, ZoneNote } from "./HistoryView"
import { HistoryRecovery } from "./HistoryRecovery"

// The Career Overview: totals, activity, characters, places and personal records across the stored encounters. Go selects,
// classifies, sums and ranks everything (internal/history/career.go); this view lays out what it returned, and every number
// leads back to the History records behind it.

export interface CareerViewProps {
  theme: ThemeId
  /** Changes whenever the live engine may have finished an encounter, so the page asks Go again. */
  refreshKey: string
  indicator?: HistoryIndicator
  /** The page's range and filters; the window keeps them while another page shows. */
  state: CareerState
  onState: (state: CareerState) => void
  /** Opens the History page on a date, an encounter or a filtered list. */
  onOpen: (intent: HistoryIntent) => void
}

type RecordKind = "bestDps" | "bestDamage" | "bestPeak" | "maxHit"
const recordKinds: readonly RecordKind[] = ["bestDps", "bestDamage", "bestPeak", "maxHit"]

type TrendKind = "encounters" | "damage" | "time" | "bestDps"
const trendKinds: readonly TrendKind[] = ["encounters", "damage", "time", "bestDps"]

/** How many characters and role combinations show before "show all". */
const collapsedRoles = 12
const collapsedCombos = 6

function useCareerFormats() {
  const { t } = useTranslation("history")
  const format = useFormatters()
  return useMemo(() => ({
    damage: (value: number, lowerBound?: boolean) => `${lowerBound ? "≥ " : ""}${format.integer(value)}`,
    hours: (seconds: number) => {
      const minutes = Math.floor(seconds / 60)
      return t("career.hoursMinutes", { h: format.integer(Math.floor(minutes / 60)), m: format.integer(minutes % 60) })
    },
  }), [t, format])
}

/** A mode Go resolved: the open world is labelled here, a category is named by the Master. */
function ModeText({ mode }: { mode: HistoryMode }) {
  const { t } = useTranslation("history")
  if (!mode.kind) return <span className="ww-history-unknown">{t("unidentified")}</span>
  return <LinkedName name={{ name: mode.kind === "open_world" ? t("modeOpenWorld") : mode.name, status: mode.status }} fallback={t("unidentified")} title={t("correlatedNote")} />
}

function StageText({ stage, instanceId }: { stage: HistoryName; instanceId?: number }) {
  const { t } = useTranslation("history")
  return <LinkedName name={stage} fallback={t("unidentified")} id={instanceId} title={t("correlatedNote")} />
}

/** The avatars of the static roles behind a record, named from the career's own role list. */
function RecordRoles({ roles, known }: { roles: readonly number[]; known: ReadonlyMap<number, CareerRole> }) {
  const { t } = useTranslation("common")
  return <span className="ww-history-roles">
    {roles.map(role => {
      const info = known.get(role)
      return <Tooltip key={role} text={info?.name.name ?? t("unknownRole", { id: role })} trigger="child" placement="top">
        <span data-role-id={role}><CharacterAvatar roleId={role} elementId={info?.elementId} size={22} /></span>
      </Tooltip>
    })}
  </span>
}

function RecordRow({ kind, record, known, onOpen }: { kind: RecordKind; record: CareerRecord | null | undefined; known: ReadonlyMap<number, CareerRole>; onOpen: (record: CareerRecord) => void }) {
  const { t } = useTranslation(["history", "common"])
  const format = useFormatters()
  const label = <span className="ww-career-record-label">{t(`career.record.${kind}`)}</span>
  if (!record) return <div className="ww-career-record" data-field={`record-${kind}`} data-empty="true">{label}<span className="ww-history-unknown">{t("career.recordEmpty")}</span></div>
  return <button type="button" className="ww-career-record" data-field={`record-${kind}`} data-encounter-id={record.encounterId} title={t("career.openRecord")} onClick={() => onOpen(record)}>
    {label}
    <strong className="ww-career-record-value ww-num">{format.integer(record.value)}</strong>
    <span className="ww-career-record-who">
      {kind === "maxHit"
        ? <>{record.roleId !== undefined && <CharacterAvatar roleId={record.roleId} elementId={record.elementId} size={22} />}
          <span className="ww-truncate">{record.roleName?.name ?? (record.roleId ? t("unknownRole", { ns: "common", id: record.roleId }) : t("unidentified"))}</span>
          {record.skillName && <span className="ww-career-record-skill"><LinkedName name={record.skillName} fallback={t("unidentified")} id={record.skillId} title={t("correlatedNote")} /></span>}</>
        : <RecordRoles roles={record.roles} known={known} />}
    </span>
    <span className="ww-career-record-where">
      <span className="ww-num">{record.start.date} {record.start.clock}</span>
      <span><ModeText mode={record.mode} /></span>
      {record.instanceId !== undefined && <span><StageText stage={record.stage} instanceId={record.instanceId} /></span>}
      <span className="ww-history-version">{record.gameVersion || t("library.versionUnknown")}</span>
    </span>
  </button>
}

/** A role's participations per date of the range as a strip of bars, one column per date. */
function RoleStrip({ days, first, last, color }: { days: CareerRole["days"]; first: number; last: number; color: string }) {
  const width = last - first + 1
  if (width <= 0) return null
  const max = days.reduce((best, day) => Math.max(best, day.encounters), 0)
  return <svg className="ww-career-strip" viewBox={`0 0 ${width} 12`} preserveAspectRatio="none" aria-hidden="true" style={{ "--ww-strip-color": color } as CSSProperties}>
    <rect className="ww-career-strip-track" x={0} y={11} width={width} height={1} />
    {max > 0 && days.map(day => {
      const x = dayNumber(day.date) - first
      if (x < 0 || x >= width) return null
      const height = Math.max(2, (day.encounters / max) * 11)
      return <rect key={day.date} className="ww-career-strip-bar" x={x} y={12 - height} width={Math.max(0.8, Math.min(1, width / 60))} height={height} />
    })}
  </svg>
}

/** A record value that opens the encounter it came from; without a record it is a dash. */
function RecordLink({ record, field, onOpen }: { record: CareerRecord | undefined; field: string; onOpen: (record: CareerRecord) => void }) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  if (!record) return <span className="ww-num" data-field={field}>—</span>
  return <button type="button" className="ww-career-link ww-num" data-field={field} data-encounter-id={record.encounterId} title={t("career.openRecord")}
    onClick={event => { event.stopPropagation(); onOpen(record) }} onKeyDown={event => event.stopPropagation()}>{format.integer(record.value)}</button>
}

function RoleCard({ role, first, last, onList, onRecord }: { role: CareerRole; first: number; last: number; onList: () => void; onRecord: (record: CareerRecord) => void }) {
  const { t } = useTranslation(["history", "common"])
  const format = useFormatters()
  const formats = useCareerFormats()
  const name = role.name.name ?? t("unknownRole", { ns: "common", id: role.roleId })
  return <div className="ww-career-role" data-field="career-role" data-role-id={role.roleId} data-element={role.elementId}>
    <button type="button" className="ww-career-role-open" aria-label={t("career.characterOpen", { name })} title={t("career.characterOpen", { name })} onClick={onList}>
      <CharacterAvatar roleId={role.roleId} elementId={role.elementId} size={40} />
      <span className="ww-career-role-main">
        <span className="ww-career-role-line"><strong className="ww-truncate">{name}</strong><span className="ww-num ww-career-role-count">{t("career.characterEncounters", { n: format.integer(role.encounters) })}</span></span>
        <RoleStrip days={role.days} first={first} last={last} color={elementColor(role.elementId)} />
      </span>
    </button>
    <span className="ww-career-role-facts">
      <span><span className="ww-career-fact-label">{t("career.damage")}</span><span className="ww-num" data-field="role-damage" data-lower-bound={role.damageLowerBound === true}>{formats.damage(role.damage, role.damageLowerBound)}</span></span>
      <span><span className="ww-career-fact-label">{t("career.characterBestDps")}</span><RecordLink record={role.bestDps} field="role-best-dps" onOpen={onRecord} /></span>
      <span><span className="ww-career-fact-label">{t("career.characterMaxHit")}</span><RecordLink record={role.maxHit} field="role-max-hit" onOpen={onRecord} /></span>
    </span>
    <span className="ww-history-note">
      {t("career.characterCounted", { n: format.integer(role.counted) })}
      {role.trialEncounters ? ` · ${t("career.characterTrial", { n: format.integer(role.trialEncounters) })}` : ""}
      {` · ${t("career.rangeDates", { from: role.firstDate, to: role.lastDate })}`}
    </span>
  </div>
}

/** One measure per date of the range as bars. A date without a value (no counted or no complete record) draws nothing. */
function TrendBars({ first, last, values, format, label, empty }: { first: number; last: number; values: ReadonlyMap<string, { value: number; lowerBound?: boolean }>; format: (value: number, lowerBound?: boolean) => string; label: string; empty: string }) {
  const dates = useDateFormats()
  const width = last - first + 1
  let max = 0
  for (const entry of values.values()) max = Math.max(max, entry.value)
  if (width <= 0 || max <= 0) return <p className="ww-dash-empty-note" data-field="trend-empty">{empty}</p>
  const bar = Math.min(0.86, Math.max(0.5, 1 - width / 400))
  return <div className="ww-career-trend" data-field="trend" data-days={width}>
    <div className="ww-career-trend-scale ww-num" aria-hidden="true"><span>{format(max)}</span><span>0</span></div>
    <svg className="ww-career-trend-plot" viewBox={`0 0 ${width} 100`} preserveAspectRatio="none" role="img" aria-label={label}>
      <line className="ww-career-trend-grid" x1={0} x2={width} y1={50} y2={50} />
      {[...values].map(([date, entry]) => {
        const x = dayNumber(date) - first
        if (x < 0 || x >= width || entry.value <= 0) return null
        const height = Math.max(1.5, (entry.value / max) * 100)
        return <rect key={date} className="ww-career-trend-bar" data-date={date} data-lower-bound={entry.lowerBound === true} x={x + (1 - bar) / 2} y={100 - height} width={bar} height={height}>
          <title>{`${dates.long(date)} · ${format(entry.value, entry.lowerBound)}`}</title>
        </rect>
      })}
    </svg>
    <div className="ww-career-trend-axis ww-num" aria-hidden="true"><span>{dates.short(dateOfDay(first))}</span><span>{dates.short(dateOfDay(last))}</span></div>
  </div>
}

function PlaceTable({ places, kind, onOpen, onRecord }: { places: readonly CareerPlace[]; kind: "mode" | "stage"; onOpen: (place: CareerPlace) => void; onRecord: (record: CareerRecord) => void }) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  const formats = useCareerFormats()
  if (places.length === 0) return <p className="ww-dash-empty-note">{t("career.placesEmpty")}</p>
  return <div className="ww-dash-skills-wrap" style={{ maxHeight: 320 }}><table className="ww-history-table" data-field={`career-${kind}s`}>
    <thead><tr>
      <th>{t(kind === "mode" ? "columns.mode" : "columns.scene")}</th>
      {kind === "stage" && <th>{t("columns.mode")}</th>}
      <th className="ww-num">{t("career.encounters")}</th><th className="ww-num">{t("career.placeCounted")}</th>
      <th className="ww-num">{t("career.damage")}</th><th className="ww-num">{t("career.characterBestDps")}</th>
    </tr></thead>
    <tbody>{places.map(place => {
      // A group of instances without a known mode has no filter to open.
      const openable = kind === "stage" ? place.instanceId !== undefined : place.key !== "unknown" && place.key !== ""
      const name = kind === "mode" ? <ModeText mode={place.mode} /> : <StageText stage={place.stage} instanceId={place.instanceId} />
      return <tr key={`${place.key}-${place.instanceId ?? ""}`} className={openable ? "ww-history-row" : undefined} tabIndex={openable ? 0 : undefined} role={openable ? "button" : undefined}
        data-key={place.key} data-instance-id={place.instanceId} aria-label={openable ? t("career.placeOpen") : undefined}
        onClick={openable ? () => onOpen(place) : undefined} onKeyDown={openable ? event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpen(place) } } : undefined}>
        <td>{name}</td>
        {kind === "stage" && <td><ModeText mode={place.mode} /></td>}
        <td className="ww-num">{format.integer(place.encounters)}</td>
        <td className="ww-num">{format.integer(place.counted)}</td>
        <td className="ww-num">{place.counted > 0 ? formats.damage(place.damage, place.damageLowerBound) : "—"}</td>
        <td className="ww-num"><RecordLink record={place.bestDps} field="place-best-dps" onOpen={onRecord} /></td>
      </tr>
    })}</tbody>
  </table></div>
}

/** The dates the page lays out: Go's range, an open start from the first stored encounter, an open end at today. */
function shownRange(career: HistoryCareer): { first: number; last: number } | null {
  const end = career.to || career.today
  const start = career.from || career.totals.first?.start.date || career.days[0]?.date || end
  if (!end) return null
  const last = dayNumber(end)
  const first = Math.min(Math.max(dayNumber(start), last - maxCalendarDays + 1), last)
  return { first, last }
}

export function CareerView({ theme, refreshKey, indicator, state, onState, onOpen }: CareerViewProps) {
  const { t, i18n } = useTranslation(["history", "common"])
  const format = useFormatters()
  const formats = useCareerFormats()
  const dates = useDateFormats()
  const zone = useHostTimeZone()
  const settledKey = useSettledKey(refreshKey)
  const [career, setCareer] = useState<HistoryCareer | null>(null)
  const [failed, setFailed] = useState(false)
  // A retry or a rebuild that opened the history counts as a refresh of its own.
  const [reopened, setReopened] = useState(0)
  const [allRoles, setAllRoles] = useState(false)
  const [allCombos, setAllCombos] = useState(false)
  const [trend, setTrend] = useState<TrendKind>("encounters")
  // The trend of one role instead of the whole selection; 0 is the whole selection.
  const [trendRole, setTrendRole] = useState(0)
  const query = useMemo(() => careerStateQuery(state), [state])

  useEffect(() => {
    let current = true
    host.historyCareer(query, zone).then(view => {
      if (!current) return
      setFailed(false)
      setCareer(view)
    }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [query, zone, settledKey, reopened])

  const set = (change: Partial<CareerState>) => onState({ ...state, ...change })
  const known = useMemo(() => new Map((career?.roles ?? []).map(role => [role.roleId, role])), [career])
  const range = career ? shownRange(career) : null
  const fullDays = career && range ? (career.from && career.to ? dayNumber(career.to) - dayNumber(career.from) + 1 : range.last - range.first + 1) : 0
  const layout = useMemo(() => career && range ? rangeHeatmapLayout(dateOfDay(range.first), dateOfDay(range.last), career.days) : null, [career, range?.first, range?.last])
  const roleTrend = trendRole !== 0 ? career?.roles.find(role => role.roleId === trendRole) : undefined
  // A role has no combat time of its own, so its trend offers the other measures.
  const shownTrend: TrendKind = roleTrend && trend === "time" ? "encounters" : trend
  const trendValues = useMemo(() => {
    const values = new Map<string, { value: number; lowerBound?: boolean }>()
    if (roleTrend) {
      // Go's per-date values of the role: participations, its damage in counted encounters, its best DPS in complete ones.
      for (const day of roleTrend.days) {
        if (shownTrend === "encounters") values.set(day.date, { value: day.encounters })
        else if (shownTrend === "damage" && day.damage > 0) values.set(day.date, { value: day.damage, lowerBound: day.damageLowerBound })
        else if (shownTrend === "bestDps" && day.bestDps) values.set(day.date, { value: day.bestDps })
      }
      return values
    }
    for (const day of career?.days ?? []) {
      // Go's per-date values, as stored: damage and time cover counted encounters, the best DPS complete records only.
      if (shownTrend === "encounters") values.set(day.date, { value: day.encounters })
      else if (shownTrend === "damage" && day.counted > 0) values.set(day.date, { value: day.damage, lowerBound: day.damageLowerBound })
      else if (shownTrend === "time" && day.counted > 0) values.set(day.date, { value: day.seconds })
      else if (shownTrend === "bestDps" && day.bestDps) values.set(day.date, { value: day.bestDps })
    }
    return values
  }, [career, roleTrend, shownTrend])
  const trendFormat = shownTrend === "damage" ? formats.damage : shownTrend === "time" ? formats.hours : format.integer

  const library = (focus?: Parameters<typeof careerLibraryQuery>[2]) => career && onOpen({ kind: "library", query: careerLibraryQuery(career, state, focus) })
  const openRecord = useCallback((record: CareerRecord) => onOpen({ kind: "encounter", encounterId: record.encounterId, date: record.start.date }), [onOpen])
  const openDay = useCallback((date: string) => onOpen({ kind: "day", date }), [onOpen])
  const dayLabel = useCallback((cell: HeatmapDay) => cell.count > 0 ? t("dayTooltip", { date: dates.long(cell.date), n: format.integer(cell.count) }) : t("noEncounterTooltip", { date: dates.long(cell.date) }), [t, dates, format])

  const any = { value: "", label: t("library.any") }
  const presetOptions = useMemo(() => careerPresets.map(preset => ({ value: preset, label: t(`career.range.${preset}`) })), [i18n.language])
  const versionOptions = useMemo(() => [any, ...(career?.versions ?? []).map(version => ({ value: version, label: version }))], [career, i18n.language])
  const modeOptions = useMemo(() => [any, ...(career?.filters.modes ?? []).map(mode => ({ value: mode.key, label: `${mode.kind === "open_world" ? t("modeOpenWorld") : mode.name} (${format.integer(mode.count)})` }))], [career, i18n.language])
  const stageOptions = useMemo(() => [any, ...(career?.filters.stages ?? []).filter(stage => stage.name.name).map(stage => ({ value: String(stage.instanceId), label: `${stage.name.name} (${format.integer(stage.count)})` }))], [career, i18n.language])
  const trendOptions = useMemo(() => trendKinds.filter(kind => !roleTrend || kind !== "time").map(kind => ({ value: kind, label: t(`career.trend.${kind}`) })), [i18n.language, roleTrend])
  const trendRoleOptions = useMemo(() => [{ value: "0", label: t("career.trendAll") }, ...(career?.roles ?? []).map(role => ({ value: String(role.roleId), label: role.name.name ?? t("unknownRole", { ns: "common", id: role.roleId }) }))], [career, i18n.language])

  const totals = career?.totals
  const filtered = state.preset !== "all" || state.gameVersion !== undefined || state.mode !== undefined || state.stage !== undefined
  // A history that cannot be opened is no empty history: it shows the recovery prompt, never the first-run text.
  const unavailable = career?.status.state === "unavailable"
  const empty = career !== null && totals?.encounters === 0 && !unavailable
  const note = statusNote(career?.status)
  const roles = career?.roles ?? []
  const combos = career?.combos ?? []
  const shownRoles = allRoles ? roles : roles.slice(0, collapsedRoles)
  const shownCombos = allCombos ? combos : combos.slice(0, collapsedCombos)
  const rangeText = career && (career.from || career.to || totals?.first) ? t("career.rangeDates", { from: dates.short(career.from || totals?.first?.start.date || career.today), to: dates.short(career.to || career.today) }) : ""

  return <div className="ww-history ww-has-backdrop" data-field="career" data-preset={state.preset}><ThemeBackdrop theme={theme} intensity="art" /><div className="ww-history-layout">
    <Reveal className="ww-history-top">
      <h2 className="ww-history-title">{t("career.title")}</h2>
      <HistoryIndicatorChip indicator={indicator} />
    </Reveal>
    <Reveal index={1}><Card className="ww-history-filters">
      <div className="ww-career-filters" data-field="career-filters">
        <div className="ww-history-filter"><span>{t("career.rangeLabel")}</span>
          <span data-field="career-preset" data-preset={state.preset}><Segmented label={t("career.rangeLabel")} value={state.preset} options={presetOptions} onChange={(preset: CareerPreset) => set({ preset })} /></span>
        </div>
        {state.preset === "custom" && <>
          <label className="ww-history-filter"><span>{t("library.from")}</span><input className="ww-input" type="date" value={state.from ?? ""} max={state.to || undefined} data-field="career-from" onChange={event => set({ from: event.target.value || undefined })} /></label>
          <label className="ww-history-filter"><span>{t("library.to")}</span><input className="ww-input" type="date" value={state.to ?? ""} min={state.from || undefined} data-field="career-to" onChange={event => set({ to: event.target.value || undefined })} /></label>
        </>}
        <div className="ww-history-filter"><span>{t("columns.version")}</span><Select label={t("columns.version")} value={state.gameVersion ?? ""} options={versionOptions} onChange={value => set({ gameVersion: value || undefined })} /></div>
        <div className="ww-history-filter"><span>{t("columns.mode")}</span><Select label={t("columns.mode")} value={state.mode ?? ""} options={modeOptions} onChange={value => set({ mode: value || undefined })} /></div>
        <div className="ww-history-filter"><span>{t("columns.scene")}</span><Select label={t("columns.scene")} value={state.stage !== undefined ? String(state.stage) : ""} options={stageOptions} onChange={value => set({ stage: value ? Number(value) : undefined })} /></div>
      </div>
      <div className="ww-history-filter-foot">
        <span className="ww-history-total" data-field="career-range" data-from={career?.from ?? ""} data-to={career?.to ?? ""}>{rangeText}</span>
        <Button size="sm" variant="ghost" disabled={!filtered} data-field="career-reset" onClick={() => onState({ preset: "all" })}>{t("library.reset")}</Button>
      </div>
      {note && <p className="ww-history-note" data-tone={note.tone} data-field="history-status" data-reason={career?.status.reason}>{t(note.key)}</p>}
      {failed && !unavailable && <p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p>}
    </Card></Reveal>
    {unavailable && <Reveal index={2}><Card className="ww-history-recovery-card"><HistoryRecovery rebuildable={career?.status.rebuildable === true} retry={host.retryHistory} rebuild={host.rebuildHistory} onRecovered={() => { setFailed(false); setReopened(count => count + 1) }} /></Card></Reveal>}

    {empty && <Reveal index={2}><Card className="ww-history-context ww-career-empty" data-field="career-empty" data-filtered={filtered}>
      <Icon name="chart" size={22} />
      <div>
        <h3>{t(filtered ? "career.emptyFiltered" : "career.emptyTitle")}</h3>
        {!filtered && <p className="ww-history-note">{t("career.empty")}</p>}
      </div>
    </Card></Reveal>}

    {career && totals && !empty && <>
      <div className="ww-history-stats" data-field="career-totals">
        <StatCard index={2} hero icon="list" label={t("career.encounters")} field="career-encounters" value={totals.encounters} format={format.integer} note={t("career.encountersNote", { counted: format.integer(totals.counted) })} scope="career" stale={false} numberMode="tween" />
        <StatCard index={3} icon="spark" label={t("career.damage")} field="career-damage" status={totals.damageLowerBound ? "lower_bound" : "available"} value={totals.damage} format={value => formats.damage(value, totals.damageLowerBound)} note={t(totals.damageLowerBound ? "career.damageLowerBound" : "career.damageNote")} scope="career" stale={false} numberMode="tween" />
        <StatCard index={4} icon="clock" label={t("career.time")} field="career-time" value={totals.seconds} format={formats.hours} note={t("career.timeNote")} scope="career" stale={false} numberMode="tween" />
        <StatCard index={5} icon="grid" label={t("career.activeDays")} field="career-active-days" value={totals.activeDays} format={format.integer} note={totals.first && totals.latest ? t("career.activeDaysNote", { first: dates.short(totals.first.start.date), latest: dates.short(totals.latest.start.date) }) : undefined} scope="career" stale={false} numberMode="tween" />
      </div>
      {(totals.withoutNotifiedDamage ?? 0) > 0 && <p className="ww-history-note" data-tone="warn" data-field="older-accounting-note">{t("career.olderAccounting", { count: totals.withoutNotifiedDamage })}</p>}
      <Reveal index={6}><Card className="ww-history-context">
        <div className="ww-career-classes" data-field="career-classes">
          <span className="ww-career-fact-label">{t("career.classes")}</span>
          {recordClasses.filter(value => (totals.classes[value] ?? 0) > 0).map(value => <span key={value} className="ww-career-class" data-class={value}><ClassChip value={value} /><span className="ww-num">{format.integer(totals.classes[value] ?? 0)}</span></span>)}
        </div>
      </Card></Reveal>

      <div className="ww-career-split">
        <Reveal index={7}><Card className="ww-history-heat">
          <div className="ww-history-heat-head">
            <h3>{t("heatmapTitle")}</h3>
            <span className="ww-history-total" data-field="career-heat-total" data-total={layout?.total ?? 0}>{layout ? t("career.heatmapTotal", { n: format.integer(layout.total) }) : ""}</span>
          </div>
          {layout && range && <ActivityHeatmap weeks={layout.weeks} monthStarts={layout.monthStarts} today={career.today} onSelect={openDay}
            monthLabel={index => dates.month(layout.months[index] ?? 0)} weekdayLabel={dates.weekday} dayLabel={dayLabel} less={t("less")} more={t("more")}
            label={t("career.heatmapLabel", { from: dates.long(dateOfDay(range.first)), to: dates.long(dateOfDay(range.last)) })} />}
          <div className="ww-history-filter-foot">
            <p className="ww-history-note">{t("career.dayHint")}</p>
            <Button size="sm" variant="ghost" data-field="career-list" onClick={() => library()}>{t("career.listAll")}</Button>
          </div>
          {fullDays > maxCalendarDays && <p className="ww-history-note" data-field="career-heat-trimmed">{t("career.heatmapTrimmed")}</p>}
          <ZoneNote zone={career.zone} />
        </Card></Reveal>
        <Reveal index={8}><Card className="ww-history-context ww-career-records" data-field="career-records">
          <div className="ww-dash-card-head"><h3>{t("career.records")}</h3><span className="ww-history-note">{t("career.recordsNote")}</span></div>
          {recordKinds.map(kind => <RecordRow key={kind} kind={kind} record={career.records[kind]} known={known} onOpen={openRecord} />)}
        </Card></Reveal>
      </div>

      <Reveal index={9}><Card className="ww-history-context" data-field="career-roles">
        <div className="ww-dash-card-head"><h3>{t("career.characters")}</h3><span className="ww-history-note">{t("career.charactersNote")}</span></div>
        {roles.length === 0 ? <p className="ww-dash-empty-note">{t("career.charactersEmpty")}</p> : <div className="ww-career-role-grid">
          {range && shownRoles.map(role => <RoleCard key={role.roleId} role={role} first={range.first} last={range.last} onList={() => library({ role: role.roleId })} onRecord={openRecord} />)}
        </div>}
        {roles.length > collapsedRoles && <div className="ww-history-more"><span /><Button size="sm" variant="ghost" data-field="career-roles-all" onClick={() => setAllRoles(all => !all)}>{allRoles ? t("career.showLess") : t("career.showAll", { n: format.integer(roles.length) })}</Button></div>}
      </Card></Reveal>

      <div className="ww-career-split">
        <Reveal index={10}><Card className="ww-history-context" data-field="career-trends">
          <div className="ww-dash-card-head"><h3>{t("career.trends")}</h3></div>
          <div className="ww-career-trend-controls">
            <span data-field="career-trend" data-trend={shownTrend}><Segmented label={t("career.trendLabel")} value={shownTrend} options={trendOptions} onChange={(kind: TrendKind) => setTrend(kind)} /></span>
            <span className="ww-career-trend-role" data-field="career-trend-role" data-role-id={roleTrend?.roleId ?? 0}><Select label={t("career.trendRole")} value={String(roleTrend?.roleId ?? 0)} options={trendRoleOptions} onChange={value => setTrendRole(Number(value))} /></span>
          </div>
          {range && <TrendBars first={range.first} last={range.last} values={trendValues} format={trendFormat} label={t(`career.trend.${shownTrend}`)} empty={t("career.trendEmpty")} />}
          <p className="ww-history-note">{t(roleTrend ? `career.trendRoleNote.${shownTrend}` : `career.trendNote.${shownTrend}`)}</p>
          {shownTrend === "bestDps" && <p className="ww-history-note" data-tone="warn" data-field="career-dps-caution">{t("career.dpsCaution")}</p>}
        </Card></Reveal>
        <Reveal index={11}><Card className="ww-history-context" data-field="career-combos">
          <div className="ww-dash-card-head"><h3>{t("career.combos")}</h3></div>
          <p className="ww-history-note">{t("career.combosNote")}</p>
          {combos.length === 0 ? <p className="ww-dash-empty-note">{t("career.combosEmpty")}</p> : <ul className="ww-career-combos">
            {shownCombos.map(combo => <li key={combo.roles.map(role => role.roleId).join("-")} className="ww-career-combo" data-field="career-combo" data-roles={combo.roles.map(role => role.roleId).join(",")}>
              <span className="ww-history-roles">{combo.roles.map(role => <Tooltip key={role.roleId} text={role.name.name ?? t("unknownRole", { ns: "common", id: role.roleId })} trigger="child" placement="top">
                <span data-role-id={role.roleId}><CharacterAvatar roleId={role.roleId} elementId={role.elementId} size={26} /></span>
              </Tooltip>)}</span>
              <span className="ww-num">{t("career.characterEncounters", { n: format.integer(combo.encounters) })}</span>
              <span className="ww-num" data-lower-bound={combo.damageLowerBound === true}>{formats.damage(combo.damage, combo.damageLowerBound)}</span>
              <span className="ww-career-combo-best"><span className="ww-career-fact-label">{t("career.characterBestDps")}</span><RecordLink record={combo.bestDps} field="combo-best-dps" onOpen={openRecord} /></span>
            </li>)}
          </ul>}
          {combos.length > collapsedCombos && <div className="ww-history-more"><span /><Button size="sm" variant="ghost" data-field="career-combos-all" onClick={() => setAllCombos(all => !all)}>{allCombos ? t("career.showLess") : t("career.showAll", { n: format.integer(combos.length) })}</Button></div>}
        </Card></Reveal>
      </div>

      <Reveal index={12}><Card className="ww-history-context" data-field="career-places">
        <div className="ww-dash-card-head"><h3>{t("career.places")}</h3></div>
        <p className="ww-history-note">{t("career.placesNote")}</p>
        <div className="ww-career-split">
          <PlaceTable places={career.modes} kind="mode" onOpen={place => library({ mode: place.key })} onRecord={openRecord} />
          <PlaceTable places={career.stages} kind="stage" onOpen={place => library({ stage: place.instanceId })} onRecord={openRecord} />
        </div>
      </Card></Reveal>

      <Reveal index={13}><Card className="ww-history-context" data-field="career-rules">
        <div className="ww-dash-card-head"><h3>{t("career.rules")}</h3></div>
        <ul className="ww-career-rules">
          {["totals", "records", "dps", "excluded", "stored"].map(rule => <li key={rule}>{t(`career.rule.${rule}`)}</li>)}
        </ul>
      </Card></Reveal>
    </>}
  </div></div>
}
