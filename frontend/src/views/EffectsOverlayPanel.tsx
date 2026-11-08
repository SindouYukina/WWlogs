import { useCallback, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { EffectCell, EffectMore, ListeningStatus, StackRow, effectFill, effectSeconds, lowEffectSeconds } from "@/design"
import { isFrozen, type CombatData } from "@/lib/combat-data"
import { countdownFraction, effectRow, type EffectRowModel } from "@/lib/combat-view"
import { effectCellSize, effectRemaining, fitEffectStrip, orderEffects, stripCellWidth, type EffectCellSize, type EffectStripLayout } from "@/lib/effects"
import { useListeningLabels } from "@/lib/useListeningLabels"

type StripItem =
  | { kind: "effect"; key: string; size: EffectCellSize; row: EffectRowModel }
  | { kind: "more"; key: "more"; count: number }

const px = (style: CSSStyleDeclaration, name: string) => Number.parseFloat(style.getPropertyValue(name)) || 0

const stripKey = (item: StripItem) => item.key

/**
 * Measures the strip. Cell sizes come from the design tokens, so a token or a scaled window changes the layout and the
 * count of hidden effects together. It only runs when the strip changes size, so a countdown never measures anything.
 */
function useStripLayout() {
  const strip = useRef<HTMLDivElement>(null)
  const [layout, setLayout] = useState<EffectStripLayout | undefined>(undefined)
  useLayoutEffect(() => {
    const element = strip.current
    if (!element) return
    const measure = () => {
      const style = getComputedStyle(element)
      // Rects are in zoomed viewport pixels while the tokens are not. The display scale zooms the whole surface, so the
      // layout is computed in the strip's own pixels: a window scaled together with its content holds the same cells.
      const zoom = element.currentCSSZoom || 1
      const next: EffectStripLayout = {
        width: Math.floor(element.getBoundingClientRect().width / zoom + 0.01),
        icon: px(style, "--ww-effect-icon"),
        gauge: px(style, "--ww-effect-gauge"),
        gap: px(style, "--ww-effect-gap"),
        more: px(style, "--ww-effect-more"),
      }
      setLayout(current => current && Object.keys(next).every(key => current[key as keyof EffectStripLayout] === next[key as keyof EffectStripLayout]) ? current : next)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  return { strip, layout }
}

export interface EffectsOverlayPanelProps {
  data: CombatData
  /** Surface opacity, 0 to 1. */
  alpha?: number
}

/**
 * The one Effects Overlay: one line of effects, soonest to end first, as icons with their stacks and time and never as
 * names or IDs. The holder, source and instance of every effect stay in the engine's data; this view only stops grouping
 * by them. Cells pass the production trust gate: a stale or suspect snapshot yields no countdown.
 */
export function EffectsOverlayPanel({ data, alpha }: EffectsOverlayPanelProps) {
  const { t } = useTranslation(["overlay", "common"])
  const describeListening = useListeningLabels()
  const { snapshot, connection, now } = data
  const frozen = isFrozen(data)
  const view = snapshot?.view
  // The engine lists only display-eligible effects and counts the rest; the panel orders them but never filters them.
  const listed = view?.effects.active ?? []
  const eligible = view?.effects.counts?.eligible ?? listed.length
  const unclassified = view?.effects.counts?.unclassified ?? 0
  const { strip, layout } = useStripLayout()
  const ordered = useMemo(() => orderEffects(listed), [listed])
  const { visible, overflow } = useMemo(() => fitEffectStrip(ordered, effectCellSize, eligible, layout), [ordered, eligible, layout])
  const items = useMemo<StripItem[]>(() => {
    const cells = ordered.slice(0, visible).map<StripItem>(effect => ({
      kind: "effect",
      key: effect.key,
      size: effectCellSize(effect),
      row: { ...effectRow(effect, frozen ? undefined : effectRemaining(effect, snapshot, connection, now)), stale: frozen },
    }))
    return overflow > 0 ? [...cells, { kind: "more", key: "more", count: overflow }] : cells
  }, [ordered, visible, overflow, frozen, snapshot, connection, now])
  const widthOf = useCallback((item: StripItem) => !layout ? 0 : item.kind === "more" ? layout.more : stripCellWidth(item.size, layout), [layout])
  const describe = useCallback((seconds: string | undefined, stacks: number | undefined, hasIcon: boolean) => [
    t(hasIcon ? "tileGameIcon" : "tileNoIcon"),
    seconds !== undefined ? t("tileRemaining", { seconds }) : undefined,
    stacks !== undefined ? t("tileStacks", { count: stacks }) : undefined,
  ].filter(Boolean).join(t("tileSeparator")), [t])
  const renderItem = (item: StripItem) => {
    if (item.kind === "more") return <EffectMore count={item.count} label={[t("omitted", { count: item.count }), unclassified > 0 && t("unclassified", { count: unclassified })].filter(Boolean).join(" · ")} />
    const { row, size } = item
    const remaining = row.countdown.kind === "unknown" ? undefined : row.countdown.remaining
    const fraction = countdownFraction(row.countdown)
    return <EffectCell effectId={row.effectId} size={size} icon={!row.hudIcon ? "none" : row.hudIconGeneric ? "generic" : "game"} kind={row.kind} elementId={row.elementId} fill={fraction === undefined ? undefined : effectFill(fraction)} untimed={row.countdown.kind === "unknown"} seconds={remaining === undefined ? undefined : effectSeconds(remaining)} low={remaining !== undefined && remaining > 0 && remaining <= lowEffectSeconds} stackCount={row.stackCount} stale={row.stale} describe={describe} />
  }
  const listening = snapshot?.listening
  const text = listening ? describeListening(listening) : null
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  return <div className="ww-effects" style={style} data-field="effects" data-count={eligible} data-visible={visible} data-overflow={overflow} data-unclassified={unclassified}>
    {listening && text && <span className="ww-effects-status" data-field="listening" data-state={listening.state} data-reason={listening.reason}><ListeningStatus state={listening.state} label={text.label} detail={text.detail} variant="dot" /></span>}
    <div ref={strip} className="ww-effects-strip" data-field="effect-strip">
      {eligible === 0
        ? <div className="ww-combat-empty ww-effects-empty">{t("noEffects")}</div>
        : <StackRow items={items} keyOf={stripKey} widthOf={widthOf} gap={layout?.gap} renderItem={renderItem} />}
    </div>
  </div>
}
