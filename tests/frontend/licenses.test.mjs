import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"
import test from "node:test"
import { productLicenseText } from "../../frontend/vite.env.mjs"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const manifest = JSON.parse(read("frontend/locales/manifest.json"))
const fontsCss = read("frontend/src/design/typography.css")

test("the licenses list exactly the fonts the design system bundles", () => {
  const source = read("frontend/src/lib/font-licenses.ts")
  assert.ok(source.includes('name: "Inter"') && source.includes('name: "Noto Sans CJK"'))
  assert.ok(!/misans|sarasa/i.test(source), "a Development candidate must not be listed as a shipped font")
  const faces = [...fontsCss.matchAll(/url\("\.\/fonts\/([^"]+)"\)/g)].map(match => match[1])
  assert.deepEqual([...new Set(faces)].sort(), ["inter-var.woff2", "noto-jp-var.woff2", "noto-kr-var.woff2", "noto-sc-var.woff2", "noto-tc-var.woff2"])
})

test("the legal text is the complete, unmodified license file", () => {
  const source = read("frontend/src/lib/font-licenses.ts")
  for (const [file, notice] of [["Inter-OFL.txt", "Copyright (c) 2016 The Inter Project Authors (https://github.com/rsms/inter)"], ["NotoSansCJK-OFL.txt", "Copyright 2014-2021 Adobe (http://www.adobe.com/), with Reserved Font Name 'Source'"]]) {
    assert.ok(source.includes(`fonts/LICENSES/${file}?raw`), `${file} must be imported directly`)
    const text = read(`frontend/src/design/fonts/LICENSES/${file}`)
    assert.equal(text.split("\n")[0].trim(), notice)
    for (const clause of ["SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007", "PREAMBLE", "DEFINITIONS", "TERMINATION", "DISCLAIMER"]) assert.ok(text.includes(clause), `${file}: ${clause}`)
    // The two source archives word this heading differently; each keeps its own.
    assert.match(text, /PERMISSION (AND|&) CONDITIONS/, file)
  }
})

test("every locale names the page and its labels, and none translates the legal text", () => {
  const keys = ["usage", "faces", "version", "authors", "copyright", "license", "links", "showText", "hideText", "intro", "linkFailed"]
  const titles = { "zh-TW": "授權", "zh-CN": "授权", en: "Licenses", ja: "ライセンス", ko: "라이선스" }
  assert.equal(manifest.supportedLocales.length, 5)
  for (const locale of manifest.supportedLocales) {
    const settings = JSON.parse(read(`frontend/locales/${locale}/settings.json`))
    assert.equal(settings.pages.licenses, titles[locale], locale)
    for (const key of keys) assert.ok(settings.licenses[key], `${locale}: ${key}`)
    for (const key of ["inter", "noto"]) assert.ok(settings.licenses.usageOf[key], `${locale}: usage of ${key}`)
    for (const key of ["project", "fontSource", "license"]) assert.ok(settings.licenses.link[key], `${locale}: link ${key}`)
    assert.ok(!/PERMISSION (AND|&) CONDITIONS/.test(JSON.stringify(settings)), `${locale} must not carry license text`)
  }
})

// SHA-256 of the FSF's https://www.gnu.org/licenses/agpl-3.0.txt, with its LF line endings.
const agplText = "0d96a4ff68ad6d4b6f1f30f713b18d5184912ba8dd389f86aa7710db079abcb0"
const sha256 = text => createHash("sha256").update(text, "utf8").digest("hex")

test("LICENSE is the unmodified FSF text of the GNU AGPL v3.0, and the build bundles exactly that text", () => {
  // A checkout may store the file with CRLF; the terms are the same, and the build passes the LF original on.
  const license = read("LICENSE")
  assert.equal(sha256(license), agplText, "LICENSE must stay the FSF's agpl-3.0.txt, word for word")
  assert.match(license, /^ +GNU AFFERO GENERAL PUBLIC LICENSE\n +Version 3, 19 November 2007\n/)
  assert.equal(sha256(productLicenseText()), agplText, "Settings > About must show the LICENSE file itself")
  assert.ok(read("frontend/vite.config.ts").includes("__WWLOGS_LICENSE_TEXT__: JSON.stringify(productLicenseText())"))
  const source = read("frontend/src/lib/legal-notices.ts")
  assert.ok(source.includes('name: "GNU Affero General Public License v3.0 only"') && source.includes('spdx: "AGPL-3.0-only"') && source.includes("text: __WWLOGS_LICENSE_TEXT__"))
  assert.doesNotMatch(source, /or[- ]later/i)
})

test("every locale states the software license and the game-material notice, with the proper names supplied by the code", () => {
  for (const locale of manifest.supportedLocales) {
    const settings = JSON.parse(read(`frontend/locales/${locale}/settings.json`))
    for (const key of ["softwareLicense", "gameMaterial"]) assert.ok(settings.sections[key], `${locale}: sections.${key}`)
    for (const key of ["summary", "name", "spdx", "source", "repository", "text", "textHint"]) assert.ok(settings.softwareLicense[key], `${locale}: softwareLicense.${key}`)
    for (const key of ["owner", "scope", "license", "unofficial", "officialSite"]) assert.ok(settings.licenses.game[key], `${locale}: licenses.game.${key}`)
    // The license name, its SPDX identifier and the rights holder are interpolated, so no language can alter them.
    assert.match(settings.softwareLicense.summary, /\{\{license\}\}/, locale)
    assert.match(settings.licenses.game.license, /\{\{spdx\}\}/, locale)
    for (const key of ["owner", "unofficial"]) assert.match(settings.licenses.game[key], /\{\{holder\}\}/, `${locale}: ${key}`)
    assert.doesNotMatch(JSON.stringify(settings), /KURO|Affero|AGPL/i, `${locale} must not spell out a name the code supplies`)
  }
})

test("the About page shows WWlogs' license and the Licenses page the game-material notice, from one source", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  const about = /function AboutPage[\s\S]*?\n\}\n/.exec(view)[0]
  assert.ok(about.includes("<SoftwareLicenseSection openLink={controller.openLicenseLink} />"))
  assert.ok(about.indexOf('data-field="app-version"') < about.indexOf("<SoftwareLicenseSection"), "the version information stays first")
  const page = read("frontend/src/views/LicensesPage.tsx")
  for (const value of ["productLicense.name", "productLicense.spdx", "productLicense.text", "sourceRepository.url", "gameMaterial.holder", "gameMaterial.officialSite.url"]) assert.ok(page.includes(value), value)
  assert.ok(page.indexOf("<GameMaterialNotice") < page.indexOf("fontLicenses.map"), "the notice comes first and the font licenses stay")
  // Every visible sentence is a translation: the page holds no notice text and no proper name of its own.
  assert.doesNotMatch(page, /KURO|Wuthering|Affero|AGPL|鳴潮|鸣潮|명조/)
})

test("the entry sits after every other group, and both profiles share one page", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  assert.ok(view.indexOf('value: "about"') < view.indexOf('value: "licenses"'))
  assert.match(view, /pinned: true, items: \[\{ value: "licenses"/)
  assert.ok(view.includes("<LicensesPage"))
  assert.ok(!read("frontend/src/showcase/useMockSettings.ts").includes("LicensesPage"), "the Showcase reuses the product page")
  assert.match(read("frontend/src/design/components/layout.tsx"), /data-pinned=\{group\.pinned === true\}/)
})
