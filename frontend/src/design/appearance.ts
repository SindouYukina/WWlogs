import { installMotionTokens } from "./motion/tokens.ts"
import { resolveTheme, type ThemeId } from "./themes/registry.ts"

// Applies the persisted appearance to this window. Every window calls it from its own snapshots; nothing happens when
// the values are unchanged, so a heartbeat costs no style recalculation.
let current: { theme: ThemeId; reduceMotion: boolean } | null = null
let uninstall: (() => void) | null = null

export function applyAppearance(theme: string | undefined, reduceMotion: boolean, scale = 1): void {
  const next = resolveTheme(theme)
  if (current && current.theme === next && current.reduceMotion === reduceMotion && scale === currentScale) return
  current = { theme: next, reduceMotion }
  currentScale = scale
  document.documentElement.dataset.wwTheme = next
  uninstall?.()
  uninstall = installMotionTokens({ flavor: next, scale, preference: reduceMotion ? "reduced" : undefined })
}

let currentScale = 1
