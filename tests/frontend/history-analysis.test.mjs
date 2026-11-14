import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { formatMs, fullQuery, minimumRangeMs, parseSkillRow, querySpan, rangeQuery, skillRowKey, statsRowKey, statusRowKey, timeTicks, visibleRows } from "../../frontend/src/lib/history-analysis.ts"

const source = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8")

test("times read as minutes, seconds and milliseconds from the encounter start", () => {
  assert.equal(formatMs(0), "00:00.000")
  assert.equal(formatMs(15000), "00:15.000")
  assert.equal(formatMs(65250.4), "01:05.250")
  assert.equal(formatMs(3599999), "59:59.999")
  assert.equal(formatMs(-5), "00:00.000")
})

test("a dragged range becomes the query's range, and one covering the encounter is the whole fight", () => {
  const query = { ...fullQuery, contributor: "role-1-2", enemy: 3 }
  assert.deepEqual(rangeQuery(query, 25000, 15000, 60000), { ...query, fromMs: 15000, toMs: 25000 })
  assert.deepEqual(rangeQuery(query, -100, 61000, 60000), { ...query, fromMs: 0, toMs: 0 })
  assert.deepEqual(rangeQuery(query, 1000.4, 2000.6, 60000), { ...query, fromMs: 1000, toMs: 2001 })
  assert.equal(fullQuery.fromMs, 0)
  assert.equal(fullQuery.toMs, 0)
  assert.equal(fullQuery.enemy, -1)
  assert.equal(fullQuery.effects, "hud")
  assert.ok(minimumRangeMs > 0)
})

test("a committed query is drawn as its range at once; the whole encounter has none", () => {
  assert.equal(querySpan(fullQuery, 60000), null)
  assert.deepEqual(querySpan({ ...fullQuery, fromMs: 15000, toMs: 25000 }, 60000), { fromMs: 15000, toMs: 25000 })
  // toMs 0 is the end of the encounter, and a range never reaches past it.
  assert.deepEqual(querySpan({ ...fullQuery, fromMs: 15000, toMs: 0 }, 60000), { fromMs: 15000, toMs: 60000 })
  assert.deepEqual(querySpan({ ...fullQuery, fromMs: 15000, toMs: 90000 }, 60000), { fromMs: 15000, toMs: 60000 })
  assert.equal(querySpan({ ...fullQuery, fromMs: 0, toMs: 60000 }, 60000), null)
  // The range the drag commits is the one drawn: rangeQuery and querySpan agree.
  const committed = rangeQuery(fullQuery, 40000.4, 12000.6, 60000)
  assert.deepEqual(querySpan(committed, 60000), { fromMs: 12001, toMs: 40000 })
})

