import { useCallback, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, CombatPanel, Icon, Segmented, Slider, type CombatState } from "@/design"
import { combatRows, type CombatRowModel } from "@/lib/combat-view"
import { snapshotStale } from "@/lib/effects"
import { dataState } from "@/lib/snapshot"
import type { FeedControls } from "../useMockFeed"
import { roleElement, useFormatters, useRoleNames } from "../format"
import { masterRoles } from "../master-fixture.ts"

/** Maps engine trust states onto the footer band. Stale or untrusted data is labeled, never animated as live. */
export function useCombatPanelProps(controls: FeedControls, numberMode: "tween" | "roll", alpha?: number) {
  const { t } = useTranslation("showcase")
  const format = useFormatters()
  const nameOf = useRoleNames()
  const { snapshot } = controls.output
  const view = snapshot.view
  const encounter = view?.encounters.current
  const rows = useMemo(() => encounter ? combatRows(encounter) : [], [encounter])
  const stale = snapshotStale(snapshot, controls.now) || controls.paused
  const trust = dataState(snapshot)
  const state: CombatState = trust === "untrusted" ? "untrusted" : stale ? "stale" : !encounter ? "waiting" : encounter.state === "ACTIVE" ? "combat" : "ended"
  const usable = encounter?.dps.status === "available" || encounter?.dps.status === "lower_bound"
  const elementOf = useCallback((row: CombatRowModel) => roleElement(row.roleId), [])
  const subtitleOf = useCallback((row: CombatRowModel) => row.kind === "vehicle" ? t("combat.vehicleNote") : row.damageTaken > 0 ? t("combat.taken", { value: format.compact(row.damageTaken) }) : undefined, [t, format])
  return {
    rows, elementOf, nameOf, subtitleOf,
    totalDamage: encounter?.totals.damageDealt,
    teamDps: usable ? encounter?.dps.value : undefined,
    time: format.clock(encounter?.elapsedSeconds),
    state,
    stateLabel: t(`combat.state.${state}`),
    note: encounter && encounter.unassignedRole.outgoingEvents > 0 ? t("combat.unassignedNote", { value: format.integer(encounter.unassignedRole.damageDealt) }) : undefined,
    labels: { totalDamage: t("combat.totalDamage"), teamDps: t("combat.teamDps"), member: t("combat.member"), dps: t("combat.dps"), damage: t("combat.damage"), share: t("combat.share"), empty: t("combat.empty") },
    formatDps: format.integer,
    formatDamage: format.integer,
    formatShare: format.percent,
    numberMode,
    scope: encounter?.encounterId ?? "none",
    alpha,
  }
}

export function CombatSection({ controls, numberMode, setNumberMode }: { controls: FeedControls; numberMode: "tween" | "roll"; setNumberMode: (mode: "tween" | "roll") => void }) {
  const { t, i18n } = useTranslation("showcase")
  const [alpha, setAlpha] = useState(0.74)
  const props = useCombatPanelProps(controls, numberMode, alpha)
  const { apply, publishMs, setPublishMs, paused, setPaused } = controls
  const active = props.rows.filter(row => row.kind === "role")
  const nameOfRole = (roleId: number) => {
    const names = masterRoles.find(role => role.roleId === roleId)?.names as Readonly<Record<string, string>> | undefined
    return names?.[i18n.language] ?? names?.en ?? String(roleId)
  }
  return <div className="sc-demo">
    <div className="sc-stage" data-backdrop="combat">
      <div className="sc-stage-overlay" style={{ width: 470 }}><CombatPanel {...props} /></div>
    </div>
    <aside className="sc-controls">
      <h3>{t("controls.dataEvents")}</h3>
      <div className="sc-button-grid">
        {active.map(row => <Button key={row.key} size="sm" onClick={() => apply(feed => feed.burst(row.roleId ?? 0))}><Icon name="bolt" size={13} />{t("controls.burst", { name: nameOfRole(row.roleId ?? 0) })}</Button>)}
        <Button size="sm" onClick={() => apply(feed => feed.swapRanks())}>{t("controls.swap")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.replaceRole())}>{t("controls.replaceRole")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.toggleVehicle())}>{t("controls.vehicle")}</Button>
      </div>
      <h3>{t("controls.lifecycle")}</h3>
      <div className="sc-button-grid">
        <Button size="sm" variant="primary" onClick={() => apply(feed => feed.resetEncounter())}><Icon name="reset" size={13} />{t("controls.reset")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.endEncounter())}>{t("controls.end")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.setIdle())}>{t("controls.idle")}</Button>
        <Button size="sm" onClick={() => setPaused(!paused)}><Icon name={paused ? "play" : "pause"} size={13} />{t(paused ? "controls.resume" : "controls.stall")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.toggleUntrusted())}>{t("controls.untrusted")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.toggleUnassigned())}>{t("controls.unassigned")}</Button>
      </div>
      <h3>{t("controls.publishRate")}</h3>
      <Segmented label={t("controls.publishRate")} value={String(publishMs)} options={[{ value: "100", label: "100 ms" }, { value: "250", label: "250 ms" }, { value: "500", label: "500 ms" }, { value: "1000", label: "1 s" }]} onChange={value => setPublishMs(Number(value))} />
      <h3>{t("controls.numberStyle")}</h3>
      <Segmented label={t("controls.numberStyle")} value={numberMode} options={[{ value: "tween", label: t("controls.tween") }, { value: "roll", label: t("controls.roll") }]} onChange={setNumberMode} />
      <h3>{t("controls.opacity")}</h3>
      <div className="sc-slider-row"><Slider label={t("controls.opacity")} min={35} max={95} value={Math.round(alpha * 100)} onChange={value => setAlpha(value / 100)} /><span className="ww-num">{Math.round(alpha * 100)}%</span></div>
      <p className="sc-note">{t("combat.note")}</p>
    </aside>
  </div>
}
