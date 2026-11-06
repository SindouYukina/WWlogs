import { useCallback, useMemo } from "react"
import { useTranslation } from "react-i18next"
import { CombatPanel, ListeningStatus, type CombatState } from "@/design"
import { displayedEncounter, isFrozen, usableDps, type CombatData } from "@/lib/combat-data"
import { combatRows, intervalRows, type CombatRowModel } from "@/lib/combat-view"
import { useFormatters, useRowNames } from "@/lib/format"
import { dataState } from "@/lib/snapshot"
import { useListeningLabels } from "@/lib/useListeningLabels"

const elementOf = (row: CombatRowModel) => row.elementId

/** Maps engine trust states onto the panel. Stale or untrusted data is labeled, never animated as live. */
function useCombatPanelProps(data: CombatData, numberMode: "tween" | "roll", alpha?: number) {
  const { t } = useTranslation(["overlay", "common"])
  const format = useFormatters()
  const nameOf = useRowNames()
  const view = data.snapshot?.view ?? null
  const encounter = displayedEncounter(view)
  const rows = useMemo(() => encounter ? combatRows(encounter) : view ? intervalRows(view) : [], [encounter, view])
  const frozen = isFrozen(data)
  const trust = dataState(data.snapshot)
  const state: CombatState = trust === "untrusted" ? "untrusted" : frozen ? "stale" : view?.encounters.current ? "combat" : encounter ? "ended" : "waiting"
  const dps = encounter?.dps ?? view?.statistics.encounterDps
  const lowerBound = dps?.status === "lower_bound"
  // A lower bound is never shown as an exact value.
  const formatDps = useMemo(() => lowerBound ? (value: number) => `≥ ${format.integer(value)}` : format.integer, [lowerBound, format])
  const subtitleOf = useCallback((row: CombatRowModel) => row.kind === "vehicle" ? t("combat.vehicleNote") : row.damageTaken > 0 ? t("combat.taken", { value: format.compact(row.damageTaken) }) : undefined, [t, format])
  const unassigned = encounter?.unassignedRole ?? view?.unassignedRole
  const attributionIncomplete = (unassigned?.outgoingEvents ?? 0) > 0 || (view?.statistics.unattributed.outgoingEvents ?? 0) > 0
  const diagnostics = data.snapshot?.service.app.profile === "development"
  return {
    rows, elementOf, nameOf, subtitleOf,
    totalDamage: encounter?.totals.damageDealt ?? view?.statistics.damageDealt,
    teamDps: usableDps(dps) ? dps?.value : undefined,
    time: format.clock(encounter?.elapsedSeconds),
    state,
    stateLabel: trust === "not_capturing" && state !== "untrusted" ? t("notCapturing", { ns: "common" }) : t(`combat.state.${state}`),
    note: !attributionIncomplete ? undefined : diagnostics ? t("combat.unassignedNote", { value: format.integer(unassigned?.damageDealt ?? 0) }) : t("attributionIncomplete", { ns: "common" }),
    labels: { totalDamage: t("combat.totalDamage"), teamDps: t("combat.teamDps"), member: t("combat.member"), dps: t("combat.dps"), damage: t("combat.damage"), share: t("combat.share"), empty: t("combat.empty") },
    formatDps,
    formatDamage: format.integer,
    formatShare: format.percent,
    numberMode,
    scope: encounter?.encounterId ?? "none",
    alpha,
  }
}

export interface CombatOverlayPanelProps {
  data: CombatData
  numberMode?: "tween" | "roll"
  /** Surface opacity, 0 to 1. */
  alpha?: number
}

/** The one Combat Overlay. The listening status comes from the same snapshot as the numbers, in its compact form. */
export function CombatOverlayPanel({ data, numberMode = "tween", alpha }: CombatOverlayPanelProps) {
  const describe = useListeningLabels()
  const props = useCombatPanelProps(data, numberMode, alpha)
  const listening = data.snapshot?.listening
  const text = listening ? describe(listening) : null
  return <div data-field="combat-overlay" data-state={props.state}><CombatPanel {...props} status={listening && text ? <span data-field="listening" data-state={listening.state} data-reason={listening.reason}><ListeningStatus state={listening.state} label={text.label} detail={text.detail} variant="compact" /></span> : undefined} /></div>
}
