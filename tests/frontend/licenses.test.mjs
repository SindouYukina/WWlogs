import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { existsSync, readFileSync, readdirSync } from "node:fs"
import test from "node:test"
import { libraryLicenseGaps, productLicenseText } from "../../frontend/vite.env.mjs"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const manifest = JSON.parse(read("frontend/locales/manifest.json"))
const fontsCss = read("frontend/src/design/typography.css")
const libraryFolder = "frontend/src/lib/library-licenses"
const libraries = JSON.parse(read(`${libraryFolder}/manifest.json`)).components

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
    assert.ok(!/PERMISSION (AND|&) CONDITIONS|Redistribution and use in source and binary forms|Permission is hereby granted/.test(JSON.stringify(settings)), `${locale} must not carry license text`)
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
  assert.ok(source.includes('productName = "WWlogs"') && source.includes('holder: "SindouYukina"') && source.includes('notice: "Copyright (C) 2026 SindouYukina"'), "the fixed names have one source")
  assert.doesNotMatch(source, /or[- ]later/i)
})

// The legal sentences of the About and Licenses pages. Their fixed names come from legal-notices.ts by interpolation.
const legalSentences = settings => ({
  "softwareLicense.grant": settings.softwareLicense.grant,
  "softwareLicense.warranty": settings.softwareLicense.warranty,
  "softwareLicense.thirdParty": settings.softwareLicense.thirdParty,
  "licenses.game.owner": settings.licenses.game.owner,
  "licenses.game.scope": settings.licenses.game.scope,
  "licenses.game.license": settings.licenses.game.license,
  "licenses.game.unofficial": settings.licenses.game.unofficial,
  "licenses.libraries.intro": settings.licenses.libraries.intro,
})
const placeholders = {
  "softwareLicense.grant": ["license", "product"],
  "softwareLicense.warranty": ["license", "product"],
  "softwareLicense.thirdParty": ["copyright", "page", "product", "rightsHolder", "spdx"],
  "licenses.game.owner": ["rightsHolder"],
  "licenses.game.scope": ["product"],
  "licenses.game.license": ["product", "spdx"],
  "licenses.game.unofficial": ["product", "rightsHolder"],
  "licenses.libraries.intro": ["product"],
}

test("every locale states the copyright, license, warranty disclaimer and rights notices in its own language", () => {
  const english = legalSentences(JSON.parse(read("frontend/locales/en/settings.json")))
  for (const locale of manifest.supportedLocales) {
    const settings = JSON.parse(read(`frontend/locales/${locale}/settings.json`))
    for (const key of ["softwareLicense", "gameMaterial", "libraryLicenses"]) assert.ok(settings.sections[key], `${locale}: sections.${key}`)
    for (const key of ["grant", "warranty", "thirdParty", "holder", "name", "spdx", "source", "repository", "text", "textHint"]) assert.ok(settings.softwareLicense[key], `${locale}: softwareLicense.${key}`)
    assert.equal(settings.softwareLicense.summary, undefined, `${locale}: the summary was split into the grant and the warranty disclaimer`)
    for (const key of ["owner", "scope", "license", "unofficial", "officialSite"]) assert.ok(settings.licenses.game[key], `${locale}: licenses.game.${key}`)
    for (const key of ["go", "frontend"]) assert.ok(settings.licenses.libraries.groups[key], `${locale}: licenses.libraries.groups.${key}`)
    for (const [key, sentence] of Object.entries(legalSentences(settings))) {
      const names = [...new Set([...sentence.matchAll(/\{\{(\w+)\}\}/g)].map(match => match[1]))].sort()
      assert.deepEqual(names, placeholders[key], `${locale}: ${key} interpolates the fixed names`)
      // WWlogs, the copyright notice and holder, the license name, its identifier and KURO GAMES are never spelled out.
      assert.doesNotMatch(sentence, /WWlogs|SindouYukina|Copyright \(C\)|KURO|AGPL|Affero/i, `${locale}: ${key}`)
      // Each language states the notice in its own words: no English paragraph is shown to every language.
      if (locale !== "en") assert.notEqual(sentence, english[key], `${locale}: ${key} is not translated`)
    }
    if (locale !== "en") assert.doesNotMatch(settings.softwareLicense.warranty, /WITHOUT ANY WARRANTY|MERCHANTABILITY/, `${locale}: the warranty disclaimer is translated`)
  }
})

