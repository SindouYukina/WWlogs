import { cloneElement, isValidElement, memo, useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ButtonHTMLAttributes, type ReactNode } from "react"
import { createPortal } from "react-dom"
import { commitNumberEntry, previewNumberEntry, readNumberEntry, stepNumber } from "@/lib/number-entry"
import { Icon } from "./Icon"

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost" | "danger"
  size?: "sm" | "md"
}

/** Press compresses at once and releases on a spring; hover and color changes never move layout. */
export function Button({ variant = "secondary", size = "md", className, type = "button", ...props }: ButtonProps) {
  return <button {...props} type={type} className={className ? `ww-btn ${className}` : "ww-btn"} data-variant={variant} data-size={size} />
}

export interface ToggleProps {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  disabled?: boolean
}

export const Toggle = memo(function Toggle({ checked, onChange, label, disabled }: ToggleProps) {
  return <button type="button" role="switch" className="ww-toggle" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}><span className="ww-toggle-knob" /></button>
})

interface IndicatorBox {
  offset: number
  size: number
}

/**
 * Tracks the active item of a group so one indicator can travel between items. The first placement does not animate.
 * Measuring happens only when the selection or container size changes, never per frame.
 */
export function useSlidingIndicator(active: string, axis: "x" | "y") {
  const container = useRef<HTMLDivElement>(null)
  const [box, setBox] = useState<IndicatorBox | null>(null)
  const [ready, setReady] = useState(false)
  const measure = useCallback(() => {
    const element = container.current?.querySelector<HTMLElement>("[data-indicator-target='true']")
    if (!element) return setBox(null)
    setBox(axis === "x" ? { offset: element.offsetLeft, size: element.offsetWidth } : { offset: element.offsetTop, size: element.offsetHeight })
  }, [axis])
  useLayoutEffect(measure, [measure, active])
  useEffect(() => {
    const element = container.current
    if (!element) return
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    const frame = requestAnimationFrame(() => setReady(true))
    return () => {
      observer.disconnect()
      cancelAnimationFrame(frame)
    }
  }, [measure])
  return { container, box, ready }
}

export interface SegmentOption<T extends string> {
  value: T
  label: ReactNode
}

export interface SegmentedProps<T extends string> {
  value: T
  options: ReadonlyArray<SegmentOption<T>>
  onChange: (value: T) => void
  label: string
  /** "tabs" and "underline" use tab semantics; the default is a radio group. */
  variant?: "segmented" | "tabs" | "underline"
  /**
   * Gives every option the width of the widest. The indicator then keeps one width and only translates, which the
   * compositor can animate without a layout per frame; use it for value scales such as intervals or rates.
   */
  equal?: boolean
}

/** Segmented control and tabs share this implementation. Only the indicator moves between options. */
export function Segmented<T extends string>({ value, options, onChange, label, variant = "segmented", equal = false }: SegmentedProps<T>) {
  const { container, box, ready } = useSlidingIndicator(value, "x")
  const tabs = variant !== "segmented"
  const onKeyDown = (event: React.KeyboardEvent) => {
    const direction = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0
    if (direction === 0) return
    event.preventDefault()
    const index = options.findIndex(option => option.value === value)
    const next = options[(index + direction + options.length) % options.length]
    if (next) onChange(next.value)
  }
  return <div ref={container} className="ww-seg" role={tabs ? "tablist" : "radiogroup"} aria-label={label} data-variant={variant} data-equal={equal || undefined} data-ready={ready} onKeyDown={onKeyDown}>
    {box && <span className="ww-seg-indicator" aria-hidden="true" style={{ width: box.size, transform: `translateX(${box.offset}px)` }} />}
    {options.map(option => {
      const selected = option.value === value
      return <button key={option.value} type="button" className="ww-seg-item" tabIndex={selected ? 0 : -1} role={tabs ? "tab" : "radio"} {...(tabs ? { "aria-selected": selected } : { "aria-checked": selected })} data-indicator-target={selected} onClick={() => onChange(option.value)}>{option.label}</button>
    })}
  </div>
}

export interface SelectOption<T extends string> {
  value: T
  label: string
}

export interface SelectProps<T extends string> {
  value: T
  options: ReadonlyArray<SelectOption<T>>
  onChange: (value: T) => void
  label: string
  disabled?: boolean
}

const closeMs = 120

