import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { clockText, dateOfDay, dayNumber, defaultDate, heatLevel, heatmapLayout, isLeapYear, offsetText, recordedClock, yearBounds } from "../../frontend/src/lib/history.ts"
import { timelineFromSnapshot } from "../../frontend/src/lib/timeline.ts"

const cells = layout => layout.weeks.flat().filter(cell => cell !== null)

test("a year has one cell per day, leap years included, in week columns starting on Sunday", () => {
  for (const [year, days] of [[2023, 365], [2024, 366], [2026, 365], [2100, 365], [2000, 366]]) {
    const layout = heatmapLayout(year, [])
    const all = cells(layout)
    assert.equal(all.length, days, `${year}`)
    assert.equal(layout.days, days)
    assert.equal(all[0].date, `${year}-01-01`)
    assert.equal(all.at(-1).date, `${year}-12-31`)
    assert.equal(new Set(all.map(cell => cell.date)).size, days)
    // Every column has seven rows and a cell sits in the row of its weekday.
    for (const week of layout.weeks) assert.equal(week.length, 7)
    for (const cell of all) assert.equal(cell.weekday, new Date(`${cell.date}T00:00:00Z`).getUTCDay())
  }
  assert.equal(isLeapYear(2024), true)
  assert.equal(isLeapYear(2100), false)
  assert.equal(isLeapYear(2000), true)
  assert.ok(cells(heatmapLayout(2024, [])).some(cell => cell.date === "2024-02-29"))
  assert.ok(!cells(heatmapLayout(2026, [])).some(cell => cell.date === "2026-02-29"))
})

test("2026 starts on a Thursday and needs 53 week columns; 2028 needs 54", () => {
  const layout = heatmapLayout(2026, [])
  assert.equal(layout.weeks.length, 53)
  assert.deepEqual(layout.weeks[0].slice(0, 4), [null, null, null, null])
  assert.equal(layout.weeks[0][4].date, "2026-01-01")
  // A leap year starting on a Saturday spans 54 week columns.
  assert.equal(heatmapLayout(2028, []).weeks.length, 54)
})

test("each month label sits on the column of the month's first day", () => {
  const layout = heatmapLayout(2026, [])
  assert.equal(layout.monthStarts.length, 12)
  layout.monthStarts.forEach((column, month) => {
    const first = `2026-${String(month + 1).padStart(2, "0")}-01`
    assert.ok(layout.weeks[column].some(cell => cell?.date === first), first)
  })
})

test("counts and levels come from the given days only; the busiest day is the top level", () => {
  const layout = heatmapLayout(2026, [{ date: "2026-11-10", encounters: 8 }, { date: "2026-11-09", encounters: 1 }, { date: "2026-03-03", encounters: 4 }, { date: "2025-12-31", encounters: 99 }])
  const byDate = new Map(cells(layout).map(cell => [cell.date, cell]))
  assert.equal(layout.total, 13)
  assert.equal(layout.max, 8)
  assert.equal(byDate.get("2026-11-10").level, 4)
  assert.equal(byDate.get("2026-11-09").level, 1)
  assert.equal(byDate.get("2026-03-03").level, 2)
  assert.equal(byDate.get("2026-01-01").level, 0)
  assert.equal(heatLevel(0, 10), 0)
  assert.equal(heatLevel(1, 1000), 1)
  assert.equal(heatLevel(10, 10), 4)
})

test("an empty year has no levels and the switcher still reaches this year", () => {
  const layout = heatmapLayout(2026, [])
  assert.equal(layout.total, 0)
  assert.ok(cells(layout).every(cell => cell.level === 0))
  assert.deepEqual(yearBounds({ firstYear: 0, lastYear: 0 }, 2026), { first: 2026, last: 2026 })
  assert.deepEqual(yearBounds({ firstYear: 2024, lastYear: 2025 }, 2026), { first: 2024, last: 2026 })
})

test("the recorded clock uses the offset stored with the encounter, never the viewer's zone", () => {
  // 15:30 UTC recorded at UTC+9 is 00:30 the next day there, and the list shows 00:30:00 wherever it is opened.
  assert.deepEqual(recordedClock("2024-02-28T15:30:00Z", 9 * 3600), { hours: 0, minutes: 30, seconds: 0 })
  assert.deepEqual(recordedClock("2024-02-28T15:30:00Z", -5 * 3600), { hours: 10, minutes: 30, seconds: 0 })
  assert.equal(clockText({ hours: 7, minutes: 5, seconds: 9 }), "07:05:09")
  assert.equal(offsetText(9 * 3600), "UTC+09:00")
  assert.equal(offsetText(-(3 * 3600 + 30 * 60)), "UTC-03:30")
})

test("calendar arithmetic is time-zone free", () => {
  assert.equal(dateOfDay(dayNumber("2024-02-28") + 1), "2024-02-29")
  assert.equal(dateOfDay(dayNumber("2024-12-31") + 1), "2025-01-01")
  assert.equal(dayNumber("1970-01-01"), 0)
})

test("the default day is today when it has encounters, otherwise the newest day with any", () => {
  const view = { year: 2026, days: [{ date: "2026-10-27", encounters: 12 }, { date: "2026-11-09", encounters: 9 }] }
  assert.equal(defaultDate(view, "2026-11-09"), "2026-11-09")
  assert.equal(defaultDate(view, "2026-11-11"), "2026-11-09")
  assert.equal(defaultDate({ year: 2026, days: [] }, "2026-11-11"), undefined)
})

test("a stored timeline draws through the same adapter as the live one", () => {
  const timeline = { encounterId: "history-7", final: true, intervalSeconds: 1, startedAt: "", endAt: "", durationSeconds: 2, damageDealt: 30,
    contributors: [{ kind: "role", playerId: 1, roleId: 1505 }],
    points: [{ startSeconds: 0, durationSeconds: 1, state: "complete", damage: 10, dps: 10, contributorDamage: [10] }, { startSeconds: 1, durationSeconds: 1, state: "partial", damage: 20, dps: 20, contributorDamage: [20] }] }
  const chart = timelineFromSnapshot(timeline, "history-7")
  assert.equal(chart.final, true)
  assert.deepEqual(chart.series.get("team").slice(0, 2), [10, 20])
  assert.deepEqual(chart.series.get("role-1-1505").slice(0, 2), [10, 20])
  // A timeline of another encounter is never drawn.
  assert.equal(timelineFromSnapshot(timeline, "history-8").series.size, 0)
})

test("every history string exists in every bundled language and the namespace is registered", () => {
  const manifest = JSON.parse(readFileSync(new URL("../../frontend/locales/manifest.json", import.meta.url), "utf8"))
  assert.ok(manifest.namespaces.includes("history"))
  const flatten = (value, prefix = "") => Object.entries(value).flatMap(([key, entry]) => typeof entry === "string" ? [prefix + key] : flatten(entry, `${prefix}${key}.`))
  const read = locale => JSON.parse(readFileSync(new URL(`../../frontend/locales/${locale}/history.json`, import.meta.url), "utf8"))
  const baseline = flatten(read(manifest.fallbackLocale)).sort()
  for (const locale of manifest.supportedLocales) assert.deepEqual(flatten(read(locale)).sort(), baseline, locale)
  // The view reads these keys; a missing one would show the raw key.
  for (const key of ["unidentified", "notRecorded", "incomplete", "state.abandoned", "state.open", "integrity.UNRELIABLE", "outcome.INCOMPLETE", "unavailable.newer_schema", "localOnly"]) {
    assert.ok(baseline.includes(key), key)
  }
})
