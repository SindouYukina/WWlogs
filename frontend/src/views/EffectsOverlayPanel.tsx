import { useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { EffectChip, EffectGroupMark, EffectTile, ListeningStatus, tileSeconds } from "@/design"
import { isFrozen, type CombatData } from "@/lib/combat-data"
import { effectRow, type EffectRowModel } from "@/lib/combat-view"
import { effectGroupOf, effectRemaining, fitEffectGrid, type EffectGridLayout } from "@/lib/effects"
import type { Effect } from "@/lib/snapshot"
import { useListeningLabels } from "@/lib/useListeningLabels"

interface EffectView {
  effect: Effect
  row: EffectRowModel
}

const px = (style: CSSStyleDeclaration, name: string) => Number.parseFloat(style.getPropertyValue(name)) || 0

/** Only an effect the game draws with an icon of its own becomes a tile; every other effect is a chip. */
const isTile = (effect: Effect) => effect.hudIcon === true

/**
 * Measures the panel the tiles are laid out in. Tile and group sizes come from the design tokens, so a token or a scaled
 * window changes the layout and the count of hidden effects together. It only runs when the window or the head changes
 * size, so a countdown never measures anything, and a hidden window, which does not resize, does no work.
 */
function useGridLayout(enabled: boolean) {
  const root = useRef<HTMLDivElement>(null)
  const head = useRef<HTMLDivElement>(null)
  const [layout, setLayout] = useState<EffectGridLayout | undefined>(undefined)
  useLayoutEffect(() => {
    const panel = root.current
    const title = head.current
    if (!enabled || !panel || !title) return
    const measure = () => {
      const style = getComputedStyle(panel)
      const box = panel.getBoundingClientRect()
      // Rects are in zoomed viewport pixels while the tokens are not. The display scale zooms the whole surface, so the
      // layout is computed in the panel's own pixels: a window scaled together with its content holds the same tiles.
      const zoom = panel.currentCSSZoom || 1
      const next: EffectGridLayout = {
        available: Math.floor(box.height / zoom + 0.01),
        width: Math.floor(box.width / zoom + 0.01),
        head: Math.ceil(title.getBoundingClientRect().height / zoom - 0.01),
        sectionGap: Number.parseFloat(style.rowGap) || 0,
        hint: px(style, "--ww-row-effects-omitted"),
        tile: px(style, "--ww-effect-tile"),
        tileGap: px(style, "--ww-effect-tile-gap"),
        chip: px(style, "--ww-effect-chip"),
        chipGap: px(style, "--ww-effect-chip-gap"),
        blockGap: px(style, "--ww-effect-block-gap"),
        groupHead: px(style, "--ww-effect-group-head"),
        groupHeadGap: px(style, "--ww-effect-group-head-gap"),
        groupGap: px(style, "--ww-effect-group-gap"),
      }
      setLayout(current => current && Object.keys(next).every(key => current[key as keyof EffectGridLayout] === next[key as keyof EffectGridLayout]) ? current : next)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(panel)
    observer.observe(title)
    return () => observer.disconnect()
  }, [enabled])
  return { root, head, layout }
}

export interface EffectsOverlayPanelProps {
  data: CombatData
  /** Surface opacity, 0 to 1. */
  alpha?: number
  /** Fits the tiles to the size of the enclosing window. A click-through window cannot be scrolled, so tiles that do not fit become a count. */
  fitHeight?: boolean
}

/**
 * The one Effects Overlay. Like the game's HUD it shows effects as icons with their stacks and time, grouped by holder,
 * and never as names or IDs: the game draws no effect names, and most effects have none. Effects the game draws without
 * an icon keep a neutral mark. Tiles pass the production trust gate: a stale or suspect snapshot yields no countdown.
 */
export function EffectsOverlayPanel({ data, alpha, fitHeight = false }: EffectsOverlayPanelProps) {
  const { t } = useTranslation(["overlay", "common"])
  const describe = useListeningLabels()
  const { snapshot, connection, now } = data
  const frozen = isFrozen(data)
  const view = snapshot?.view
  // The engine lists only display-eligible effects in priority order and counts the rest; the panel neither filters
  // nor reorders them, it only groups the drawn prefix by holder.
  const listed = view?.effects.active ?? []
  const eligible = view?.effects.counts?.eligible ?? listed.length
  const unclassified = view?.effects.counts?.unclassified ?? 0
  const { root, head, layout } = useGridLayout(fitHeight)
  const grid = useMemo(() => fitEffectGrid(listed, effectGroupOf, isTile, eligible, unclassified, fitHeight ? layout : undefined), [listed, eligible, unclassified, fitHeight, layout])
  const groups = useMemo(() => {
    const viewOf = (effect: Effect): EffectView => {
      const remaining = frozen ? undefined : effectRemaining(effect, snapshot, connection, now)
      return { effect, row: { ...effectRow(effect, remaining), stale: frozen } }
    }
    return grid.groups.map(({ group, tiles, chips }) => ({
      group,
      label: group.kind === "role" ? holderLabel([...tiles, ...chips][0]!) : t(group.kind === "enemy" ? "targetEnemy" : group.kind === "team" ? "holderTeam" : "holderUnresolved"),
      tiles: tiles.map(viewOf),
      chips: chips.map(viewOf),
    }))
  }, [grid, frozen, snapshot, connection, now, t])
  function holderLabel(effect: Effect) {
    if (effect.holderName && effect.holderNameStatus === "VERIFIED") return effect.holderName
    return t("unknownRole", { ns: "common", id: effect.holderRoleId })
  }
  const tileLabel = (row: EffectRowModel) => [
    t(row.hudIcon ? "tileGameIcon" : "tileNoIcon"),
    row.countdown.kind !== "unknown" ? t("tileRemaining", { seconds: tileSeconds(row.countdown.remaining) }) : undefined,
    row.stackCount !== undefined && row.stackCount > 1 ? t("tileStacks", { count: row.stackCount }) : undefined,
  ].filter(Boolean).join(t("tileSeparator"))
  const { overflow } = grid
  const listening = snapshot?.listening
  const text = listening ? describe(listening) : null
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  return <div ref={root} className="ww-effects" style={style} data-field="effects" data-fit={fitHeight}>
    <div ref={head} className="ww-effects-head"><span>{t("effects")}</span><span className="ww-effects-head-end">{listening && text && <span data-field="listening" data-state={listening.state} data-reason={listening.reason}><ListeningStatus state={listening.state} label={text.label} detail={text.detail} variant="dot" /></span>}<span className="ww-num" data-field="effects-count" data-count={eligible}>{eligible}</span></span></div>
    {grid.visible === 0 && overflow === 0 && <div className="ww-combat-empty ww-effects-empty">{t("noEffects")}</div>}
    {groups.length > 0 && <div className="ww-effect-groups" data-field="effect-groups">
      {groups.map(({ group, label, tiles, chips }) => <div key={group.key} className="ww-effect-group" data-group={group.kind} data-role={group.roleId}>
        <EffectGroupMark kind={group.kind} roleId={group.roleId} label={label} />
        <div className="ww-effect-group-body">
          {tiles.length > 0 && <div className="ww-effect-tiles">{tiles.map(({ effect, row }) => <EffectTile key={effect.key} effect={row} label={tileLabel(row)} tickMs={data.tickMs} />)}</div>}
          {chips.length > 0 && <div className="ww-effect-chips">{chips.map(({ effect, row }) => <EffectChip key={effect.key} effect={row} label={tileLabel(row)} tickMs={data.tickMs} />)}</div>}
        </div>
      </div>)}
    </div>}
    {(overflow > 0 || unclassified > 0) && <div className="ww-effects-omitted" data-field="effects-omitted" data-count={overflow} data-unclassified={unclassified}>{[overflow > 0 && t("omitted", { count: overflow }), unclassified > 0 && t("unclassified", { count: unclassified })].filter(Boolean).join(" · ")}</div>}
  </div>
}