export function Select<T extends string>({ value, options, onChange, label, disabled = false }: SelectProps<T>) {
  const id = useId()
  const trigger = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<"closed" | "open" | "closing">("closed")
  const [active, setActive] = useState(0)
  const [rect, setRect] = useState({ left: 0, top: 0, width: 0 })
  const selected = options.find(option => option.value === value)
  const closeTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const close = useCallback(() => {
    setState(current => current === "open" ? "closing" : current)
    clearTimeout(closeTimer.current)
    closeTimer.current = setTimeout(() => setState("closed"), closeMs)
  }, [])
  const open = () => {
    const bounds = trigger.current?.getBoundingClientRect()
    if (!bounds || disabled) return
    clearTimeout(closeTimer.current)
    setRect({ left: bounds.left, top: bounds.bottom + 4, width: bounds.width })
    setActive(Math.max(0, options.findIndex(option => option.value === value)))
    setState("open")
  }
  useEffect(() => () => clearTimeout(closeTimer.current), [])
  // A control that becomes disabled while its list is open closes the list, so no stale choice can be committed.
  useEffect(() => { if (disabled) close() }, [disabled, close])
  useEffect(() => {
    if (state !== "open") return
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node
      if (!list.current?.contains(target) && !trigger.current?.contains(target)) close()
    }
    const onScroll = (event: Event) => { if (!list.current?.contains(event.target as Node)) close() }
    document.addEventListener("pointerdown", onPointer)
    window.addEventListener("scroll", onScroll, true)
    window.addEventListener("resize", close)
    return () => {
      document.removeEventListener("pointerdown", onPointer)
      window.removeEventListener("scroll", onScroll, true)
      window.removeEventListener("resize", close)
    }
  }, [state, close])
  const commit = (index: number) => {
    const option = options[index]
    if (option) onChange(option.value)
    close()
    trigger.current?.focus()
  }
  const onKeyDown = (event: React.KeyboardEvent) => {
    if (state !== "open") {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault()
        open()
      }
      return
    }
    if (event.key === "Escape") {
      event.preventDefault()
      close()
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault()
      setActive(index => (index + (event.key === "ArrowDown" ? 1 : -1) + options.length) % options.length)
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault()
      commit(active)
    } else if (event.key === "Tab") {
      close()
    }
  }
  return <>
    <button ref={trigger} type="button" className="ww-select-trigger" role="combobox" aria-haspopup="listbox" aria-expanded={state === "open"} aria-controls={id} aria-label={label} disabled={disabled} onClick={() => state === "open" ? close() : open()} onKeyDown={onKeyDown}>
      <span className="ww-truncate">{selected?.label ?? ""}</span><Icon name="chevron" size={14} />
    </button>
    {state !== "closed" && createPortal(
      <div ref={list} className="ww-root ww-popover" data-state={state} style={{ left: rect.left, top: rect.top, width: rect.width }}>
        <div id={id} className="ww-listbox" role="listbox" aria-label={label}>
          {options.map((option, index) => <div key={option.value} role="option" className="ww-option" aria-selected={option.value === value} data-active={index === active} onPointerEnter={() => setActive(index)} onClick={() => commit(index)}>
            <span className="ww-truncate">{option.label}</span>{option.value === value && <Icon name="check" size={14} />}
          </div>)}
        </div>
      </div>, document.body)}
  </>
}

export interface SliderProps {
  value: number
  min: number
  max: number
  step?: number
  onChange: (value: number) => void
  label: string
}

/** A native range input keeps keyboard and assistive support; it sits invisibly over the drawn track. */
export function Slider({ value, min, max, step = 1, onChange, label }: SliderProps) {
  const [dragging, setDragging] = useState(false)
  const fraction = max > min ? Math.min(1, Math.max(0, (value - min) / (max - min))) : 0
  return <div className="ww-slider" data-dragging={dragging} style={{ "--ww-value": fraction } as React.CSSProperties}>
    <div className="ww-slider-track"><div className="ww-slider-fill" /></div>
    <div className="ww-slider-thumb" />
    <input type="range" aria-label={label} min={min} max={max} step={step} value={value} onChange={event => onChange(Number(event.target.value))} onPointerDown={() => setDragging(true)} onPointerUp={() => setDragging(false)} onPointerCancel={() => setDragging(false)} onLostPointerCapture={() => setDragging(false)} />
  </div>
}

export interface StepperProps {
  value: number
  unit?: string
  step: number
  min: number
  max: number
  onChange: (value: number) => void
  decreaseLabel: string
  increaseLabel: string
}

export function Stepper({ value, unit, step, min, max, onChange, decreaseLabel, increaseLabel }: StepperProps) {
  const clamp = (next: number) => Math.min(max, Math.max(min, next))
  return <div className="ww-stepper">
    <span className="ww-stepper-value"><span className="ww-num">{value}</span>{unit && <span className="ww-stepper-unit">{unit}</span>}</span>
    <button type="button" aria-label={decreaseLabel} onClick={() => onChange(clamp(value - step))}>−</button>
    <button type="button" aria-label={increaseLabel} onClick={() => onChange(clamp(value + step))}>+</button>
  </div>
}

export interface NumberFieldProps {
  value: number
  unit?: string
  /** The buttons move to the next multiple of step; typing reaches every whole number in the range. */
  step: number
  min: number
  max: number
  onChange: (value: number) => void
  label: string
  decreaseLabel: string
  increaseLabel: string
}

