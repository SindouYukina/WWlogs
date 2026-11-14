import { memo, useCallback, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from "react"

/** One day of the heatmap. level is 0 for none and 1–4 by the share of the busiest day. */
export interface HeatmapDay {
  date: string
  count: number
  level: 0 | 1 | 2 | 3 | 4
  weekday: number
}

export interface ActivityHeatmapProps {
  /** Week columns, Sunday first; null marks a day outside the year. */
  weeks: ReadonlyArray<ReadonlyArray<HeatmapDay | null>>
  /** The first week column of each month. */
  monthStarts: readonly number[]
  selected?: string
  today?: string
  onSelect: (date: string) => void
  monthLabel: (month: number) => string
  weekdayLabel: (weekday: number) => string
  /** The text a day says on hover, on focus and to assistive technology. */
  dayLabel: (day: HeatmapDay) => string
  less: string
  more: string
  label: string
}

const levels = [0, 1, 2, 3, 4] as const
// Weekday rows that carry a label, as a contribution calendar does.
const labelledWeekdays = [1, 3, 5]

/**
 * A year of days as a contribution calendar: one column per week, a cell per day shaded by its count. A day is chosen by
 * click or by keyboard (arrow keys move a day or a week, Home/End jump to the row's ends, Enter or Space chooses). Colors come
 * from the theme's accent, so every theme draws its own ramp.
 */
export const ActivityHeatmap = memo(function ActivityHeatmap({ weeks, monthStarts, selected, today, onSelect, monthLabel, weekdayLabel, dayLabel, less, more, label }: ActivityHeatmapProps) {
  const frame = useRef<HTMLDivElement>(null)
  const [tip, setTip] = useState<{ text: string; x: number; y: number } | null>(null)
  const days = useMemo(() => weeks.flat().filter((day): day is HeatmapDay => day !== null), [weeks])
  const index = useMemo(() => new Map(days.map((day, position) => [day.date, position])), [days])
  const [focusDate, setFocusDate] = useState<string | undefined>(undefined)
  // The roving tab stop: the chosen day, else today, else the first day of the year.
  const tabStop = focusDate !== undefined && index.has(focusDate) ? focusDate : selected !== undefined && index.has(selected) ? selected : today !== undefined && index.has(today) ? today : days[0]?.date

  const showTip = useCallback((day: HeatmapDay, element: HTMLElement) => {
    const box = frame.current?.getBoundingClientRect()
    const cell = element.getBoundingClientRect()
    if (!box) return
    // The tip stays inside the heatmap, so a day at either edge never pushes it out of the card.
    const x = Math.min(Math.max(cell.left + cell.width / 2 - box.left, 96), Math.max(96, box.width - 96))
    setTip({ text: dayLabel(day), x, y: cell.top - box.top })
  }, [dayLabel])

  const move = (event: KeyboardEvent<HTMLButtonElement>, day: HeatmapDay) => {
    const position = index.get(day.date) ?? 0
    let target = position
    switch (event.key) {
      case "ArrowLeft": target = position - 7; break
      case "ArrowRight": target = position + 7; break
      case "ArrowUp": target = position - 1; break
      case "ArrowDown": target = position + 1; break
      case "Home": target = position - day.weekday; break
      case "End": target = position + (6 - day.weekday); break
      case "Enter":
      case " ":
        event.preventDefault()
        onSelect(day.date)
        return
      default: return
    }
    event.preventDefault()
    const next = days[Math.max(0, Math.min(days.length - 1, target))]
    if (!next) return
    setFocusDate(next.date)
    frame.current?.querySelector<HTMLButtonElement>(`[data-date="${next.date}"]`)?.focus()
  }

  return <div className="ww-heatmap" ref={frame} onPointerLeave={() => setTip(null)}>
    <div className="ww-heatmap-scroll"><div className="ww-heatmap-body" style={{ "--ww-heatmap-weeks": weeks.length } as CSSProperties}>
      <div className="ww-heatmap-grid" role="group" aria-label={label}>
        {monthStarts.map((week, month) => week === undefined ? null : <span key={`m${month}`} className="ww-heatmap-month" aria-hidden="true" style={{ gridColumn: week + 2 }}>{monthLabel(month)}</span>)}
        {labelledWeekdays.map(weekday => <span key={`w${weekday}`} className="ww-heatmap-weekday" aria-hidden="true" style={{ gridRow: weekday + 2 }}>{weekdayLabel(weekday)}</span>)}
        {weeks.map((week, column) => week.map((day, row) => day === null ? null : <button
          key={day.date}
          type="button"
          className="ww-heatmap-cell"
          style={{ gridColumn: column + 2, gridRow: row + 2 }}
          data-date={day.date}
          data-level={day.level}
          data-count={day.count}
          data-today={day.date === today || undefined}
          aria-pressed={day.date === selected}
          aria-label={dayLabel(day)}
          tabIndex={day.date === tabStop ? 0 : -1}
          onClick={() => { setFocusDate(day.date); onSelect(day.date) }}
          onKeyDown={event => move(event, day)}
          onPointerEnter={event => showTip(day, event.currentTarget)}
          onFocus={event => { setFocusDate(day.date); showTip(day, event.currentTarget) }}
          onBlur={() => setTip(null)}
        />))}
      </div>
      <div className="ww-heatmap-legend" aria-hidden="true">
        <span>{less}</span>
        {levels.map(level => <span key={level} className="ww-heatmap-cell ww-heatmap-swatch" data-level={level} />)}
        <span>{more}</span>
      </div>
    </div></div>
    {tip && <div className="ww-heatmap-tip" role="presentation" style={{ left: tip.x, top: tip.y }}>{tip.text}</div>}
  </div>
})