test("releasing a drag draws the committed range, not the previous answer's", () => {
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  assert.match(view, /const selected = useMemo\(\(\) => querySpan\(query, durationMs\)/)
  // The timeline, the lanes and the range label follow the selection; the numbers and sparklines follow Go's answer.
  assert.match(view, /<AnalysisTimeline [^>]*range=\{selected\}/)
  assert.match(view, /<TimelineLanes [^>]*range=\{selected\}/)
  assert.match(view, /data-field="range-label">\{selectedLabel\}/)
  assert.match(view, /<Sparkline bins=\{row\.stat\.bins\} range=\{range\}/)
})

test("time ticks stay at round steps and never crowd the axis", () => {
  assert.deepEqual(timeTicks(10000), [0, 2000, 4000, 6000, 8000, 10000])
  for (const duration of [500, 7000, 61000, 311000, 3600000, 6 * 3600000]) {
    const ticks = timeTicks(duration)
    assert.ok(ticks.length <= 9, `${duration}: ${ticks.length}`)
    assert.equal(ticks[0], 0)
    assert.ok(ticks.at(-1) <= duration + 1)
  }
  assert.deepEqual(timeTicks(0), [0])
})

test("skill row keys name a contributor and a SkillId, with 0 as the row without one", () => {
  assert.equal(skillRowKey("role-7-1505", 150501), "role-7-1505:150501")
  assert.equal(skillRowKey("role-7-1505", 0), "role-7-1505:unassigned")
  assert.equal(skillRowKey("vehicle-motorcycle", undefined), "vehicle-motorcycle:unassigned")
  assert.deepEqual(parseSkillRow("role-7-1505:150501"), { contributor: "role-7-1505", skillId: 150501 })
  assert.deepEqual(parseSkillRow("role-7-1505:unassigned"), { contributor: "role-7-1505", skillId: 0 })
  assert.equal(parseSkillRow("broken"), null)
})

test("only the rows in view of a long lane list are mounted", () => {
  assert.deepEqual(visibleRows(0, 40, 0, 480), { first: 0, last: -1 })
  assert.deepEqual(visibleRows(500, 40, 0, 480), { first: 0, last: 18 })
  const middle = visibleRows(500, 40, 8000, 480)
  assert.ok(middle.first > 150 && middle.last - middle.first <= 26, JSON.stringify(middle))
  assert.equal(visibleRows(500, 40, 20000, 480).last, 499)
})

test("the workspace computes no statistic, coverage or attribution of its own", () => {
  // Every number comes from the Go answers; the view only lays them out and formats them.
  const view = source("frontend/src/views/HistoryAnalysis.tsx")
  const lib = source("frontend/src/lib/history-analysis.ts")
  for (const text of [view, lib]) {
    assert.doesNotMatch(text, /confirmedMs\s*\/|\.reduce\(\(sum/, "coverage or sums are not computed in the frontend")
    assert.doesNotMatch(text, /getTimezoneOffset|toLocaleTimeString/, "no time zone conversion in the frontend")
  }
  // The only arithmetic on engine values is the per-interval DPS of a line, as the Dashboard draws it.
  assert.match(view, /contributorDamage\[entry\.position\] \?\? 0\) \/ point\.durationSeconds/)
  assert.match(source("frontend/src/lib/timeline.ts"), /contributorDamage\[position\] \?\? 0\) \/ point\.durationSeconds/)
})

test("the timeline and the lanes share one gutter so a moment lines up across them", () => {
  const css = source("frontend/src/design/views.css")
  for (const rule of [".ww-atl-body { display: grid; grid-template-columns: var(--ww-analysis-gutter)", ".ww-lane { position: absolute; top: 0; left: 0; right: 0; height: var(--ww-lane-height); display: grid; grid-template-columns: var(--ww-analysis-gutter)", ".ww-atl-time { display: grid; grid-template-columns: var(--ww-analysis-gutter)"]) {
    assert.ok(css.includes(rule), rule)
  }
  // The lanes span the timeline's width: the Timeline tab stacks its details under them.
  assert.match(source("frontend/src/views/HistoryAnalysis.tsx"), /<div className="ww-analysis-stack">\s*<EvidenceLegend \/>/)
})

test("a status row has its own key, which parses back to the role and its DamageId", () => {
  assert.equal(statusRowKey("role-7-1409", 1001), "role-7-1409:status:1001")
  assert.deepEqual(parseSkillRow("role-7-1409:status:1001"), { contributor: "role-7-1409", skillId: 0, statusDamageId: 1001 })
  assert.deepEqual(parseSkillRow("role-7-1409:unassigned"), { contributor: "role-7-1409", skillId: 0 })
  assert.deepEqual(parseSkillRow("role-7-1409:140901"), { contributor: "role-7-1409", skillId: 140901 })
  assert.equal(statsRowKey({ contributor: "role-7-1409", skillId: 0, statusDamageId: 1001 }), "role-7-1409:status:1001")
  assert.equal(statsRowKey({ contributor: "role-7-1409", skillId: 0 }), "role-7-1409:unassigned")
  assert.notEqual(statusRowKey("role-7-1409", 1001), skillRowKey("role-7-1409", 1001), "a status row never shares a key with a skill row")
})
