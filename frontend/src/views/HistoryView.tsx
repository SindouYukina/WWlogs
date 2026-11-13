import { useCallback, useEffect, useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { ActivityHeatmap, Button, Card, Chip, ContentSwitch, Icon, LiveChart, Reveal, Segmented, ThemeBackdrop, useEntrance, type HeatmapDay, type ThemeId } from "@/design"
import { usableDps } from "@/lib/combat-data"
import { combatRows, detailRow, skillBreakdowns } from "@/lib/combat-view"
import { useFormatters, useRowNames, elementColor } from "@/lib/format"
import { defaultDate, heatmapLayout, indicatorText, offsetText, yearBounds, type HistoryChange, type HistoryDay, type HistoryEncounterDetail, type HistoryEncounterSummary, type HistoryIndicator, type HistoryLocalTime, type HistoryStatus, type HistoryYear, type HistoryZone } from "@/lib/history"
import { host } from "@/lib/host"
import { dpsText, usableHealing } from "@/lib/snapshot"
import { timelineFromSnapshot } from "@/lib/timeline"
import { useDetailPick } from "@/lib/useDetailPick"
import { useHostTimeZone } from "@/lib/useHostTimeZone"
import { chartSeries, RoleBoard, SkillTable, StatCard } from "./DashboardView"
import { AnalysisWorkspace, EnemiesCard, LinkedName, type TimelineMode } from "./HistoryAnalysis"
import { HistoryLibraryView } from "./HistoryLibrary"
import { defaultLibraryQuery, type HistoryIntent, type HistoryLibraryQuery } from "@/lib/history-library"
import { useSettledKey } from "@/lib/useSettledKey"

export interface HistoryViewProps {
  theme: ThemeId
  /** Changes whenever the live engine may have finished an encounter, so the history reloads what it shows. */
  refreshKey: string
  /** The host's Combat History indicator. */
  indicator?: HistoryIndicator
  /** Where another page sent this page; a new seq is a new request. */
  intent?: { seq: number; target: HistoryIntent }
  /** Returns to the page that sent the intent (the Career Overview), keeping that page's state. */
  onReturn?: () => void
}

/** The host's recording indicator as one chip, with the reason behind a problem state. */
export function HistoryIndicatorChip({ indicator }: { indicator: HistoryIndicator | undefined }) {
  const { t } = useTranslation("history")
  const text = indicatorText(indicator)
  return <span className="ww-history-indicator" data-field="history-indicator" data-state={indicator?.state ?? ""} data-reason={indicator?.reason ?? ""} title={text.detail ? t(text.detail) : undefined}>
    <Chip tone={text.tone} dot>{t(text.key)}</Chip>
  </span>
}

/** The notes an unfinished or incomplete record leads with: why it is so, and what its stored values describe. */
function recordNotes(summary: HistoryEncounterSummary): Array<{ key: string; tone: "warn" | "critical" }> {
  const notes: Array<{ key: string; tone: "warn" | "critical" }> = []
  const reason = summary.incompleteReason
  if (summary.state === "abandoned") notes.push({ key: reason ? `incompleteReason.${reason}` : "abandonedNote", tone: "critical" })
  else if (!summary.complete) notes.push({ key: reason ? `incompleteReason.${reason}` : "incompleteNote", tone: "warn" })
  return notes
}

type ChipTone = "neutral" | "good" | "warn" | "critical"

/** The one status a row or a detail leads with: the most severe of its history state, completeness and integrity. */
function recordState(summary: HistoryEncounterSummary): { key: string; tone: ChipTone } {
  if (summary.state === "open") return { key: "state.open", tone: "good" }
  if (summary.state === "abandoned") return { key: "state.abandoned", tone: "critical" }
  if (!summary.complete) return { key: "incomplete", tone: "warn" }
  if (summary.integrity === "UNRELIABLE") return { key: "integrity.UNRELIABLE", tone: "critical" }
  if (summary.integrity === "DEGRADED") return { key: "integrity.DEGRADED", tone: "warn" }
  return { key: "integrity.INTACT", tone: "neutral" }
}

export function useDateFormats() {
  const { i18n } = useTranslation()
  const locale = i18n.language
  return useMemo(() => {
    const utc = { timeZone: "UTC" } as const
    const month = new Intl.DateTimeFormat(locale, { ...utc, month: "short" })
    const weekday = new Intl.DateTimeFormat(locale, { ...utc, weekday: "short" })
    const long = new Intl.DateTimeFormat(locale, { ...utc, year: "numeric", month: "long", day: "numeric", weekday: "short" })
    const short = new Intl.DateTimeFormat(locale, { ...utc, month: "short", day: "numeric" })
    const parse = (date: string) => { const [y = 1970, m = 1, d = 1] = date.split("-").map(Number); return Date.UTC(y, m - 1, d) }
    return {
      month: (index: number) => month.format(Date.UTC(2023, index, 1)),
      // 2023-01-01 was a Sunday.
      weekday: (index: number) => weekday.format(Date.UTC(2023, 0, 1 + index)),
      long: (date: string) => long.format(parse(date)),
      short: (date: string) => short.format(parse(date)),
    }
  }, [locale])
}

/** The tooltip of a clock: the view zone's offset at that instant, and the recorded offset when it was recorded elsewhere. */
function useClockTitle() {
  const { t } = useTranslation("history")
  return useCallback((time: HistoryLocalTime, recorded: HistoryEncounterSummary["recorded"]) => recorded.offsetSeconds === time.offsetSeconds
    ? offsetText(time.offsetSeconds)
    : `${offsetText(time.offsetSeconds)} · ${t("recordedZone", { offset: offsetText(recorded.offsetSeconds) })}`, [t])
}

/** Which zone the dates on the page follow, so a date never reads as the zone where a fight was recorded. */
export function ZoneNote({ zone }: { zone: HistoryZone | undefined }) {
  const { t } = useTranslation("history")
  if (!zone) return null
  const name = zone.id === "Local" ? t("zoneLocal") : zone.id
  return <p className="ww-history-note" data-field="history-zone" data-zone={zone.id} data-fallback={zone.fallback === true}>
    {t("zoneNote", { zone: name, offset: offsetText(zone.offsetSeconds) })}{zone.fallback ? ` ${t("zoneFallback")}` : ""}
  </p>
}

function SceneCell({ summary }: { summary: HistoryEncounterSummary }) {
  const { t } = useTranslation("history")
  if (!summary.scene.known) return <span className="ww-history-unknown">{t("unidentified")}</span>
  return <LinkedName name={summary.sceneName} fallback={t("unidentified")} id={summary.scene.instanceId} title={t("correlatedNote")} />
}

/**
 * The major.minor game version stored with the encounter's protocol session. An encounter without one shows a dash: the
 * running build's version is never put in its place, so a list may hold 3.8 and 3.9 encounters side by side.
 */
function GameVersion({ summary }: { summary: HistoryEncounterSummary }) {
  const { t } = useTranslation("history")
  if (!summary.gameVersion) return <span className="ww-history-unknown" data-field="game-version" data-version="" title={t("versionUnknown")}>—</span>
  return <span className="ww-history-version" data-field="game-version" data-version={summary.gameVersion}>{summary.gameVersion}</span>
}

/** The kind of play Go resolved for the encounter's instance; the open world is labelled here, a category named by Go. */
function ModeCell({ summary }: { summary: HistoryEncounterSummary }) {
  const { t } = useTranslation("history")
  const mode = summary.mode
  if (!summary.scene.known || !mode?.kind) return <span className="ww-history-unknown">{t("unidentified")}</span>
  const name = mode.kind === "open_world" ? t("modeOpenWorld") : mode.name
  return <span data-mode-kind={mode.kind}><LinkedName name={{ name, status: mode.status }} fallback={t("unidentified")} title={t("correlatedNote")} /></span>
}

function EnemyCell({ summary }: { summary: HistoryEncounterSummary }) {
  const { t } = useTranslation("history")
  const enemy = summary.mainEnemy
  if (!enemy) return <span className="ww-history-unknown">—</span>
  return <span className="ww-history-enemy">
    <LinkedName name={summary.mainEnemyName} fallback={t("unidentified")} id={enemy.monsterId ?? enemy.templateId} title={t("correlatedNote")} />
    {enemy.sameType > 1 && <span className="ww-history-mark">{t("sameType", { n: enemy.sameType })}</span>}
  </span>
}

function DpsCell({ summary }: { summary: HistoryEncounterSummary }) {
  const { t, i18n } = useTranslation("history")
  const dps = summary.dps
  if (usableDps(dps)) return <span data-status={dps.status}>{dpsText(dps, i18n.language)}</span>
  if (dps.status === "unreliable") return <span className="ww-history-unknown" data-status={dps.status} title={t("unreliableDps")}>{t("unreliableShort")}</span>
  return <span className="ww-history-unknown" data-status={dps.status}>—</span>
}

function StatusChip({ summary }: { summary: HistoryEncounterSummary }) {
  const { t } = useTranslation("history")
  const state = recordState(summary)
  return <span data-field="record-state" data-state={summary.state} data-complete={summary.complete} data-integrity={summary.integrity ?? ""}><Chip tone={state.tone} dot>{t(state.key)}</Chip></span>
}

function DayList({ day, dateLabel, onOpen, onMore, loading }: { day: HistoryDay | null; dateLabel: string; onOpen: (id: number) => void; onMore: () => void; loading: boolean }) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  const clockTitle = useClockTitle()
  if (!day) return <p className="ww-dash-empty-note">{t("selectDay")}</p>
  if (day.total === 0) return <p className="ww-dash-empty-note" data-field="day-empty">{t("emptyDay")}</p>
  const open = (id: number) => (event: KeyboardEvent) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpen(id) } }
  return <>
    <div className="ww-dash-skills-wrap" style={{ maxHeight: "none" }}><table className="ww-history-table" data-field="day-list" data-date={day.date} data-total={day.total}>
      <thead><tr>
        <th>{t("columns.start")}</th><th className="ww-history-version-column">{t("columns.version")}</th><th>{t("columns.mode")}</th><th>{t("columns.scene")}</th><th>{t("columns.enemy")}</th>
        <th className="ww-num">{t("columns.duration")}</th><th className="ww-num">{t("columns.dps")}</th><th>{t("columns.state")}</th>
      </tr></thead>
      <tbody>{day.encounters.map(summary => <tr key={summary.id} className="ww-history-row" tabIndex={0} role="button" aria-label={t("openEncounter", { time: summary.start.clock, date: dateLabel })}
        data-encounter-id={summary.id} data-source={summary.source} data-local-date={summary.localDate} data-started-at={summary.startedAt} onClick={() => onOpen(summary.id)} onKeyDown={open(summary.id)}>
        <td className="ww-num ww-history-time" data-field="start-clock" title={clockTitle(summary.start, summary.recorded)}>{summary.start.clock}</td>
        <td className="ww-history-version-column"><GameVersion summary={summary} /></td>
        <td data-field="mode"><ModeCell summary={summary} /></td>
        <td data-field="scene"><SceneCell summary={summary} /></td>
        <td data-field="enemy"><EnemyCell summary={summary} /></td>
        <td className="ww-num" data-field="duration">{summary.durationSeconds === undefined ? "—" : format.clock(summary.durationSeconds)}</td>
        <td className="ww-num" data-field="dps"><DpsCell summary={summary} /></td>
        <td><StatusChip summary={summary} /></td>
      </tr>)}</tbody>
    </table></div>
    {day.total > day.encounters.length && <div className="ww-history-more">
      <span>{t("shownOf", { shown: format.integer(day.encounters.length), total: format.integer(day.total) })}</span>
      <Button size="sm" disabled={loading} onClick={onMore} data-field="day-more">{t("loadMore")}</Button>
    </div>}
  </>
}

