import { memo, useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { AnalysisTimeline, Button, Card, Chip, ContentSwitch, DamageBar, EffectLanes, GameAssetImage, Segmented, Select, useEntrance, type LaneRowModel, type TimelineLine } from "@/design"
import { usableDps } from "@/lib/combat-data"
import { combatRows, skillBreakdown, type CombatRowModel } from "@/lib/combat-view"
import { elementColor, useFormatters, useRowNames } from "@/lib/format"
import type { HistoryEncounterDetail, HistoryName } from "@/lib/history"
import { eventPageSize, formatMs, fullQuery, minimumRangeMs, parseSkillRow, querySpan, rangeQuery, skillRowKey, type ActorRef, type AnalysisQuery, type EffectCoverage, type EffectLanes as EffectLanesData, type EffectRow, type EffectScope, type EncounterAnalysis, type EnemyRange, type EventKind, type EventPage, type EventRow, type SkillStats } from "@/lib/history-analysis"
import { host } from "@/lib/host"
import { usableHealing, type Encounter, type Timeline } from "@/lib/snapshot"
import { contributorKey, teamSeriesKey, unassignedSeriesKey } from "@/lib/timeline"
import { RoleBoard, StatCard } from "./DashboardView"

// The analysis workspace of one stored encounter. Every number, interval, coverage and name comes from Go (HistoryAnalysis,
// HistoryEffects, HistoryEvents); this view holds the shared selection (range, contributor, enemy, effect scope, skill) and
// lays the answers out against one time axis.

type Tab = "skills" | "effects" | "events"
type Focus = { kind: "effect"; key: string } | { kind: "skill"; key: string } | null

const derivedNoteKey = { damage_list: "derivedSkillNote", skill_type: "derivedSkillTypeNote", tune_break: "derivedTuneBreakNote", echo: "derivedEchoSkillNote" } as const

/** The DPS lines of the analysis timeline: the team, each contributor in RoleId order then the vehicle, then unassigned damage. */
function timelineLines(timeline: Timeline | undefined, rows: readonly CombatRowModel[], labels: { team: string; unassigned: string }, nameOf: (row: CombatRowModel) => string): TimelineLine[] {
  if (!timeline) return []
  const interval = (index: number) => {
    const point = timeline.points[index]!
    return { fromMs: point.startSeconds * 1000, toMs: (point.startSeconds + point.durationSeconds) * 1000, settled: point.state !== "in_progress" && point.durationSeconds > 0 }
  }
  const lines: TimelineLine[] = [{ key: teamSeriesKey, label: labels.team, color: "var(--ww-ink-100)", aggregate: true,
    points: timeline.points.map((point, index) => { const at = interval(index); return { fromMs: at.fromMs, toMs: at.toMs, value: at.settled ? point.dps : null } }) }]
  const contributors = timeline.contributors.map((contributor, position) => ({ key: contributorKey(contributor), position }))
  const byKey = new Map(rows.map(row => [row.key, row]))
  const ordered = contributors.filter(entry => byKey.has(entry.key)).sort((a, b) => {
    const ra = byKey.get(a.key)!, rb = byKey.get(b.key)!
    return ra.kind === rb.kind ? (ra.roleId ?? 0) - (rb.roleId ?? 0) : ra.kind === "role" ? -1 : 1
  })
  const unassigned = contributors.find(entry => entry.key === unassignedSeriesKey)
  for (const entry of [...ordered, ...(unassigned ? [unassigned] : [])]) {
    const row = byKey.get(entry.key)
    lines.push({
      key: entry.key, label: row ? nameOf(row) : labels.unassigned, color: row ? elementColor(row.elementId) : "var(--ww-ink-400)",
      // A contributor's interval damage over the same interval duration as the team line, as the Dashboard draws it.
      points: timeline.points.map((point, index) => { const at = interval(index); return { fromMs: at.fromMs, toMs: at.toMs, value: at.settled ? (point.contributorDamage[entry.position] ?? 0) / point.durationSeconds : null } }),
    })
  }
  return lines
}

/** A Master name that came through a versioned link carries the derivation mark; a missing one says so plainly. */
export function LinkedName({ name, fallback, id, title }: { name: HistoryName; fallback: string; id?: number; title: string }) {
  if (name.name) return <span className="ww-truncate">{name.name}<span className="ww-history-mark" title={title} aria-label={title}>≈</span></span>
  // An unnamed identity keeps its raw ID beside the plain word, never a plausible name.
  return <span className="ww-history-unknown">{fallback}{id ? <span className="ww-history-mark ww-num" data-field="raw-id">#{id}</span> : null}</span>
}

/**
 * The encounter's enemy instances: two of the same type are two rows. In the workspace the flows are those of the selected
 * range and a row selects the enemy filter; without it they are the stored whole-encounter totals.
 */
export function EnemiesCard({ detail, ranges, selected, onSelect }: { detail: HistoryEncounterDetail; ranges?: readonly EnemyRange[]; selected?: number; onSelect?: (ordinal: number) => void }) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  const summary = detail.summary
  const byOrdinal = new Map((ranges ?? []).map(range => [range.ordinal, range]))
  return <Card className="ww-history-enemies">
    <div className="ww-dash-card-head"><h3>{t("enemies")}</h3><span className="ww-history-total">{summary.enemiesRecorded ? t("enemyCount", { n: format.integer(detail.enemies.length) }) : ""}</span></div>
    {!summary.enemiesRecorded ? <p className="ww-dash-empty-note">{t("enemiesNotRecorded")}</p> : detail.enemies.length === 0 ? <p className="ww-dash-empty-note">{t("noEnemies")}</p> :
      <div className="ww-dash-skills-wrap"><table className="ww-history-table" data-field="enemies">
        <thead><tr><th>{t("enemyName")}</th><th className="ww-num">{t("enemyDamageTaken")}</th><th className="ww-num">{t("enemyHits")}</th><th className="ww-num">{t("enemyDamageDealt")}</th></tr></thead>
        <tbody>{detail.enemies.map(enemy => {
          const range = byOrdinal.get(enemy.ordinal)
          const values = ranges ? range : enemy
          const selectable = onSelect !== undefined && range?.mapped === true
          return <tr key={enemy.ordinal} data-ordinal={enemy.ordinal} data-monster-id={enemy.monsterId ?? 0} data-template-id={enemy.templateId ?? 0} data-selected={selected === enemy.ordinal}
            className={selectable ? "ww-history-row" : undefined} tabIndex={selectable ? 0 : undefined} title={ranges && range?.mapped === false ? t("analysis.enemyUnmapped") : undefined}
            onClick={selectable ? () => onSelect(enemy.ordinal) : undefined} onKeyDown={selectable ? event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onSelect(enemy.ordinal) } } : undefined}>
            <td><LinkedName name={enemy.name} fallback={t("unidentified")} id={enemy.monsterId ?? enemy.templateId} title={t("correlatedNote")} /><span className="ww-history-mark ww-num">#{enemy.ordinal + 1}</span></td>
            <td className="ww-num">{values ? format.integer(values.damageTaken) : "—"}</td>
            <td className="ww-num">{values ? format.integer(values.hitsTaken) : "—"}</td>
            <td className="ww-num">{values ? format.integer(values.damageDealt) : "—"}</td>
          </tr>
        })}</tbody>
      </table></div>}
  </Card>
}

