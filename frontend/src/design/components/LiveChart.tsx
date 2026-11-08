import { memo, useMemo, useRef, useState } from "react"
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

function niceMax(value: number): number {
  if (value <= 0) return 1
  const magnitude = 10 ** Math.floor(Math.log10(value))
  for (const step of [1, 1.5, 2, 3, 4, 5, 7.5, 10]) {
    if (value <= step * magnitude) return step * magnitude
  }
  return 10 * magnitude
}

/** Unwritten future slots collapse onto the last known point so the path keeps its point count while it grows. */
function linePath(values: ReadonlyArray<number | null>, slots: number, max: number, height: number): string {
  let lastY = height
  let lastX = 0
  const parts: string[] = []
  for (let slot = 0; slot < slots; slot++) {
    const value = values[slot]
    if (value !== null && value !== undefined) {
      lastX = (slot / Math.max(1, slots - 1)) * plotWidth
      lastY = height - (value / max) * height
    }
    parts.push(`${slot === 0 ? "M" : "L"}${lastX.toFixed(1)} ${lastY.toFixed(1)}`)
  }
  return parts.join(" ")
}

/**
 * Hand-written SVG: a chart library would outweigh the rest of the dashboard. One y-axis, recessive grid, hover layer.
 *
 * The legend focuses a series without changing any color: hovering or focusing an item previews it, clicking locks it,
 * and Escape releases the lock. The focused line stays fully opaque and a little thicker while the others fade. Every
 * line is its own SVG layer, and every line also has a thicker copy, drawn after all lines and hidden until its line is
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
  const plot = useRef<HTMLDivElement>(null)
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
    <div className="ww-chart-body" style={{ height }} role="img" aria-label={ariaLabel}>
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
