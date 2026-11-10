/**
 * The focused series of a chart: a selected series keeps its emphasis, otherwise a hovered or keyboard-focused legend item
 * previews. A key that is no longer charted focuses nothing. Keys are the series' stable identities, never ranks or positions.
 */
export function legendFocus(hovered: string | null, locked: string | null, keys: readonly string[]): string | null {
  if (locked !== null && keys.includes(locked)) return locked
  return hovered !== null && keys.includes(hovered) ? hovered : null
}

/** Clicking the locked item releases it; clicking another item moves the lock there. */
export function toggleLegendLock(locked: string | null, key: string): string | null {
  return locked === key ? null : key
}
