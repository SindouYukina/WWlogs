import { memo, useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AnimatedNumber, Button, Card, CharacterAvatar, Chip, ContentSwitch, DamageBar, Icon, LiveChart, Reveal, Segmented, StackList, ThemeBackdrop, gameAssetUrl, type ChartSeries, type IconName, type ThemeId } from "@/design"
import { combatRows, type CombatRowModel } from "@/lib/combat-view"
import { snapshotStale } from "@/lib/effects"
import type { FeedControls } from "../useMockFeed"
import { historySlots, type MockSkillRow } from "../mock-feed.ts"
import { roleColor, roleElement, useFormatters, useRoleNames } from "../format"

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
}

const StatCard = memo(function StatCard({ icon, label, value, format, note, scope, stale, hero = false, numberMode, index }: StatProps) {
  return <Reveal index={index} className={hero ? "sd-stat-wrap sd-stat-hero" : "sd-stat-wrap"}>
    <Card interactive className="sd-stat">
      <div className="sd-stat-label"><Icon name={icon} size={14} />{label}</div>
      <div className="sd-stat-value"><AnimatedNumber value={value} format={format} mode={numberMode} scope={scope} stale={stale} /></div>
      {note && <div className="sd-stat-note">{note}</div>}
    </Card>
  </Reveal>
})

function RoleBoard({ rows, selected, onSelect, scope, stale, numberMode }: { rows: readonly CombatRowModel[]; selected: string; onSelect: (key: string) => void; scope: string; stale: boolean; numberMode: "tween" | "roll" }) {
  const format = useFormatters()
  const nameOf = useRoleNames()
  const ordered = useMemo(() => [...rows].sort((a, b) => a.rank - b.rank), [rows])
  return <StackList items={ordered} keyOf={row => row.key} rowHeight={58} gap={6} renderItem={row => <div className="sd-role" data-selected={row.key === selected} data-element={roleElement(row.roleId)} role="button" tabIndex={0} onClick={() => onSelect(row.key)} onKeyDown={event => { if (event.key === "Enter" || event.key === " ") onSelect(row.key) }}>
    <CharacterAvatar roleId={row.roleId} elementId={roleElement(row.roleId)} size={42} fallback={row.kind === "vehicle" ? <Icon name="bolt" size={18} /> : "?"} />
    <div className="sd-role-main">
      <div className="sd-role-line"><strong className="ww-truncate">{nameOf(row)}</strong><span className="ww-num sd-role-dps"><AnimatedNumber value={row.dps} format={format.integer} mode={numberMode} scope={scope} stale={stale} /></span></div>
      <DamageBar share={row.share} scope={scope} />
      <div className="sd-role-line sd-role-sub"><span className="ww-num"><AnimatedNumber value={row.damage} format={format.integer} mode="tween" scope={scope} stale={stale} /></span><span className="ww-num">{row.share === undefined ? "—" : format.percent(row.share)}</span></div>
    </div>
  </div>} />
}

const SkillTable = memo(function SkillTable({ rows, color }: { rows: MockSkillRow[]; color: string }) {
  const { t, i18n } = useTranslation("showcase")
  const format = useFormatters()
  return <table className="sd-skills" style={{ "--ww-series": color } as React.CSSProperties}>
    <thead><tr><th>{t("dashboard.skill")}</th><th>{t("combat.damage")}</th><th>{t("dashboard.hits")}</th><th>{t("combat.share")}</th></tr></thead>
    <tbody>{rows.map(row => <tr key={row.skillId}>
      <td><span className="sd-skill-name"><img src={gameAssetUrl("skill", row.skillId)} alt="" decoding="async" onError={event => { event.currentTarget.style.visibility = "hidden" }} /><span className="ww-truncate">{row.names[i18n.language] ?? row.names.en ?? `#${row.skillId}`}</span></span></td>
      <td className="ww-num"><AnimatedNumber value={row.damage} format={format.integer} /></td>
      <td className="ww-num">{format.integer(row.hits)}</td>
      <td><div className="sd-skill-share"><DamageBar share={row.share} /><span className="ww-num">{format.percent(row.share)}</span></div></td>
    </tr>)}</tbody>
  </table>
})

