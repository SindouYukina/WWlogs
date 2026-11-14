import { memo, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { gameAssetUrl, type GameAssetKind } from "../assets"
import { barEntrance, motionReduced } from "../motion/tokens.ts"
import { useGameAsset } from "../useGameAsset"
import { AnimatedNumber } from "./AnimatedNumber"
import { Icon } from "./Icon"
import { StackList } from "./layout"
import type { CombatRowModel } from "@/lib/combat-view"

export interface DamageBarProps {
  /** 0..1. Undefined means the engine reported no share; the bar then draws a neutral hatch rather than a value. */
  share: number | undefined
  /** "track" is a thin meter; "row" fills its positioned parent as a background. */
  fill?: "track" | "row"
  /** A new scope snaps instead of animating from the previous encounter's share. */
  scope?: string
  /**
   * The bar's place in a list that plays the entrance: the bar grows from zero once, when it mounts, after this many stagger
   * steps (at most barEntrance.maxSteps). Only its value at mount counts, and the entrance plays to its end; later value
   * changes animate from the current length as usual. Without it, or under reduced motion, a bar appears at its value.
   */
  enter?: number
}

/** About one pixel on the widest bars. Smaller changes keep the bar settled, so a steady fight draws no bar frames. */
const shareQuantum = 1 / 480

/** Three transform-only layers: a fast fill, a slower trail that shows recent change, and a bright leading edge. */
export const DamageBar = memo(function DamageBar({ share, fill = "track", scope = "", enter }: DamageBarProps) {
  const previousScope = useRef(scope)
  const snap = previousScope.current !== scope
  useEffect(() => { previousScope.current = scope })
  // Decided once, at mount: a list that stops offering the entrance afterwards never cuts a growing bar short, and a bar that
  // stays mounted never grows in again. The attribute goes when the animation ends or is cancelled by reduced motion.
  const [entrance, setEntrance] = useState(() => enter === undefined || motionReduced() ? undefined : Math.min(Math.max(0, enter), barEntrance.maxSteps))
  const bar = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const element = bar.current
    if (entrance === undefined || !element) return
    const done = (event: AnimationEvent) => { if (event.animationName === "ww-bar-grow") setEntrance(undefined) }
    element.addEventListener("animationend", done)
    element.addEventListener("animationcancel", done)
    return () => {
      element.removeEventListener("animationend", done)
      element.removeEventListener("animationcancel", done)
    }
  }, [entrance])
  const value = share === undefined || !Number.isFinite(share) ? 0 : Math.round(Math.min(1, Math.max(0, share)) / shareQuantum) * shareQuantum
  const style = entrance === undefined ? { "--ww-share": value } : { "--ww-share": value, "--ww-bar-index": entrance }
  return <div ref={bar} className="ww-bar" aria-hidden="true" data-fill={fill} data-unknown={share === undefined} data-snap={snap} data-enter={entrance !== undefined || undefined} style={style as CSSProperties}>
    <div className="ww-bar-trail" /><div className="ww-bar-fill" /><div className="ww-bar-edge" />
  </div>
})

export interface CharacterAvatarProps {
  /** The static role whose art to show: a row's staticRoleId (a trial role's parent), never a guess. */
  roleId?: number
  elementId?: number
  size?: number
  /** Shown when there is no image. It is never a guessed portrait. */
  fallback?: ReactNode
}

export const CharacterAvatar = memo(function CharacterAvatar({ roleId, elementId, size = 34, fallback = "?" }: CharacterAvatarProps) {
  const url = roleId === undefined ? undefined : gameAssetUrl("character-avatar", roleId)
  const { state, onLoad, onError } = useGameAsset(url)
  return <span className="ww-avatar" data-element={elementId} data-loaded={state === "loaded"} data-asset-state={url && state} style={{ "--ww-avatar-size": `${size}px` } as CSSProperties}>
    <span className="ww-avatar-fallback" aria-hidden="true">{state === "failed" ? "!" : fallback}</span>
    {url && <img src={url} alt="" decoding="async" draggable={false} onLoad={onLoad} onError={onError} />}
  </span>
})

/** A game image that hides itself when it has none to show. It never falls back to another ID's image. */
export const GameAssetImage = memo(function GameAssetImage({ kind, id }: { kind: GameAssetKind; id: number }) {
  const url = gameAssetUrl(kind, id)
  const { state, onLoad, onError } = useGameAsset(url)
  return <img src={url} alt="" decoding="async" draggable={false} data-asset-state={state} style={state === "missing" || state === "failed" ? { visibility: "hidden" } : undefined} onLoad={onLoad} onError={onError} />
})