/** An endpoint's name: a role's verified name, an enemy's or other entity's Master name, or what kind of endpoint it is. */
function useActorName() {
  const { t } = useTranslation("history")
  return useCallback((actor: ActorRef | undefined, enemyLabel: (ordinal: number) => string): string => {
    if (!actor) return "—"
    if (actor.kind === "enemy" && actor.enemy !== undefined) return enemyLabel(actor.enemy)
    const name = actor.name.name
    switch (actor.kind) {
      case "role": return name ?? t("analysis.actor.role", { id: actor.roleId })
      case "summon": return t("analysis.actor.summon", { name: name ?? t("analysis.actor.role", { id: actor.roleId }) })
      case "vehicle": return t("analysis.actor.vehicle")
      case "team": return t("analysis.actor.team")
      case "team_unassigned": return t("analysis.actor.teamUnassigned")
      case "other": return name ? `${name} ≈` : t("analysis.actor.other")
      default: return t("analysis.actor.unknown")
    }
  }, [t])
}

function coverageText(coverage: EffectCoverage | undefined, percent: (value: number) => string): string {
  if (!coverage) return "—"
  if (coverage.exact && coverage.ratio !== undefined) return percent(coverage.ratio)
  return coverage.upper !== undefined ? `${percent(coverage.lower)} – ${percent(coverage.upper)}` : `≥ ${percent(coverage.lower)}`
}

