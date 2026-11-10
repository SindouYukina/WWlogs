/** What a typed or pasted whole-number entry means. */
export type NumberEntry = { kind: "empty" } | { kind: "invalid" } | { kind: "number"; value: number }

const numeric = /^[+-]?(\d+(\.\d*)?|\.\d+)$/

/**
 * Reads the text of a whole-number field. Full-width digits and a trailing percent sign, which an IME or a paste can
 * bring, are accepted; a decimal rounds to the nearest whole number. Anything else is not a number.
 */
export function readNumberEntry(text: string): NumberEntry {
  const trimmed = text.normalize("NFKC").trim().replace(/%$/, "").trim()
  if (trimmed === "") return { kind: "empty" }
  if (!numeric.test(trimmed)) return { kind: "invalid" }
  // Math.round(-0.4) is -0; adding 0 keeps the result a plain zero.
  return { kind: "number", value: Math.round(Number(trimmed)) + 0 }
}

/** The value to preview while typing: only a complete number within the range, so an unfinished entry changes nothing. */
export function previewNumberEntry(text: string, min: number, max: number): number | undefined {
  const entry = readNumberEntry(text)
  return entry.kind === "number" && entry.value >= min && entry.value <= max ? entry.value : undefined
}

/** The value Enter or leaving the field commits: a number is clamped to the range; empty or other text keeps the fallback. */
export function commitNumberEntry(text: string, fallback: number, min: number, max: number): number {
  const entry = readNumberEntry(text)
  return entry.kind === "number" ? Math.min(max, Math.max(min, entry.value)) : fallback
}

/** The next multiple of step above or below value, within the range, so a button step from 137 lands on 140 or 135. */
export function stepNumber(value: number, step: number, direction: 1 | -1, min: number, max: number): number {
  const next = direction > 0 ? Math.floor(value / step) * step + step : Math.ceil(value / step) * step - step
  return Math.min(max, Math.max(min, next))
}
