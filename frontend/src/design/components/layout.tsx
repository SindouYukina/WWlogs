import { useCallback, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { reconcilePresence, type PresenceEntry } from "../motion/spring.ts"
import { presenceExitMs } from "../motion/tokens.ts"
import { Icon, type IconName } from "./Icon"
import { useSlidingIndicator } from "./controls"

export interface RevealProps {
  /** Position in a staggered group. */
  index?: number
  className?: string
  children: ReactNode
}

/** Entrance for cards and sections. Runs once per mount. */
export function Reveal({ index = 0, className, children }: RevealProps) {
  return <div className={className ? `ww-reveal ${className}` : "ww-reveal"} style={{ "--ww-index": index } as CSSProperties}>{children}</div>
}

export interface ContentSwitchProps {
  /** A new key plays the entrance; the previous content is replaced at once rather than held for an exit. */
  contentKey: string
  direction?: "forward" | "back" | "none"
  className?: string
  children: ReactNode
}

export function ContentSwitch({ contentKey, direction = "none", className, children }: ContentSwitchProps) {
  return <div key={contentKey} className={className ? `ww-switch ${className}` : "ww-switch"} data-direction={direction}>{children}</div>
}

export function Collapse({ open, children }: { open: boolean; children: ReactNode }) {
  return <div className="ww-collapse" data-open={open} aria-hidden={!open}><div>{children}</div></div>
}

export interface CardProps {
  interactive?: boolean
  className?: string
  style?: CSSProperties
  children: ReactNode
}

/** Hover lift and pointer spotlight only move and fade dedicated layers. A resting card has no ongoing cost. */
export function Card({ interactive = false, className, style, children }: CardProps) {
  const spot = useRef<HTMLDivElement>(null)
  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect()
    if (spot.current) spot.current.style.transform = `translate(${event.clientX - bounds.left}px, ${event.clientY - bounds.top}px)`
  }
  return <div className={className ? `ww-card ${className}` : "ww-card"} style={style} data-interactive={interactive} onPointerMove={interactive ? onPointerMove : undefined}>
    {interactive && <><div className="ww-card-lift" /><div className="ww-card-fx"><div ref={spot} className="ww-card-spot" /></div></>}
    <div className="ww-card-body">{children}</div>
  </div>
}

export interface StackListProps<T> {
  items: readonly T[]
  keyOf: (item: T) => string
  rowHeight: number
  gap?: number
  renderItem: (item: T, phase: PresenceEntry<T>["phase"]) => ReactNode
  role?: "list"
  label?: string
}

/**
 * Keyed rows positioned by transform. Rows render in a stable order, so reordering never moves DOM nodes or reloads
 * images; a removed row keeps its slot through its exit while the rows below close the gap.
 */
export function StackList<T>({ items, keyOf, rowHeight, gap = 4, renderItem, role = "list", label }: StackListProps<T>) {
  const [previousItems, setPreviousItems] = useState(items)
  const [entries, setEntries] = useState<Array<PresenceEntry<T>>>(() => reconcilePresence<T>([], items, keyOf, 0, 0))
  if (items !== previousItems) {
    setPreviousItems(items)
    setEntries(reconcilePresence(entries, items, keyOf, Date.now(), presenceExitMs))
  }
  const slots = useRef(new Map<string, number>())
  items.forEach((item, index) => slots.current.set(keyOf(item), index))
  const exiting = entries.filter(entry => entry.phase === "exit")
  const soonest = exiting.reduce((min, entry) => Math.min(min, entry.removeAt ?? Infinity), Infinity)
  useEffect(() => {
    if (!Number.isFinite(soonest)) return
    const timer = setTimeout(() => {
      const now = Date.now()
      setEntries(current => current.filter(entry => {
        const keep = entry.phase !== "exit" || (entry.removeAt ?? 0) > now
        if (!keep) slots.current.delete(entry.key)
        return keep
      }))
    }, Math.max(0, soonest - Date.now()))
    return () => clearTimeout(timer)
  }, [soonest])
  const height = items.length > 0 ? items.length * (rowHeight + gap) - gap : 0
  const ordered = [...entries].sort((a, b) => a.key.localeCompare(b.key))
  return <div className="ww-stack" role={role} aria-label={label} style={{ height }}>
    {ordered.map(entry => {
      const slot = slots.current.get(entry.key) ?? 0
      return <div key={entry.key} className="ww-stack-item" role="listitem" aria-posinset={slot + 1} aria-setsize={items.length} data-phase={entry.phase} style={{ height: rowHeight, transform: `translateY(${slot * (rowHeight + gap)}px)` }}>
        <div className="ww-stack-inner" style={{ height: "100%" }}>{renderItem(entry.item, entry.phase)}</div>
      </div>
    })}
  </div>
}