/** The effect's identity as Go named it: a glossary term with its derivation mark, or the plain unknown word with its ID. */
function EffectName({ row }: { row: EffectRow }) {
  const { t } = useTranslation("history")
  const icon = row.hudIcon && row.effectId !== undefined
  return <span className="ww-analysis-effect" data-polarity={row.polarity}>
    {icon && <GameAssetImage kind="buff" id={row.effectId!} />}
    {row.name.name
      ? <span className="ww-truncate">{row.name.name}<span className="ww-history-mark" title={t("correlatedNote")}>≈</span></span>
      : <span className="ww-truncate ww-history-unknown">{t("analysis.effects.unknownEffect")}</span>}
    {row.effectId !== undefined && <span className="ww-history-mark ww-num" data-field="effect-id">#{row.effectId}</span>}
  </span>
}

function Sparkline({ bins, range, durationMs }: { bins: number[]; range: { fromMs: number; toMs: number } | null; durationMs: number }) {
  const max = Math.max(1, ...bins)
  const width = 96, height = 18
  const step = width / Math.max(1, bins.length)
  return <svg className="ww-analysis-spark" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" aria-hidden="true">
    {range && durationMs > 0 && <rect className="ww-analysis-spark-range" x={(range.fromMs / durationMs) * width} width={((range.toMs - range.fromMs) / durationMs) * width} y={0} height={height} />}
    {bins.map((value, index) => value > 0 && <rect key={index} x={index * step + 0.5} width={Math.max(1, step - 1)} y={height - (value / max) * height} height={(value / max) * height} />)}
  </svg>
}

export interface AnalysisWorkspaceProps {
  detail: HistoryEncounterDetail
}

/**
 * The History analysis workspace: a toolbar of shared filters, the selection's numbers, the DPS timeline on which a range is
 * dragged, and the Timeline, Skills, Effects and Events tabs, each with a detail panel for what is selected in it.
 */
export function AnalysisWorkspace({ detail }: AnalysisWorkspaceProps) {
  const { t, i18n } = useTranslation(["history", "dashboard", "common"])
  const a = (key: string, options?: Record<string, unknown>) => t(`analysis.${key}`, { ns: "history", ...options })
  const format = useFormatters()
  const nameOf = useRowNames()
  const actorName = useActorName()
  const id = detail.summary.id
  const [query, setQuery] = useState<AnalysisQuery>(fullQuery)
  const [analysis, setAnalysis] = useState<EncounterAnalysis | null>(null)
  const [lanes, setLanes] = useState<EffectLanesData | null>(null)
  const [failed, setFailed] = useState(false)
  const [tab, setTab] = useState<Tab>("skills")
  const [focus, setFocus] = useState<Focus>(null)
  // A new encounter starts from the whole fight with nothing selected.
  const [scope, setScope] = useState(id)
  if (scope !== id) {
    setScope(id)
    setQuery(fullQuery)
    setFocus(null)
    setAnalysis(null)
    setLanes(null)
  }
  useEffect(() => {
    let current = true
    host.historyAnalysis(id, query).then(answer => { if (current) { setAnalysis(answer); setFailed(false) } }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [id, query])
  const lanesQuery = useMemo(() => ({ ...fullQuery, contributor: query.contributor, enemy: query.enemy, effects: query.effects }), [query.contributor, query.enemy, query.effects])
  useEffect(() => {
    let current = true
    host.historyEffects(id, lanesQuery).then(answer => { if (current) setLanes(answer) }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [id, lanesQuery])

  const encounter: Encounter | null = analysis?.encounter ?? null
  // The damage bars of an encounter grow in once, when its analysis first arrives; later tab switches and filter changes
  // move them from where they are.
  const entering = useEntrance(id, analysis?.state === "ready")
  const rows = useMemo(() => encounter ? combatRows(encounter) : [], [encounter])
  const rowByKey = useMemo(() => new Map(rows.map(row => [row.key, row])), [rows])
  const durationMs = analysis?.range.durationMs ?? 0
  // The range the numbers describe: the one Go answered for.
  const range = useMemo(() => analysis && !analysis.range.full ? { fromMs: analysis.range.fromMs, toMs: analysis.range.toMs } : null, [analysis?.range.full, analysis?.range.fromMs, analysis?.range.toMs])
  // The range the user selected, drawn at once: a release never shows the previous range for a frame while Go answers.
  const selected = useMemo(() => querySpan(query, durationMs), [query.fromMs, query.toMs, durationMs])
  const lines = useMemo(() => timelineLines(analysis?.timeline, rows, { team: t("teamDps", { ns: "dashboard" }), unassigned: t("unassigned", { ns: "common" }) }, nameOf), [analysis?.timeline, rows, nameOf, t])
  const enemyLabel = useCallback((ordinal: number) => {
    const enemy = detail.enemies.find(candidate => candidate.ordinal === ordinal)
    const name = enemy?.name.name ?? t("unidentified")
    return a("enemyInstance", { name, n: ordinal + 1 })
  }, [detail.enemies, t, i18n.language])
  const contributorOptions = useMemo(() => [{ value: "", label: a("team") }, ...rows.map(row => ({ value: row.key, label: nameOf(row) }))], [rows, nameOf, i18n.language])
  const enemyOptions = useMemo(() => [{ value: "-1", label: a("allEnemies") }, ...(analysis?.enemies ?? []).filter(enemy => enemy.mapped).map(enemy => ({ value: String(enemy.ordinal), label: enemyLabel(enemy.ordinal) }))], [analysis?.enemies, enemyLabel, i18n.language])
  const setContributor = useCallback((key: string | null) => setQuery(q => ({ ...q, contributor: key && (key.startsWith("role-") || key.startsWith("vehicle-")) ? key : "", skillRow: "" })), [])
  const onRange = useCallback((from: number, to: number) => setQuery(q => rangeQuery(q, from, to, durationMs)), [durationMs])
  const resetRange = useCallback(() => setQuery(q => ({ ...q, fromMs: 0, toMs: 0 })), [])
  const rangeLabel = range ? a("rangeValue", { from: formatMs(range.fromMs), to: formatMs(range.toMs) }) : a("fullRange")
  const selectedLabel = selected ? a("rangeValue", { from: formatMs(selected.fromMs), to: formatMs(selected.toMs) }) : a("fullRange")
  const selectedRow = query.contributor ? rowByKey.get(query.contributor) : undefined
  const whoLabel = selectedRow ? nameOf(selectedRow) : a("team")

  if (failed && !analysis) return <Card className="ww-history-context"><p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p></Card>
  if (!analysis) return <Card className="ww-history-context"><p className="ww-dash-empty-note">{a("loading")}</p></Card>
  if (analysis.state !== "ready") return <Card className="ww-history-context"><p className="ww-dash-empty-note" data-field="analysis-state" data-state={analysis.state}>{a(`state.${analysis.state}`)}</p></Card>

  const selection = analysis.selection
  const dps = selection.dps
  const lowerBound = dps.status === "lower_bound"
  const dpsFormat = (value: number) => lowerBound ? `≥ ${format.integer(value)}` : format.integer(value)
  const healing = selection.healing
  const healingReason = selection.healing.reason
  const healingShown = usableHealing(healing)
  const healingFormat = (value: number) => healing.status === "lower_bound" ? `≥ ${format.integer(value)}` : format.integer(value)
  const cardScope = `analysis:${id}`
  const cardNote = `${rangeLabel} · ${whoLabel}`
  const hitsRow = query.skillRow ? parseSkillRow(query.skillRow) : null
  return <div className="ww-analysis" data-field="analysis" data-encounter-id={id} data-range-from={analysis.range.fromMs} data-range-to={analysis.range.toMs} data-full={analysis.range.full}
    data-contributor={analysis.query.contributor} data-enemy={analysis.query.enemy} data-scope={analysis.query.effects}>
    <Card className="ww-analysis-toolbar">
      <div className="ww-analysis-toolbar-row" role="toolbar" aria-label={a("toolbar")}>
        <label className="ww-analysis-filter"><span>{a("contributor")}</span><span data-field="filter-contributor"><Select value={query.contributor} options={contributorOptions} onChange={setContributor} label={a("contributor")} /></span></label>
        <label className="ww-analysis-filter"><span>{a("enemy")}</span><span data-field="filter-enemy"><Select value={String(query.enemy)} options={enemyOptions} onChange={value => setQuery(q => ({ ...q, enemy: Number(value), skillRow: "" }))} label={a("enemy")} /></span></label>
        <div className="ww-analysis-filter ww-analysis-range"><span>{a("range")}</span><strong className="ww-num" data-field="range-label">{selectedLabel}</strong>
          <Button size="sm" variant="ghost" disabled={!selected} onClick={resetRange} data-field="range-reset">{a("resetRange")}</Button></div>
        <div className="ww-analysis-filter"><span>{a("effectScope")}</span><Segmented value={query.effects} onChange={(value: EffectScope) => setQuery(q => ({ ...q, effects: value }))} label={a("effectScope")}
          options={[{ value: "hud", label: a("scope.hud") }, { value: "all", label: a("scope.all") }]} /></div>
      </div>
      {!analysis.consistent && <p className="ww-history-note" data-tone="warn" data-field="analysis-inconsistent">{a("inconsistent")}</p>}
      {failed && <p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p>}
    </Card>
    <div className="ww-history-stats" data-field="analysis-cards">
      <StatCard index={0} hero icon="bolt" label={t("encounterDps", { ns: "common" })} field="analysis-dps" status={dps.status} value={usableDps(dps) ? dps.value : undefined} format={dpsFormat} note={cardNote} scope={cardScope} stale={false} numberMode="ease" />
      <StatCard index={1} icon="spark" label={t("confirmedDamage", { ns: "dashboard" })} field="analysis-damage" value={selection.damage} format={format.integer} note={a("hitsNote", { n: format.integer(selection.hits) })} scope={cardScope} stale={false} numberMode="ease" />
      <StatCard index={2} icon="shield" label={t("damageTaken", { ns: "dashboard" })} field="analysis-taken" value={selection.taken} format={format.integer} note={a("takenHitsNote", { n: format.integer(selection.takenHits) })} scope={cardScope} stale={false} numberMode="ease" />
      <StatCard index={3} icon="heal" label={t("healing", { ns: "dashboard" })} field="analysis-healing" status={healing.status} value={healingShown ? healing.reported : undefined} format={healingFormat}
        note={healingShown ? t("healingNote", { ns: "dashboard", value: healingFormat(healing.effective) }) : healingReason === "enemy_filter" ? a("healingNoEnemy") : t("healingUnavailable", { ns: "dashboard" })} scope={cardScope} stale={false} numberMode="ease" />
    </div>
    <Card className="ww-analysis-timeline">
      <div className="ww-dash-card-head"><h3>{a("timelineTitle")}</h3>
        <span className="ww-history-total">{hitsRow ? a("hitsShown", { n: format.integer(analysis.hits.length) }) : a("dragHint")}</span></div>
      <div data-field="analysis-timeline" data-series={lines.length} data-valid={analysis.timelineValid}>
        <AnalysisTimeline durationMs={durationMs} lines={lines} focused={query.contributor || null} onSelectLine={setContributor} range={selected} onRange={onRange} onReset={resetRange}
          gaps={analysis.gaps} marks={analysis.hits} formatValue={format.compact} formatTime={formatMs} minimumRangeMs={minimumRangeMs} height={200}
          labels={{ aria: a("timelineTitle"), empty: t("timelineMissing"), gap: a("gap"), mark: a("hitsShown", { n: analysis.hits.length }), legend: a("timelineTitle"), drag: a("dragHint") }} />
      </div>
      {analysis.gaps.length > 0 && <p className="ww-history-note" data-tone="warn">{a("gapNote", { n: analysis.gaps.length })}</p>}
      <div className="ww-analysis-lanes-head"><h4>{a("lanesTitle")}</h4></div>
      <TimelineLanes lanes={lanes} analysis={analysis} range={selected} focus={focus} setFocus={setFocus} actorName={actorName} enemyLabel={enemyLabel} />
    </Card>
    <Card className="ww-analysis-tabs">
      <div className="ww-analysis-tabs-head">
        <Segmented variant="underline" value={tab} onChange={(value: Tab) => setTab(value)} label={a("tabsLabel")}
          options={[{ value: "skills", label: a("tab.skills") }, { value: "effects", label: a("tab.effects") }, { value: "events", label: a("tab.events") }]} />
      </div>
      <ContentSwitch contentKey={tab}>
        {tab === "skills" && <SkillsTab analysis={analysis} encounter={encounter} rows={rows} query={query} setQuery={setQuery} setContributor={setContributor} range={range} entering={entering} />}
        {tab === "effects" && <EffectsTab lanes={lanes} analysis={analysis} focus={focus} setFocus={setFocus} actorName={actorName} enemyLabel={enemyLabel} range={range} />}
        {tab === "events" && <EventsTab id={id} query={query} analysis={analysis} lanes={lanes} actorName={actorName} enemyLabel={enemyLabel} rowByKey={rowByKey} />}
      </ContentSwitch>
    </Card>
    <EnemiesCard detail={detail} ranges={analysis.enemies} selected={query.enemy} onSelect={ordinal => setQuery(q => ({ ...q, enemy: q.enemy === ordinal ? -1 : ordinal, skillRow: "" }))} />
  </div>
}

interface EffectTabProps {
  lanes: EffectLanesData | null
  analysis: EncounterAnalysis
  range: { fromMs: number; toMs: number } | null
  focus: Focus
  setFocus: (focus: Focus) => void
  actorName: (actor: ActorRef | undefined, enemyLabel: (ordinal: number) => string) => string
  enemyLabel: (ordinal: number) => string
}

function useLaneLabels() {
  const { t } = useTranslation("history")
  return useMemo(() => ({
    markers: Object.fromEntries(["applied", "start_unconfirmed", "removed", "holder_removed", "superseded", "untracked", "instant", "continues"].map(kind => [kind, t(`analysis.marker.${kind}`)])),
    evidence: Object.fromEntries(["confirmed", "stated", "unknown"].map(kind => [kind, t(`analysis.evidence.${kind}`)])),
  }), [t])
}

function EffectNotes({ lanes }: { lanes: EffectLanesData }) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  return <>
    {lanes.hidden > 0 && <p className="ww-history-note" data-field="effects-hidden">{t("analysis.effects.hidden", { n: format.integer(lanes.hidden) })}</p>}
    {lanes.stale > 0 && <p className="ww-history-note" data-field="effects-stale">{t("analysis.effects.stale", { n: format.integer(lanes.stale) })}</p>}
    {(lanes.more ?? 0) > 0 && <p className="ww-history-note" data-tone="warn">{t("analysis.effects.more", { n: format.integer(lanes.more ?? 0) })}</p>}
    {lanes.rejected > 0 && <p className="ww-history-note" data-tone="warn">{t("coverage.effectsRejected", { n: format.integer(lanes.rejected) })}</p>}
    {lanes.truncated && <p className="ww-history-note" data-tone="warn">{t("analysis.effects.truncated")}</p>}
  </>
}

function EvidenceLegend() {
  const { t } = useTranslation("history")
  return <div className="ww-analysis-legend" aria-hidden="true">
    {(["confirmed", "stated", "unknown"] as const).map(kind => <span key={kind}><i className="ww-lane-swatch" data-evidence={kind} />{t(`analysis.evidence.${kind}`)}</span>)}
    <span><i className="ww-lane-swatch" data-evidence="gap" />{t("analysis.gap")}</span>
    <span><i className="ww-lane-mark-swatch" data-kind="start_unconfirmed" />{t("analysis.marker.start_unconfirmed")}</span>
  </div>
}

function laneRows(rows: readonly EffectRow[], actorName: EffectTabProps["actorName"], enemyLabel: (ordinal: number) => string): LaneRowModel[] {
  return rows.map(row => ({ key: row.key, label: <EffectName row={row} />, sub: actorName(row.holder, enemyLabel), polarity: row.polarity, segments: row.segments, markers: row.markers }))
}

/**
 * The effect lanes of the unified timeline: the effects of the scope on the same axis as the DPS lines right above them, with
 * the selected one's details below.
 */
const TimelineLanes = memo(function TimelineLanes({ lanes, analysis, range, focus, setFocus, actorName, enemyLabel }: EffectTabProps) {
  const { t } = useTranslation("history")
  const labels = useLaneLabels()
  const rows = useMemo(() => lanes ? laneRows(lanes.rows, actorName, enemyLabel) : [], [lanes, actorName, enemyLabel])
  const selected = focus?.kind === "effect" ? focus.key : null
  if (!lanes) return <p className="ww-dash-empty-note">{t("analysis.loading")}</p>
  if (!lanes.available) return <p className="ww-dash-empty-note" data-field="effects-unavailable">{t("analysis.effects.notStored")}</p>
  // The lanes span the same width as the DPS timeline above them, so a moment lines up; the details follow below.
  return <div className="ww-analysis-stack">
    <EvidenceLegend />
    {rows.length === 0 ? <p className="ww-dash-empty-note" data-field="lanes-empty">{t("analysis.effects.none")}</p>
      : <EffectLanes rows={rows} durationMs={lanes.durationMs} range={range} gaps={lanes.gaps} selected={selected} onSelect={key => setFocus(key ? { kind: "effect", key } : null)} visibleRows={8} labels={{ aria: t("analysis.lanesTitle"), ...labels }} />}
    <EffectNotes lanes={lanes} />
    {selected && <EffectDetail lanes={lanes} analysis={analysis} rowKey={selected} actorName={actorName} enemyLabel={enemyLabel} />}
  </div>
})

function EffectDetail({ lanes, analysis, rowKey, actorName, enemyLabel }: { lanes: EffectLanesData; analysis: EncounterAnalysis; rowKey: string | null; actorName: EffectTabProps["actorName"]; enemyLabel: (ordinal: number) => string }) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  const row = rowKey ? lanes.rows.find(candidate => candidate.key === rowKey) : undefined
  const coverage = rowKey ? analysis.effects.rows.find(candidate => candidate.key === rowKey) : undefined
  return <aside className="ww-analysis-detail" data-field="effect-detail" data-key={row?.key ?? ""}>
    {!row ? <p className="ww-dash-empty-note">{t("analysis.effects.selectHint")}</p> : <>
      <h4><EffectName row={row} /></h4>
      <div className="ww-history-chips"><Chip tone={row.polarity === "buff" ? "good" : row.polarity === "debuff" ? "critical" : "neutral"}>{t(`analysis.polarity.${row.polarity}`)}</Chip>{row.startUnconfirmed && <Chip tone="warn">{t("analysis.marker.start_unconfirmed")}</Chip>}</div>
      <dl className="ww-analysis-facts">
        <div><dt>{t("analysis.effects.holder")}</dt><dd>{actorName(row.holder, enemyLabel)}</dd></div>
        <div><dt>{t("analysis.effects.source")}</dt><dd>{row.sources.length === 0 ? t("analysis.actor.unknown") : row.sources.map(source => actorName(source, enemyLabel)).join("、")}</dd></div>
        <div><dt>{t("analysis.effects.coverage")}</dt><dd className="ww-num" data-field="detail-coverage" data-exact={coverage?.exact === true}>{coverageText(coverage, format.percent)}</dd></div>
        {coverage && <>
          <div><dt>{t("analysis.evidenceShort.confirmed")}</dt><dd className="ww-num" title={t("analysis.evidence.confirmed")}>{formatMs(coverage.confirmedMs)}</dd></div>
          <div><dt>{t("analysis.evidenceShort.stated")}</dt><dd className="ww-num" title={t("analysis.evidence.stated")}>{formatMs(coverage.partialMs)}</dd></div>
          <div><dt>{t("analysis.evidenceShort.unknown")}</dt><dd className="ww-num" title={t("analysis.evidence.unknown")}>{formatMs(coverage.unknownMs)}</dd></div>
          {coverage.gapMs > 0 && <div><dt>{t("analysis.gap")}</dt><dd className="ww-num">{formatMs(coverage.gapMs)}</dd></div>}
          <div><dt>{t("analysis.effects.maxStack")}</dt><dd className="ww-num">{coverage.maxStack ?? "—"}</dd></div>
          <div><dt>{t("analysis.effects.applies")}</dt><dd className="ww-num">{format.integer(coverage.applies)}</dd></div>
        </>}
      </dl>
      <p className="ww-history-note">{coverage?.exact ? t("analysis.effects.exactNote") : t("analysis.effects.boundsNote")}</p>
      <ol className="ww-analysis-intervals" data-field="detail-intervals">{row.segments.slice(0, 40).map((segment, index) => <li key={index} data-evidence={segment.evidence}>
        <span className="ww-num">{formatMs(segment.fromMs)} – {formatMs(segment.toMs)}</span><span>{t(`analysis.evidence.${segment.evidence}`)}</span>{segment.stack !== undefined && <span className="ww-num">×{segment.stack}</span>}
      </li>)}</ol>
      {row.segments.length > 40 && <p className="ww-history-note">{t("analysis.effects.moreIntervals", { n: row.segments.length - 40 })}</p>}
    </>}
  </aside>
}

/** The Effects tab: every effect of the scope with its coverage of the selected range. */
const EffectsTab = memo(function EffectsTab({ lanes, analysis, focus, setFocus, actorName, enemyLabel }: EffectTabProps) {
  const { t } = useTranslation("history")
  const format = useFormatters()
  const selected = focus?.kind === "effect" ? focus.key : null
  const coverage = useMemo(() => new Map(analysis.effects.rows.map(row => [row.key, row])), [analysis.effects.rows])
  if (!lanes) return <p className="ww-dash-empty-note">{t("analysis.loading")}</p>
  if (!lanes.available) return <p className="ww-dash-empty-note" data-field="effects-unavailable">{t("analysis.effects.notStored")}</p>
  return <div className="ww-analysis-split">
    <div className="ww-analysis-main-col">
      {lanes.rows.length === 0 ? <p className="ww-dash-empty-note">{t("analysis.effects.none")}</p> :
        <div className="ww-dash-skills-wrap ww-analysis-scroll"><table className="ww-history-table ww-analysis-table" data-field="effects-table">
          <thead><tr><th>{t("analysis.effects.effect")}</th><th>{t("analysis.effects.holder")}</th><th className="ww-num">{t("analysis.effects.coverage")}</th>
            <th className="ww-num" title={t("analysis.evidence.confirmed")}>{t("analysis.evidenceShort.confirmed")}</th><th className="ww-num">{t("analysis.effects.maxStack")}</th><th className="ww-num">{t("analysis.effects.applies")}</th></tr></thead>
          <tbody>{lanes.rows.map(row => {
            const rowCoverage = coverage.get(row.key)
            return <tr key={row.key} className="ww-history-row" tabIndex={0} data-key={row.key} data-selected={row.key === selected} data-exact={rowCoverage?.exact === true} onClick={() => setFocus(row.key === selected ? null : { kind: "effect", key: row.key })}
              onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setFocus(row.key === selected ? null : { kind: "effect", key: row.key }) } }}>
              <td><EffectName row={row} /></td>
              <td className="ww-truncate">{actorName(row.holder, enemyLabel)}</td>
              <td className="ww-num" data-field="coverage" title={rowCoverage && !rowCoverage.exact ? t("analysis.effects.boundsNote") : undefined}>{coverageText(rowCoverage, format.percent)}{rowCoverage?.startUnconfirmed && <span className="ww-history-mark" title={t("analysis.marker.start_unconfirmed")}>?</span>}</td>
              <td className="ww-num">{rowCoverage ? formatMs(rowCoverage.confirmedMs) : "—"}</td>
              <td className="ww-num">{rowCoverage?.maxStack ?? "—"}</td>
              <td className="ww-num">{rowCoverage ? format.integer(rowCoverage.applies) : "—"}</td>
            </tr>
          })}</tbody>
        </table></div>}
      <EffectNotes lanes={lanes} />
    </div>
    <EffectDetail lanes={lanes} analysis={analysis} rowKey={selected} actorName={actorName} enemyLabel={enemyLabel} />
  </div>
})