/**
 * A whole number that can be typed, pasted or stepped. While the user is typing, the field keeps their text even when
 * the value changes elsewhere, and only a complete in-range number previews. Enter or leaving the field commits the
 * entry, clamped to the range; empty or non-numeric text restores the value from before typing, and so does Escape.
 */
export function NumberField({ value, unit, step, min, max, onChange, label, decreaseLabel, increaseLabel }: NumberFieldProps) {
  const [text, setText] = useState<string | null>(null)
  const before = useRef(value)
  const entry = text === null ? undefined : readNumberEntry(text)
  const invalid = text !== null && entry?.kind !== "empty" && previewNumberEntry(text, min, max) === undefined
  const type = (next: string) => {
    if (text === null) before.current = value
    setText(next)
    const preview = previewNumberEntry(next, min, max)
    if (preview !== undefined && preview !== value) onChange(preview)
  }
  const commit = () => {
    if (text === null) return
    const next = commitNumberEntry(text, before.current, min, max)
    setText(null)
    if (next !== value) onChange(next)
  }
  const restore = () => {
    if (text === null) return
    setText(null)
    if (before.current !== value) onChange(before.current)
  }
  const stepBy = (direction: 1 | -1) => {
    setText(null)
    const next = stepNumber(value, step, direction, min, max)
    if (next !== value) onChange(next)
  }
  return <div className="ww-stepper" data-invalid={invalid}>
    <span className="ww-stepper-value">
      <input className="ww-stepper-input ww-num" type="text" inputMode="numeric" enterKeyHint="done" autoComplete="off" spellCheck={false} maxLength={12} aria-label={label} aria-invalid={invalid} value={text ?? String(value)}
        onChange={event => type(event.target.value)} onBlur={commit}
        onKeyDown={event => {
          if (event.key === "Enter") { event.preventDefault(); commit() }
          else if (event.key === "Escape") { event.preventDefault(); restore() }
        }} />
      {unit && <span className="ww-stepper-unit">{unit}</span>}
    </span>
    <button type="button" aria-label={decreaseLabel} disabled={value <= min} onClick={() => stepBy(-1)}>−</button>
    <button type="button" aria-label={increaseLabel} disabled={value >= max} onClick={() => stepBy(1)}>+</button>
  </div>
}

export interface TooltipProps {
  text: string
  children: ReactNode
  /**
   * "self" makes the wrapper the focusable trigger (an info icon). "child" leaves focus to the wrapped control, which the
   * text then describes, so a button keeps one tab stop and its own accessible name.
   */
  trigger?: "self" | "child"
  /** Below suits controls at the top edge of a window. */
  placement?: "top" | "bottom"
}

const showDelayMs = 240
// Half the tooltip's max-width plus a margin, so a tooltip next to a window edge stays inside the window.
const edgeInset = 148

/** Positioned at the document level so scrolling panes never clip it. Not mounted until it is needed. */
export function Tooltip({ text, children, trigger = "self", placement = "top" }: TooltipProps) {
  const anchor = useRef<HTMLSpanElement>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const describedBy = `${useId()}-text`
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const show = () => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => {
      const bounds = anchor.current?.getBoundingClientRect()
      if (!bounds) return
      const x = Math.min(Math.max(bounds.left + bounds.width / 2, edgeInset), Math.max(edgeInset, window.innerWidth - edgeInset))
      setPosition({ x, y: placement === "bottom" ? bounds.bottom + 6 : bounds.top - 6 })
    }, showDelayMs)
  }
  const hide = () => {
    clearTimeout(timer.current)
    setPosition(null)
  }
  useEffect(() => () => clearTimeout(timer.current), [])
  const self = trigger === "self"
  const content = !self && isValidElement<{ "aria-describedby"?: string }>(children) ? cloneElement(children, { "aria-describedby": describedBy }) : children
  return <span ref={anchor} className="ww-tooltip-trigger" data-trigger={trigger} tabIndex={self ? 0 : undefined} aria-label={self ? text : undefined} onPointerEnter={show} onPointerLeave={hide} onFocus={show} onBlur={hide}>
    {content}
    {!self && <span id={describedBy} hidden>{text}</span>}
    {position && createPortal(<div role="tooltip" className="ww-root ww-tooltip" data-placement={placement} style={{ left: position.x, top: position.y }}>{text}</div>, document.body)}
  </span>
}

export interface ChipProps {
  tone?: "neutral" | "good" | "warn" | "critical" | "accent"
  dot?: boolean
  children: ReactNode
}

export function Chip({ tone = "neutral", dot, children }: ChipProps) {
  return <span className="ww-chip" data-tone={tone}>{dot && <span className="ww-chip-dot" />}{children}</span>
}
