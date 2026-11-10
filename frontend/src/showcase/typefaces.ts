// Development-only typeface schemes for side-by-side comparison. Bundled fonts are already registered by typography.css;
// the others load from the explicit -font-root directory, so they never enter a build.
export type SchemeId = "inter-noto" | "sarasa" | "misans" | "system"

export const schemeIds: SchemeId[] = ["inter-noto", "sarasa", "misans", "system"]

export interface FontFaceInfo {
  family: string
  file: string
  weight: string
  scope: string
  unitsPerEm: number
  ascent: number
  descent: number
  xHeight?: number
  capHeight?: number
  glyphs: number
  bytes: number
  axes: Record<string, number[]>
  /** Code points the file maps, as inclusive [first, last] pairs. Absent in manifests made before coverage was recorded. */
  ranges?: ReadonlyArray<readonly [number, number]>
}

export interface SchemeManifest {
  label: string
  license: string
  bundled: boolean
  faces: FontFaceInfo[]
}

export type FontManifest = Partial<Record<SchemeId, SchemeManifest>>

type Locale = "zh-TW" | "zh-CN" | "ja" | "ko" | "en"

const systemCjk: Record<Locale, string> = {
  "zh-TW": `"Microsoft JhengHei UI", "Microsoft YaHei UI", "Yu Gothic UI", "Malgun Gothic"`,
  "zh-CN": `"Microsoft YaHei UI", "Microsoft JhengHei UI", "Yu Gothic UI", "Malgun Gothic"`,
  ja: `"Yu Gothic UI", "Meiryo UI", "Microsoft JhengHei UI", "Malgun Gothic"`,
  ko: `"Malgun Gothic", "Microsoft JhengHei UI", "Yu Gothic UI"`,
  en: `"Microsoft JhengHei UI", "Microsoft YaHei UI", "Yu Gothic UI", "Malgun Gothic"`,
}

const notoFamily: Record<Locale, string> = { "zh-TW": "WW Noto TC", "zh-CN": "WW Noto SC", ja: "WW Noto JP", ko: "WW Noto KR", en: "WW Noto TC" }
const sarasaFamily: Record<Locale, string> = { "zh-TW": "Sarasa UI TC", "zh-CN": "Sarasa UI SC", ja: "Sarasa UI J", ko: "Sarasa UI K", en: "Sarasa UI TC" }
const misansFamilies = (key: Locale) => key === "zh-TW" || key === "en" ? ["MiSans TC", "MiSans"] : ["MiSans"]

function localeKey(locale: string): Locale {
  return (locale in systemCjk ? locale : "en") as Locale
}

/** The two stacks a scheme uses for one language. */
export function schemeStacks(scheme: SchemeId, locale: string): { latin: string; cjk: string } {
  const key = localeKey(locale)
  const system = systemCjk[key]
  switch (scheme) {
    case "inter-noto":
      return { latin: key === "en" ? `"WW Inter", "WW Inter Punctuation"` : `"WW Inter"`, cjk: `"${notoFamily[key]}", ${system}` }
    case "sarasa":
      return { latin: `"${sarasaFamily[key]}"`, cjk: `"${sarasaFamily[key]}", ${system}` }
    case "misans": {
      const family = misansFamilies(key).map(name => `"${name}"`).join(", ")
      return { latin: family, cjk: `${family}, ${system}` }
    }
    default:
      return { latin: `"Segoe UI Variable Text", "Segoe UI"`, cjk: system }
  }
}

/** The families a scheme must really provide for one language. A system font is not part of what is being compared. */
export function schemeFamilies(scheme: SchemeId, locale: string): string[] {
  const key = localeKey(locale)
  switch (scheme) {
    case "inter-noto": return ["WW Inter", notoFamily[key]]
    case "sarasa": return [sarasaFamily[key]]
    case "misans": return misansFamilies(key)
    default: return []
  }
}

/** The manifest faces a scheme needs for one language. */
export function requiredFaces(manifest: FontManifest | null, scheme: SchemeId, locale: string): FontFaceInfo[] {
  const families = schemeFamilies(scheme, locale)
  return (manifest?.[scheme]?.faces ?? []).filter(face => families.includes(face.family))
}

export type FontFaceOutcome = "loaded" | "missing" | "failed"

export type SchemeStatusKind =
  /** Uses whatever Windows provides, by design. */
  | "baseline"
  | "loading"
  /** Every required file was fetched and decoded by the browser. */
  | "loaded"
  /** The candidate directory or its manifest could not be read. */
  | "no-root"
  /** At least one required file does not exist. */
  | "missing"
  /** A required file could not be requested or decoded. */
  | "failed"

export interface SchemeStatus {
  kind: SchemeStatusKind
  missing: string[]
  failed: string[]
}

export const loadingStatus: SchemeStatus = { kind: "loading", missing: [], failed: [] }

/**
 * Combines per-file results. A family declared in CSS proves nothing, and neither does document.fonts.check(), which is
 * true when no face matches at all: only a completed request and a successful decode of every required file count.
 */
export function schemeStatusFrom(required: readonly FontFaceInfo[], outcomes: ReadonlyMap<string, FontFaceOutcome>): SchemeStatus {
  const named = (outcome: FontFaceOutcome) => required.filter(face => outcomes.get(face.file) === outcome).map(face => face.file)
  const missing = named("missing")
  const failed = named("failed")
  if (required.length === 0) return { kind: "missing", missing: [], failed: [] }
  if (missing.length > 0) return { kind: "missing", missing, failed }
  if (failed.length > 0) return { kind: "failed", missing, failed }
  return required.every(face => outcomes.get(face.file) === "loaded") ? { kind: "loaded", missing, failed } : { ...loadingStatus }
}

/** True when text in this scheme really is that scheme, so showing it under the scheme's name is honest. */
export function schemeUsable(status: SchemeStatus): boolean {
  return status.kind === "loaded" || status.kind === "baseline"
}

/** Whitespace is never drawn from a font face, so it is not a coverage question. */
function codePointsOf(text: string): Set<number> {
  const points = new Set<number>()
  for (const char of text) {
    if (char.trim() !== "") points.add(char.codePointAt(0)!)
  }
  return points
}

function inRanges(point: number, ranges: ReadonlyArray<readonly [number, number]>): boolean {
  let low = 0
  let high = ranges.length - 1
  while (low <= high) {
    const middle = (low + high) >> 1
    const range = ranges[middle]!
    if (point < range[0]) high = middle - 1
    else if (point > range[1]) low = middle + 1
    else return true
  }
  return false
}

/**
 * Characters of text that no required face maps, which the browser therefore draws with a system fallback. Undefined means
 * coverage is unknown, because a manifest does not record it; that is not reported as full coverage.
 */
export function uncoveredCharacters(text: string, faces: readonly FontFaceInfo[]): string[] | undefined {
  if (faces.length === 0 || faces.some(face => face.ranges === undefined)) return undefined
  const uncovered: string[] = []
  for (const point of codePointsOf(text)) {
    if (!faces.some(face => inRanges(point, face.ranges!))) uncovered.push(String.fromCodePoint(point))
  }
  return uncovered
}

/** Reads the manifest. An HTML fallback page or a malformed file counts as no candidate directory, never as an empty one. */
export async function fetchFontManifest(fetcher: typeof fetch = fetch): Promise<FontManifest | null> {
  try {
    const response = await fetcher("/preview-fonts/manifest.json", { cache: "no-store" })
    if (!response.ok) return null
    const manifest = await response.json() as FontManifest
    return manifest && typeof manifest === "object" ? manifest : null
  } catch {
    return null
  }
}
