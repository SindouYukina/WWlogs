import { useMemo, type CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { BuffStatusItem, ListeningStatus, StackList } from "@/design"
import { isFrozen, type CombatData } from "@/lib/combat-data"
import { effectRow, type EffectRowModel } from "@/lib/combat-view"
import { effectRemaining, relevantEffect } from "@/lib/effects"
import { useFormatters } from "@/lib/format"
import type { Effect } from "@/lib/snapshot"
import { useListeningLabels } from "@/lib/useListeningLabels"

interface EffectView {
  effect: Effect
  row: EffectRowModel
}

const keyOf = (item: EffectView) => item.effect.key

/** Bounds the list so a burst of effects cannot grow the window's work without limit. */
const maxVisibleEffects = 12

export interface EffectsOverlayPanelProps {
  data: CombatData
  /** Surface opacity, 0 to 1. */
  alpha?: number
}

/**
 * The one Effects Overlay. Rows pass the production trust gate: a stale or suspect snapshot yields no countdown. Polarity
 * and names are shown only as the engine and Master report them; an unverified effect keeps its raw ID.
 */
export function EffectsOverlayPanel({ data, alpha }: EffectsOverlayPanelProps) {
  const { t } = useTranslation(["overlay", "common"])
  const format = useFormatters()
  const describe = useListeningLabels()
  const { snapshot, connection, now } = data
  const frozen = isFrozen(data)
  const view = snapshot?.view
  const relevant = useMemo(() => (view?.effects.active ?? []).filter(relevantEffect), [view])
  const items = useMemo<EffectView[]>(() => relevant.slice(0, maxVisibleEffects).map(effect => {
    const remaining = frozen ? undefined : effectRemaining(effect, snapshot, connection, now)
    return { effect, row: { ...effectRow(effect, remaining), stale: frozen } }
  }), [relevant, frozen, snapshot, connection, now])
  const omitted = Math.max(0, relevant.length - maxVisibleEffects) + (view?.effects.omitted ?? 0)
  const holder = (effect: Effect) => {
    if (effect.holderName && effect.holderNameStatus === "VERIFIED") return effect.holderName
    return effect.holderRoleId !== undefined ? t("unknownRole", { ns: "common", id: effect.holderRoleId }) : undefined
  }
  const listening = snapshot?.listening
  const text = listening ? describe(listening) : null
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  return <div className="ww-effects" style={style} data-field="effects">
    <div className="ww-effects-head"><span>{t("effects")}</span><span className="ww-effects-head-end">{listening && text && <span data-field="listening" data-state={listening.state} data-reason={listening.reason}><ListeningStatus state={listening.state} label={text.label} detail={text.detail} variant="dot" /></span>}<span className="ww-num">{relevant.length}</span></span></div>
    {items.length === 0 && <div className="ww-combat-empty ww-effects-empty">{t("noEffects")}</div>}
    <StackList items={items} keyOf={keyOf} rowHeight={38} gap={5} renderItem={({ effect, row }) => {
      const verified = effect.termStatus === "VERIFIED" && effect.termName
      // A correlated glossary term is shown as a related term beside the raw ID, never as the effect's confirmed name.
      const related = effect.termStatus === "CORRELATED" ? effect.termName : undefined
      const meta = [related && t("relatedTerm", { term: related }), holder(effect)].filter(Boolean).join(" · ")
      const countdown = row.countdown
      return <BuffStatusItem effect={row} title={verified ? effect.termName! : t("unknownEffect", { id: effect.effectId ?? "—" })} unnamed={!verified} meta={meta} tickMs={data.tickMs} remainingText={countdown.kind === "unknown" ? "—" : `~${format.seconds(countdown.remaining)}`} />
    }} />
    {omitted > 0 && <div className="ww-effects-omitted">{t("omitted", { count: omitted })}</div>}
  </div>
}
