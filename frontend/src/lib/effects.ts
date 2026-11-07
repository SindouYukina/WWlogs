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
  if(snapshot.view.health.context.state!=="VALID" || snapshot.view.effects.status!=="complete" || effect.stale)return undefined
  if(effect.lifecycle!=="active" || effect.remainingStatus!=="estimated" || effect.remainingSeconds===undefined || !Number.isFinite(effect.remainingSeconds))return undefined
  return Math.max(0,effect.remainingSeconds)
}

/** The measured sizes one Effects panel lays out against, in CSS pixels. */
export interface EffectLayout {
  available: number
  head: number
  row: number
  rowGap: number
  /** The gap between the panel's head, list and count of hidden effects. */
  sectionGap: number
  hint: number
}

/** How many whole rows fit when every effect is shown, and when the count of hidden effects also needs a line. */
export interface EffectCapacity {
  all: number
  withHint: number
}

export function effectCapacity(layout: EffectLayout): EffectCapacity {
  const rows = (space: number) => Math.max(0, Math.floor((space + layout.rowGap) / (layout.row + layout.rowGap)))
  const list = layout.available - layout.head - layout.sectionGap
  return { all: rows(list), withHint: rows(list - layout.hint - layout.sectionGap) }
}

/**
 * Splits the engine's display set into shown rows and an overflow count. The engine decides which effects are eligible
 * (Go effectDisplayFor) and lists them in its order, at most a bounded number; `eligible` counts all of them, so
 * visible + overflow always equals eligible. Unclassified effects are not part of that set: they only need the hint line.
 * The split depends only on counts and the window's capacity, so countdowns and row animations cannot make it move.
 */
export function visibleEffects(eligible: number, listed: number, unclassified: number, capacity: EffectCapacity): { visible: number; overflow: number } {
  const rows = Math.min(listed, eligible)
  const visible = rows === eligible && unclassified === 0 && rows <= capacity.all ? rows : Math.min(rows, capacity.withHint)
  return { visible, overflow: eligible - visible }
}
