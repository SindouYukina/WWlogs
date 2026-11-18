/** The locale a window shows for a snapshot: the snapshot's own when it is a bundled locale, otherwise the fallback. */
export function snapshotLocale(locale: string, supported: readonly string[], fallback: string): string {
  return supported.includes(locale) ? locale : fallback
}

/** The part of an i18next instance that applyUiLocale uses. */
export interface UiLanguage {
  readonly language?: string
  changeLanguage(language: string): unknown
}

/**
 * Applies a window's effective locale. i18next announces every changeLanguage call, even to the language it already
 * has, and every component that translates text renders again on the announcement, so a snapshot that keeps the
 * language leaves i18next and the document alone. Returns whether the language changed.
 */
export function applyUiLocale(i18n: UiLanguage, root: { lang: string }, locale: string): boolean {
  if (root.lang !== locale) root.lang = locale
  if (i18n.language === locale) return false
  void i18n.changeLanguage(locale)
  return true
}
