import { useCallback, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { EffectCell, EffectMore, StackRow, effectFill, effectSeconds, lowEffectSeconds } from "@/design"
import { isFrozen, type CombatData } from "@/lib/combat-data"
import { countdownFraction, effectRow, type EffectRowModel } from "@/lib/combat-view"
import { effectRemaining, effectsOverlayMode, fitEffectStrip, type EffectStripLayout } from "@/lib/effects"
import type { Connection, Effect, Snapshot } from "@/lib/snapshot"

type StripItem =
  | { kind: "effect"; key: string; row: EffectRowModel }
  | { kind: "more"; key: "more"; count: number }

const px = (style: CSSStyleDeclaration, name: string) => Number.parseFloat(style.getPropertyValue(name)) || 0

const stripKey = (item: StripItem) => item.key

const noEffects: readonly Effect[] = []

/**
 * Measures the rows. Cell sizes come from the design tokens, so a token or a scaled window changes the layout and the
 * count of hidden effects together. It only runs when the rows change size, so a countdown never measures anything.
 */
function useStripLayout() {
  const rows = useRef<HTMLDivElement>(null)
  const [layout, setLayout] = useState<EffectStripLayout | undefined>(undefined)
  useLayoutEffect(() => {
    const element = rows.current
    if (!element) return
    const measure = () => {
      const style = getComputedStyle(element)
      // Rects are in zoomed viewport pixels while the tokens are not. The display scale zooms the whole surface, so the
      // layout is computed in the rows' own pixels: a window scaled together with its content holds the same cells.
      const zoom = element.currentCSSZoom || 1
      const next: EffectStripLayout = {
        width: Math.floor(element.getBoundingClientRect().width / zoom + 0.01),
        icon: px(style, "--ww-effect-icon"),
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
  return { rows, layout }
}

interface EffectRowProps {
  polarity: "buff" | "debuff"
  effects: readonly Effect[]
  /** Cells of this row, including any the engine's bound left out. */
  cells: number
  layout: EffectStripLayout | undefined
  frozen: boolean
  snapshot: Snapshot | null
  connection: Connection
  now: number
}

/** One line of the Overlay. An empty row keeps its height and draws nothing, so the other row never moves. */
function EffectRow({ polarity, effects, cells, layout, frozen, snapshot, connection, now }: EffectRowProps) {
  const { t } = useTranslation(["overlay", "common"])
  const { visible, overflow } = useMemo(() => fitEffectStrip(effects, cells, layout), [effects, cells, layout])
  const items = useMemo<StripItem[]>(() => {
    const drawn = effects.slice(0, visible).map<StripItem>(effect => ({
      kind: "effect",
      key: effect.key,
      row: { ...effectRow(effect, frozen ? undefined : effectRemaining(effect, snapshot, connection, now)), stale: frozen },
    }))
    return overflow > 0 ? [...drawn, { kind: "more", key: "more", count: overflow }] : drawn
  }, [effects, visible, overflow, frozen, snapshot, connection, now])
  const widthOf = useCallback((item: StripItem) => !layout ? 0 : item.kind === "more" ? layout.more : layout.icon, [layout])
  const describe = useCallback((target: EffectRowModel["target"], seconds: string | undefined, stacks: number | undefined, hasIcon: boolean) => [
    t(hasIcon ? "tileGameIcon" : "tileNoIcon"),
    target === "enemy" ? t("tileOnEnemy") : undefined,
    seconds !== undefined ? t("tileRemaining", { seconds }) : undefined,
    stacks !== undefined ? t("tileStacks", { count: stacks }) : undefined,
  ].filter(Boolean).join(t("tileSeparator")), [t])
  const renderItem = (item: StripItem) => {
    if (item.kind === "more") return <EffectMore count={item.count} label={t("omitted", { count: item.count })} />
    const { row } = item
    const remaining = row.countdown.kind === "unknown" ? undefined : row.countdown.remaining
    const fraction = countdownFraction(row.countdown)
    return <EffectCell effectId={row.effectId} target={row.target} elementId={row.elementId} fill={fraction === undefined ? undefined : effectFill(fraction)} untimed={row.countdown.kind === "unknown"} seconds={remaining === undefined ? undefined : effectSeconds(remaining)} low={remaining !== undefined && remaining <= lowEffectSeconds} stackCount={row.stackCount} stale={row.stale} describe={(seconds, stacks, hasIcon) => describe(row.target, seconds, stacks, hasIcon)} />
  }
  return <div className="ww-effects-strip" data-field="effect-row" data-polarity={polarity} data-count={cells} data-visible={visible} data-overflow={overflow}>
    {cells > 0 && <StackRow items={items} keyOf={stripKey} widthOf={widthOf} gap={layout?.gap} renderItem={renderItem} label={t(polarity === "buff" ? "rowBuffs" : "rowDebuffs")} />}
  </div>
}

export interface EffectsOverlayPanelProps {
  data: CombatData
  /** Surface opacity, 0 to 1. */
  alpha?: number
  /** Edit Mode: the window is being placed or sized, so an empty strip shows a preview of its area. */
  editing?: boolean
}

/**
 * The one Effects Overlay: BUFF on the first line and DEBUFF on the second, each soonest to end first, as icons with their
 * stacks and time and never as names or IDs. The holder, source and instance of every effect stay in the engine's data;
 * this view only stops grouping by them. Cells pass the production trust gate: a stale or suspect snapshot yields no
 * countdown. The listening status lives on the Combat Overlay; the two are used together, so it is not repeated here.
 * Outside Edit Mode an Overlay without cells draws nothing at all (effectsOverlayMode).
 */
export function EffectsOverlayPanel({ data, alpha, editing = false }: EffectsOverlayPanelProps) {
  const { t } = useTranslation(["overlay", "common"])
  const { snapshot, connection, now } = data
  const view = snapshot?.view
  const frozen = isFrozen(data) || view?.effects.clockStalled === true
  // The engine classifies, filters and orders both rows; this view only lays them out.
  const buffs = view?.effects.buffs
  const debuffs = view?.effects.debuffs
  const buffCells = (buffs?.effects.length ?? 0) + (buffs?.truncated ?? 0)
  const debuffCells = (debuffs?.effects.length ?? 0) + (debuffs?.truncated ?? 0)
  const cells = buffCells + debuffCells
  const mode = effectsOverlayMode(cells, editing)
  const { rows, layout } = useStripLayout()
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  // The rows stay mounted in every mode, so their measured width is ready when the first effect arrives.
  return <div className="ww-effects" style={style} data-field="effects" data-mode={mode} data-count={cells}>
    <div ref={rows} className="ww-effects-rows" data-field="effect-rows">
      {mode === "edit-preview" && cells === 0
        ? <div className="ww-combat-empty ww-effects-preview" data-field="effects-preview"><span className="ww-truncate">{t("preview")}</span></div>
        : <>
          <EffectRow polarity="buff" effects={buffs?.effects ?? noEffects} cells={buffCells} layout={layout} frozen={frozen} snapshot={snapshot} connection={connection} now={now} />
          <EffectRow polarity="debuff" effects={debuffs?.effects ?? noEffects} cells={debuffCells} layout={layout} frozen={frozen} snapshot={snapshot} connection={connection} now={now} />
        </>}
    </div>
  </div>
}
