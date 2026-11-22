import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { hostLocaleFiles } from "../../frontend/vite.env.mjs"

const read = path => JSON.parse(readFileSync(new URL(`../../frontend/${path}`, import.meta.url), "utf8").replace(/^\uFEFF/, ""))
const manifest = read("locales/manifest.json")

// The Go host shows the startup namespace before any window exists, when a launch cannot start, and reads it from the
// archive (internal/desktop/startup_message.go), so the build must carry it for every bundled language.
test("the archive carries the manifest and every language's startup texts for the host", () => {
  const files = new Map()
  hostLocaleFiles(manifest).generateBundle.call({ emitFile: file => files.set(file.fileName, file.source) })
  assert.deepEqual(JSON.parse(files.get("locales/manifest.json")), manifest)
  assert.ok(manifest.namespaces.includes("startup"), "startup is a namespace of every language")
  for (const locale of manifest.supportedLocales) {
    const emitted = files.get(`locales/${locale}/startup.json`)
    assert.ok(emitted, `${locale}: startup texts are emitted`)
    assert.deepEqual(JSON.parse(emitted), read(`locales/${locale}/startup.json`), `${locale}: the copy is the tracked text`)
  }
  assert.equal(files.size, manifest.supportedLocales.length + 1, "nothing else is emitted for the host")
})
