// How a press on the DPS lines of the History analysis timeline is read: a drag along the time axis selects a range, a
// press that stays in place is a click, and a press that wandered off is neither. Pure, so tests can check every case.

/** How far, in CSS pixels, a press may move and still be a click: the jitter of a hand on a mouse or a pen, not a drag. */
export const clickMoveLimit = 5

export interface PlotPress {
  /** Where the press started, in CSS pixels of the viewport. */
  x: number
  y: number
  /** The moment of the encounter under the press, in milliseconds. */
  ms: number
  /** It moved beyond the click limit at some moment, in any direction: it is no longer a click, even after it comes back. */
  wandered: boolean
}

/** The press once the pointer has reached (clientX, clientY). */
export function pressMovedTo(press: PlotPress, clientX: number, clientY: number): PlotPress {
  return press.wandered || Math.hypot(clientX - press.x, clientY - press.y) <= clickMoveLimit ? press : { ...press, wandered: true }
}

/**
 * The range a press covers with the pointer at clientX (the moment ms): there is one only once the pointer has moved
 * along the time axis beyond the click limit, so moving back within it shows and commits none.
 */
export function draggedRange(press: PlotPress, clientX: number, ms: number): { fromMs: number; toMs: number } | null {
  return Math.abs(clientX - press.x) > clickMoveLimit ? { fromMs: Math.min(press.ms, ms), toMs: Math.max(press.ms, ms) } : null
}

/** What releasing a press does: commit a range, select or release a line, return to the whole encounter, or nothing. */
export type PlotRelease = { kind: "range"; fromMs: number; toMs: number } | { kind: "line"; key: string | null } | { kind: "whole" } | null

export interface PlotSelection {
  /** The selected line, or null. */
  focused: string | null
  /** The selected range, or null for the whole encounter. */
  range: { fromMs: number; toMs: number } | null
  minimumRangeMs: number
}

/**
 * A release at (clientX, clientY), the moment ms. A drag commits the range it covers when that is long enough, and nothing
 * otherwise. A press that wandered beyond the click limit and did not end as a drag does nothing. A click on a line selects
 * that line, or releases it when it is the selected one, and keeps the range; a click away from every line returns a
 * selected range to the whole encounter and keeps the line, and does nothing without a range. `lineAt` names the line
 * within reach of the release (null for none); it is asked only for a click.
 */
export function plotRelease(press: PlotPress, clientX: number, clientY: number, ms: number, lineAt: () => string | null, selection: PlotSelection): PlotRelease {
  const range = draggedRange(press, clientX, ms)
  if (range) return range.toMs - range.fromMs >= selection.minimumRangeMs ? { kind: "range", ...range } : null
  if (pressMovedTo(press, clientX, clientY).wandered) return null
  const hit = lineAt()
  if (hit !== null) return { kind: "line", key: hit === selection.focused ? null : hit }
  return selection.range ? { kind: "whole" } : null
}