interface SkillsTabProps {
  analysis: EncounterAnalysis
  encounter: Encounter | null
  rows: readonly CombatRowModel[]
  query: AnalysisQuery
  setQuery: (update: (query: AnalysisQuery) => AnalysisQuery) => void
  setContributor: (key: string | null) => void
  range: { fromMs: number; toMs: number } | null
  /** The encounter just opened: the bars play their entrance. */
  entering: boolean
}

/** The Skills tab: the range's ranking, the selected contributor's skill rows with hit statistics, and the selected skill. */
const SkillsTab = memo(function SkillsTab({ analysis, encounter, rows, query, setQuery, setContributor, range, entering }: SkillsTabProps) {
  const { t } = useTranslation(["history", "dashboard"])
  const d = (key: string, options?: Record<string, unknown>) => t(key, { ns: "dashboard", ...options })
  const format = useFormatters()
  const nameOf = useRowNames()
  const top = rows.find(row => row.kind === "role" && row.damage > 0) ?? rows[0]
  const current = (query.contributor ? rows.find(row => row.key === query.contributor) : undefined) ?? top
  const contributor = useMemo(() => {
    if (!encounter || !current) return undefined
    if (current.kind === "vehicle") return encounter.vehicle
    return encounter.roles.find(role => `role-${role.playerId}-${role.roleId}` === current.key)
  }, [encounter, current])
  const breakdown = contributor ? skillBreakdown(contributor) : undefined
  const stats = useMemo(() => new Map(analysis.skills.filter(stat => stat.contributor === current?.key).map(stat => [stat.skillId, stat])), [analysis.skills, current?.key])
  const selectedSkill = query.skillRow ? parseSkillRow(query.skillRow) : null
  const pick = (skillId: number | undefined) => {
    if (!current) return
    const key = skillRowKey(current.key, skillId)
    setQuery(q => ({ ...q, skillRow: q.skillRow === key ? "" : key }))
  }
  const color = elementColor(current?.elementId)
  // This tab mounts afresh for each encounter (it waits for that encounter's analysis), so one scope serves it: a filter change
  // moves the ranking's bars and numbers from their current values rather than snapping.
  const scope = "analysis-ranking"
  const skillRows: Array<{ key: string; skillId: number; name: ReactNode; damage: number; share?: number; stat?: SkillStats }> = []
  if (breakdown && current) {
    for (const row of breakdown.rows) skillRows.push({ key: skillRowKey(current.key, row.skillId), skillId: row.skillId, damage: row.damage, share: row.share, stat: stats.get(row.skillId),
      name: <span className="ww-dash-skill-name" title={row.showId ? `SkillId ${row.skillId}` : undefined}><GameAssetImage kind={row.iconKind ?? "skill"} id={row.iconId ?? row.skillId} /><span className="ww-truncate">{row.name ?? d("unnamedSkill")}</span>{row.derived && <span className="ww-dash-skill-derived" title={d(derivedNoteKey[row.derived])}>≈</span>}{row.showId && <span className="ww-dash-skill-id ww-num">{row.skillId}</span>}</span> })
    if (breakdown.unassigned) skillRows.push({ key: skillRowKey(current.key, 0), skillId: 0, damage: breakdown.unassigned.damage, share: breakdown.unassigned.share, stat: stats.get(0),
      name: <span className="ww-dash-skill-name">{breakdown.unassigned.iconKind !== undefined && breakdown.unassigned.iconId !== undefined && <GameAssetImage kind={breakdown.unassigned.iconKind} id={breakdown.unassigned.iconId} />}<span className="ww-truncate">{breakdown.unassigned.name ?? d("unassignedSkill")}</span>{breakdown.unassigned.derived && <span className="ww-dash-skill-derived">≈</span>}{breakdown.unassigned.derived && <span className="ww-dash-skill-noid">{d("noSkillIdTag")}</span>}</span> })
  }
  const focused = skillRows.find(row => selectedSkill && current && selectedSkill.contributor === current.key && row.skillId === selectedSkill.skillId)
  return <div className="ww-analysis-skills">
    <div className="ww-analysis-board" data-field="analysis-ranking">
      <div className="ww-dash-card-head"><h3>{d("ranking")}</h3></div>
      {rows.length === 0 ? <p className="ww-dash-empty-note">{d("noRoles")}</p> : <RoleBoard rows={rows} selected={current?.key ?? ""} onSelect={key => setContributor(key === query.contributor ? null : key)} scope={scope} stale={false} numberMode="ease" enter={entering} />}
      {encounter?.completeness.roleAttributionComplete === false && <p className="ww-dash-warning">{t("attributionIncomplete", { ns: "common" })}</p>}
    </div>
    <div className="ww-analysis-stack">
      <div className="ww-analysis-main-col">
        <div className="ww-dash-card-head"><h3 data-field="skills-for" data-key={current?.key ?? ""}>{current ? nameOf(current) : "—"}</h3><span className="ww-history-total">{t("analysis.skills.hitsAreNotCasts")}</span></div>
        {skillRows.length === 0 ? <p className="ww-dash-empty-note">{d("noSkillRows")}</p> :
          <div className="ww-dash-skills-wrap ww-analysis-scroll"><table className="ww-dash-skills ww-analysis-skill-table" data-field="analysis-skills" style={{ "--ww-series": color } as React.CSSProperties}>
            <thead><tr><th>{d("skill")}</th><th>{d("damageColumn")}</th><th>{d("shareColumn")}</th><th>{t("analysis.skills.hits")}</th><th>{t("analysis.skills.maxHit")}</th><th>{t("analysis.skills.averageHit")}</th><th>{t("analysis.skills.distribution")}</th></tr></thead>
            <tbody>{skillRows.map((row, index) => <tr key={row.key} className="ww-history-row" tabIndex={0} data-skill-row={row.key} data-selected={focused?.key === row.key} onClick={() => pick(row.skillId)}
              onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); pick(row.skillId) } }}>
              <td>{row.name}</td>
              <td className="ww-num">{format.integer(row.damage)}</td>
              <td><div className="ww-dash-skill-share"><DamageBar share={row.share} scope={`skills:${analysis.query.contributor}`} enter={entering ? index : undefined} /><span className="ww-num">{row.share === undefined ? "—" : format.percent(row.share)}</span></div></td>
              <td className="ww-num">{row.stat ? format.integer(row.stat.hits) : "—"}</td>
              <td className="ww-num">{row.stat ? format.integer(row.stat.maxHit) : "—"}</td>
              <td className="ww-num">{row.stat && row.stat.hits > 0 ? format.integer(row.stat.averageHit) : "—"}</td>
              <td>{row.stat && <Sparkline bins={row.stat.bins} range={range} durationMs={analysis.range.durationMs} />}</td>
            </tr>)}</tbody>
          </table></div>}
        <p className="ww-history-note">{t("analysis.skills.selectHint")}</p>
      </div>
      <aside className="ww-analysis-detail" data-field="skill-detail" data-key={focused?.key ?? ""}>
        {!focused ? <p className="ww-dash-empty-note">{t("analysis.skills.detailHint")}</p> : <>
          <h4>{focused.name}</h4>
          <dl className="ww-analysis-facts">
            <div><dt>{d("damageColumn")}</dt><dd className="ww-num">{format.integer(focused.damage)}</dd></div>
            <div><dt>{d("shareColumn")}</dt><dd className="ww-num">{focused.share === undefined ? "—" : format.percent(focused.share)}</dd></div>
            <div><dt>{t("analysis.skills.hits")}</dt><dd className="ww-num">{focused.stat ? format.integer(focused.stat.hits) : "—"}</dd></div>
            <div><dt>{t("analysis.skills.maxHit")}</dt><dd className="ww-num">{focused.stat ? format.integer(focused.stat.maxHit) : "—"}</dd></div>
            <div><dt>{t("analysis.skills.averageHit")}</dt><dd className="ww-num">{focused.stat && focused.stat.hits > 0 ? format.integer(focused.stat.averageHit) : "—"}</dd></div>
            <div><dt>{t("analysis.skills.marked")}</dt><dd className="ww-num" data-field="skill-hits-marked">{format.integer(analysis.hits.length)}{analysis.hitsTruncated ? "+" : ""}</dd></div>
          </dl>
          {focused.stat && <Sparkline bins={focused.stat.bins} range={range} durationMs={analysis.range.durationMs} />}
          <p className="ww-history-note">{t("analysis.skills.distributionNote")}</p>
          <p className="ww-history-note">{t("coverage.skillCastsNotStored")}</p>
        </>}
      </aside>
    </div>
  </div>
})

