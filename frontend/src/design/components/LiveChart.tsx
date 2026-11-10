import { memo, useMemo, useRef, useState } from "react"
import { lineVertices, nearestLine } from "./chart-hit"
import { legendFocus, toggleLegendLock } from "./legend-focus"

export interface ChartSeries {
  key: string
  label: string
  /** CSS color, normally var(--ww-element-*). The same entity keeps its color regardless of rank. */
  color: string
  /** One value per slot; null slots are not drawn. All series share one slot count. */
  values: ReadonlyArray<number | null>
  /** A total of the other series (the team line). It dims less while another series is focused, so it never reads as changed. */
  aggregate?: boolean
}

export interface LiveChartProps {
  series: readonly ChartSeries[]
  /** Number of x slots. Keeping this fixed lets line paths morph between updates. */
  slots: number
  height?: number
  formatValue: (value: number) => string
  formatSlot: (slot: number) => string
  emptyLabel: string
  ariaLabel: string
  /** What the chart shows, such as the encounter. A new scope clears the legend's hover and lock. */
  scope?: string
}

const plotWidth = 1000
/** How far the pointer may travel between press and release, in CSS pixels, for the release to still be a click. */
const clickMoveLimit = 5

function niceMax(value: number): number {
  if (value <= 0) return 1
  const magnitude = 10 ** Math.floor(Math.log10(value))
  for (const step of [1, 1.5, 2, 3, 4, 5, 7.5, 10]) {
    if (value <= step * magnitude) return step * magnitude
  }
  return 10 * magnitude
}

