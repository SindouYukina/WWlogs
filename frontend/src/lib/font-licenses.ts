// The fonts this product actually ships and the license files that came with them. The legal text is the unmodified file
// from each font's source archive, bundled into the executable so it can be read offline. It is never translated.
import interLicense from "../design/fonts/LICENSES/Inter-OFL.txt?raw"
import notoLicense from "../design/fonts/LICENSES/NotoSansCJK-OFL.txt?raw"

export type FontLicenseId = "inter" | "noto"
export type FontLicenseLinkKind = "project" | "fontSource" | "license"

export interface FontLicenseLink {
  kind: FontLicenseLinkKind
  url: string
}

export interface FontLicense {
  id: FontLicenseId
  name: string
  /** The bundled faces, named as their font files name them. */
  faces: string
  version: string
  authors: string
  licenseName: string
  /** The copyright notice as the license file states it. */
  notice: string
  text: string
  links: readonly FontLicenseLink[]
}

const firstLine = (text: string) => text.split(/\r?\n/, 1)[0]?.trim() ?? ""

export const fontLicenses: readonly FontLicense[] = [
  {
    id: "inter",
    name: "Inter",
    faces: "Inter Variable",
    version: "4.1",
    authors: "The Inter Project Authors (Rasmus Andersson)",
    licenseName: "SIL Open Font License, Version 1.1",
    notice: firstLine(interLicense),
    text: interLicense,
    links: [
      { kind: "project", url: "https://github.com/rsms/inter" },
      { kind: "license", url: "https://openfontlicense.org/" },
    ],
  },
  {
    id: "noto",
    name: "Noto Sans CJK",
    faces: "Noto Sans TC, Noto Sans SC, Noto Sans JP, Noto Sans KR",
    version: "2.004",
    authors: "Adobe, Google (Noto Project)",
    licenseName: "SIL Open Font License, Version 1.1",
    notice: firstLine(notoLicense),
    text: notoLicense,
    links: [
      { kind: "project", url: "https://github.com/notofonts/noto-cjk" },
      { kind: "fontSource", url: "https://github.com/google/fonts" },
      { kind: "license", url: "https://openfontlicense.org/" },
    ],
  },
]
