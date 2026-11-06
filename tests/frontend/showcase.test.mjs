import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"
import { showcaseEnabled } from "../../frontend/vite.env.mjs"

function flatten(value, prefix = "", result = new Set()) {
  for (const [key, entry] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (entry && typeof entry === "object") flatten(entry, path, result)
    else result.add(path)
  }
  return result
}

const read = name => JSON.parse(readFileSync(join("frontend/src/showcase/locales", name), "utf8"))

test("the Showcase is excluded from a build that names no profile", () => {
  const saved = process.env.WWLOGS_FRONTEND_PROFILE
  try {
    delete process.env.WWLOGS_FRONTEND_PROFILE
    assert.equal(showcaseEnabled("build"), false)
    process.env.WWLOGS_FRONTEND_PROFILE = "public"
    assert.equal(showcaseEnabled("build"), false)
    process.env.WWLOGS_FRONTEND_PROFILE = "development"
    assert.equal(showcaseEnabled("build"), true)
    delete process.env.WWLOGS_FRONTEND_PROFILE
    assert.equal(showcaseEnabled("serve"), true, "the local dev server is a development surface")
  } finally {
    if (saved === undefined) delete process.env.WWLOGS_FRONTEND_PROFILE
    else process.env.WWLOGS_FRONTEND_PROFILE = saved
  }
})

test("Showcase language bundles have the same keys", () => {
  assert.deepEqual([...flatten(read("zh-TW.json"))].sort(), [...flatten(read("en.json"))].sort())
})

test("the typography specimen has real text in every language", () => {
  for (const locale of ["zh-CN", "ja", "ko"]) {
    const bundle = read(`${locale}.json`)
    assert.ok(bundle.typography.sample && bundle.typography.glyphs && bundle.dashboard.title && bundle.combat.teamDps, locale)
    assert.ok(/[\u3040-\u30ff\u3400-\u9fff\uac00-\ud7af]/.test(bundle.typography.sample), locale)
  }
})

test("production views do not import the Showcase or mock data", () => {
  const files = ["frontend/src/views", "frontend/src/lib", "frontend/src/design", "frontend/src/design/components", "frontend/src/design/motion"]
    .flatMap(dir => readdirSync(dir, { withFileTypes: true }).filter(entry => entry.isFile() && /\.tsx?$/.test(entry.name)).map(entry => join(dir, entry.name)))
  assert.ok(files.length > 10)
  for (const file of files) assert.doesNotMatch(readFileSync(file, "utf8"), /showcase\/|mock-feed|master-fixture/, file)
})

test("design components take motion timing from tokens, not hard-coded durations", () => {
  const css = readFileSync("frontend/src/design/components.css", "utf8")
  const allowed = new Set(["0ms", "250ms", "900ms"])
  for (const match of css.matchAll(/\b\d+m?s\b/g)) assert.ok(allowed.has(match[0]), `unexpected literal duration ${match[0]}`)
  assert.doesNotMatch(css, /backdrop-filter/, "backdrop-filter cannot sample behind a transparent window")
  assert.doesNotMatch(css, /transition:[^;]*\b(width|height|top|left)\b(?![^;]*morph)/, "layout properties do not transition outside shared indicators")
})
