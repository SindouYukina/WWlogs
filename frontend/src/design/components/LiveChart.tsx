import { memo, useMemo, useRef, useState } from "react"

export interface ChartSeries {
  key: string
  label: string
  /** CSS color, normally var(--ww-element-*). The same entity keeps its color regardless of rank. */
  color: string
  /** One value per slot; null slots are not drawn. All series share one slot count. */
  values: ReadonlyArray<number | null>
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

/** Hand-written SVG: a chart library would outweigh the rest of the dashboard. One y-axis, recessive grid, hover layer. */
export const LiveChart = memo(function LiveChart({ series, slots, height = 200, formatValue, formatSlot, emptyLabel, ariaLabel }: LiveChartProps) {
  const [hover, setHover] = useState<number | null>(null)
  const plot = useRef<HTMLDivElement>(null)
  const max = useMemo(() => niceMax(Math.max(0, ...series.flatMap(s => s.values.map(v => v ?? 0)))), [series])
  const hasData = series.some(s => s.values.some(v => v !== null))
  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect()
    setHover(Math.max(0, Math.min(slots - 1, Math.round(((event.clientX - bounds.left) / bounds.width) * (slots - 1)))))
  }
  const hoverX = hover === null ? 0 : (hover / Math.max(1, slots - 1)) * 100
  const ticks = [1, 0.5, 0]
  return <div className="ww-chart" role="img" aria-label={ariaLabel}>
    <div className="ww-chart-legend">{series.map(s => <span key={s.key} className="ww-chart-legend-item"><i style={{ background: s.color }} />{s.label}</span>)}</div>
    <div className="ww-chart-body" style={{ height }}>
      <div className="ww-chart-axis ww-num" aria-hidden="true">{ticks.map(t => <span key={t} style={{ top: `${(1 - t) * 100}%` }}>{formatValue(max * t)}</span>)}</div>
      <div ref={plot} className="ww-chart-plot" onPointerMove={onPointerMove} onPointerLeave={() => setHover(null)}>
        {ticks.map(t => <div key={t} className="ww-chart-grid" style={{ top: `${(1 - t) * 100}%` }} />)}
        {hasData
          ? <svg viewBox={`0 0 ${plotWidth} ${height}`} preserveAspectRatio="none" aria-hidden="true">
              {series.map(s => <path key={s.key} className="ww-chart-line" stroke={s.color} d={linePath(s.values, slots, max, height)} />)}
            </svg>
          : <div className="ww-chart-empty">{emptyLabel}</div>}
        {hover !== null && hasData && <>
          <div className="ww-chart-cross" style={{ left: `${hoverX}%` }} />
          <div className="ww-chart-tip" data-flip={hoverX > 66} style={{ left: `${hoverX}%` }}>
            <div className="ww-chart-tip-title ww-num">{formatSlot(hover)}</div>
            {series.map(s => {
              const value = s.values[hover]
              return <div key={s.key} className="ww-chart-tip-row"><i style={{ background: s.color }} /><span className="ww-truncate">{s.label}</span><b className="ww-num">{value === null || value === undefined ? "—" : formatValue(value)}</b></div>
            })}
          </div>
        </>}
      </div>
    </div>
  </div>
})
