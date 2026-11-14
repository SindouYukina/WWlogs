import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, Card, CharacterAvatar, Chip, Icon, Select, Tooltip } from "@/design"
import { useFormatters } from "@/lib/format"
import type { CompareContributor, CompareEncounter, CompareValue, HistoryComparison } from "@/lib/history-compare"
import { host } from "@/lib/host"
import { StatusSkillName } from "./DashboardView"
import { LinkedName } from "./HistoryAnalysis"
import { ClassChip } from "./HistoryLibrary"

// The Encounter Comparison: two to four stored encounters side by side. Go reads each one as its History detail shows it,
// lines contributors and skills up, states the conditions the encounters share and gives the changes from the first one;
// this view lays that out. It never weighs the conditions into a score.

export interface HistoryCompareProps {
  ids: readonly number[]
  zone: string
  /** Changes when the history may have changed; the comparison then asks again. */
  refreshKey: string
  onOpen: (id: number) => void
  onRemove: (id: number) => void
  /** Called with the encounters that are no longer stored, so the set drops them. */
  onMissing: (ids: number[]) => void
  onBack: () => void
}

type ChipTone = "neutral" | "good" | "warn" | "critical"

const conditionTone: Record<string, ChipTone> = { same: "good", different: "warn", unknown: "neutral" }
const conditionLabel: Record<string, string> = { game_version: "columns.version", mode: "columns.mode", stage: "columns.scene", main_enemy: "columns.enemy" }

function useDelta() {
  const format = useFormatters()
  return (delta: number | undefined) => delta === undefined ? null : `${delta > 0 ? "+" : ""}${format.percent(delta)}`
}

/** One compared value with what it is: a lower bound shows ≥, part of a result a mark, no value a dash. */
function ValueCell({ value, format, field }: { value: CompareValue; format: (value: number) => string; field: string }) {
  const { t } = useTranslation("history")
  const delta = useDelta()
  const status = value.status
  if (value.value === undefined || status === "unavailable" || status === "unreliable") {
    return <td className="ww-num ww-history-unknown" data-field={field} data-status={status} title={t(`compare.status.${status}`)}>—</td>
  }
  return <td className="ww-num" data-field={field} data-status={status}>
    <span title={status === "available" ? undefined : t(`compare.status.${status}`)}>{status === "lower_bound" ? "≥ " : ""}{format(value.value)}{status === "partial" && <span className="ww-history-mark">*</span>}</span>
    {value.delta !== undefined && <span className="ww-compare-delta" data-sign={Math.sign(value.delta)}>{delta(value.delta)}</span>}
  </td>
}

function EncounterHead({ encounter, index, onOpen, onRemove }: { encounter: CompareEncounter; index: number; onOpen: () => void; onRemove: () => void }) {
  const { t } = useTranslation("history")
  const mode = encounter.mode
  return <th className="ww-compare-head" data-encounter-id={encounter.id} data-class={encounter.class}>
    <div className="ww-compare-head-line"><span className="ww-num ww-compare-time">{encounter.localDate} {encounter.start.clock}</span>{index === 0 && <Chip tone="neutral">{t("compare.base")}</Chip>}</div>
    <div className="ww-compare-head-line ww-history-note">
      <span className="ww-history-version">{encounter.gameVersion || t("library.versionUnknown")}</span>
      <span>{!encounter.scene.known || !mode?.kind ? t("unidentified") : mode.kind === "open_world" ? t("modeOpenWorld") : mode.name}</span>
    </div>
    <div className="ww-compare-head-line ww-history-note"><LinkedName name={encounter.sceneName} fallback={t("unidentified")} id={encounter.scene.instanceId} title={t("correlatedNote")} /></div>
    <div className="ww-compare-head-line"><ClassChip value={encounter.class} /></div>
    <div className="ww-compare-head-line">
      <Button size="sm" variant="ghost" data-field="compare-open" onClick={onOpen}>{t("compare.open")}</Button>
      <Button size="sm" variant="ghost" data-field="compare-remove" onClick={onRemove}>{t("compare.remove")}</Button>
    </div>
  </th>
}

function ContributorName({ contributor }: { contributor: CompareContributor }) {
  const { t } = useTranslation(["history", "common"])
  if (contributor.kind === "vehicle") return <span className="ww-compare-who"><Icon name="bolt" size={16} />{t("motorcycle", { ns: "common" })}</span>
  if (contributor.kind === "unassigned") return <span className="ww-compare-who ww-history-unknown">{t("unassigned", { ns: "common" })}</span>
  const name = contributor.name.name ?? t("unknownRole", { ns: "common", id: contributor.roleId })
  return <span className="ww-compare-who"><CharacterAvatar roleId={contributor.roleId} elementId={contributor.elementId} size={24} /><span className="ww-truncate">{name}</span></span>
}

