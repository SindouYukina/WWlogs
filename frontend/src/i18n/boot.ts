/**
 * The language the first render uses: the one the host names in the window's URL when it is a bundled locale. Windows
 * without it, such as the Showcase, start on the fallback as before. i18next's fallback language is not affected.
 */
export function bootLocale(search: string, supported: readonly string[], fallback: string): string {
  const named = new URLSearchParams(search).get("lang")
  return named !== null && supported.includes(named) ? named : fallback
}
