import type { CombatTimeline } from "./combat-data.ts"
import type { Timeline, TimelineContributor, ViewSnapshot } from "./snapshot.ts"

// Adapts the engine's DPS timeline (Go EncounterTimelineSnapshot) to the chart's series. Every DPS value is the
// engine's own: the team line is its interval DPS and each contributor line is that contributor's interval damage over
// the same interval duration. Nothing is accumulated, smoothed or timed here.

/** The chart keeps at least this many slots so a young encounter grows from the left instead of stretching. */
export const minimumTimelineSlots = 61

export const teamSeriesKey = "team"
export const unassignedSeriesKey = "unassigned"

/** The series key of one contributor; roles and vehicles use the same key as their ranking rows. */
export function contributorKey(contributor: TimelineContributor): string {
  switch (contributor.kind) {
    case "role": return `role-${contributor.playerId ?? 0}-${contributor.roleId ?? 0}`
    case "vehicle": return `vehicle-${contributor.vehicleKind ?? ""}`
    default: return unassignedSeriesKey
  }
}

/** An empty timeline: the source exists, there is just no encounter yet. */
export function emptyTimeline(): CombatTimeline {
  return { slots: minimumTimelineSlots, slotSeconds: 1, series: new Map() }
}

/**
 * The chart data for the encounter the Dashboard shows. A timeline of another encounter is not shown, and an interval
 * still in progress is not drawn: only settled intervals (complete, or the real-duration last interval of a finished
 * encounter) become points.
 */
export function timelineFromView(view: ViewSnapshot | null | undefined, encounterId: string | undefined): CombatTimeline {
  const timeline: Timeline | undefined = view?.timeline
  if (!timeline || encounterId === undefined || timeline.encounterId !== encounterId) return emptyTimeline()
  const settled = timeline.points.map(point => point.state !== "in_progress" && point.dps !== null && point.durationSeconds > 0)
  const series = new Map<string, Array<number | null>>()
  series.set(teamSeriesKey, timeline.points.map((point, index) => settled[index] ? point.dps : null))
  timeline.contributors.forEach((contributor, position) => {
    series.set(contributorKey(contributor), timeline.points.map((point, index) =>
      settled[index] ? (point.contributorDamage[position] ?? 0) / point.durationSeconds : null))
  })
  return {
    slots: Math.max(minimumTimelineSlots, timeline.points.length),
    slotSeconds: timeline.intervalSeconds > 0 ? timeline.intervalSeconds : 1,
    series,
    encounterId: timeline.encounterId,
    final: timeline.final,
  }
}