test("the About page shows the copyright, license and disclaimers, and the Licenses page every notice, from one source", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  const about = /function AboutPage[\s\S]*?\n\}\n/.exec(view)[0]
  assert.ok(about.includes("<SoftwareLicenseSection openLink={controller.openLicenseLink} />"))
  assert.ok(about.indexOf('data-field="app-version"') < about.indexOf("<SoftwareLicenseSection"), "the version information stays first")
  const page = read("frontend/src/views/LicensesPage.tsx")
  for (const value of ["productCopyright.notice", "productCopyright.holder", "productLicense.name", "productLicense.spdx", "productLicense.text", "sourceRepository.url", "gameMaterial.holder", "gameMaterial.officialSite.url", "productName"]) assert.ok(page.includes(value), value)
  for (const key of ["softwareLicense.grant", "softwareLicense.warranty", "softwareLicense.thirdParty", "softwareLicense.holder"]) assert.ok(page.includes(`t("${key}"`), key)
  const order = ["<GameMaterialNotice", "fontLicenses.map", "<LibraryLicenses />"].map(mark => page.indexOf(mark, page.indexOf("export function LicensesPage")))
  assert.ok(order.every(index => index > 0) && order[0] < order[1] && order[1] < order[2], "the game notice, then the font licenses, then the libraries")
  // Every visible sentence is a translation: the page holds no notice text and no proper name of its own.
  assert.doesNotMatch(page, /KURO|Wuthering|Affero|AGPL|SindouYukina|Copyright \(C\)|鳴潮|鸣潮|명조/)
})

test("every listed library carries the unmodified license files of its source, and nothing else is in the folder", () => {
  const ids = libraries.map(library => library.id)
  assert.equal(new Set(ids).size, ids.length, "component ids are unique")
  const folders = readdirSync(libraryFolder, { withFileTypes: true }).filter(entry => entry.isDirectory()).map(entry => entry.name)
  assert.deepEqual(folders.sort(), [...ids].sort(), "one folder per listed component")
  const lock = JSON.parse(read("frontend/package-lock.json")).packages
  let compared = 0
  for (const library of libraries) {
    assert.ok(["go", "frontend"].includes(library.group) && library.license && library.notices.length && library.files.length, library.id)
    assert.deepEqual(readdirSync(`${libraryFolder}/${library.id}`).sort(), [...library.files].sort(), `${library.id}: exactly the listed files`)
    for (const file of library.files) assert.ok(read(`${libraryFolder}/${library.id}/${file}`).length > 0, `${library.id}/${file}`)
    // The notice shown on the card is a line of the component's own license file.
    const licenseText = read(`${libraryFolder}/${library.id}/${library.files[0]}`)
    for (const notice of library.notices) assert.ok(licenseText.includes(notice), `${library.id}: "${notice}" is in ${library.files[0]}`)
    if (library.source !== "npm") continue // Go: tests/desktop/licenses_test.go compares them with the toolchain and module cache
    assert.equal(lock[`node_modules/${library.name}`]?.version, library.version, `${library.name}: the lockfile pins the listed version`)
    if (library.licenseFrom) continue // @wailsio/runtime ships no license file; the Wails module's is compared in Go
    const installed = `frontend/node_modules/${library.name}`
    assert.equal(JSON.parse(read(`${installed}/package.json`)).version, library.version, `${library.name}: installed version`)
    for (const file of library.files) {
      assert.ok(existsSync(`${installed}/${file}`), `${library.name}: ${file} is in the package`)
      assert.equal(read(`${libraryFolder}/${library.id}/${file}`), read(`${installed}/${file}`), `${library.id}/${file} is the package's own file`)
      compared++
    }
  }
  assert.ok(compared >= 10, `compared ${compared} npm license files`)
  const source = read("frontend/src/lib/library-licenses.ts")
  assert.ok(source.includes('import.meta.glob<string>("./library-licenses/*/*", { query: "?raw", import: "default", eager: true })'))
})

test("a frontend build fails when its bundle and the library license manifest disagree", () => {
  const ids = names => names.map(name => `/x/node_modules/${name}/index.js`)
  const bundled = libraries.filter(library => library.group === "frontend" && !library.generated).map(library => library.name)
  assert.deepEqual(libraryLicenseGaps([...ids(bundled), "/x/src/main.tsx", "\0rolldown/runtime.js"], libraries), { unlisted: [], unbundled: [] })
  // A package without a listed license, also a scoped one and one with Windows separators, is reported.
  assert.deepEqual(libraryLicenseGaps([...ids(bundled), "/x/node_modules/left-pad/index.js", "C:\\x\\node_modules\\@scope\\pkg\\a.js"], libraries).unlisted, ["@scope/pkg", "left-pad"])
  // A listed package the bundle no longer contains is reported; generated contributions are not expected as modules.
  assert.deepEqual(libraryLicenseGaps(ids(bundled.filter(name => name !== "scheduler")), libraries).unbundled, ["scheduler"])
  assert.ok(libraries.filter(library => library.generated).every(library => !libraryLicenseGaps(ids(bundled), libraries).unbundled.includes(library.name)))
  assert.match(read("frontend/vite.config.ts"), /libraryLicenseGuard\(\)/)
})

test("the entry sits after every other group, and both profiles share one page", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  assert.ok(view.indexOf('value: "about"') < view.indexOf('value: "licenses"'))
  assert.match(view, /pinned: true, items: \[\{ value: "licenses"/)
  assert.ok(view.includes("<LicensesPage"))
  assert.ok(!read("frontend/src/showcase/useMockSettings.ts").includes("LicensesPage"), "the Showcase reuses the product page")
  assert.match(read("frontend/src/design/components/layout.tsx"), /data-pinned=\{group\.pinned === true\}/)
})