interface EventsTabProps {
  id: number
  query: AnalysisQuery
  analysis: EncounterAnalysis
  lanes: EffectLanesData | null
  actorName: EffectTabProps["actorName"]
  enemyLabel: (ordinal: number) => string
  rowByKey: Map<string, CombatRowModel>
}

const allKinds: EventKind[] = ["damage", "healing", "effect"]

/** The Events tab: the stored results and effect changes of the selection, in the order the engine processed them. */
const EventsTab = memo(function EventsTab({ id, query, analysis, lanes, actorName, enemyLabel }: EventsTabProps) {
  const { t } = useTranslation(["history", "dashboard"])
  const format = useFormatters()
  const [kinds, setKinds] = useState<EventKind[]>(allKinds)
  const [offset, setOffset] = useState(0)
  const [page, setPage] = useState<EventPage | null>(null)
  const [failed, setFailed] = useState(false)
  const key = `${id}:${query.fromMs}:${query.toMs}:${query.contributor}:${query.enemy}:${query.effects}:${query.skillRow}:${kinds.join()}`
  const [pageKey, setPageKey] = useState(key)
  if (pageKey !== key) {
    setPageKey(key)
    setOffset(0)
  }
  useEffect(() => {
    let current = true
    host.historyEvents(id, { ...query, kinds, effectRow: "", offset, limit: eventPageSize }).then(answer => { if (current) { setPage(answer); setFailed(false) } }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [id, query, kinds, offset])
  // Skill and effect names come from the same answers the other tabs show.
  const skillNames = useMemo(() => {
    const names = new Map<string, string>()
    const encounter = analysis.encounter
    if (!encounter) return names
    const add = (contributor: string, source: Parameters<typeof skillBreakdown>[0]) => {
      const breakdown = skillBreakdown(source)
      breakdown?.rows.forEach(row => names.set(skillRowKey(contributor, row.skillId), row.name ? `${row.name}${row.derived ? " ≈" : ""}` : `${t("unnamedSkill", { ns: "dashboard" })} ${row.skillId}`))
      if (breakdown?.unassigned) names.set(skillRowKey(contributor, 0), breakdown.unassigned.name ?? t("unassignedSkill", { ns: "dashboard" }))
    }
    encounter.roles.forEach(role => add(`role-${role.playerId}-${role.roleId}`, role))
    if (encounter.vehicle) add(`vehicle-${encounter.vehicle.kind}`, encounter.vehicle)
    return names
  }, [analysis.encounter, t])
  const effectRows = useMemo(() => new Map((lanes?.rows ?? []).map(row => [row.key, row])), [lanes])
  const toggle = (kind: EventKind) => setKinds(current => current.includes(kind) ? (current.length > 1 ? current.filter(k => k !== kind) : current) : [...current, kind])
  const detail = (row: EventRow): ReactNode => {
    if (row.kind === "effect") {
      const effect = row.effectRow ? effectRows.get(row.effectRow) : undefined
      return <span className="ww-analysis-event-detail">{effect ? <EffectName row={effect} /> : <span className="ww-history-unknown">#{row.effectId ?? "—"}</span>}
        <span className="ww-history-mark">{t(`analysis.change.${row.change ?? "apply"}`)} · {t(`analysis.observation.${row.observation ?? "observed"}`)}</span></span>
    }
    if (row.skillRow) return skillNames.get(row.skillRow) ?? (row.skillId ? `SkillId ${row.skillId}` : t("unassignedSkill", { ns: "dashboard" }))
    return <span className="ww-history-unknown">{t(`analysis.flow.${row.flow ?? "unknown"}`)}</span>
  }
  const amount = (row: EventRow) => row.kind === "effect" ? (row.stack !== undefined ? `×${row.stack}` : "—") : row.kind === "healing" ? `${format.integer(row.amount ?? 0)} / ${format.integer(row.effective ?? 0)}` : format.integer(row.amount ?? 0)
  return <div className="ww-analysis-events">
    <div className="ww-analysis-toolbar-row">
      <div className="ww-analysis-kinds" role="group" aria-label={t("analysis.events.kinds")}>
        {allKinds.map(kind => <Button key={kind} size="sm" variant={kinds.includes(kind) ? "secondary" : "ghost"} aria-pressed={kinds.includes(kind)} onClick={() => toggle(kind)} data-field={`event-kind-${kind}`}>{t(`analysis.events.kind.${kind}`)}</Button>)}
      </div>
      {page && <span className="ww-history-total ww-num" data-field="events-total" data-total={page.total}>{page.total === 0 ? t("analysis.events.none") : t("analysis.events.range", { from: format.integer(page.offset + 1), to: format.integer(page.offset + page.rows.length), total: format.integer(page.total) })}</span>}
      <div className="ww-analysis-pager">
        <Button size="sm" variant="ghost" disabled={!page || page.offset === 0} onClick={() => setOffset(Math.max(0, offset - eventPageSize))} data-field="events-previous">{t("analysis.events.previous")}</Button>
        <Button size="sm" variant="ghost" disabled={!page || page.offset + page.rows.length >= page.total} onClick={() => setOffset(offset + eventPageSize)} data-field="events-next">{t("analysis.events.next")}</Button>
      </div>
    </div>
    {failed && <p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p>}
    {page && !page.effectsAvailable && kinds.includes("effect") && <p className="ww-history-note">{t("analysis.effects.notStored")}</p>}
    {page && page.rows.length > 0 && <div className="ww-dash-skills-wrap ww-analysis-scroll ww-analysis-events-wrap"><table className="ww-history-table ww-analysis-table" data-field="events-table" data-offset={page.offset}>
      <thead><tr><th className="ww-num">{t("analysis.events.time")}</th><th>{t("analysis.events.kindColumn")}</th><th>{t("analysis.events.source")}</th><th>{t("analysis.events.target")}</th><th>{t("analysis.events.detail")}</th><th className="ww-num">{t("analysis.events.amount")}</th></tr></thead>
      <tbody>{page.rows.map(row => <tr key={row.key} data-kind={row.kind} data-key={row.key} data-flow={row.flow} data-amount={row.amount}>
        <td className="ww-num">{row.atMs === undefined ? "—" : formatMs(row.atMs)}</td>
        <td><span className="ww-analysis-kind" data-kind={row.kind}>{t(`analysis.events.kind.${row.kind}`)}</span>{row.downgraded && <span className="ww-history-mark" title={t("analysis.events.downgraded")}>!</span>}</td>
        <td className="ww-truncate">{actorName(row.source, enemyLabel)}</td>
        <td className="ww-truncate">{actorName(row.target, enemyLabel)}</td>
        <td className="ww-truncate">{detail(row)}</td>
        <td className="ww-num">{amount(row)}</td>
      </tr>)}</tbody>
    </table></div>}
  </div>
})
