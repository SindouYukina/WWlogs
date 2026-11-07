import { useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { BuffStatusItem, ListeningStatus, StackList } from "@/design"
import { isFrozen, type CombatData } from "@/lib/combat-data"
import { effectRow, type EffectRowModel } from "@/lib/combat-view"
import { effectCapacity, effectRemaining, relevantEffect, visibleEffects, type EffectCapacity } from "@/lib/effects"
import { useFormatters } from "@/lib/format"
import type { Effect } from "@/lib/snapshot"
import { useListeningLabels } from "@/lib/useListeningLabels"

interface EffectView {
  effect: Effect
  row: EffectRowModel
}

const keyOf = (item: EffectView) => item.effect.key

const rowHeight = 38
const rowGap = 5

/** For a panel that is not fitted to a window, such as a review stage that grows with its content. */
const unfittedCapacity: EffectCapacity = { all: 12, withHint: 12 }

/**
 * Measures how many whole rows the window can hold. It only runs when the window or the head changes size, so a countdown
 * never measures anything, and a hidden window, which does not resize, does no work.
 */
function useFittedCapacity(enabled: boolean) {
  const root = useRef<HTMLDivElement>(null)
  const head = useRef<HTMLDivElement>(null)
  const [capacity, setCapacity] = useState(unfittedCapacity)
  useLayoutEffect(() => {
    const panel = root.current
    const title = head.current
    if (!enabled || !panel || !title) return
    const measure = () => {
      const style = getComputedStyle(panel)
      const next = effectCapacity({
        available: Math.floor(panel.getBoundingClientRect().height),
        head: Math.ceil(title.getBoundingClientRect().height),
        row: rowHeight,
        rowGap,
        sectionGap: Number.parseFloat(style.rowGap) || 0,
        hint: Number.parseFloat(style.getPropertyValue("--ww-row-effects-omitted")) || 0,
      })
      setCapacity(current => current.all === next.all && current.withHint === next.withHint ? current : next)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(panel)
    observer.observe(title)
    return () => observer.disconnect()
  }, [enabled])
  return { root, head, capacity }
}

export interface EffectsOverlayPanelProps {
  data: CombatData
  /** Surface opacity, 0 to 1. */
  alpha?: number
  /** Fits the rows to the height of the enclosing window. A click-through window cannot be scrolled, so rows that do not fit become a count. */
  fitHeight?: boolean
}

/**
 * The one Effects Overlay. Rows pass the production trust gate: a stale or suspect snapshot yields no countdown. Polarity
 * and names are shown only as the engine and Master report them; an unverified effect keeps its raw ID.
 */
export function EffectsOverlayPanel({ data, alpha, fitHeight = false }: EffectsOverlayPanelProps) {
  const { t } = useTranslation(["overlay", "common"])
  const format = useFormatters()
  const describe = useListeningLabels()
  const { snapshot, connection, now } = data
  const frozen = isFrozen(data)
  const view = snapshot?.view
  const relevant = useMemo(() => (view?.effects.active ?? []).filter(relevantEffect), [view])
  const { root, head, capacity } = useFittedCapacity(fitHeight)
  const { visible, omitted } = visibleEffects(relevant.length, view?.effects.omitted ?? 0, capacity)
  const items = useMemo<EffectView[]>(() => relevant.slice(0, visible).map(effect => {
    const remaining = frozen ? undefined : effectRemaining(effect, snapshot, connection, now)
    return { effect, row: { ...effectRow(effect, remaining), stale: frozen } }
  }), [relevant, visible, frozen, snapshot, connection, now])
  const holder = (effect: Effect) => {
    if (effect.holderName && effect.holderNameStatus === "VERIFIED") return effect.holderName
    return effect.holderRoleId !== undefined ? t("unknownRole", { ns: "common", id: effect.holderRoleId }) : undefined
  }
  const listening = snapshot?.listening
  const text = listening ? describe(listening) : null
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  return <div ref={root} className="ww-effects" style={style} data-field="effects" data-fit={fitHeight}>
    <div ref={head} className="ww-effects-head"><span>{t("effects")}</span><span className="ww-effects-head-end">{listening && text && <span data-field="listening" data-state={listening.state} data-reason={listening.reason}><ListeningStatus state={listening.state} label={text.label} detail={text.detail} variant="dot" /></span>}<span className="ww-num">{relevant.length}</span></span></div>
    {items.length === 0 && omitted === 0 && <div className="ww-combat-empty ww-effects-empty">{t("noEffects")}</div>}
    <StackList items={items} keyOf={keyOf} rowHeight={rowHeight} gap={rowGap} renderItem={({ effect, row }) => {
      const verified = effect.termStatus === "VERIFIED" && effect.termName
      // A correlated glossary term is shown as a related term beside the raw ID, never as the effect's confirmed name.
      const related = effect.termStatus === "CORRELATED" ? effect.termName : undefined
      const meta = [related && t("relatedTerm", { term: related }), holder(effect)].filter(Boolean).join(" · ")
      const countdown = row.countdown
      return <BuffStatusItem effect={row} title={verified ? effect.termName! : t("unknownEffect", { id: effect.effectId ?? "—" })} unnamed={!verified} meta={meta} tickMs={data.tickMs} remainingText={countdown.kind === "unknown" ? "—" : `~${format.seconds(countdown.remaining)}`} />
    }} />
    {omitted > 0 && <div className="ww-effects-omitted" data-field="effects-omitted" data-count={omitted}>{t("omitted", { count: omitted })}</div>}
  </div>
}
