/**
 * The focused series of a chart legend: a hovered or keyboard-focused item previews, otherwise the locked one is shown.
 * A key that is no longer charted focuses nothing. Keys are the series' stable identities, never ranks or positions.
 */
export function legendFocus(hovered: string | null, locked: string | null, keys: readonly string[]): string | null {
  if (hovered !== null && keys.includes(hovered)) return hovered
  return locked !== null && keys.includes(locked) ? locked : null
}

/** Clicking the locked item releases it; clicking another item moves the lock there. */
export function toggleLegendLock(locked: string | null, key: string): string | null {
  return locked === key ? null : key
}
