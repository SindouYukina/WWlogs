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

/** Who holds an effect, the only grouping the Effects Overlay draws: one row group per local role, then other local
 * holders, enemies, and holders whose relation the capture could not establish. */
export interface EffectGroup {
  key: string
  kind: "role" | "team" | "enemy" | "unresolved"
  roleId?: number
}

export function effectGroupOf(effect: Pick<Effect, "display" | "holderRoleId">): EffectGroup {
  switch (effect.display?.target) {
    case "team": return effect.holderRoleId !== undefined ? { key: `role-${effect.holderRoleId}`, kind: "role", roleId: effect.holderRoleId } : { key: "team", kind: "team" }
    case "enemy": return { key: "enemy", kind: "enemy" }
    default: return { key: "unresolved", kind: "unresolved" }
  }
}

const groupRank: Record<EffectGroup["kind"], number> = { role: 0, team: 1, enemy: 2, unresolved: 3 }

/** A fixed group order (roles by RoleId, other team holders, enemies, unresolved) so a new effect never moves a group. */
export function compareEffectGroups(a: EffectGroup, b: EffectGroup): number {
  return groupRank[a.kind] - groupRank[b.kind] || (a.roleId ?? 0) - (b.roleId ?? 0)
}

/** The measured sizes one Effects panel lays out against, in CSS pixels. */
export interface EffectGridLayout {
  /** Height and width of the panel. */
  available: number
  width: number
  head: number
  /** The gap between the panel's head, tile groups and count of hidden effects. */
  sectionGap: number
  hint: number
  /** An effect the game draws with its icon is a tile. */
  tile: number
  tileGap: number
  /** An effect without a game icon is a small gauge chip after its holder's tiles. */
  chip: number
  chipGap: number
  /** The gap between a holder's tiles and its chips. */
  blockGap: number
  /** Width of a group's holder mark and the gap between it and the tiles. */
  groupHead: number
  groupHeadGap: number
  groupGap: number
}

/** Items per line of one group, for a given item size and gap. */
export function effectGridColumns(layout: EffectGridLayout, size = layout.tile, gap = layout.tileGap): number {
  return Math.max(1, Math.floor((layout.width - layout.groupHead - layout.groupHeadGap + gap) / (size + gap)))
}

interface GroupCount { tiles: number; chips: number }

function blockHeight(count: number, columns: number, size: number, gap: number): number {
  const lines = Math.ceil(count / columns)
  return lines === 0 ? 0 : lines * size + (lines - 1) * gap
}

function gridHeight(counts: Iterable<GroupCount>, layout: EffectGridLayout): number {
  const tileColumns = effectGridColumns(layout)
  const chipColumns = effectGridColumns(layout, layout.chip, layout.chipGap)
  let height = 0
  let groups = 0
  for (const { tiles, chips } of counts) {
    if (tiles + chips === 0) continue
    const tileBlock = blockHeight(tiles, tileColumns, layout.tile, layout.tileGap)
    const chipBlock = blockHeight(chips, chipColumns, layout.chip, layout.chipGap)
    // A holder mark is as tall as a tile, so a holder with chips only still takes a tile's height.
    height += Math.max(layout.tile, tileBlock + chipBlock + (tileBlock > 0 && chipBlock > 0 ? layout.blockGap : 0))
    groups += 1
  }
  return groups === 0 ? 0 : height + (groups - 1) * layout.groupGap
}

export interface EffectGroupView<T> {
  group: EffectGroup
  /** Effects the game draws with an icon, in the engine's order. */
  tiles: T[]
  /** Effects without a game icon, in the engine's order. */
  chips: T[]
}

export interface EffectGrid<T> {
  groups: EffectGroupView<T>[]
  visible: number
  /** Eligible effects not drawn: visible + overflow always equals eligible. */
  overflow: number
}

/** An unfitted panel (a review stage that grows with its content) shows at most this many effects. */
export const unfittedEffectLimit = 48

/**
 * Splits the engine's display set into drawn effects and an overflow count. The engine decides which effects are
 * eligible (Go effectDisplayFor) and their priority order; this keeps the longest prefix of that order that fits the
 * panel once grouped by holder (tiles first, then chips), leaving a line for the count of hidden effects whenever one is
 * needed. Unclassified effects are not part of the display set: they only need that line. The split depends only on
 * the listed effects, their holders and the panel's measured size, so countdowns and animations cannot make it move.
 */
export function fitEffectGrid<T>(listed: readonly T[], groupOf: (item: T) => EffectGroup, isTile: (item: T) => boolean, eligible: number, unclassified: number, layout: EffectGridLayout | undefined): EffectGrid<T> {
  const total = Math.min(listed.length, eligible)
  let visible = Math.min(total, unfittedEffectLimit)
  if (layout) {
    const counts = new Map<string, GroupCount>()
    visible = 0
    // Height grows with every effect; only the hint line can disappear, and only when every eligible effect is drawn.
    // So the prefixes that fit are 1..k, plus possibly the whole set without a hint: no early exit.
    for (let next = 1; next <= total; next++) {
      const item = listed[next - 1]!
      const key = groupOf(item).key
      const count = counts.get(key) ?? { tiles: 0, chips: 0 }
      if (isTile(item)) count.tiles += 1
      else count.chips += 1
      counts.set(key, count)
      const hint = next < eligible || unclassified > 0 ? layout.hint + layout.sectionGap : 0
      if (gridHeight(counts.values(), layout) <= layout.available - layout.head - layout.sectionGap - hint) visible = next
    }
  }
  const groups = new Map<string, EffectGroupView<T>>()
  for (const item of listed.slice(0, visible)) {
    const group = groupOf(item)
    const entry = groups.get(group.key) ?? { group, tiles: [], chips: [] }
    if (isTile(item)) entry.tiles.push(item)
    else entry.chips.push(item)
    groups.set(group.key, entry)
  }
  return { groups: [...groups.values()].sort((a, b) => compareEffectGroups(a.group, b.group)), visible, overflow: eligible - visible }
}
