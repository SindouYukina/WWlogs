import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"
import { showcaseEnabled } from "../../frontend/vite.env.mjs"
import { MockFeed } from "../../frontend/src/showcase/mock-feed.ts"

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

const runFeed = (feed, seconds, step = 0.25) => {
  let last
  for (let elapsed = 0; elapsed < seconds; elapsed += step) last = feed.publish(step)
  return last
}
const encounterOf = output => output.snapshot.view.encounters.current
const teamBuckets = history => {
  const series = [...history.values()]
  const longest = Math.max(0, ...series.map(values => values.length))
  return Array.from({ length: longest }, (_, index) => series.reduce((sum, values) => sum + (values[index] ?? 0), 0))
}

test("the Showcase peak DPS is the highest completed one-second team bucket and never falls", () => {
  const feed = new MockFeed()
  const first = runFeed(feed, 0.75)
  assert.equal(encounterOf(first).peakDps.status, "insufficient_data", "no complete bucket yet")
  let highest = 0
  for (let second = 0; second < 20; second++) {
    const output = runFeed(feed, 1)
    const peak = encounterOf(output).peakDps
    assert.equal(peak.status, "available")
    assert.equal(peak.durationSeconds, 1)
    assert.ok(peak.value >= highest, "the peak never falls")
    highest = peak.value
    assert.ok(Math.abs(peak.value - Math.max(...teamBuckets(output.history))) < 1e-6, "the peak is the timeline's highest bucket")
  }
})

test("a scripted burst lands in the bucket the timeline and the peak read", () => {
  const feed = new MockFeed()
  runFeed(feed, 3)
  const before = encounterOf(runFeed(feed, 1)).peakDps.value
  feed.burst(feed.roles[0].roleId)
  const after = runFeed(feed, 1.25)
  assert.ok(encounterOf(after).peakDps.value > before + 20000)
  assert.ok(Math.abs(encounterOf(after).peakDps.value - Math.max(...teamBuckets(after.history))) < 1e-6)
})

test("Showcase healing separates effective, reported and overheal, and only missing HP counts as effective", () => {
  const feed = new MockFeed()
  let previous = 0
  let output
  for (let second = 0; second < 40; second++) {
    output = runFeed(feed, 1)
    const healing = encounterOf(output).healing
    assert.equal(healing.reported, healing.effective + healing.overheal)
    assert.ok(healing.effective <= healing.reported)
    assert.ok(healing.effective >= previous, "effective healing only accumulates")
    previous = healing.effective
    assert.equal(healing.unknownHealerEvents, 0)
  }
  const healing = encounterOf(output).healing
  assert.ok(healing.events >= 20, `${healing.events} events`)
  assert.ok(healing.reported > 0)
  assert.ok(healing.overheal > 0, "scripted healing also overheals")
  assert.deepEqual(output.snapshot.view.statistics.healing, healing)
})

test("a stopped Showcase encounter holds peak and healing and a new one starts from zero", () => {
  const feed = new MockFeed()
  runFeed(feed, 6)
  feed.endEncounter()
  const ended = feed.publish(0.25)
  const held = runFeed(feed, 4)
  assert.deepEqual(encounterOf(held).peakDps, encounterOf(ended).peakDps)
  assert.deepEqual(encounterOf(held).healing, encounterOf(ended).healing)
  feed.resetEncounter()
  const reset = feed.publish(0.25)
  assert.equal(encounterOf(reset).peakDps.value, 0)
  assert.equal(encounterOf(reset).healing.reported, 0)
  assert.equal(encounterOf(reset).healing.events, 0)
})