export interface SettingsSectionProps {
  title: string
  collapsible?: boolean
  defaultOpen?: boolean
  children: ReactNode
}

export function SettingsSection({ title, collapsible = false, defaultOpen = true, children }: SettingsSectionProps) {
  const [open, setOpen] = useState(defaultOpen)
  const body = <div>{children}</div>
  if (!collapsible) {
    return <section className="ww-section"><div className="ww-section-head"><span>{title}</span></div>{body}</section>
  }
  return <section className="ww-section">
    <button type="button" className="ww-section-head ww-focusable" data-collapsible="true" aria-expanded={open} onClick={() => setOpen(!open)}><span>{title}</span><Icon name="chevron" size={14} /></button>
    <Collapse open={open}>{body}</Collapse>
  </section>
}

export interface SettingsRowProps {
  title: string
  description?: string
  hint?: ReactNode
  children: ReactNode
}

/** The label sits on the left and the control on the right, on every settings page. */
export function SettingsRow({ title, description, hint, children }: SettingsRowProps) {
  return <div className="ww-row">
    <div className="ww-row-label"><span className="ww-row-title">{title}{hint}</span>{description && <span className="ww-row-desc">{description}</span>}</div>
    <div className="ww-row-control">{children}</div>
  </div>
}

export interface SidebarItem<T extends string> {
  value: T
  label: string
  icon: IconName
}

export interface SidebarGroupModel<T extends string> {
  /** Empty means the group has no heading. */
  label: string
  items: ReadonlyArray<SidebarItem<T>>
  /** Keeps the group at the bottom of the rail, after everything else. */
  pinned?: boolean
}

export interface SidebarProps<T extends string> {
  value: T
  groups: ReadonlyArray<SidebarGroupModel<T>>
  onChange: (value: T) => void
  label: string
}

/** Fixed navigation. One indicator travels across the whole rail, including between groups. */
export function Sidebar<T extends string>({ value, groups, onChange, label }: SidebarProps<T>) {
  const { container, box, ready } = useSlidingIndicator(value, "y")
  const onKeyDown = useCallback((event: React.KeyboardEvent) => {
    const direction = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0
    if (direction === 0) return
    event.preventDefault()
    const flat = groups.flatMap(group => group.items)
    const index = flat.findIndex(item => item.value === value)
    const next = flat[(index + direction + flat.length) % flat.length]
    if (next) onChange(next.value)
  }, [groups, onChange, value])
  return <nav className="ww-side" aria-label={label} onKeyDown={onKeyDown}>
    <div ref={container} className="ww-side-rail">
      {box && <span className="ww-side-indicator" aria-hidden="true" data-ready={ready} style={{ height: box.size, transform: `translateY(${box.offset}px)` }} />}
      {groups.map(group => <div className="ww-side-group" data-pinned={group.pinned === true} key={group.label || "pinned"}>
        {group.label && <div className="ww-side-label">{group.label}</div>}
        <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
          {group.items.map(item => <button key={item.value} type="button" className="ww-side-item" aria-current={item.value === value ? "page" : undefined} data-indicator-target={item.value === value} onClick={() => onChange(item.value)}><Icon name={item.icon} /><span className="ww-truncate">{item.label}</span></button>)}
        </div>
      </div>)}
    </div>
  </nav>
}
