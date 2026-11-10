/** A vertex of a drawn line, in the chart's drawing units. */
export type ChartVertex = readonly [x: number, y: number]

/**
 * The vertices a line is drawn through, one per slot: a null value stays on the last known point, so the line keeps its
 * vertex count while it grows and the geometry always matches what the path shows.
 */
export function lineVertices(values: ReadonlyArray<number | null>, slots: number, max: number, height: number, plotWidth: number): ChartVertex[] {
  let lastY = height
  let lastX = 0
  const vertices: ChartVertex[] = []
  for (let slot = 0; slot < slots; slot++) {
    const value = values[slot]
    if (value !== null && value !== undefined) {
      lastX = (slot / Math.max(1, slots - 1)) * plotWidth
      lastY = height - (value / max) * height
    }
    vertices.push([lastX, lastY])
  }
  return vertices
}

export interface HitLine {
  key: string
  aggregate?: boolean
  vertices: readonly ChartVertex[]
}

/** How close a line must be, in CSS pixels, for a click to select it: a little more than the width of a finger on a thin line. */
export const lineHitReach = 12

/** Distances closer than this are the same line for picking; a team total lying on one role's line never takes the click. */
const tieMargin = 0.5

/**
 * The line nearest the pointer within reach, or null. Pointer position and vertices are in CSS pixels after scaling, so the
 * reach does not change with how far the chart is stretched. The nearest line wins rather than the topmost one; on an exact
 * tie a character's line is preferred to a team total drawn on top of it.
 */
export function nearestLine(lines: readonly HitLine[], pointerX: number, pointerY: number, scaleX: number, scaleY: number, reach = lineHitReach): string | null {
  let best: { key: string; aggregate: boolean; distance: number } | null = null
  for (const line of lines) {
    const distance = distanceToLine(line.vertices, pointerX, pointerY, scaleX, scaleY)
    if (distance > reach) continue
    const aggregate = line.aggregate === true
    if (best === null || distance < best.distance - tieMargin || (Math.abs(distance - best.distance) <= tieMargin && best.aggregate && !aggregate)) {
      best = { key: line.key, aggregate, distance }
    }
  }
  return best === null ? null : best.key
}

function distanceToLine(vertices: readonly ChartVertex[], pointerX: number, pointerY: number, scaleX: number, scaleY: number): number {
  let nearest = Number.POSITIVE_INFINITY
  let previous: ChartVertex | undefined
  for (const vertex of vertices) {
    if (previous !== undefined) nearest = Math.min(nearest, distanceToSegment(pointerX, pointerY, previous[0] * scaleX, previous[1] * scaleY, vertex[0] * scaleX, vertex[1] * scaleY))
    previous = vertex
  }
  return nearest
}

function distanceToSegment(x: number, y: number, fromX: number, fromY: number, toX: number, toY: number): number {
  const dx = toX - fromX
  const dy = toY - fromY
  const lengthSquared = dx * dx + dy * dy
  const along = lengthSquared === 0 ? 0 : Math.max(0, Math.min(1, ((x - fromX) * dx + (y - fromY) * dy) / lengthSquared))
  return Math.hypot(x - (fromX + along * dx), y - (fromY + along * dy))
}
