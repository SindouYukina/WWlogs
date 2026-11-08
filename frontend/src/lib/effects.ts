import type { Snapshot, Effect, Connection } from "./snapshot"

// Freshness is a display warning, never a replacement for the Go engine's integrity judgement.
export function snapshotStale(snapshot:Snapshot|null, now:number):boolean {
  if(!snapshot || snapshot.service.state!=="running")return false
  const at=Date.parse(snapshot.publishedAt)
  return !Number.isFinite(at) || now-at>5000
}

// Do not extrapolate wall time: Go remainingSeconds already uses the capture clock, including paced replay.
// Game time-stop is not proven, so even a fresh finite result remains explicitly estimated.
export function effectRemaining(effect:Effect,snapshot:Snapshot|null,connection:Connection,now:number):number|undefined {
  if(!snapshot?.view || connection!=="connected" || snapshot.service.state!=="running" || snapshotStale(snapshot,now))return undefined
  if(snapshot.view.health.context.state!=="VALID" || snapshot.view.effects.status!=="complete" || snapshot.view.effects.clockStalled===true || effect.stale || effect.maintained===true)return undefined
  if(effect.lifecycle!=="active" || effect.remainingStatus!=="estimated" || effect.remainingSeconds===undefined || !Number.isFinite(effect.remainingSeconds))return undefined
  return Math.max(0,effect.remainingSeconds)
}

type OrderedEffect = Pick<Effect, "key" | "lifecycle" | "remainingStatus" | "expectedExpiry" | "stale" | "hudIcon" | "maintained">

/**
 * The moment an effect is expected to end, in capture time, or undefined when it has no reliable time. An expiry only
 * moves when the engine reports a new time, so sorting by it never reorders effects because time passed. Trust gates that
 * hide every countdown at once (a stalled snapshot, a suspect context) leave it in place, so effects do not reshuffle
 * while their seconds are hidden.
 */
export function effectExpiryMs(effect: Pick<Effect, "lifecycle" | "remainingStatus" | "expectedExpiry" | "stale" | "maintained">): number | undefined {
  if (effect.stale || effect.maintained || effect.lifecycle !== "active" || effect.remainingStatus !== "estimated" || effect.expectedExpiry === undefined) return undefined
  const at = Date.parse(effect.expectedExpiry)
  return Number.isFinite(at) ? at : undefined
}

/**
 * The strip's order: soonest to end first, effects without a reliable time last. Equal keys keep a fixed order through
 * the instance key, so effects that expire together never trade places. Among effects without a time, those the game
 * draws with an icon come first, as in the engine's list.
 */
export function compareEffects(a: OrderedEffect, b: OrderedEffect): number {
  const expiryA = effectExpiryMs(a)
  const expiryB = effectExpiryMs(b)
  if (expiryA !== undefined && expiryB !== undefined && expiryA !== expiryB) return expiryA - expiryB
  if ((expiryA === undefined) !== (expiryB === undefined)) return expiryA === undefined ? 1 : -1
  if (expiryA === undefined && (a.hudIcon === true) !== (b.hudIcon === true)) return a.hudIcon === true ? -1 : 1
  return a.key < b.key ? -1 : a.key > b.key ? 1 : 0
}

export function orderEffects<T extends OrderedEffect>(effects: readonly T[]): T[] {
  return [...effects].sort(compareEffects)
}

/** An effect with an icon of its own is a full cell; any other effect is a narrow gauge, so it never passes for one. */
export type EffectCellSize = "icon" | "gauge"

export function effectCellSize(effect: Pick<Effect, "hudIcon" | "hudIconGeneric">): EffectCellSize {
  return effect.hudIcon === true && effect.hudIconGeneric !== true ? "icon" : "gauge"
}

/** The measured sizes of the strip, in CSS pixels. */
export interface EffectStripLayout {
  width: number
  icon: number
  gauge: number
  gap: number
  /** The count of effects that did not fit. */
  more: number
}

export const stripCellWidth = (size: EffectCellSize, layout: EffectStripLayout) => size === "icon" ? layout.icon : layout.gauge

export interface EffectStrip {
  /** How many of the ordered effects are drawn. */
  visible: number
  /** Cells not drawn: visible + overflow always equals the cells the engine listed and truncated. */
  overflow: number
}

/**
 * Splits the ordered effects into the longest prefix that fits one line and a count of the rest, leaving room for that
 * count whenever one is needed, so the soonest to end stay visible. It depends only on the ordered effects and the
 * measured width, so a countdown cannot change it. The engine's display set is never filtered here.
 */
export function fitEffectStrip<T>(ordered: readonly T[], sizeOf: (item: T) => EffectCellSize, eligible: number, layout: EffectStripLayout | undefined): EffectStrip {
  const total = Math.min(ordered.length, eligible)
  if (layout === undefined) return { visible: 0, overflow: eligible }
  let used = 0
  let fitting = 0
  let fittingWithCount = 0
  for (let index = 0; index < total; index++) {
    const next = used + (index > 0 ? layout.gap : 0) + stripCellWidth(sizeOf(ordered[index]!), layout)
    if (next > layout.width) break
    used = next
    fitting = index + 1
    if (next + layout.gap + layout.more <= layout.width) fittingWithCount = fitting
  }
  const visible = fitting === eligible ? fitting : fittingWithCount
  return { visible, overflow: eligible - visible }
}
