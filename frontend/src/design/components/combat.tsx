import { memo, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { defaultTickMs } from "../motion/tokens.ts"
import { gameAssetUrl, type GameAssetKind } from "../assets"
import { useGameAsset } from "../useGameAsset"
import { AnimatedNumber } from "./AnimatedNumber"
import { Icon, type IconName } from "./Icon"
import { StackList } from "./layout"
import { countdownFraction, type CombatRowModel, type EffectRowModel } from "@/lib/combat-view"

export interface DamageBarProps {
  /** 0..1. Undefined means the engine reported no share; the bar then draws a neutral hatch rather than a value. */
  share: number | undefined
  /** "track" is a thin meter; "row" fills its positioned parent as a background. */
  fill?: "track" | "row"
  /** A new scope snaps instead of animating from the previous encounter's share. */
  scope?: string
}

/** About one pixel on the widest bars. Smaller changes keep the bar settled, so a steady fight draws no bar frames. */
const shareQuantum = 1 / 480

/** Three transform-only layers: a fast fill, a slower trail that shows recent change, and a bright leading edge. */
export const DamageBar = memo(function DamageBar({ share, fill = "track", scope = "" }: DamageBarProps) {
  const previousScope = useRef(scope)
  const snap = previousScope.current !== scope
  useEffect(() => { previousScope.current = scope })
  const value = share === undefined || !Number.isFinite(share) ? 0 : Math.round(Math.min(1, Math.max(0, share)) / shareQuantum) * shareQuantum
  return <div className="ww-bar" aria-hidden="true" data-fill={fill} data-unknown={share === undefined} data-snap={snap} style={{ "--ww-share": value } as CSSProperties}>
    <div className="ww-bar-trail" /><div className="ww-bar-fill" /><div className="ww-bar-edge" />
  </div>
})

export interface CharacterAvatarProps {
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
      <CharacterAvatar roleId={row.roleId} elementId={elementId} size={36} fallback={row.kind === "vehicle" ? <Icon name="bolt" size={15} /> : "?"} />
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

/** The Combat Overlay surface, optimized for a three-character team. It takes engine-derived rows and owns no state. */
export const CombatPanel = memo(function CombatPanel({ rows, elementOf, nameOf, subtitleOf, totalDamage, teamDps, time, state, stateLabel, note, labels, formatDps, formatDamage, formatShare, numberMode = "tween", scope = "", alpha, status }: CombatPanelProps) {
  const stale = state === "stale" || state === "untrusted"
  const ordered = [...rows].sort((a, b) => a.rank - b.rank)
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  return <div className="ww-combat" style={style}>
    <header className="ww-combat-head">
      <div className="ww-combat-stat"><span>{labels.totalDamage}</span><b><AnimatedNumber value={totalDamage} format={formatDamage} mode={numberMode} scope={scope} stale={stale} /></b></div>
      <div className="ww-combat-stat" data-primary="true"><span>{labels.teamDps}</span><b><AnimatedNumber value={teamDps} format={formatDps} mode={numberMode} scope={scope} stale={stale} /></b></div>
      <div className="ww-combat-time ww-num">{status}{time}</div>
    </header>
    <div className="ww-combat-columns"><span>{labels.member}</span><span>{labels.dps}</span><span>{labels.damage}</span><span>{labels.share}</span></div>
    <div className="ww-combat-rows">
      {ordered.length === 0
        ? <div className="ww-combat-empty">{labels.empty}</div>
        : <StackList items={ordered} keyOf={rowKey} rowHeight={46} gap={4} renderItem={row => <CharacterDamageRow row={row} elementId={elementOf(row)} name={nameOf(row)} subtitle={subtitleOf?.(row)} formatDps={formatDps} formatDamage={formatDamage} formatShare={formatShare} numberMode={numberMode} scope={scope} stale={stale} />} />}
    </div>
    {note && <div className="ww-combat-note" role="status"><Icon name="alert" size={12} />{note}</div>}
    <footer className="ww-state-band" data-state={state}><span>{stateLabel}</span></footer>
  </div>
})

/** The neutral mark of an effect that the game draws without an icon of its own, by what its definition says it is for. */
export type EffectMark = "attribute" | "enemy-link" | "state"

export function effectMarkOf(kind: string): EffectMark {
  if (kind === "stat_modifier") return "attribute"
  if (kind === "runtime_link") return "enemy-link"
  return "state"
}

const effectMarkIcons: Record<EffectMark, IconName> = { attribute: "sliders", "enemy-link": "link", state: "spark" }

export interface EffectTileProps {
  effect: EffectRowModel
  /** A spoken description for assistive technology. It never names the effect or quotes its ID. */
  label: string
  tickMs?: number
  lowSeconds?: number
}

const lowDefaultSeconds = 3

/** Seconds as the tile shows them: whole seconds, rounded up, and whole minutes from 100 seconds. */
export function tileSeconds(remaining: number): string {
  return remaining < 100 ? String(Math.ceil(remaining)) : `${Math.round(remaining / 60)}m`
}

/**
 * One effect as the game's HUD draws it: an icon, a stack count from two stacks and a time bar; no name and no ID. The
 * icon is requested only when the game has one for this effect (EffectRowModel.hudIcon); every other effect keeps an
 * outlined neutral mark, never another effect's or a skill's icon. The bar only reflects the engine's remaining time.
 */
export const EffectTile = memo(function EffectTile({ effect, label, tickMs = defaultTickMs, lowSeconds = lowDefaultSeconds }: EffectTileProps) {
  const iconUrl = effect.hudIcon && effect.effectId !== undefined ? gameAssetUrl("buff", effect.effectId) : undefined
  const icon = useGameAsset(iconUrl)
  const pulse = usePulse(effect.refreshToken)
  const stackPulse = usePulse(effect.stackCount)
  const { countdown } = effect
  const remaining = countdown.kind === "unknown" ? undefined : countdown.remaining
  const fraction = countdownFraction(countdown)
  const low = remaining !== undefined && remaining <= lowSeconds && remaining > 0
  const drawn = iconUrl !== undefined && icon.state !== "missing" && icon.state !== "failed"
  const mark = effectMarkOf(effect.kind)
  return <div className="ww-effect-tile" role="img" aria-label={label} data-effect-id={effect.effectId} data-icon={drawn ? "game" : "mark"} data-mark={drawn ? undefined : mark} data-polarity={effect.polarity} data-low={low} data-pulse={pulse} data-stale={effect.stale}>
    {drawn
      ? <img src={iconUrl} alt="" decoding="async" draggable={false} onLoad={icon.onLoad} onError={icon.onError} />
      : <span className="ww-effect-tile-mark" aria-hidden="true"><Icon name={effectMarkIcons[mark]} size={14} /></span>}
    {fraction !== undefined && <span className="ww-effect-tile-bar" aria-hidden="true" style={{ "--ww-remaining": fraction, "--ww-tick": `${tickMs}ms` } as CSSProperties}><span /></span>}
    {remaining !== undefined && <span className="ww-effect-tile-time ww-num" aria-hidden="true">{tileSeconds(remaining)}</span>}
    {effect.stackCount !== undefined && effect.stackCount > 1 && <span className="ww-effect-tile-stack ww-num" aria-hidden="true" data-pulse={stackPulse}>{effect.stackCount}</span>}
  </div>
})

export interface EffectChipProps {
  effect: EffectRowModel
  /** A spoken description for assistive technology. It never names the effect or quotes its ID. */
  label: string
  tickMs?: number
  lowSeconds?: number
}

/**
 * An effect the game draws without an icon of its own, as a small neutral gauge: its shape tells what the definition
 * says it is for (EffectMark), its fill the share of the duration left, a hollow chip has no engine time, and a dot marks
 * two or more stacks. It is deliberately smaller than a tile, so it never passes for an icon the game shows.
 */
export const EffectChip = memo(function EffectChip({ effect, label, tickMs = defaultTickMs, lowSeconds = lowDefaultSeconds }: EffectChipProps) {
  const pulse = usePulse(effect.refreshToken)
  const { countdown } = effect
  const remaining = countdown.kind === "unknown" ? undefined : countdown.remaining
  const fraction = countdown.kind === "timed" ? countdownFraction(countdown) : countdown.kind === "remaining-only" ? 1 : undefined
  const low = remaining !== undefined && remaining <= lowSeconds && remaining > 0
  return <span className="ww-effect-chip" role="img" aria-label={label} data-effect-id={effect.effectId} data-mark={effectMarkOf(effect.kind)} data-polarity={effect.polarity} data-timed={fraction !== undefined} data-low={low} data-stacked={effect.stackCount !== undefined && effect.stackCount > 1} data-pulse={pulse} data-stale={effect.stale}>
    {fraction !== undefined && <span className="ww-effect-chip-fill" style={{ "--ww-remaining": fraction, "--ww-tick": `${tickMs}ms` } as CSSProperties} />}
  </span>
})

/** The holder mark at the start of a tile group: the role's avatar, or a neutral mark for other holders. */
export const EffectGroupMark = memo(function EffectGroupMark({ kind, roleId, label }: { kind: "role" | "team" | "enemy" | "unresolved"; roleId?: number; label: string }) {
  return <span className="ww-effect-group-mark" role="img" aria-label={label} data-group={kind}>
    {kind === "role"
      ? <CharacterAvatar roleId={roleId} size={24} />
      : <span className="ww-effect-group-glyph" aria-hidden="true"><Icon name={kind === "enemy" ? "target" : kind === "team" ? "list" : "question"} size={14} /></span>}
  </span>
})
