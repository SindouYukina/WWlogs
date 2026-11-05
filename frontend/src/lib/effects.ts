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

export function relevantEffect(effect:Effect):boolean {
  return effect.combatClass!=="non_combat" && [effect.holderRelation,effect.sourceRelation].some(value=>value==="local_role"||value==="local_owned")
}
