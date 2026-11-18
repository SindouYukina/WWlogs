import type { Snapshot, Effect, Connection } from "./snapshot"

// Freshness is a display warning, never a replacement for the Go engine's integrity judgement.
export function snapshotStale(snapshot:Snapshot|null, now:number):boolean {
  if(!snapshot || snapshot.service.state!=="running")return false
  const at=Date.parse(snapshot.publishedAt)
  return !Number.isFinite(at) || now-at>5000
}

// The live clock (useClock) feeds only snapshotStale and effectRemaining, and both read it only while the service runs,
// so the clock needs to tick only then.
export function liveClockActive(snapshot:Snapshot|null):boolean {
  return snapshot?.service.state==="running"
}

// The engine decides whether a countdown can be trusted (ViewEffect.showCountdown) and what it reads; this only adds the
// frontend's own state: the connection, a stopped service and a snapshot too old to be current. It never extrapolates.
export function effectRemaining(effect:Effect,snapshot:Snapshot|null,connection:Connection,now:number):number|undefined {
  if(!snapshot?.view || connection!=="connected" || snapshot.service.state!=="running" || snapshotStale(snapshot,now))return undefined
  if(effect.showCountdown!==true || effect.remainingSeconds===undefined || !Number.isFinite(effect.remainingSeconds))return undefined
  return Math.max(0,effect.remainingSeconds)
}

/**
 * What the Effects Overlay window shows. Content and Edit Mode are separate inputs:
 * - hidden: outside Edit Mode with no cell. Nothing is drawn, not even a background, and the host lets the mouse through.
 * - visible: outside Edit Mode with cells. The rows are drawn as usual.
 * - edit-preview: in Edit Mode. The rows are drawn when there are cells; otherwise a preview of the area they take, so the
 *   window can be placed and sized while no effect is active.
 * cells counts both of the engine's rows, including the cells beyond its bound that the strip shows as a count.
 */
export type EffectsOverlayMode = "hidden" | "visible" | "edit-preview"

export function effectsOverlayMode(cells: number, editing: boolean): EffectsOverlayMode {
  if (editing) return "edit-preview"
  return cells > 0 ? "visible" : "hidden"
}

/** The measured sizes of a row, in CSS pixels. */
export interface EffectStripLayout {
  width: number
  icon: number
  gap: number
  /** The count of effects that did not fit. */
  more: number
}

export interface EffectStrip {
  /** How many of the ordered effects are drawn. */
  visible: number
  /** Cells not drawn: visible + overflow always equals the cells the row was given. */
  overflow: number
}

/**
 * Splits a row's ordered effects into the longest prefix that fits one line and a count of the rest, leaving room for that
 * count whenever one is needed, so the soonest to end stay visible. It depends only on the row's length and the measured
 * width, so a countdown cannot change it. The engine's display set is never filtered here.
 */
export function fitEffectStrip<T>(ordered: readonly T[], eligible: number, layout: EffectStripLayout | undefined): EffectStrip {
  const total = Math.min(ordered.length, eligible)
  if (layout === undefined) return { visible: 0, overflow: eligible }
  let used = 0
  let fitting = 0
  let fittingWithCount = 0
  for (let index = 0; index < total; index++) {
    const next = used + (index > 0 ? layout.gap : 0) + layout.icon
    if (next > layout.width) break
    used = next
    fitting = index + 1
    if (next + layout.gap + layout.more <= layout.width) fittingWithCount = fitting
  }
  const visible = fitting === eligible ? fitting : fittingWithCount
  return { visible, overflow: eligible - visible }
}
