import { memo, useMemo, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { AnimatedNumber, Card, CharacterAvatar, Chip, ContentSwitch, DamageBar, GameAssetImage, Icon, LiveChart, Reveal, Segmented, StackList, ThemeBackdrop, type ChartSeries, type IconName, type ThemeId } from "@/design"
import { dataChip, displayedEncounter, isFrozen, usableDps, type CombatData, type SkillBreakdown } from "@/lib/combat-data"
import { teamSeriesKey, unassignedSeriesKey } from "@/lib/timeline"
import { combatRows, detailRow, selectionScope, type CombatRowModel } from "@/lib/combat-view"
import { useDetailPick, type DetailPick } from "@/lib/useDetailPick"
import { elementColor, useFormatters, useRowNames } from "@/lib/format"
import { dpsText, usableHealing, type ViewSnapshot } from "@/lib/snapshot"
import { statusText } from "@/i18n"

interface StatProps {
  icon: IconName
  label: string
  value: number | undefined
  format: (value: number) => string
  note?: string
  scope: string
  stale: boolean
  hero?: boolean
  numberMode: "tween" | "roll"
  index: number
  field: string
  status?: string
}

const StatCard = memo(function StatCard({ icon, label, value, format, note, scope, stale, hero = false, numberMode, index, field, status }: StatProps) {
  return <Reveal index={index} className={hero ? "ww-dash-stat-wrap ww-dash-stat-hero" : "ww-dash-stat-wrap"}>
    <Card interactive className="ww-dash-stat">
      <div className="ww-dash-stat-label"><Icon name={icon} size={14} />{label}</div>
      <div className="ww-dash-stat-value" data-field={field} data-status={status}><AnimatedNumber value={value} format={format} mode={numberMode} scope={scope} stale={stale} /></div>
      {note && <div className="ww-dash-stat-note">{note}</div>}
    </Card>
  </Reveal>
})

function RoleBoard({ rows, selected, onSelect, scope, stale, numberMode }: { rows: readonly CombatRowModel[]; selected: string; onSelect: (key: string) => void; scope: string; stale: boolean; numberMode: "tween" | "roll" }) {
  const format = useFormatters()
  const nameOf = useRowNames()
  return <StackList items={rows} keyOf={row => row.key} rowHeight={58} gap={6} renderItem={row => <div className="ww-dash-role" data-key={row.key} data-rank={row.rank} data-field={row.kind === "vehicle" ? "vehicle" : undefined} data-selected={row.key === selected} data-element={row.elementId} role="button" tabIndex={0} onClick={() => onSelect(row.key)} onKeyDown={event => { if (event.key === "Enter" || event.key === " ") onSelect(row.key) }}>
    <CharacterAvatar roleId={row.roleId} elementId={row.elementId} size={42} fallback={row.kind === "vehicle" ? <Icon name="bolt" size={18} /> : "?"} />
    <div className="ww-dash-role-main">
      <div className="ww-dash-role-line"><strong className="ww-truncate">{nameOf(row)}</strong><span className="ww-num ww-dash-role-dps"><AnimatedNumber value={row.dps} format={format.integer} mode={numberMode} scope={scope} stale={stale} /></span></div>
      <DamageBar share={row.share} scope={scope} />
      <div className="ww-dash-role-line ww-dash-role-sub"><span className="ww-num"><AnimatedNumber value={row.damage} format={format.integer} mode="tween" scope={scope} stale={stale} /></span><span className="ww-num">{row.share === undefined ? "—" : format.percent(row.share)}</span></div>
    </div>
  </div>} />
}

const derivedNoteKey = { damage_list: "derivedSkillNote", skill_type: "derivedSkillTypeNote", tune_break: "derivedTuneBreakNote", echo: "derivedEchoSkillNote" } as const
const derivedUnassignedNoteKey = { echo: "derivedUnassignedEchoNote", damage_list: "derivedUnassignedSkillNote", damage_type: "derivedUnassignedOutroNote" } as const

/** The engine's skill split of one contributor. Rows plus the unassigned row add up to the contributor's damage. */
const SkillTable = memo(function SkillTable({ breakdown, color }: { breakdown: SkillBreakdown; color: string }) {
  const { t } = useTranslation("dashboard")
  const format = useFormatters()
  const share = (value: number | undefined) => value === undefined ? "—" : format.percent(value)
  const { rows, unassigned } = breakdown
  if (rows.length === 0 && !unassigned) return <p className="ww-dash-empty-note">{t("noSkillRows")}</p>
  return <div className="ww-dash-skills-wrap"><table className="ww-dash-skills" data-field="skills" style={{ "--ww-series": color } as React.CSSProperties}>
    <thead><tr><th>{t("skill")}</th><th>{t("damageColumn")}</th><th>{t("damageEvents")}</th><th>{t("shareColumn")}</th></tr></thead>
    <tbody>{rows.map(row => <tr key={row.skillId} data-skill-id={row.skillId} data-named={row.name !== undefined} data-name-source={row.derived}>
      <td><span className="ww-dash-skill-name" title={row.showId ? `SkillId ${row.skillId}` : undefined}><GameAssetImage kind={row.iconKind ?? "skill"} id={row.iconId ?? row.skillId} /><span className="ww-truncate">{row.name ?? t("unnamedSkill")}</span>{row.derived && <span className="ww-dash-skill-derived" data-field="derived-name" title={t(derivedNoteKey[row.derived])} aria-label={t(derivedNoteKey[row.derived])}>≈</span>}{row.showId && <span className="ww-dash-skill-id ww-num ww-selectable" data-field="skill-id">{row.skillId}</span>}</span></td>
      <td className="ww-num"><AnimatedNumber value={row.damage} format={format.integer} /></td>
      <td className="ww-num">{format.integer(row.events)}</td>
      <td><div className="ww-dash-skill-share"><DamageBar share={row.share} /><span className="ww-num">{share(row.share)}</span></div></td>
    </tr>)}
    {unassigned && <tr className="ww-dash-skill-unassigned" data-field="unassigned-skill" data-named={unassigned.name !== undefined} data-name-source={unassigned.derived}>
      <td><span className="ww-dash-skill-name" title={unassigned.derived ? t(derivedUnassignedNoteKey[unassigned.derived]) : t("unassignedSkillNote")}>{unassigned.iconKind !== undefined && unassigned.iconId !== undefined && <GameAssetImage kind={unassigned.iconKind} id={unassigned.iconId} />}<span className="ww-truncate" data-field="unassigned-name">{unassigned.name ?? t("unassignedSkill")}</span>{unassigned.derived && <span className="ww-dash-skill-derived" data-field="derived-name" title={t(derivedUnassignedNoteKey[unassigned.derived])} aria-label={t(derivedUnassignedNoteKey[unassigned.derived])}>≈</span>}</span></td>
      <td className="ww-num"><AnimatedNumber value={unassigned.damage} format={format.integer} /></td>
      <td className="ww-num">{format.integer(unassigned.events)}</td>
      <td><div className="ww-dash-skill-share"><DamageBar share={unassigned.share} /><span className="ww-num">{share(unassigned.share)}</span></div></td>
    </tr>}</tbody>
  </table></div>
})

/** The engine's own health report. Nothing here is computed in the frontend. */
function IntegrityPanel({ view, diagnostics }: { view: ViewSnapshot | null; diagnostics: boolean }) {
  const { t, i18n } = useTranslation(["dashboard", "common"])
  const c = (key: string) => t(key, { ns: "common" })
  const number = (value: number | undefined) => value === undefined ? "—" : value.toLocaleString(i18n.language)
  const integrity = view?.statistics.integrity
  return <div className="ww-dash-integrity">
    <p className="ww-dash-empty-note" data-field="integrity" data-status={view?.health.dataIntegrity}>{!view ? c("waiting") : t(integrity?.status === "INTACT" ? "intactNote" : integrity?.status === "DEGRADED" ? "degradedNote" : "unreliableNote")}</p>
    <dl className="ww-dash-facts ww-selectable">
      <dt>{c("context")}</dt><dd data-field="context">{statusText(view?.health.context.state)}</dd>
      <dt>{c("transport")}</dt><dd data-field="transport">{statusText(view?.health.transport.state)}</dd>
      <dt>{t("transportLosses")}</dt><dd className="ww-num">{number(integrity?.transportLosses)}</dd>
      <dt>{t("unrecovered")}</dt><dd className="ww-num">{number(integrity?.unrecoveredLosses)}</dd>
      <dt>{t("downgraded")}</dt><dd className="ww-num">{number(integrity?.contextDowngradedEvents)}</dd>
      {diagnostics && <>
        <dt>{t("drops")}</dt><dd className="ww-num">{view?.health.capture.kernelDropped.status === "available" ? number(view.health.capture.kernelDropped.value) : c("unknown")}</dd>
        <dt>{t("paired")}</dt><dd className="ww-num">{view ? `${number(view.health.rpcPairing.paired)} / ${number(view.health.rpcPairing.requests)}` : "—"}</dd>
        <dt>{t("finished")}</dt><dd className="ww-num" data-field="encounters-finished">{number(view?.encounters.completedTotal)}</dd>
        <dt>{c("unassigned")}</dt><dd className="ww-num" data-field="unassigned">{number(view?.unassignedRole.damageDealt)}</dd>
        <dt>{t("unknownOwnership")}</dt><dd className="ww-num" data-field="unknown-ownership">{t("unknownTotals", { out: number(view?.statistics.unattributed.outgoingDamage), in: number(view?.statistics.unattributed.incomingDamage) })}</dd>
      </>}
    </dl>
    {view?.health.context.state === "SUSPECT" && <p className="ww-dash-warning">{t("suspectNote")}</p>}
  </div>
}

type DetailTab = "skills" | "integrity"

export interface DashboardViewProps {
  data: CombatData
  theme: ThemeId
  numberMode?: "tween" | "roll"
  /** Extra controls beside the clock, for review surfaces. */
  actions?: ReactNode
  /** The ranking pick, held by the window so it outlives a page switch. Without it the Dashboard keeps its own. */
  detailPick?: DetailPick
}

/**
 * The one Dashboard. It renders only what its provider reports. A number without a source is shown as unavailable, and
 * nothing is calculated here beyond formatting the engine's values.
 */
export function DashboardView({ data, theme, numberMode = "tween", actions, detailPick }: DashboardViewProps) {
  const { t, i18n } = useTranslation(["dashboard", "common"])
  const c = (key: string) => t(key, { ns: "common" })
  const format = useFormatters()
  const nameOf = useRowNames()
  const snapshot = data.snapshot
  const view = snapshot?.view ?? null
  const encounter = displayedEncounter(view)
  const rows = useMemo(() => { const scope = encounter ?? view; return scope ? combatRows(scope) : [] }, [encounter, view])
  const stale = isFrozen(data)
  const chip = dataChip(data)
  const scope = encounter?.encounterId ?? "none"
  // The detail follows this encounter's top character until the user picks a row; the pick lasts for this encounter only.
  // It is view state: nothing is written back to the engine.
  const detailScope = selectionScope(view, encounter)
  const ownPick = useDetailPick(detailScope)
  const [manual, pick] = detailPick ?? ownPick
  const hasSkills = data.skillsOf !== undefined
  const [tab, setTab] = useState<DetailTab>(hasSkills ? "skills" : "integrity")
  const detail = detailRow(rows, detailScope, manual)
  const selected = detail.row
  const dps = encounter?.dps ?? view?.statistics.encounterDps
  const lowerBound = dps?.status === "lower_bound"
  // A lower bound is never shown as an exact value.
  const dpsFormat = useMemo(() => lowerBound ? (value: number) => `≥ ${format.integer(value)}` : format.integer, [lowerBound, format])
  // The highest 1-second interval of this encounter's timeline; outside an encounter there is none.
  const peak = encounter?.peakDps
  const peakText = usableDps(peak) ? dpsText(peak, i18n.language) : "—"
  // The local team's healing in this encounter, or in the capture interval when no encounter is shown.
  const healing = encounter ? encounter.healing : view?.statistics.healing
  const healingShown = usableHealing(healing)
  const healingFormat = useMemo(() => healing?.status === "lower_bound" ? (value: number) => `≥ ${format.integer(value)}` : format.integer, [healing?.status, format])
  const timeline = data.timeline
  // The timeline changes once per second; the memo keeps the chart from re-rendering at the publish rate.
  const timelineKey = timeline ? `${timeline.encounterId ?? ""}:${timeline.slots}:${timeline.slotSeconds}:${timeline.final === true}:` + [...timeline.series].map(([key, values]) => `${key}=${values.length}:${values.at(-1) ?? ""}`).join() : ""
  const series = useMemo<ChartSeries[]>(() => {
    if (!timeline) return []
    const fit = (values: ReadonlyArray<number | null> | undefined) => Array.from({ length: timeline.slots }, (_, slot) => values?.[slot] ?? null)
    const lines: ChartSeries[] = []
    const team = timeline.series.get(teamSeriesKey)
    if (team) lines.push({ key: teamSeriesKey, label: t("teamDps"), color: "var(--ww-ink-100)", values: fit(team), aggregate: true })
    // Roles keep their element color in RoleId order, then the vehicle; the same contributor keeps its line when ranks change.
    for (const row of rows.filter(row => timeline.series.has(row.key)).sort((a, b) => (a.kind === b.kind ? (a.roleId ?? 0) - (b.roleId ?? 0) : a.kind === "role" ? -1 : 1))) {
      lines.push({ key: row.key, label: nameOf(row), color: elementColor(row.elementId), values: fit(timeline.series.get(row.key)) })
    }
    const unassigned = timeline.series.get(unassignedSeriesKey)
    if (unassigned) lines.push({ key: unassignedSeriesKey, label: c("unassigned"), color: "var(--ww-ink-400)", values: fit(unassigned) })
    return lines
  }, [timelineKey, rows, nameOf, t])
  const skills = data.skillsOf && selected ? data.skillsOf(selected.key) : undefined
  // The engine's facts for the scope on screen: this encounter when one is shown, not the whole capture.
  const completeness = (encounter ?? view)?.completeness
  const diagnostics = snapshot?.service.app.profile === "development"
  return <div className="ww-dash ww-has-backdrop"><ThemeBackdrop theme={theme} intensity="art" /><div className="ww-dash-layout">
    <Reveal className="ww-dash-top">
      <div>
        <div className="ww-caps">{encounter ? t("encounterNumber", { id: encounter.sequence }) : c("noEncounter")}</div>
        <h2 className="ww-dash-title">{t("summaryTitle")}</h2>
      </div>
      <div className="ww-dash-top-right">
        <span data-field="encounter-state" data-state={view?.encounters.state}><Chip tone={chip.tone} dot>{c(chip.key)}</Chip></span>
        <span className="ww-dash-clock ww-num" data-field="encounter-elapsed"><Icon name="clock" size={14} />{format.clock(encounter?.elapsedSeconds)}</span>
        {actions}
      </div>
    </Reveal>
    <div className="ww-dash-stats">
      <StatCard index={0} hero icon="bolt" label={c("encounterDps")} field="encounter-dps" status={dps?.status ?? "unavailable"} value={usableDps(dps) ? dps?.value : undefined} format={dpsFormat} note={view ? t("peakNote", { value: peakText }) : undefined} scope={scope} stale={stale} numberMode={numberMode} />
      <StatCard index={1} icon="spark" label={t("confirmedDamage")} field="damage-dealt" value={encounter?.totals.damageDealt ?? view?.statistics.damageDealt} format={format.integer} note={t(encounter ? "damageNote" : "interval")} scope={scope} stale={stale} numberMode={numberMode} />
      <StatCard index={2} icon="shield" label={t("damageTaken")} field="damage-taken" value={encounter?.totals.damageTaken ?? view?.statistics.damageTaken} format={format.integer} note={t(encounter ? "takenNote" : "interval")} scope={scope} stale={stale} numberMode={numberMode} />
      <StatCard index={3} icon="heal" label={t("healing")} field="healing" status={healing?.status ?? "unavailable"} value={healingShown ? healing.reported : undefined} format={healingFormat} note={healingShown ? t("healingNote", { value: healingFormat(healing.effective) }) : t("healingUnavailable")} scope={scope} stale={stale} numberMode={numberMode} />
    </div>
    <div className="ww-dash-main">
      <Reveal index={4} className="ww-dash-board-wrap">
        <Card className="ww-dash-board">
          <div className="ww-dash-card-head"><h3>{t("ranking")}</h3></div>
          <div data-field="roles">
            {rows.length === 0 ? <p className="ww-dash-empty-note">{t("noRoles")}</p> : <RoleBoard rows={rows} selected={selected?.key ?? ""} onSelect={pick} scope={scope} stale={stale} numberMode={numberMode} />}
          </div>
          {completeness?.roleAttributionComplete === false && <p className="ww-dash-warning" data-field="attribution-incomplete">{c("attributionIncomplete")}</p>}
          {completeness !== undefined && completeness.unknownOwnershipEvents > 0 && <p className="ww-dash-warning" data-field="unknown-ownership-note">{t("unknownOwnershipNote", { ns: "common", value: format.integer(completeness.unknownOwnershipDamage) })}</p>}
          {selected?.roleId !== undefined && <ContentSwitch contentKey={selected.key} className="ww-dash-portrait"><GameAssetImage kind="character-portrait" id={selected.roleId} /></ContentSwitch>}
        </Card>
      </Reveal>
      <Reveal index={5} className="ww-dash-detail-wrap">
        <Card className="ww-dash-chart-card">
          <div className="ww-dash-card-head"><h3>{t("timeline")}</h3>{!timeline && <Chip>{t("noSource")}</Chip>}</div>
          <div data-field="timeline" data-encounter={timeline?.encounterId ?? ""} data-final={timeline?.final === true} data-series={series.length}><LiveChart series={series} slots={timeline?.slots ?? 61} height={190} formatValue={format.compact} formatSlot={slot => format.clock(slot * (timeline?.slotSeconds ?? 1))} emptyLabel={t(timeline ? "chartEmpty" : "timelineUnavailable")} ariaLabel={t("timeline")} scope={timeline?.encounterId ?? ""} /></div>
        </Card>
        <Card className="ww-dash-detail">
          <div className="ww-dash-card-head" data-field="detail" data-selection={selected ? detail.mode : "none"} data-key={selected?.key ?? ""} data-scope={detailScope}>
            <h3>{selected ? nameOf(selected) : "—"}</h3>
            <Segmented variant="underline" label={t("breakdown")} value={tab} onChange={setTab} options={[{ value: "skills", label: t("skills") }, { value: "integrity", label: c("integrity") }]} />
          </div>
          <ContentSwitch contentKey={`${tab}-${selected?.key ?? ""}`}>
            {tab === "skills" && (skills === undefined ? <p className="ww-dash-empty-note">{t("skillsUnavailable")}</p> : <SkillTable breakdown={skills} color={elementColor(selected?.elementId)} />)}
            {tab === "integrity" && <IntegrityPanel view={view} diagnostics={diagnostics} />}
          </ContentSwitch>
        </Card>
      </Reveal>
    </div>
  </div></div>
}