/** What the record stored besides its totals: every combat result and effect lifecycle change, and never a skill cast. */
function CoverageFacts({ detail }: { detail: HistoryEncounterDetail }) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  const effects = detail.effects
  return <div className="ww-history-coverage" data-field="coverage">
    <dl className="ww-history-facts ww-history-facts-wrap">
      <div><dt>{t("coverage.combat")}</dt><dd>{t("coverage.combatStored")}</dd></div>
      <div><dt>{t("coverage.effects")}</dt><dd data-field="coverage-effects" data-changes={effects.changes}>{t("coverage.effectsStored", { changes: format.integer(effects.changes), instances: format.integer(effects.instances) })}</dd></div>
      <div><dt>{t("coverage.skillCasts")}</dt><dd className="ww-history-unknown">{t("coverage.skillCastsNotStored")}</dd></div>
    </dl>
    {effects.derived + effects.untracked > 0 && <p className="ww-history-note">{t("coverage.effectsDerived", { derived: format.integer(effects.derived), untracked: format.integer(effects.untracked) })}</p>}
    {detail.session.effectsRejected > 0 && <p className="ww-history-note" data-tone="warn">{t("coverage.effectsRejected", { n: format.integer(detail.session.effectsRejected) })}</p>}
  </div>
}

/** Deleting one stored encounter: the first press asks, the second deletes. The host refuses one still recording. */
function DeleteEncounter({ summary, onDeleted }: { summary: HistoryEncounterSummary; onDeleted: (change: HistoryChange) => void }) {
  const { t } = useTranslation("history")
  const [asking, setAsking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState("")
  const recording = summary.state === "open"
  const remove = () => {
    setBusy(true)
    setProblem("")
    host.deleteHistoryEncounter(summary.id).then(change => {
      if (change.refused) { setProblem(`delete.refused.${change.refused}`); return }
      onDeleted(change)
    }).catch(() => setProblem("delete.failed")).finally(() => { setBusy(false); setAsking(false) })
  }
  return <div className="ww-history-delete" data-field="delete-encounter" data-asking={asking}>
    {!asking
      ? <Button size="sm" variant="ghost" disabled={recording || busy} title={recording ? t("delete.recording") : undefined} data-field="delete-encounter-start" onClick={() => setAsking(true)}>{t("delete.action")}</Button>
      : <div className="ww-history-confirm" role="group" aria-label={t("delete.confirmTitle")}>
        <p>{t("delete.confirm")}</p>
        <div className="ww-history-confirm-actions">
          <Button size="sm" variant="danger" disabled={busy} data-field="delete-encounter-confirm" onClick={remove}>{t("delete.confirmAction")}</Button>
          <Button size="sm" disabled={busy} data-field="delete-encounter-cancel" onClick={() => setAsking(false)}>{t("delete.cancel")}</Button>
        </div>
      </div>}
    {problem && <p className="ww-history-note" data-tone="critical" role="alert">{t(problem)}</p>}
  </div>
}

function EncounterDetail({ detail, backLabel, onBack, onDeleted, timelineMode, setTimelineMode }: { detail: HistoryEncounterDetail; backLabel: string; onBack: () => void; onDeleted: (change: HistoryChange) => void; timelineMode: TimelineMode; setTimelineMode: (mode: TimelineMode) => void }) {
  const { t, i18n } = useTranslation(["history", "dashboard", "common"])
  const c = (key: string) => t(key, { ns: "common" })
  const d = (key: string, options?: Record<string, unknown>) => t(key, { ns: "dashboard", ...options })
  const format = useFormatters()
  const dates = useDateFormats()
  const nameOf = useRowNames()
  const { summary, encounter, session } = detail
  const scope = `history:${summary.id}`
  const rows = useMemo(() => encounter ? combatRows(encounter) : [], [encounter])
  const [manual, pick] = useDetailPick(scope)
  const selected = detailRow(rows, scope, manual).row
  // The checkpoint's bars grow in once, when the encounter opens; another role's skills then appear at their value.
  const entering = useEntrance(summary.id, true)
  const breakdowns = useMemo(() => encounter ? skillBreakdowns(encounter) : new Map(), [encounter])
  const timeline = useMemo(() => timelineFromSnapshot(detail.timeline, encounter?.encounterId), [detail.timeline, encounter])
  const series = useMemo(() => chartSeries(timeline, rows, { team: d("teamDps"), unassigned: c("unassigned") }, nameOf), [timeline, rows, nameOf, i18n.language])
  const dps = encounter?.dps
  const lowerBound = dps?.status === "lower_bound"
  const dpsFormat = (value: number) => lowerBound ? `≥ ${format.integer(value)}` : format.integer(value)
  const healing = encounter?.healing
  const healingShown = usableHealing(healing)
  const healingFormat = (value: number) => healing?.status === "lower_bound" ? `≥ ${format.integer(value)}` : format.integer(value)
  const peakText = usableDps(encounter?.peakDps) ? dpsText(encounter?.peakDps, i18n.language) : "—"
  const clockTitle = useClockTitle()
  const start = summary.start.clock
  // An encounter that runs past midnight names the date it ended on.
  const end = summary.end ? `${summary.end.date !== summary.start.date ? `${dates.short(summary.end.date)} ` : ""}${summary.end.clock}` : "—"
  const skills = selected ? breakdowns.get(selected.key) : undefined
  // A finished encounter whose results were stored one by one is explored in the analysis workspace; an unfinished one
  // shows the checkpoint it left, which is not a final result and is never split by time.
  const analysed = summary.state === "finished" && encounter !== null
  return <div className="ww-history-detail" data-field="encounter-detail" data-encounter-id={summary.id} data-state={summary.state}>
    <div className="ww-history-detail-head">
      <div className="ww-history-years">
        <Button size="sm" variant="ghost" onClick={onBack} data-field="detail-back"><span style={{ display: "inline-flex", transform: "rotate(90deg)" }}><Icon name="chevron" size={13} /></span>{backLabel}</Button>
        <h3>{t("detailTitle", { time: start, date: dates.long(summary.localDate) })}</h3>
      </div>
      <div className="ww-history-chips">
        <StatusChip summary={summary} />
        <Chip>{t(`outcome.${summary.outcome}`)}</Chip>
        {summary.source === "replay" && <Chip tone="neutral">{t("source.replay")}</Chip>}
      </div>
    </div>
    <Card className="ww-history-context ww-history-summary">
      <div className="ww-history-summary-row">
        <dl className="ww-history-facts">
          <div><dt>{t("columns.version")}</dt><dd className="ww-num"><GameVersion summary={summary} /></dd></div>
          <div><dt>{t("columns.mode")}</dt><dd data-field="mode"><ModeCell summary={summary} /></dd></div>
          <div><dt>{t("columns.scene")}</dt><dd><SceneCell summary={summary} /></dd></div>
          <div><dt>{t("columns.enemy")}</dt><dd><EnemyCell summary={summary} /></dd></div>
          <div><dt>{t("timeRange")}</dt><dd className="ww-num" data-field="time-range" title={clockTitle(summary.start, summary.recorded)}>{start} – {end}</dd></div>
          <div><dt>{t("columns.duration")}</dt><dd className="ww-num">{summary.durationSeconds === undefined ? "—" : format.clock(summary.durationSeconds)}</dd></div>
          <div><dt>{t("recordedBy")}</dt><dd>{t(`source.${session.source}`)}</dd></div>
        </dl>
        <DeleteEncounter summary={summary} onDeleted={onDeleted} />
      </div>
      {recordNotes(summary).map(note => <p key={note.key} className="ww-history-note" data-tone={note.tone} data-field="record-note">{t(note.key)}</p>)}
      {summary.checkpoint && <p className="ww-history-note" data-tone="warn" data-field="checkpoint-note">{t("checkpointNote", { time: summary.checkpoint.clock })}</p>}
      {summary.integrity === "DEGRADED" && <p className="ww-history-note" data-tone="warn">{d("degradedNote")}</p>}
      {summary.integrity === "UNRELIABLE" && <p className="ww-history-note" data-tone="critical">{d("unreliableNote")}</p>}
      <CoverageFacts detail={detail} />
    </Card>
    {analysed ? <AnalysisWorkspace detail={detail} timelineMode={timelineMode} setTimelineMode={setTimelineMode} /> : !encounter ? <Card className="ww-history-context"><p className="ww-dash-empty-note" data-field="no-statistics">{t(summary.state === "open" ? "openNote" : "abandonedNote")}</p></Card> : <>
      <div className="ww-history-stats">
        <StatCard index={0} hero icon="bolt" label={c("encounterDps")} field="encounter-dps" status={dps?.status ?? "unavailable"} value={usableDps(dps) ? dps?.value : undefined} format={dpsFormat} note={d("peakNote", { value: peakText })} scope={scope} stale={false} numberMode="tween" />
        <StatCard index={1} icon="spark" label={d("confirmedDamage")} field="damage-dealt" value={encounter.totals.damageDealt} format={format.integer} note={d("damageNote")} scope={scope} stale={false} numberMode="tween" />
        <StatCard index={2} icon="shield" label={d("damageTaken")} field="damage-taken" value={encounter.totals.damageTaken} format={format.integer} note={d("takenNote")} scope={scope} stale={false} numberMode="tween" />
        <StatCard index={3} icon="heal" label={d("healing")} field="healing" status={healing?.status ?? "unavailable"} value={healingShown ? healing.reported : undefined} format={healingFormat} note={healingShown ? d("healingNote", { value: healingFormat(healing.effective) }) : d("healingUnavailable")} scope={scope} stale={false} numberMode="tween" />
      </div>
      <div className="ww-history-main">
        <Card className="ww-dash-board">
          <div className="ww-dash-card-head"><h3>{d("ranking")}</h3></div>
          <div data-field="roles">{rows.length === 0 ? <p className="ww-dash-empty-note">{d("noRoles")}</p> : <RoleBoard rows={rows} selected={selected?.key ?? ""} onSelect={pick} scope={scope} stale={false} numberMode="tween" enter={entering} />}</div>
          {encounter.completeness.roleAttributionComplete === false && <p className="ww-dash-warning">{c("attributionIncomplete")}</p>}
        </Card>
        <div className="ww-dash-detail-wrap">
          <Card className="ww-dash-chart-card">
            <div className="ww-dash-card-head"><h3>{d("timeline")}</h3>{!detail.timelineValid && <Chip>{t("timelineMissing")}</Chip>}</div>
            <div data-field="timeline" data-series={series.length}><LiveChart series={series} slots={timeline.slots} height={190} formatValue={format.compact} formatSlot={slot => format.clock(slot * timeline.slotSeconds)} emptyLabel={t("timelineMissing")} ariaLabel={d("timeline")} scope={scope} /></div>
          </Card>
          <Card className="ww-dash-detail">
            <div className="ww-dash-card-head" data-field="detail" data-key={selected?.key ?? ""}><h3>{selected ? nameOf(selected) : "—"}</h3></div>
            <ContentSwitch contentKey={`skills-${selected?.key ?? ""}`}>
              {skills === undefined ? <p className="ww-dash-empty-note">{d("skillsUnavailable")}</p> : <SkillTable breakdown={skills} color={elementColor(selected?.elementId)} enter={entering} />}
            </ContentSwitch>
          </Card>
        </div>
      </div>
    </>}
    {!analysed && <EnemiesCard detail={detail} />}
  </div>
}

/** The reason a history cannot record or be read, for the status line. */
export function statusNote(status: HistoryStatus | undefined): { key: string; tone: "warn" | "critical" } | null {
  if (!status || status.state === "ready") return null
  const reason = status.reason ?? "open_failed"
  return { key: `unavailable.${reason}`, tone: status.state === "read_only" ? "warn" : "critical" }
}

/**
 * The Combat History page: a year of encounters as an Activity Heatmap, the chosen day's encounters, and one encounter's
 * stored result. Everything shown is what the Go history returns; this view only lays it out.
 */
export function HistoryView({ theme, refreshKey, indicator, intent, onReturn }: HistoryViewProps) {
  const { t } = useTranslation(["history", "common"])
  const format = useFormatters()
  const dates = useDateFormats()
  const zone = useHostTimeZone()
  // A page that sends the History page somewhere decides where it opens: a date, an encounter or a filtered list.
  const target = intent?.target
  const dated = target && target.kind !== "library" ? target.date : undefined
  // 0 asks Go for the current year of the zone; a year switch names one.
  const [year, setYear] = useState(() => dated ? Number(dated.slice(0, 4)) : 0)
  const [yearView, setYearView] = useState<HistoryYear | null>(null)
  const [date, setDate] = useState<string | undefined>(dated)
  const [day, setDay] = useState<HistoryDay | null>(null)
  const [openId, setOpenId] = useState<number | null>(() => target?.kind === "encounter" ? target.encounterId : null)
  // The encounter the sending page opened goes back to that page.
  const [returnId, setReturnId] = useState<number | null>(() => target?.kind === "encounter" ? target.encounterId : null)
  // The timeline presentation the user chose last stays chosen for the next encounter they open.
  const [timelineMode, setTimelineMode] = useState<TimelineMode>("icon")
  const [detail, setDetail] = useState<HistoryEncounterDetail | null>(null)
  // The calendar lists one day; the library lists every stored encounter by filters. Each keeps its own state while an
  // encounter is open, so going back returns to the same day or the same filtered list.
  const [mode, setMode] = useState<"calendar" | "list">(() => target?.kind === "library" ? "list" : "calendar")
  const [libraryQuery, setLibraryQuery] = useState<HistoryLibraryQuery>(() => target?.kind === "library" ? target.query : defaultLibraryQuery)
  const [failed, setFailed] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  // A deletion changes what every query returns, so it counts as a refresh of its own.
  const [changes, setChanges] = useState(0)
  const [deleted, setDeleted] = useState(false)
  const settledKey = useSettledKey(refreshKey)
  // A later request while the page is open moves it the same way the first one opened it.
  const [appliedSeq, setAppliedSeq] = useState(intent?.seq)
  useEffect(() => {
    if (!intent || intent.seq === appliedSeq) return
    setAppliedSeq(intent.seq)
    const next = intent.target
    if (next.kind === "library") {
      setMode("list")
      setLibraryQuery(next.query)
      setOpenId(null)
      setReturnId(null)
      return
    }
    setMode("calendar")
    setYear(Number(next.date.slice(0, 4)))
    setDate(next.date)
    setOpenId(next.kind === "encounter" ? next.encounterId : null)
    setReturnId(next.kind === "encounter" ? next.encounterId : null)
  }, [intent, appliedSeq])

  useEffect(() => {
    let current = true
    host.historyYear(year, zone).then(view => {
      if (!current) return
      setFailed(false)
      setYearView(view)
      setDate(selected => selected !== undefined && selected.startsWith(`${view.year}-`) ? selected : defaultDate(view, view.today))
    }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [year, zone, settledKey, changes])

  useEffect(() => {
    if (date === undefined) { setDay(null); return }
    let current = true
    host.historyDay(date, 0, zone).then(view => { if (current) setDay(view) }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [date, zone, settledKey, changes])

  useEffect(() => {
    if (openId === null) { setDetail(null); return }
    let current = true
    host.historyEncounter(openId, zone).then(view => { if (current) setDetail(view) }).catch(() => { if (current) { setFailed(true); setOpenId(null) } })
    return () => { current = false }
  }, [openId, zone])

  const more = useCallback(() => {
    if (!day || loadingMore) return
    setLoadingMore(true)
    host.historyDay(day.date, day.encounters.length, zone).then(page => {
      setDay(previous => previous && previous.date === page.date ? { ...page, offset: 0, encounters: [...previous.encounters, ...page.encounters] } : previous)
    }).catch(() => setFailed(true)).finally(() => setLoadingMore(false))
  }, [day, loadingMore, zone])

  // Until Go answers, the page knows neither the year nor today: both are dates of the zone, and only Go converts instants.
  const shownYear = year !== 0 ? year : yearView?.year ?? 0
  const today = yearView?.today
  const currentYear = today ? Number(today.slice(0, 4)) : shownYear
  const layout = useMemo(() => heatmapLayout(shownYear, yearView?.year === shownYear ? yearView.days : []), [shownYear, yearView])
  const bounds = yearBounds(yearView ?? { firstYear: 0, lastYear: 0 }, currentYear)
  const status = yearView?.status
  const note = statusNote(status)
  const select = useCallback((value: string) => { setDate(value); setOpenId(null) }, [])
  const dayLabel = useCallback((cell: HeatmapDay) => cell.count > 0 ? t("dayTooltip", { date: dates.long(cell.date), n: format.integer(cell.count) }) : t("noEncounterTooltip", { date: dates.long(cell.date) }), [t, dates, format])
  const changeYear = (next: number) => { setYear(next); setOpenId(null) }
  const afterDelete = useCallback(() => { setOpenId(null); setDeleted(true); setChanges(count => count + 1) }, [])
  const empty = yearView !== null && yearView.firstYear === 0
  return <div className="ww-history ww-has-backdrop" data-field="history" data-year={shownYear}><ThemeBackdrop theme={theme} intensity="art" /><div className="ww-history-layout">
    <Reveal className="ww-history-top">
      <div>
        <div className="ww-caps">{t("subtitle")}</div>
        <h2 className="ww-history-title">{t("title")}</h2>
      </div>
      <HistoryIndicatorChip indicator={indicator} />
      {onReturn && <Button size="sm" variant="ghost" data-field="history-return" onClick={onReturn}><span style={{ display: "inline-flex", transform: "rotate(90deg)" }}><Icon name="chevron" size={13} /></span>{t("career.backToCareer")}</Button>}
      <span data-field="history-mode" data-mode={mode}><Segmented label={t("library.viewLabel")} value={mode} onChange={next => { setMode(next); setOpenId(null) }} options={[{ value: "calendar", label: t("library.calendar") }, { value: "list", label: t("library.list") }]} /></span>
      {mode === "calendar" && <div className="ww-history-years" data-field="year-switch">
        <Button size="sm" variant="ghost" aria-label={t("previousYear")} title={t("previousYear")} disabled={shownYear === 0 || shownYear <= bounds.first} onClick={() => changeYear(shownYear - 1)} data-field="year-previous"><span style={{ display: "inline-flex", transform: "rotate(90deg)" }}><Icon name="chevron" size={14} /></span></Button>
        <span className="ww-history-year ww-num" data-field="year">{shownYear === 0 ? "—" : shownYear}</span>
        <Button size="sm" variant="ghost" aria-label={t("nextYear")} title={t("nextYear")} disabled={shownYear === 0 || shownYear >= bounds.last} onClick={() => changeYear(shownYear + 1)} data-field="year-next"><span style={{ display: "inline-flex", transform: "rotate(-90deg)" }}><Icon name="chevron" size={14} /></span></Button>
      </div>}
    </Reveal>
    {mode === "calendar" && <Reveal index={1}><Card className="ww-history-heat">
      <div className="ww-history-heat-head">
        <h3>{t("heatmapTitle")}</h3>
        <span className="ww-history-total" data-field="year-total" data-total={layout.total}>{shownYear === 0 ? "" : t("yearTotal", { year: shownYear, n: format.integer(layout.total) })}</span>
      </div>
      {shownYear !== 0 && <ActivityHeatmap weeks={layout.weeks} monthStarts={layout.monthStarts} selected={date} today={today} onSelect={select}
        monthLabel={dates.month} weekdayLabel={dates.weekday} dayLabel={dayLabel} less={t("less")} more={t("more")} label={t("heatmapLabel", { year: shownYear })} />}
      {note && <p className="ww-history-note" data-tone={note.tone} data-field="history-status" data-reason={status?.reason}>{t(note.key)}</p>}
      {failed && <p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p>}
      {status && status.recoveredEncounters > 0 && <p className="ww-history-note" data-tone="warn">{t("recovered", { n: format.integer(status.recoveredEncounters) })}</p>}
      {empty ? <p className="ww-history-note" data-field="history-empty">{t("emptyHistory")}</p> : layout.total === 0 && yearView !== null && <p className="ww-history-note">{t("emptyYear", { year: shownYear })}</p>}
      {deleted && <p className="ww-history-note" role="status" data-field="history-deleted">{t("delete.done")}</p>}
      <ZoneNote zone={yearView?.zone} />
      {status && status.state !== "unavailable" && <p className="ww-history-note" data-field="history-storage" data-encrypted={status.encrypted}>{t(status.encrypted ? "localEncrypted" : "localOnly")}</p>}
    </Card></Reveal>}
    <Reveal index={2}><ContentSwitch contentKey={detail ? `detail-${detail.summary.id}` : mode === "list" ? "library" : `day-${date ?? ""}`}>
      {detail ? <EncounterDetail detail={detail} backLabel={onReturn && returnId === detail.summary.id ? t("career.backToCareer") : mode === "list" ? t("library.backToList") : t("back", { date: dates.short(detail.summary.localDate) })}
        onBack={onReturn && returnId === detail.summary.id ? onReturn : () => setOpenId(null)} onDeleted={afterDelete} timelineMode={timelineMode} setTimelineMode={setTimelineMode} /> :
        mode === "list" ? <HistoryLibraryView zone={zone} refreshKey={`${settledKey}-${changes}`} query={libraryQuery} onQuery={setLibraryQuery} onOpen={setOpenId} /> :
        <Card className="ww-history-day">
          <div className="ww-dash-card-head"><h3 data-field="day-title">{date ? dates.long(date) : t("noDay")}</h3>{day && day.date === date && <span className="ww-history-total" data-field="day-total">{t("dayCount", { n: format.integer(day.total) })}</span>}</div>
          <DayList day={day && day.date === date ? day : null} dateLabel={date ? dates.long(date) : ""} onOpen={setOpenId} onMore={more} loading={loadingMore} />
        </Card>}
    </ContentSwitch></Reveal>
  </div></div>
}
