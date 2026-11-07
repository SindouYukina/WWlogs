import { memo, useEffect, useLayoutEffect, useRef } from "react"
import { motionReduced, numberOmega } from "../motion/tokens.ts"
import { planNumberUpdate, stepCritical, type NumberState } from "../motion/spring.ts"
import { animateFrames } from "../motion/ticker.ts"

export interface AnimatedNumberProps {
  /** The engine value. Undefined shows the placeholder and cancels any animation. */
  value: number | undefined
  format: (value: number) => string
  /** "tween" follows the value with a critically damped counter; "roll" spins individual digits. */
  mode?: "tween" | "roll"
  /** A changed scope (for example a new encounter) shows the new value at once and never animates across scopes. */
  scope?: string
  /** A stale value is shown as it is, without motion. */
  stale?: boolean
  placeholder?: string
  className?: string
}

/**
 * Presentation only. The display never passes the engine value: the follower cannot overshoot a rising target, and the
 * final frame writes the exact value. React renders only when props change; frames write to the DOM directly.
 */
export const AnimatedNumber = memo(function AnimatedNumber({ value, format, mode = "tween", scope = "", stale = false, placeholder = "—", className }: AnimatedNumberProps) {
  if (mode === "roll") return <RollingNumber value={value} format={format} scope={scope} stale={stale} placeholder={placeholder} className={className} />
  return <TweenNumber value={value} format={format} scope={scope} stale={stale} placeholder={placeholder} className={className} />
})

type InnerProps = Required<Pick<AnimatedNumberProps, "format" | "scope" | "stale" | "placeholder">> & Pick<AnimatedNumberProps, "value" | "className">

function TweenNumber({ value, format, scope, stale, placeholder, className }: InnerProps) {
  const node = useRef<HTMLSpanElement>(null)
  const motion = useRef<NumberState>({ position: value ?? 0, velocity: 0 })
  const target = useRef(value ?? 0)
  const shown = useRef("")
  const previous = useRef<{ value: number | undefined; scope: string }>({ value: undefined, scope })
  const stop = useRef<(() => void) | null>(null)
  const formatRef = useRef(format)
  formatRef.current = format

  const write = (next: number) => {
    const text = formatRef.current(next)
    if (text !== shown.current && node.current) {
      node.current.textContent = text
      shown.current = text
    }
  }

  useLayoutEffect(() => {
    const planned = planNumberUpdate(previous.current.value, value, previous.current.scope === scope, stale)
    // Reduced motion shows each engine value at once: the counter is decoration, the value itself is never delayed.
    const plan = planned === "animate" && motionReduced() ? "snap" : planned
    previous.current = { value, scope }
    if (plan === "hide") {
      stop.current?.()
      stop.current = null
      if (node.current) node.current.textContent = placeholder
      shown.current = placeholder
      return
    }
    const next = value as number
    if (plan === "animate" && next === target.current) return
    target.current = next
    if (plan === "snap") {
      stop.current?.()
      stop.current = null
      motion.current = { position: next, velocity: 0 }
      write(next)
      return
    }
    if (stop.current) return
    stop.current = animateFrames(dt => {
      motion.current = stepCritical(motion.current, target.current, numberOmega, dt)
      const remaining = target.current - motion.current.position
      if (Math.abs(remaining) < Math.max(0.5, Math.abs(target.current) * 1e-5) && Math.abs(motion.current.velocity) < 1) {
        motion.current = { position: target.current, velocity: 0 }
        write(target.current)
        stop.current = null
        return false
      }
      write(Math.min(motion.current.position, target.current))
      return true
    })
  }, [value, scope, stale, placeholder])
  useEffect(() => () => {
    stop.current?.()
    stop.current = null
  }, [])
  // A format change (locale, precision) rewrites the current value on the next render.
  useLayoutEffect(() => {
    if (value !== undefined && !stop.current) write(motion.current.position)
  }, [format])
  return <span ref={node} className={className ? `ww-number ww-num ${className}` : "ww-number ww-num"} data-stale={stale} />
}

const digits = ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"] as const

const Digit = memo(function Digit({ digit }: { digit: number }) {
  return <span className="ww-roll-digit" aria-hidden="true"><span className="ww-roll-strip" style={{ "--ww-digit": digit } as React.CSSProperties}>{digits.map(text => <span key={text}>{text}</span>)}</span></span>
})

function RollingNumber({ value, format, scope, stale, placeholder, className }: InnerProps) {
  const previous = useRef<{ value: number | undefined; scope: string }>({ value: undefined, scope })
  const planned = planNumberUpdate(previous.current.value, value, previous.current.scope === scope, stale)
  const plan = planned === "animate" && motionReduced() ? "snap" : planned
  useEffect(() => { previous.current = { value, scope } })
  if (plan === "hide") return <span className={className ? `ww-number ww-num ${className}` : "ww-number ww-num"} data-stale={stale}>{placeholder}</span>
  const text = format(value as number)
  // Keys count from the right so a column keeps its digit when the number gains a leading digit.
  return <span className={className ? `ww-roll ww-num ${className}` : "ww-roll ww-num"} data-snap={plan === "snap"} data-stale={stale} role="img" aria-label={text}>
    {[...text].map((character, index) => {
      const key = text.length - index
      const digit = character.charCodeAt(0) - 48
      return digit >= 0 && digit <= 9 ? <Digit key={key} digit={digit} /> : <span key={key} className="ww-roll-sep" aria-hidden="true">{character}</span>
    })}
  </span>
}
