// Development-only typeface schemes for side-by-side comparison. Bundled fonts are already registered by typography.css;
// the others load from the explicit -asset-root directory, so they never enter a build.
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

/** The two stacks a scheme uses for one language. */
export function schemeStacks(scheme: SchemeId, locale: string): { latin: string; cjk: string } {
  const key = (locale in systemCjk ? locale : "en") as Locale
  const system = systemCjk[key]
  switch (scheme) {
    case "inter-noto":
      return { latin: key === "en" ? `"WW Inter", "WW Inter Punctuation"` : `"WW Inter"`, cjk: `"${notoFamily[key]}", ${system}` }
    case "sarasa":
      return { latin: `"${sarasaFamily[key]}"`, cjk: `"${sarasaFamily[key]}", ${system}` }
    case "misans": {
      const family = key === "zh-TW" || key === "en" ? `"MiSans TC", "MiSans"` : `"MiSans"`
      return { latin: family, cjk: `${family}, ${system}` }
    }
    default:
      return { latin: `"Segoe UI Variable Text", "Segoe UI"`, cjk: system }
  }
}

let injected = false

/** Registers Development comparison faces once. The browser fetches each file only when a glyph needs it. */
export async function loadFontManifest(): Promise<FontManifest> {
  const response = await fetch("/preview-fonts/manifest.json")
  if (!response.ok) throw new Error("preview fonts unavailable")
  const manifest = await response.json() as FontManifest
  if (!injected) {
    injected = true
    const rules: string[] = []
    for (const [id, scheme] of Object.entries(manifest) as Array<[SchemeId, SchemeManifest]>) {
      if (scheme.bundled) continue
      for (const face of scheme.faces) {
        rules.push(`@font-face { font-family: "${face.family}"; src: url("/preview-fonts/${id}/${face.file}"); font-weight: ${face.weight}; font-display: swap; }`)
      }
    }
    const style = document.createElement("style")
    style.textContent = rules.join("\n")
    document.head.append(style)
  }
  return manifest
}
