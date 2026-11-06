import { memo, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { defaultTickMs } from "../motion/tokens.ts"
import { gameAssetUrl } from "../assets"
import { AnimatedNumber } from "./AnimatedNumber"
import { Icon } from "./Icon"
import { StackList } from "./layout"
import { countdownFraction, type CombatRowModel, type CountdownState, type EffectRowModel } from "@/lib/combat-view"

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
  const [loaded, setLoaded] = useState(false)
  return <span className="ww-avatar" data-element={elementId} data-loaded={loaded} style={{ "--ww-avatar-size": `${size}px` } as CSSProperties}>
    <span className="ww-avatar-fallback" aria-hidden="true">{fallback}</span>
    {roleId !== undefined && <img src={gameAssetUrl("character-avatar", roleId)} alt="" decoding="async" draggable={false} onLoad={() => setLoaded(true)} onError={() => setLoaded(false)} />}
  </span>
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
}

const rowKey = (row: CombatRowModel) => row.key

/** The Combat Overlay surface, optimized for a three-character team. It takes engine-derived rows and owns no state. */
export const CombatPanel = memo(function CombatPanel({ rows, elementOf, nameOf, subtitleOf, totalDamage, teamDps, time, state, stateLabel, note, labels, formatDps, formatDamage, formatShare, numberMode = "tween", scope = "", alpha }: CombatPanelProps) {
  const stale = state === "stale" || state === "untrusted"
  const ordered = [...rows].sort((a, b) => a.rank - b.rank)
  const style = alpha === undefined ? undefined : { "--ww-alpha-overlay": alpha, "--ww-alpha-overlay-strong": Math.min(1, alpha + 0.12) } as CSSProperties
  return <div className="ww-combat" style={style}>
    <header className="ww-combat-head">
      <div className="ww-combat-stat"><span>{labels.totalDamage}</span><b><AnimatedNumber value={totalDamage} format={formatDamage} mode={numberMode} scope={scope} stale={stale} /></b></div>
      <div className="ww-combat-stat" data-primary="true"><span>{labels.teamDps}</span><b><AnimatedNumber value={teamDps} format={formatDps} mode={numberMode} scope={scope} stale={stale} /></b></div>
      <div className="ww-combat-time ww-num">{time}</div>
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

export interface BuffCountdownProps {
  state: CountdownState
  tickMs?: number
}

/**
 * The bar moves linearly between engine values over one publish interval. It never extrapolates: if snapshots stop, the
 * bar stops. An upward move is a confirmed refresh and uses a spring rather than time's linear motion.
 */
export const BuffCountdown = memo(function BuffCountdown({ state, tickMs = defaultTickMs }: BuffCountdownProps) {
  const fraction = countdownFraction(state)
  const previous = useRef(fraction)
  const jump = fraction !== undefined && previous.current !== undefined && fraction > previous.current + 0.001
  useEffect(() => { previous.current = fraction })
  return <div className="ww-countdown" aria-hidden="true" data-kind={state.kind} data-jump={jump} style={{ "--ww-remaining": fraction ?? 0, "--ww-tick": `${tickMs}ms` } as CSSProperties}>
    {fraction !== undefined && <><div className="ww-countdown-fill" /><div className="ww-countdown-low" /></>}
  </div>
})

export interface BuffStatusItemProps {
  effect: EffectRowModel
  /** Null when no verified name exists; the item then shows the raw ID. */
  title: string
  unnamed: boolean
  meta?: string
  remainingText: string
  tickMs?: number
  lowSeconds?: number
}

const lowDefaultSeconds = 3

export const BuffStatusItem = memo(function BuffStatusItem({ effect, title, unnamed, meta, remainingText, tickMs, lowSeconds = lowDefaultSeconds }: BuffStatusItemProps) {
  const [iconFailed, setIconFailed] = useState(false)
  const pulse = usePulse(effect.refreshToken)
  const stackPulse = usePulse(effect.stackCount)
  const { countdown } = effect
  const low = (countdown.kind === "timed" || countdown.kind === "remaining-only") && countdown.remaining <= lowSeconds && countdown.remaining > 0
  return <div className="ww-effect" data-polarity={effect.polarity} data-low={low} data-pulse={pulse} data-stale={effect.stale} title={title}>
    <BuffCountdown state={countdown} tickMs={tickMs} />
    <div className="ww-effect-sheen" />
    <div className="ww-effect-icon">
      {effect.effectId !== undefined && !iconFailed
        ? <img src={gameAssetUrl("buff", effect.effectId)} alt="" decoding="async" draggable={false} onError={() => setIconFailed(true)} />
        : <span className="ww-effect-icon-fallback"><Icon name="question" size={16} /></span>}
    </div>
    <div className="ww-effect-text"><span className="ww-effect-title ww-truncate" data-unnamed={unnamed}>{title}</span>{meta && <span className="ww-effect-meta ww-truncate">{meta}</span>}</div>
    <div className="ww-effect-trailing">
      {effect.stackCount !== undefined && <span className="ww-effect-stack ww-num" data-pulse={stackPulse}>{effect.stackCount}</span>}
      <span className="ww-effect-time ww-num" data-unknown={countdown.kind === "unknown"}>{remainingText}</span>
    </div>
  </div>
})