/** The path through a line's vertices. */
function linePath(values: ReadonlyArray<number | null>, slots: number, max: number, height: number): string {
  return lineVertices(values, slots, max, height, plotWidth).map(([x, y], slot) => `${slot === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ")
}

/**
 * Hand-written SVG: a chart library would outweigh the rest of the dashboard. One y-axis, recessive grid, hover layer.
 *
 * A click on a line selects it: the selection is the nearest line within reach of the pointer, never whichever line is
 * drawn on top. Clicking another line moves the selection, clicking the selected line releases it, and clicking anywhere
 * else in the chart releases it. Selection lives only in this component and does not ask the engine for anything.
 *
 * The legend focuses a series without changing any color: hovering or focusing an item previews it while nothing is
 * selected, clicking locks it, and Escape releases the lock. A locked series keeps its emphasis however the pointer moves.
 *
 * The focused line stays fully opaque and a little thicker while the others fade. Every line is its own SVG layer, and every line also has a thicker copy, drawn after all lines and hidden until its line is
 * focused, so a focused line is never covered by another of the same element color. Focus therefore changes only layer
 * opacity, both ways: the others fade, the copy fades in and back out. The compositor can run that without repainting a
 * line or restyling per frame, and nothing is mounted or removed. The transitions run only when the focus changes and
 * take the motion duration tokens, so reduced motion applies them at once. Data updates only change each path's d.
 */
export const LiveChart = memo(function LiveChart({ series, slots, height = 200, formatValue, formatSlot, emptyLabel, ariaLabel, scope }: LiveChartProps) {
  const [hover, setHover] = useState<number | null>(null)
  const [hovered, setHovered] = useState<string | null>(null)
  const [locked, setLocked] = useState<string | null>(null)
  const [focusScope, setFocusScope] = useState(scope)
  if (focusScope !== scope) {
    // A new encounter starts without the previous one's hover or lock.
    setFocusScope(scope)
    setHovered(null)
    setLocked(null)
  }
  // A selection is cleared when its series leaves the chart; a data update of the same series keeps it.
  if (locked !== null && !series.some(s => s.key === locked)) setLocked(null)
  const plot = useRef<HTMLDivElement>(null)
  const pressedAt = useRef<{ x: number; y: number } | null>(null)
  const max = useMemo(() => niceMax(Math.max(0, ...series.flatMap(s => s.values.map(v => v ?? 0)))), [series])
  const hasData = series.some(s => s.values.some(v => v !== null))
  // Paths depend on the data only, so a focus change never rebuilds them.
  const paths = useMemo(() => new Map(series.map(s => [s.key, linePath(s.values, slots, max, height)])), [series, slots, max, height])
  const focus = legendFocus(hovered, locked, series.map(s => s.key))
  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect()
    setHover(Math.max(0, Math.min(slots - 1, Math.round(((event.clientX - bounds.left) / bounds.width) * (slots - 1)))))
  }
  const release = (key: string) => setHovered(current => current === key ? null : current)
  const onChartPointerDown = (event: React.PointerEvent<HTMLDivElement>) => { pressedAt.current = { x: event.clientX, y: event.clientY } }
  const onChartClick = (event: React.MouseEvent<HTMLDivElement>) => {
    const press = pressedAt.current
    pressedAt.current = null
    // Only a press and release in place is a click on the chart; a drag that ends here is not.
    if (press === null || Math.hypot(event.clientX - press.x, event.clientY - press.y) > clickMoveLimit) return
    const box = plot.current?.getBoundingClientRect()
    const inside = box !== undefined && event.clientX >= box.left && event.clientX <= box.right && event.clientY >= box.top && event.clientY <= box.bottom
    const hit = inside && hasData ? nearestLine(series.map(s => ({ key: s.key, aggregate: s.aggregate, vertices: lineVertices(s.values, slots, max, height, plotWidth) })), event.clientX - box.left, event.clientY - box.top, box.width / plotWidth, box.height / height) : null
    setLocked(current => hit === null ? null : toggleLegendLock(current, hit))
  }
  const hoverX = hover === null ? 0 : (hover / Math.max(1, slots - 1)) * 100
  const ticks = [1, 0.5, 0]
  return <div className="ww-chart" data-focusing={focus !== null}>
    <div className="ww-chart-legend" role="group" aria-label={ariaLabel} onKeyDown={event => { if (event.key === "Escape") setLocked(null) }}>
      {series.map(s => <button key={s.key} type="button" className="ww-chart-legend-item" data-key={s.key} data-focused={s.key === focus} aria-pressed={s.key === locked}
        onPointerEnter={() => setHovered(s.key)} onPointerLeave={() => release(s.key)} onFocus={() => setHovered(s.key)} onBlur={() => release(s.key)}
        onClick={() => setLocked(current => toggleLegendLock(current, s.key))}>
        <i style={{ background: s.color }} />{s.label}
      </button>)}
    </div>
    <div className="ww-chart-body" style={{ height }} role="img" aria-label={ariaLabel} onPointerDown={onChartPointerDown} onClick={onChartClick}>
      <div className="ww-chart-axis ww-num" aria-hidden="true">{ticks.map(t => <span key={t} style={{ top: `${(1 - t) * 100}%` }}>{formatValue(max * t)}</span>)}</div>
      <div ref={plot} className="ww-chart-plot" onPointerMove={onPointerMove} onPointerLeave={() => setHover(null)}>
        {ticks.map(t => <div key={t} className="ww-chart-grid" style={{ top: `${(1 - t) * 100}%` }} />)}
        {hasData
          ? <>
              {series.map(s => <svg key={s.key} className="ww-chart-layer" data-key={s.key} data-focused={s.key === focus} data-aggregate={s.aggregate === true} viewBox={`0 0 ${plotWidth} ${height}`} preserveAspectRatio="none" aria-hidden="true">
                <path className="ww-chart-line" data-key={s.key} data-focused={s.key === focus} data-aggregate={s.aggregate === true} stroke={s.color} d={paths.get(s.key)} />
              </svg>)}
              {series.map(s => <svg key={`top-${s.key}`} className="ww-chart-layer ww-chart-layer-top" data-key={s.key} data-shown={s.key === focus} viewBox={`0 0 ${plotWidth} ${height}`} preserveAspectRatio="none" aria-hidden="true">
                <path className="ww-chart-line ww-chart-line-top" stroke={s.color} d={paths.get(s.key)} />
              </svg>)}
            </>
          : <div className="ww-chart-empty">{emptyLabel}</div>}
        {hover !== null && hasData && <>
          <div className="ww-chart-cross" style={{ left: `${hoverX}%` }} />
          <div className="ww-chart-tip" data-flip={hoverX > 66} style={{ left: `${hoverX}%` }}>
            <div className="ww-chart-tip-title ww-num">{formatSlot(hover)}</div>
            {series.map(s => {
              const value = s.values[hover]
              return <div key={s.key} className="ww-chart-tip-row" data-key={s.key} data-focused={focus === null ? undefined : s.key === focus}><i style={{ background: s.color }} /><span className="ww-truncate">{s.label}</span><b className="ww-num">{value === null || value === undefined ? "—" : formatValue(value)}</b></div>
            })}
          </div>
        </>}
      </div>
    </div>
  </div>
})