export function HistoryCompare({ ids, zone, refreshKey, onOpen, onRemove, onMissing, onBack }: HistoryCompareProps) {
  const { t, i18n } = useTranslation(["history", "common", "dashboard"])
  const format = useFormatters()
  const delta = useDelta()
  const [view, setView] = useState<HistoryComparison | null>(null)
  const [failed, setFailed] = useState(false)
  const [skillsOf, setSkillsOf] = useState("")
  const key = ids.join(",")
  useEffect(() => {
    if (ids.length < 2) { setView(null); return }
    let current = true
    host.historyCompare([...ids], zone).then(answer => {
      if (!current) return
      setFailed(false)
      setView(answer)
      if (answer.missing.length > 0) onMissing(answer.missing)
    }).catch(() => { if (current) setFailed(true) })
    return () => { current = false }
  }, [key, zone, refreshKey])

  const contributorKey = (contributor: CompareContributor) => `${contributor.kind}:${contributor.roleId ?? ""}`
  const withSkills = useMemo(() => (view?.contributors ?? []).filter(contributor => contributor.skills.length > 0), [view])
  const chosen = withSkills.find(contributor => contributorKey(contributor) === skillsOf) ?? withSkills[0]
  const skillOptions = useMemo(() => withSkills.map(contributor => ({ value: contributorKey(contributor), label: contributor.kind === "vehicle" ? t("motorcycle", { ns: "common" }) : contributor.kind === "unassigned" ? t("unassigned", { ns: "common" }) : contributor.name.name ?? t("unknownRole", { ns: "common", id: contributor.roleId }) })), [withSkills, i18n.language])

  const metricFormat = (key: string) => key === "duration" ? (value: number) => format.clock(value) : format.integer
  const encounters = view?.encounters ?? []
  const context = view?.context
  return <div className="ww-history-detail ww-compare" data-field="history-compare" data-count={ids.length}>
    <div className="ww-history-detail-head">
      <div className="ww-history-years">
        <Button size="sm" variant="ghost" onClick={onBack} data-field="compare-back"><span style={{ display: "inline-flex", transform: "rotate(90deg)" }}><Icon name="chevron" size={13} /></span>{t("compare.exit")}</Button>
        <h3>{t("compare.title", { n: format.integer(ids.length) })}</h3>
      </div>
    </div>
    {ids.length < 2 && <Card className="ww-history-context"><p className="ww-dash-empty-note" data-field="compare-need-two">{t("compare.needTwo")}</p></Card>}
    {failed && <p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p>}
    {view && encounters.length >= 2 && context && <>
      <Card className="ww-history-context" data-field="compare-context" data-reliable={context.reliable}>
        <div className="ww-dash-card-head"><h3>{t("compare.conditions")}</h3></div>
        <div className="ww-compare-conditions">
          {context.conditions.map(condition => <span key={condition.key} className="ww-compare-condition" data-field={`condition-${condition.key}`} data-state={condition.state}>
            <span className="ww-career-fact-label">{t(conditionLabel[condition.key] ?? condition.key)}</span>
            <Chip tone={conditionTone[condition.state] ?? "neutral"} dot>{t(`compare.state.${condition.state}`)}</Chip>
          </span>)}
        </div>
        <ul className="ww-career-rules">
          {context.durationRatio !== undefined && context.durationRatio > 1 && <li data-field="compare-duration-ratio">{t("compare.durationRatio", { ratio: context.durationRatio.toLocaleString(i18n.language, { maximumFractionDigits: 1, minimumFractionDigits: 1 }) })}</li>}
          {context.openWorld && <li data-field="compare-open-world">{t("compare.openWorldNote")}</li>}
          <li>{t("compare.hpNote")}</li>
          <li>{t("compare.noScore")}</li>
        </ul>
        {!context.reliable && <p className="ww-history-note" data-tone="warn" data-field="compare-unreliable">{t("compare.unreliable")}</p>}
      </Card>

      <Card className="ww-history-context" data-field="compare-metrics">
        <div className="ww-dash-card-head"><h3>{t("compare.metrics")}</h3><span className="ww-history-note">{t("compare.deltaNote")}</span></div>
        <div className="ww-dash-skills-wrap" style={{ maxHeight: "none" }}><table className="ww-history-table ww-compare-table">
          <thead><tr><th className="ww-compare-label" />{encounters.map((encounter, index) => <EncounterHead key={encounter.id} encounter={encounter} index={index} onOpen={() => onOpen(encounter.id)} onRemove={() => onRemove(encounter.id)} />)}</tr></thead>
          <tbody>{view.metrics.map(metric => <tr key={metric.key} data-metric={metric.key}>
            <th scope="row" className="ww-compare-label">{t(`compare.metric.${metric.key}`)}</th>
            {metric.values.map((value, index) => <ValueCell key={index} value={value} format={metricFormat(metric.key)} field={`metric-${metric.key}`} />)}
          </tr>)}</tbody>
        </table></div>
        {encounters.some(encounter => !encounter.results) && <p className="ww-history-note">{t("compare.noResults")}</p>}
      </Card>

      <Card className="ww-history-context" data-field="compare-contributors">
        <div className="ww-dash-card-head"><h3>{t("compare.contributors")}</h3></div>
        <p className="ww-history-note">{t("compare.contributorsNote")}</p>
        <div className="ww-dash-skills-wrap" style={{ maxHeight: "none" }}><table className="ww-history-table ww-compare-table">
          <thead><tr><th className="ww-compare-label">{t("library.character")}</th>{encounters.map((encounter, index) => <th key={encounter.id} className="ww-num">{encounter.localDate} {encounter.start.clock}{index === 0 ? ` · ${t("compare.base")}` : ""}</th>)}</tr></thead>
          <tbody>{view.contributors.map(contributor => <tr key={contributorKey(contributor)} data-kind={contributor.kind} data-role-id={contributor.roleId}>
            <th scope="row" className="ww-compare-label"><ContributorName contributor={contributor} /></th>
            {contributor.values.map((value, index) => value ? <td key={index} className="ww-num" data-field="contribution" data-status={value.status}>
              <span>{value.status === "lower_bound" ? "≥ " : ""}{format.integer(value.damage)}{value.status === "partial" && <span className="ww-history-mark">*</span>}</span>
              <span className="ww-compare-sub">{value.share !== undefined ? format.percent(value.share) : "—"}{value.dps !== undefined ? ` · ${t("compare.dps")} ${format.integer(value.dps)}` : ""}{value.trial ? ` · ${t("compare.trial")}` : ""}</span>
              {value.dpsDelta !== undefined && <span className="ww-compare-delta" data-sign={Math.sign(value.dpsDelta)} title={t("compare.dpsDeltaNote")}>{delta(value.dpsDelta)}</span>}
            </td> : <td key={index} className="ww-num ww-history-unknown" data-field="contribution" title={t("compare.notDealt")}>—</td>)}
          </tr>)}</tbody>
        </table></div>
      </Card>

      {chosen && <Card className="ww-history-context" data-field="compare-skills" data-contributor={contributorKey(chosen)}>
        <div className="ww-dash-card-head"><h3>{t("compare.skills")}</h3>
          <Select label={t("compare.skillsOf")} value={contributorKey(chosen)} options={skillOptions} onChange={setSkillsOf} />
        </div>
        <p className="ww-history-note">{t("compare.skillsNote")}</p>
        <div className="ww-dash-skills-wrap"><table className="ww-history-table ww-compare-table">
          <thead><tr><th className="ww-compare-label">{t("compare.skill")}</th>{encounters.map(encounter => <th key={encounter.id} className="ww-num">{encounter.localDate} {encounter.start.clock}</th>)}</tr></thead>
          <tbody>{chosen.skills.map(skill => <tr key={skill.unassigned ? "unassigned" : skill.statusDamageId ? `status-${skill.statusDamageId}` : skill.skillId} data-skill-id={skill.skillId ?? ""} data-status-damage-id={skill.statusDamageId ?? ""} data-unassigned={skill.unassigned === true}>
            <th scope="row" className="ww-compare-label">
              {skill.statusDamageId
                ? <StatusSkillName status={{ damageId: skill.statusDamageId, name: skill.name.name || undefined, iconKind: skill.iconKind === "buff" ? "buff" : undefined, iconId: skill.iconKind === "buff" ? skill.iconId : undefined }} />
                : skill.unassigned
                ? <Tooltip text={t("unassignedSkillNote", { ns: "dashboard" })} trigger="child" placement="top"><span className="ww-history-unknown">{t("unassignedSkill", { ns: "dashboard" })}</span></Tooltip>
                : <span className="ww-compare-skill"><LinkedName name={skill.name} fallback={t("unidentified")} id={skill.skillId} title={t("correlatedNote")} />{skill.showSkillId && skill.name.name && <span className="ww-history-mark ww-num">#{skill.skillId}</span>}</span>}
            </th>
            {skill.values.map((value, index) => value ? <td key={index} className="ww-num"><span>{format.integer(value.damage)}</span><span className="ww-compare-sub">{value.share !== undefined ? format.percent(value.share) : "—"}</span></td>
              : <td key={index} className="ww-num ww-history-unknown">—</td>)}
          </tr>)}</tbody>
        </table></div>
      </Card>}
    </>}
  </div>
}
