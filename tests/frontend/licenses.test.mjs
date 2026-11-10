import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

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

test("the entry sits after every other group, and both profiles share one page", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  assert.ok(view.indexOf('value: "about"') < view.indexOf('value: "licenses"'))
  assert.match(view, /pinned: true, items: \[\{ value: "licenses"/)
  assert.ok(view.includes("<LicensesPage"))
  assert.ok(!read("frontend/src/showcase/useMockSettings.ts").includes("LicensesPage"), "the Showcase reuses the product page")
  assert.match(read("frontend/src/design/components/layout.tsx"), /data-pinned=\{group\.pinned === true\}/)
})