/** Changes a 0/1 attribute when trigger changes, restarting a CSS animation without a timer or a re-mount. */
function usePulse(trigger: unknown): "0" | "1" | undefined {
  const [state, setState] = useState<{ trigger: unknown; count: number }>({ trigger, count: 0 })
  if (state.trigger !== trigger) setState({ trigger, count: state.count + 1 })
  return state.count === 0 ? undefined : state.count % 2 === 0 ? "0" : "1"
}

export interface CharacterDamageRowProps {
  row: CombatRowModel
  elementId?: number
  /** Resolved display name, including the UNKNOWN form. This component never invents a name. */
  name: string
  subtitle?: string
  formatDps: (value: number) => string
  formatDamage: (value: number) => string
  formatShare: (value: number) => string
  numberMode?: "tween" | "roll"
  scope?: string
  stale?: boolean
  selected?: boolean
  onSelect?: (key: string) => void
}

export const CharacterDamageRow = memo(function CharacterDamageRow({ row, elementId, name, subtitle, formatDps, formatDamage, formatShare, numberMode = "tween", scope = "", stale = false, selected = false, onSelect }: CharacterDamageRowProps) {
  // Only a rise in rank flashes: a falling row should not attract the eye.
  const previousRank = useRef(row.rank)
  const risen = useRef(0)
  if (row.rank < previousRank.current) risen.current += 1
  previousRank.current = row.rank
  const pulse = usePulse(risen.current)
  return <div className="ww-damage-row" data-element={elementId} data-pulse={pulse} data-selected={selected} data-stale={stale} data-interactive={onSelect !== undefined} onClick={onSelect ? () => onSelect(row.key) : undefined}>
    <DamageBar share={row.share} fill="row" scope={scope} />
    <div className="ww-row-flash" />
    <div className="ww-damage-row-grid">
      <CharacterAvatar roleId={row.staticRoleId} elementId={elementId} size={36} fallback={row.kind === "vehicle" ? <Icon name="bolt" size={15} /> : "?"} />
      <span className="ww-damage-row-name"><strong className="ww-truncate" title={name}>{name}</strong>{subtitle && <small className="ww-truncate">{subtitle}</small>}</span>
      <AnimatedNumber value={row.dps} format={formatDps} mode={numberMode} scope={scope} stale={stale} />
      <AnimatedNumber value={row.damage} format={formatDamage} mode={numberMode} scope={scope} stale={stale} />
      <span className="ww-damage-row-share ww-num" style={{ textAlign: "end" }}>{row.share === undefined ? "—" : formatShare(row.share)}</span>
    </div>
  </div>
})

export type CombatState = "combat" | "ended" | "waiting" | "stale" | "untrusted"

export interface CombatPanelProps {
  rows: readonly CombatRowModel[]
  elementOf: (row: CombatRowModel) => number | undefined
  nameOf: (row: CombatRowModel) => string
  subtitleOf?: (row: CombatRowModel) => string | undefined
  totalDamage: number | undefined
  teamDps: number | undefined
  time: string
  state: CombatState
  stateLabel: string
  note?: string
  labels: { totalDamage: string; teamDps: string; member: string; dps: string; damage: string; share: string; empty: string }
  formatDps: (value: number) => string
  formatDamage: (value: number) => string
  formatShare: (value: number) => string
  numberMode?: "tween" | "roll"
  scope?: string
  alpha?: number
  /** Compact listening status for the header; it never covers a number. */
  status?: ReactNode
}

const rowKey = (row: CombatRowModel) => row.key

/** The Combat Overlay surface, optimized for a three-character team. It draws the engine's rows in the order given and owns no state. */
export const CombatPanel = memo(function CombatPanel({ rows, elementOf, nameOf, subtitleOf, totalDamage, teamDps, time, state, stateLabel, note, labels, formatDps, formatDamage, formatShare, numberMode = "tween", scope = "", alpha, status }: CombatPanelProps) {
  const stale = state === "stale" || state === "untrusted"
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  return <div className="ww-combat" style={style}>
    <header className="ww-combat-head">
      <div className="ww-combat-stat"><span>{labels.totalDamage}</span><b><AnimatedNumber value={totalDamage} format={formatDamage} mode={numberMode} scope={scope} stale={stale} /></b></div>
      <div className="ww-combat-stat" data-primary="true"><span>{labels.teamDps}</span><b><AnimatedNumber value={teamDps} format={formatDps} mode={numberMode} scope={scope} stale={stale} /></b></div>
      <div className="ww-combat-time ww-num">{status}{time}</div>
    </header>
    <div className="ww-combat-columns"><span>{labels.member}</span><span>{labels.dps}</span><span>{labels.damage}</span><span>{labels.share}</span></div>
    <div className="ww-combat-rows">
      {rows.length === 0
        ? <div className="ww-combat-empty">{labels.empty}</div>
        : <StackList items={rows} keyOf={rowKey} rowHeight={46} gap={4} renderItem={row => <CharacterDamageRow row={row} elementId={elementOf(row)} name={nameOf(row)} subtitle={subtitleOf?.(row)} formatDps={formatDps} formatDamage={formatDamage} formatShare={formatShare} numberMode={numberMode} scope={scope} stale={stale} />} />}
    </div>
    {note && <div className="ww-combat-note" role="status"><Icon name="alert" size={12} />{note}</div>}
    <footer className="ww-state-band" data-state={state}><span>{stateLabel}</span></footer>
  </div>
})

