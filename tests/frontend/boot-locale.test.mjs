import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { bootLocale } from "../../frontend/src/i18n/boot.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const supported = ["zh-TW", "zh-CN", "en", "ja", "ko"]

test("the first render uses the language the host names in the window's URL", () => {
  assert.equal(bootLocale("?view=desktop&lang=ja", supported, "en"), "ja")
  assert.equal(bootLocale("?view=overlay&surface=effects&lang=zh-TW", supported, "en"), "zh-TW")
})

test("a missing or unsupported language starts on the fallback", () => {
  for (const search of ["", "?view=showcase", "?lang=", "?lang=de", "?lang=external-pack", "?lang=EN"]) assert.equal(bootLocale(search, supported, "en"), "en", search)
})

test("the boot language leaves the bundled fallback and missing-key behavior alone", () => {
  const source = read("frontend/src/i18n/index.ts")
  assert.match(source, /fallbackLng:manifest\.fallbackLocale/)
  assert.match(source, /lng:firstLocale/)
  assert.equal(JSON.parse(read("frontend/locales/manifest.json")).fallbackLocale, "en")
  assert.ok(!/navigator\.language/.test(source), "the language comes from the host, not the WebView")
})

test("the host decides the first language only before the first window opens", () => {
  const app = read("internal/desktop/app.go")
  assert.ok(app.indexOf("settings.seedInitialLocale(") < app.indexOf("capture.LoadMasterData("), "Master display names load in the seeded language")
  assert.ok(app.indexOf("settings.seedInitialLocale(") < app.indexOf("bootLocaleQuery(settings)"), "windows open with the seeded language")
  assert.match(read("internal/desktop/overlay.go"), /"\/\?view=overlay&surface="\+entry\.kind\+"&"\+bootLocaleQuery\(m\.store\)/)
})
