import { useCallback, useEffect, useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { ActivityHeatmap, Button, Card, Chip, ContentSwitch, Icon, LiveChart, Reveal, ThemeBackdrop, type HeatmapDay, type ThemeId } from "@/design"
import { usableDps } from "@/lib/combat-data"
import { combatRows, detailRow, skillBreakdowns } from "@/lib/combat-view"
import { useFormatters, useRowNames, elementColor } from "@/lib/format"
import { clockText, defaultDate, heatmapLayout, localDateOf, offsetText, recordedClock, yearBounds, type HistoryDay, type HistoryEncounterDetail, type HistoryEncounterSummary, type HistoryName, type HistoryStatus, type HistoryYear } from "@/lib/history"
import { host } from "@/lib/host"
import { dpsText, usableHealing } from "@/lib/snapshot"
import { timelineFromSnapshot } from "@/lib/timeline"
import { useDetailPick } from "@/lib/useDetailPick"
import { chartSeries, RoleBoard, SkillTable, StatCard } from "./DashboardView"

export interface HistoryViewProps {
  theme: ThemeId
  /** Changes whenever the live engine may have finished an encounter, so the history reloads what it shows. */
  refreshKey: string
}

type ChipTone = "neutral" | "good" | "warn" | "critical"

/** How long the refresh signal must stay unchanged before the page queries the history again. */
const historySettleMs = 1500

/** The one status a row or a detail leads with: the most severe of its history state, completeness and integrity. */
function recordState(summary: HistoryEncounterSummary): { key: string; tone: ChipTone } {
  if (summary.state === "open") return { key: "state.open", tone: "good" }
  if (summary.state === "abandoned") return { key: "state.abandoned", tone: "critical" }
  if (!summary.complete) return { key: "incomplete", tone: "warn" }
  if (summary.integrity === "UNRELIABLE") return { key: "integrity.UNRELIABLE", tone: "critical" }
  if (summary.integrity === "DEGRADED") return { key: "integrity.DEGRADED", tone: "warn" }
  return { key: "integrity.INTACT", tone: "neutral" }
}

function useDateFormats() {
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

/** A Master name that came through a versioned link carries the derivation mark; a missing one says so plainly. */
function LinkedName({ name, fallback, id, title }: { name: HistoryName; fallback: string; id?: number; title: string }) {
  if (name.name) return <span className="ww-truncate">{name.name}<span className="ww-history-mark" title={title} aria-label={title}>≈</span></span>
  // An unnamed identity keeps its raw ID beside the plain word, never a plausible name.
  return <span className="ww-history-unknown">{fallback}{id ? <span className="ww-history-mark ww-num" data-field="raw-id">#{id}</span> : null}</span>
}

function SceneCell({ summary }: { summary: HistoryEncounterSummary }) {
  const { t } = useTranslation("history")
  if (!summary.scene.recorded) return <span className="ww-history-unknown">{t("notRecorded")}</span>
  if (!summary.scene.known) return <span className="ww-history-unknown">{t("unidentified")}</span>
  return <LinkedName name={summary.sceneName} fallback={t("unidentified")} id={summary.scene.instanceId} title={t("correlatedNote")} />
}

function EnemyCell({ summary }: { summary: HistoryEncounterSummary }) {
  const { t } = useTranslation("history")
  const enemy = summary.mainEnemy
  if (!summary.enemiesRecorded) return <span className="ww-history-unknown">{t("notRecorded")}</span>
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
  if (!day) return <p className="ww-dash-empty-note">{t("selectDay")}</p>
  if (day.total === 0) return <p className="ww-dash-empty-note" data-field="day-empty">{t("emptyDay")}</p>
  const open = (id: number) => (event: KeyboardEvent) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpen(id) } }
  return <>
    <div className="ww-dash-skills-wrap" style={{ maxHeight: "none" }}><table className="ww-history-table" data-field="day-list" data-date={day.date} data-total={day.total}>
      <thead><tr>
        <th>{t("columns.start")}</th><th>{t("columns.mode")}</th><th>{t("columns.scene")}</th><th>{t("columns.enemy")}</th>
        <th className="ww-num">{t("columns.duration")}</th><th className="ww-num">{t("columns.dps")}</th><th>{t("columns.state")}</th>
      </tr></thead>
      <tbody>{day.encounters.map(summary => <tr key={summary.id} className="ww-history-row" tabIndex={0} role="button" aria-label={t("openEncounter", { time: clockText(recordedClock(summary.startedAt, summary.utcOffsetSeconds)), date: dateLabel })}
        data-encounter-id={summary.id} data-source={summary.source} onClick={() => onOpen(summary.id)} onKeyDown={open(summary.id)}>
        <td className="ww-num ww-history-time" title={offsetText(summary.utcOffsetSeconds)}>{clockText(recordedClock(summary.startedAt, summary.utcOffsetSeconds))}</td>
        {/* No game mode is identified from protocol evidence yet; the column says so instead of guessing from enemies. */}
        <td><span className="ww-history-unknown" data-field="mode">{t("unidentified")}</span></td>
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

function EncounterDetail({ detail, backLabel, onBack }: { detail: HistoryEncounterDetail; backLabel: string; onBack: () => void }) {
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
  const start = clockText(recordedClock(summary.startedAt, summary.utcOffsetSeconds))
  const end = summary.endedAt ? clockText(recordedClock(summary.endedAt, summary.utcOffsetSeconds)) : "—"
  const skills = selected ? breakdowns.get(selected.key) : undefined
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
    <Card className="ww-history-context">
      <dl className="ww-history-facts">
        <div><dt>{t("columns.mode")}</dt><dd className="ww-history-unknown">{t("unidentified")}</dd></div>
        <div><dt>{t("columns.scene")}</dt><dd><SceneCell summary={summary} /></dd></div>
        <div><dt>{t("columns.enemy")}</dt><dd><EnemyCell summary={summary} /></dd></div>
        <div><dt>{t("timeRange")}</dt><dd className="ww-num" title={offsetText(summary.utcOffsetSeconds)}>{start} – {end}</dd></div>
        <div><dt>{t("columns.duration")}</dt><dd className="ww-num">{summary.durationSeconds === undefined ? "—" : format.clock(summary.durationSeconds)}</dd></div>
        <div><dt>{t("recordedBy")}</dt><dd title={session.appCommit}>{t(`source.${session.source}`)} · {session.appVersion}{session.appBuild > 0 ? ` (${session.appBuild})` : ""}</dd></div>
      </dl>
      {!summary.complete && <p className="ww-history-note" data-tone="warn">{t("incompleteNote")}</p>}
      {summary.integrity === "DEGRADED" && <p className="ww-history-note" data-tone="warn">{d("degradedNote")}</p>}
      {summary.integrity === "UNRELIABLE" && <p className="ww-history-note" data-tone="critical">{d("unreliableNote")}</p>}
    </Card>
    {!encounter ? <Card className="ww-history-context"><p className="ww-dash-empty-note" data-field="no-statistics">{t(summary.state === "open" ? "openNote" : "abandonedNote")}</p></Card> : <>
      <div className="ww-history-stats">
        <StatCard index={0} hero icon="bolt" label={c("encounterDps")} field="encounter-dps" status={dps?.status ?? "unavailable"} value={usableDps(dps) ? dps?.value : undefined} format={dpsFormat} note={d("peakNote", { value: peakText })} scope={scope} stale={false} numberMode="tween" />
        <StatCard index={1} icon="spark" label={d("confirmedDamage")} field="damage-dealt" value={encounter.totals.damageDealt} format={format.integer} note={d("damageNote")} scope={scope} stale={false} numberMode="tween" />
        <StatCard index={2} icon="shield" label={d("damageTaken")} field="damage-taken" value={encounter.totals.damageTaken} format={format.integer} note={d("takenNote")} scope={scope} stale={false} numberMode="tween" />
        <StatCard index={3} icon="heal" label={d("healing")} field="healing" status={healing?.status ?? "unavailable"} value={healingShown ? healing.reported : undefined} format={healingFormat} note={healingShown ? d("healingNote", { value: healingFormat(healing.effective) }) : d("healingUnavailable")} scope={scope} stale={false} numberMode="tween" />
      </div>
      <div className="ww-history-main">
        <Card className="ww-dash-board">
          <div className="ww-dash-card-head"><h3>{d("ranking")}</h3></div>
          <div data-field="roles">{rows.length === 0 ? <p className="ww-dash-empty-note">{d("noRoles")}</p> : <RoleBoard rows={rows} selected={selected?.key ?? ""} onSelect={pick} scope={scope} stale={false} numberMode="tween" />}</div>
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
              {skills === undefined ? <p className="ww-dash-empty-note">{d("skillsUnavailable")}</p> : <SkillTable breakdown={skills} color={elementColor(selected?.elementId)} />}
            </ContentSwitch>
          </Card>
        </div>
      </div>
    </>}
    <Card className="ww-history-enemies">
      <div className="ww-dash-card-head"><h3>{t("enemies")}</h3><span className="ww-history-total">{summary.enemiesRecorded ? t("enemyCount", { n: format.integer(detail.enemies.length) }) : ""}</span></div>
      {!summary.enemiesRecorded ? <p className="ww-dash-empty-note">{t("enemiesNotRecorded")}</p> : detail.enemies.length === 0 ? <p className="ww-dash-empty-note">{t("noEnemies")}</p> :
        <div className="ww-dash-skills-wrap"><table className="ww-history-table" data-field="enemies">
          <thead><tr><th>{t("enemyName")}</th><th className="ww-num">{t("enemyDamageTaken")}</th><th className="ww-num">{t("enemyHits")}</th><th className="ww-num">{t("enemyDamageDealt")}</th></tr></thead>
          <tbody>{detail.enemies.map(enemy => <tr key={enemy.ordinal} data-ordinal={enemy.ordinal} data-monster-id={enemy.monsterId ?? 0} data-template-id={enemy.templateId ?? 0}>
            <td><LinkedName name={enemy.name} fallback={t("unidentified")} id={enemy.monsterId ?? enemy.templateId} title={t("correlatedNote")} /></td>
            <td className="ww-num">{format.integer(enemy.damageTaken)}</td>
            <td className="ww-num">{format.integer(enemy.hitsTaken)}</td>
            <td className="ww-num">{format.integer(enemy.damageDealt)}</td>
          </tr>)}</tbody>
        </table></div>}
    </Card>
  </div>
}

/** The reason a history cannot record or be read, for the status line. */
function statusNote(status: HistoryStatus | undefined): { key: string; tone: "warn" | "critical" } | null {
  if (!status || status.state === "ready") return null
  const reason = status.reason ?? "open_failed"
  return { key: `unavailable.${reason}`, tone: status.state === "read_only" ? "warn" : "critical" }
}

/**
 * The Combat History page: a year of encounters as an Activity Heatmap, the chosen day's encounters, and one encounter's
 * stored result. Everything shown is what the Go history returns; this view only lays it out.
 */
export function HistoryView({ theme, refreshKey }: HistoryViewProps) {
  const { t } = useTranslation(["history", "common"])
  const format = useFormatters()
  const dates = useDateFormats()
  const today = localDateOf(new Date())
  const currentYear = Number(today.slice(0, 4))
  const [year, setYear] = useState(currentYear)
  const [yearView, setYearView] = useState<HistoryYear | null>(null)
  const [date, setDate] = useState<string | undefined>(undefined)
  const [day, setDay] = useState<HistoryDay | null>(null)
  const [openId, setOpenId] = useState<number | null>(null)
  const [detail, setDetail] = useState<HistoryEncounterDetail | null>(null)
  const [failed, setFailed] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  // The engine's signal arrives as soon as an encounter finishes; its history is written a moment later by its own writer
  // (200 ms batches), so queries follow the signal once it has been still for historySettleMs.
  const [settledKey, setSettledKey] = useState(refreshKey)
  useEffect(() => {
    const timer = setTimeout(() => setSettledKey(refreshKey), historySettleMs)
    return () => clearTimeout(timer)
  }, [refreshKey])

  useEffect(() => {
    let current = true
    host.historyYear(year).then(view => {
      if (!current) return
      setFailed(false)
      setYearView(view)
      setDate(selected => selected !== undefined && selected.startsWith(`${year}-`) ? selected : defaultDate(view, today))
    }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [year, settledKey])

  useEffect(() => {
    if (date === undefined) { setDay(null); return }
    let current = true
    host.historyDay(date, 0).then(view => { if (current) setDay(view) }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [date, settledKey])

  useEffect(() => {
    if (openId === null) { setDetail(null); return }
    let current = true
    host.historyEncounter(openId).then(view => { if (current) setDetail(view) }).catch(() => { if (current) { setFailed(true); setOpenId(null) } })
    return () => { current = false }
  }, [openId])

  const more = useCallback(() => {
    if (!day || loadingMore) return
    setLoadingMore(true)
    host.historyDay(day.date, day.encounters.length).then(page => {
      setDay(previous => previous && previous.date === page.date ? { ...page, offset: 0, encounters: [...previous.encounters, ...page.encounters] } : previous)
    }).catch(() => setFailed(true)).finally(() => setLoadingMore(false))
  }, [day, loadingMore])

  const layout = useMemo(() => heatmapLayout(year, yearView?.year === year ? yearView.days : []), [year, yearView])
  const bounds = yearBounds(yearView ?? { firstYear: 0, lastYear: 0 }, currentYear)
  const status = yearView?.status
  const note = statusNote(status)
  const select = useCallback((value: string) => { setDate(value); setOpenId(null) }, [])
  const dayLabel = useCallback((cell: HeatmapDay) => cell.count > 0 ? t("dayTooltip", { date: dates.long(cell.date), n: format.integer(cell.count) }) : t("noEncounterTooltip", { date: dates.long(cell.date) }), [t, dates, format])
  const changeYear = (next: number) => { setYear(next); setOpenId(null) }
  const empty = yearView !== null && yearView.firstYear === 0
  return <div className="ww-history ww-has-backdrop" data-field="history" data-year={year}><ThemeBackdrop theme={theme} intensity="art" /><div className="ww-history-layout">
    <Reveal className="ww-history-top">
      <div>
        <div className="ww-caps">{t("subtitle")}</div>
        <h2 className="ww-history-title">{t("title")}</h2>
      </div>
      <div className="ww-history-years" data-field="year-switch">
        <Button size="sm" variant="ghost" aria-label={t("previousYear")} title={t("previousYear")} disabled={year <= bounds.first} onClick={() => changeYear(year - 1)} data-field="year-previous"><span style={{ display: "inline-flex", transform: "rotate(90deg)" }}><Icon name="chevron" size={14} /></span></Button>
        <span className="ww-history-year ww-num" data-field="year">{year}</span>
        <Button size="sm" variant="ghost" aria-label={t("nextYear")} title={t("nextYear")} disabled={year >= bounds.last} onClick={() => changeYear(year + 1)} data-field="year-next"><span style={{ display: "inline-flex", transform: "rotate(-90deg)" }}><Icon name="chevron" size={14} /></span></Button>
      </div>
    </Reveal>
    <Reveal index={1}><Card className="ww-history-heat">
      <div className="ww-history-heat-head">
        <h3>{t("heatmapTitle")}</h3>
        <span className="ww-history-total" data-field="year-total" data-total={layout.total}>{t("yearTotal", { year, n: format.integer(layout.total) })}</span>
      </div>
      <ActivityHeatmap weeks={layout.weeks} monthStarts={layout.monthStarts} selected={date} today={today} onSelect={select}
        monthLabel={dates.month} weekdayLabel={dates.weekday} dayLabel={dayLabel} less={t("less")} more={t("more")} label={t("heatmapLabel", { year })} />
      {note && <p className="ww-history-note" data-tone={note.tone} data-field="history-status" data-reason={status?.reason}>{t(note.key)}</p>}
      {failed && <p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p>}
      {status && status.recoveredEncounters > 0 && <p className="ww-history-note" data-tone="warn">{t("recovered", { n: format.integer(status.recoveredEncounters) })}</p>}
      {empty ? <p className="ww-history-note" data-field="history-empty">{t("emptyHistory")}</p> : layout.total === 0 && yearView !== null && <p className="ww-history-note">{t("emptyYear", { year })}</p>}
      <p className="ww-history-note">{t("localOnly")}</p>
    </Card></Reveal>
    <Reveal index={2}><ContentSwitch contentKey={detail ? `detail-${detail.summary.id}` : `day-${date ?? ""}`}>
      {detail ? <EncounterDetail detail={detail} backLabel={t("back", { date: dates.short(detail.summary.localDate) })} onBack={() => setOpenId(null)} /> :
        <Card className="ww-history-day">
          <div className="ww-dash-card-head"><h3 data-field="day-title">{date ? dates.long(date) : t("noDay")}</h3>{day && day.date === date && <span className="ww-history-total" data-field="day-total">{t("dayCount", { n: format.integer(day.total) })}</span>}</div>
          <DayList day={day && day.date === date ? day : null} dateLabel={date ? dates.long(date) : ""} onOpen={setOpenId} onMore={more} loading={loadingMore} />
        </Card>}
    </ContentSwitch></Reveal>
  </div></div>
}