/** Seconds as a cell shows them: whole seconds, rounded up, and whole minutes from 100 seconds. */
export function effectSeconds(remaining: number): string {
  return remaining < 100 ? String(Math.ceil(remaining)) : `${Math.round(remaining / 60)}m`
}

/** The last seconds of an effect, when its time turns to the warning color. */
export const lowEffectSeconds = 3

/** About half a pixel of the widest cell at 100%, so a long countdown only redraws when it visibly moves. */
const fillQuantum = 1 / 72

export function effectFill(fraction: number): number {
  return Math.round(fraction / fillQuantum) * fillQuantum
}

/** A rise this large is the engine reporting a refresh rather than a small correction of the estimate. */
const refreshStep = 0.08

export interface EffectCellProps {
  effectId?: number
  /** Whom the effect is on. An effect on an enemy carries a small corner mark, so it reads apart from one on the team. */
  target?: "team" | "enemy" | "unresolved"
  elementId?: number
  /** Time left as a share of the duration, from effectFill; undefined draws no proportion. */
  fill?: number
  /** The engine reports no time at all for this effect. */
  untimed: boolean
  /** Seconds left as text, undefined when no reliable time is shown. */
  seconds?: string
  low: boolean
  stackCount?: number
  stale: boolean
  /** Builds the spoken description; it never names the effect or quotes its ID. */
  describe: (remaining: string | undefined, stackCount: number | undefined, hasIcon: boolean) => string
}

/**
 * One effect of the strip: the remaining time fills the cell behind its icon, so the icon stays fully visible and the
 * time reads from the whole cell rather than a bar under it. The fill follows each engine value and stops when snapshots
 * stop; only a refresh animates, as one short spring. Its color is the effect's own element, never the applier's, and
 * neutral otherwise. Every listed effect is one the game draws with its own icon, and no other image ever replaces it.
 */
export const EffectCell = memo(function EffectCell({ effectId, target, elementId, fill, untimed, seconds, low, stackCount, stale, describe }: EffectCellProps) {
  const iconUrl = effectId !== undefined ? gameAssetUrl("buff", effectId) : undefined
  const asset = useGameAsset(iconUrl)
  const previous = useRef(fill)
  const rising = fill !== undefined && previous.current !== undefined && fill > previous.current + 0.001
  useEffect(() => { previous.current = fill })
  // Counted against the last rendered value, so the render that usePulse's state update repeats does not count it twice.
  const lastFill = useRef(fill)
  const refreshes = useRef(0)
  if (fill !== undefined && lastFill.current !== undefined && fill > lastFill.current + refreshStep) refreshes.current += 1
  lastFill.current = fill
  const pulse = usePulse(refreshes.current)
  const stackPulse = usePulse(stackCount)
  const drawn = iconUrl !== undefined && asset.state !== "missing" && asset.state !== "failed"
  const shownStacks = stackCount !== undefined && stackCount > 1 ? stackCount : undefined
  const label = useMemo(() => describe(seconds, shownStacks, drawn), [describe, seconds, shownStacks, drawn])
  return <div className="ww-effect-cell" role="img" aria-label={label} data-icon={drawn ? "game" : "none"} data-target={target} data-effect-id={effectId} data-element={elementId} data-pulse={pulse} data-stale={stale}>
    <div className="ww-effect-face" data-jump={rising} data-untimed={untimed}>
      {fill !== undefined && <span className="ww-effect-fill" aria-hidden="true" style={{ "--ww-remaining": fill } as CSSProperties} />}
      {drawn && <img src={iconUrl} alt="" decoding="async" draggable={false} onLoad={asset.onLoad} onError={asset.onError} />}
      {shownStacks !== undefined && <span className="ww-effect-stack ww-num" aria-hidden="true" data-pulse={stackPulse}>{shownStacks}</span>}
    </div>
    <span className="ww-effect-seconds ww-num" aria-hidden="true" data-low={low}>{seconds}</span>
  </div>
})

/** How many effects did not fit the strip. */
export const EffectMore = memo(function EffectMore({ count, label }: { count: number; label: string }) {
  return <div className="ww-effect-more ww-num" role="img" aria-label={label}>+{count}</div>
})