export function DashboardSection({ controls, numberMode, theme }: { controls: FeedControls; numberMode: "tween" | "roll"; theme: ThemeId }) {
  const { t } = useTranslation("showcase")
  const format = useFormatters()
  const nameOf = useRoleNames()
  const { snapshot, history, skillsOf } = controls.output
  const encounter = snapshot.view?.encounters.current
  const rows = useMemo(() => encounter ? combatRows(encounter) : [], [encounter])
  const stale = snapshotStale(snapshot, controls.now) || controls.paused
  const scope = encounter?.encounterId ?? "none"
  const [selectedKey, setSelected] = useState("")
  const [tab, setTab] = useState<"skills" | "chart">("skills")
  const selected = rows.find(row => row.key === selectedKey) ?? rows[0]
  useEffect(() => { if (selected && selected.key !== selectedKey) setSelected(selected.key) }, [selected, selectedKey])
  const usable = encounter?.dps.status === "available" || encounter?.dps.status === "lower_bound"
  // History changes once per second; the memo keeps the chart from re-rendering at the publish rate.
  const historyKey = [...history.values()].map(series => series.length).join() + (history.values().next().value?.at(-1) ?? "")
  const series = useMemo<ChartSeries[]>(() => rows.filter(row => row.kind === "role").sort((a, b) => (a.roleId ?? 0) - (b.roleId ?? 0)).map(row => {
    const values = history.get(row.roleId ?? 0) ?? []
    return { key: row.key, label: nameOf(row), color: roleColor(row.roleId), values: Array.from({ length: historySlots }, (_, slot) => values[slot] ?? null) }
  }), [historyKey, rows.length, nameOf])
  const skills = selected?.roleId !== undefined ? skillsOf(selected.roleId) : []
  return <div className="sd-stage ww-has-backdrop"><ThemeBackdrop theme={theme} intensity="art" /><div className="sd-layout">
    <Reveal className="sd-top">
      <div>
        <div className="ww-caps">{t("dashboard.encounter", { id: encounter?.sequence ?? "—" })}</div>
        <h2 className="sd-title">{t("dashboard.title")}</h2>
      </div>
      <div className="sd-top-right">
        <Chip tone={stale ? "warn" : encounter?.integrity.status === "INTACT" ? "good" : "critical"} dot>{stale ? t("combat.state.stale") : encounter?.integrity.status ?? t("combat.state.waiting")}</Chip>
        <span className="sd-clock ww-num"><Icon name="clock" size={14} />{format.clock(encounter?.elapsedSeconds)}</span>
        <Button size="sm" onClick={() => controls.apply(feed => feed.resetEncounter())}><Icon name="reset" size={13} />{t("controls.reset")}</Button>
      </div>
    </Reveal>
    <div className="sd-stats">
      <StatCard index={0} hero icon="bolt" label={t("combat.teamDps")} value={usable ? encounter?.dps.value : undefined} format={format.integer} note={t("dashboard.dpsNote")} scope={scope} stale={stale} numberMode={numberMode} />
      <StatCard index={1} icon="spark" label={t("combat.totalDamage")} value={encounter?.totals.damageDealt} format={format.integer} note={t("dashboard.damageNote")} scope={scope} stale={stale} numberMode={numberMode} />
      <StatCard index={2} icon="shield" label={t("dashboard.taken")} value={encounter?.totals.damageTaken} format={format.integer} note={t("dashboard.takenNote")} scope={scope} stale={stale} numberMode={numberMode} />
      <StatCard index={3} icon="list" label={t("dashboard.events")} value={snapshot.view?.roles.reduce((sum, role) => sum + role.outgoingEvents, 0)} format={format.integer} note={t("dashboard.eventsNote")} scope={scope} stale={stale} numberMode={numberMode} />
    </div>
    <div className="sd-main">
      <Reveal index={4} className="sd-board-wrap">
        <Card className="sd-board">
          <div className="sd-card-head"><h3>{t("dashboard.ranking")}</h3></div>
          <RoleBoard rows={rows} selected={selected?.key ?? ""} onSelect={setSelected} scope={scope} stale={stale} numberMode={numberMode} />
          {selected?.roleId !== undefined && <ContentSwitch contentKey={selected.key} className="sd-portrait"><img src={gameAssetUrl("character-portrait", selected.roleId)} alt="" decoding="async" onError={event => { event.currentTarget.style.display = "none" }} /></ContentSwitch>}
        </Card>
      </Reveal>
      <Reveal index={5} className="sd-detail-wrap">
        <Card className="sd-chart-card">
          <div className="sd-card-head"><h3>{t("dashboard.timeline")}</h3><Chip tone="accent">{t("dashboard.gapChip")}</Chip></div>
          <LiveChart series={series} slots={historySlots} height={190} formatValue={format.compact} formatSlot={slot => format.clock(slot)} emptyLabel={t("dashboard.chartEmpty")} ariaLabel={t("dashboard.timeline")} />
        </Card>
        <Card className="sd-detail">
          <div className="sd-card-head">
            <h3>{selected ? nameOf(selected) : "—"}</h3>
            <Segmented variant="underline" label={t("dashboard.breakdown")} value={tab} onChange={setTab} options={[{ value: "skills", label: t("dashboard.skills") }, { value: "chart", label: t("dashboard.contribution") }]} />
          </div>
          <ContentSwitch contentKey={`${tab}-${selected?.key ?? ""}`}>
            {tab === "skills"
              ? <SkillTable rows={skills} color={roleColor(selected?.roleId)} />
              : <div className="sd-contribution" data-element={roleElement(selected?.roleId)}>
                  <div><span>{t("dashboard.direct")}</span><b className="ww-num">{format.percent(0.92)}</b></div><DamageBar share={0.92} />
                  <div><span>{t("dashboard.summon")}</span><b className="ww-num">{format.percent(0.08)}</b></div><DamageBar share={0.08} />
                </div>}
          </ContentSwitch>
        </Card>
      </Reveal>
    </div>
  </div></div>
}
