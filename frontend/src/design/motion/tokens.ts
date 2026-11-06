import { springCurve, type SpringParams } from "./spring.ts"

// The single source of motion timing. CSS reads these through --ww-* variables and TypeScript through this module, so
// changing the rhythm of the product never means editing a component.

export const durations = {
  instant: 80,
  fast: 120,
  base: 180,
  moderate: 240,
  slow: 360,
  slower: 520,
} as const

export const easings = {
  /** Default for anything responding to the user: fast start, long soft landing. */
  out: "cubic-bezier(0.23, 1, 0.32, 1)",
  /** Larger surfaces: panels, page content. */
  emphasized: "cubic-bezier(0.32, 0.72, 0, 1)",
  /** Color and opacity fades. */
  standard: "cubic-bezier(0.2, 0, 0, 1)",
  /** Exits accelerate away instead of drifting. */
  exit: "cubic-bezier(0.4, 0, 1, 1)",
  /** Time itself: countdown bars must not ease. */
  linear: "linear",
} as const

export const springs = {
  /** Small controls: toggle knobs, press release, chevrons. */
  snappy: { stiffness: 420, damping: 28, mass: 0.55 },
  /** Shared indicators that travel between items. */
  morph: { stiffness: 320, damping: 29, mass: 0.85 },
  /** Page content and expanding sections. */
  gentle: { stiffness: 260, damping: 30, mass: 0.9 },
  /** Things arriving with character: applied effects, stack changes. */
  lively: { stiffness: 360, damping: 21, mass: 0.6 },
  /** Rank swaps: slight settle, no bounce that would misread as data. */
  rank: { stiffness: 240, damping: 25, mass: 0.9 },
  /** Data bars: critically damped so a bar never overstates a value. */
  data: { stiffness: 220, damping: 30, mass: 1 },
} as const satisfies Record<string, SpringParams>

export type SpringName = keyof typeof springs

/** Natural frequency for counters that follow engine values. About 700 ms to settle after an update. */
export const numberOmega = 11

/** How long a removed effect keeps its slot while it plays its exit. */
export const presenceExitMs = durations.moderate

/** Engine snapshots arrive at this cadence by default (internal/capture.DefaultPublishInterval). */
export const defaultTickMs = 250

export const staggerMs = 45

export type MotionPreference = "full" | "reduced"

export interface InstallOptions {
  /** Time multiplier for slow-motion review. 1 is the product speed. */
  scale?: number
  preference?: MotionPreference
}

/** Every CSS variable the motion system defines. Pure, so that tests can check it without a DOM. */
export function motionVariables({ scale = 1, preference = "full" }: InstallOptions = {}): Record<string, string> {
  const reduced = preference === "reduced"
  const time = (ms: number) => `${reduced ? 0 : Math.round(ms * scale)}ms`
  const variables: Record<string, string> = {}
  for (const [name, ms] of Object.entries(durations)) variables[`--ww-dur-${name}`] = time(ms)
  for (const [name, value] of Object.entries(easings)) variables[`--ww-ease-${name}`] = value
  for (const [name, params] of Object.entries(springs)) {
    const curve = springCurve(params)
    variables[`--ww-spring-${name}`] = curve.easing
    variables[`--ww-spring-${name}-dur`] = time(curve.durationMs)
  }
  variables["--ww-stagger"] = time(staggerMs)
  return variables
}

/** Installs the motion variables on the root element and follows the system reduced-motion preference. */
export function installMotionTokens(options: InstallOptions = {}): () => void {
  const root = document.documentElement
  const query = window.matchMedia("(prefers-reduced-motion: reduce)")
  const apply = () => {
    const preference = options.preference ?? (query.matches ? "reduced" : "full")
    for (const [name, value] of Object.entries(motionVariables({ ...options, preference }))) root.style.setProperty(name, value)
    root.dataset.wwMotion = preference
  }
  apply()
  query.addEventListener("change", apply)
  return () => query.removeEventListener("change", apply)
}
