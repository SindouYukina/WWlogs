// Official themes. IDs are stable and language-independent; internal/desktop/settings.go validates the same list.
export const themeIds = ["electro", "aero", "havoc", "glacio", "fusion"] as const
export type ThemeId = (typeof themeIds)[number]

export const defaultTheme: ThemeId = "electro"

/** Each theme belongs to one character, addressed by RoleInfo.Id. Display names come from locale resources. */
export const themeCharacters: Readonly<Record<ThemeId, number>> = {
  electro: 1312,
  aero: 1409,
  havoc: 1508,
  glacio: 1107,
  fusion: 1209,
}

export function resolveTheme(value: string | undefined): ThemeId {
  return themeIds.find(theme => theme === value) ?? defaultTheme
}
