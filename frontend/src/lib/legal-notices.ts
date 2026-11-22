// WWlogs' own license and the notice for the Wuthering Waves material it shows. The license text is the unmodified LICENSE
// file at the repository root, which the build reads (vite.env.mjs) and bundles into the executable so it can be read
// offline. It is never translated.

export interface NoticeLink {
  url: string
}

/** The license of WWlogs' own source code. Its name and SPDX identifier are the same in every language. */
export const productLicense = {
  name: "GNU Affero General Public License v3.0 only",
  spdx: "AGPL-3.0-only",
  text: __WWLOGS_LICENSE_TEXT__,
} as const

/** The source repository, as the repository's Git remote (origin) names it. */
export const sourceRepository: NoticeLink = { url: "https://github.com/SindouYukina/WWlogs" }

/**
 * The rights holder of the Wuthering Waves material WWlogs shows, and the game's official website. The holder is a proper
 * name and stays the same in every language; the translated notice text names it through interpolation.
 */
export const gameMaterial: { holder: string; officialSite: NoticeLink } = {
  holder: "KURO GAMES",
  officialSite: { url: "https://wutheringwaves.kurogames.com/" },
}
