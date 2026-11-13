import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { dateOfDay, dayNumber, defaultDate, heatLevel, heatmapLayout, hostTimeZone, indicatorText, isLeapYear, offsetText, yearBounds } from "../../frontend/src/lib/history.ts"
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

test("the view names its IANA zone for Go and formats offsets; it never converts an instant itself", () => {
  // Go lays every instant out in the zone the view names, so the view only reports the zone it runs in.
  assert.equal(hostTimeZone(), Intl.DateTimeFormat().resolvedOptions().timeZone)
  assert.equal(offsetText(9 * 3600), "UTC+09:00")
  assert.equal(offsetText(-(3 * 3600 + 30 * 60)), "UTC-03:30")
  assert.equal(offsetText(0), "UTC+00:00")
  const source = readFileSync(new URL("../../frontend/src/lib/history.ts", import.meta.url), "utf8")
  assert.doesNotMatch(source, /getUTCHours|getHours|toLocaleTimeString|getTimezoneOffset/)
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
  for (const key of ["unidentified", "incomplete", "state.abandoned", "state.open", "integrity.UNRELIABLE", "outcome.INCOMPLETE", "unavailable.newer_schema", "localOnly",
    "localEncrypted", "checkpointNote", "columns.version", "versionUnknown", "coverage.combatStored", "coverage.skillCastsNotStored", "delete.confirm", "clear.done"]) {
    assert.ok(baseline.includes(key), key)
  }
  // Every indicator label and reason the mapping can name exists, and every incomplete reason the host stores.
  const states = ["disabled", "waiting", "recording", "starting", "unavailable", "read_only", "write_failed", "not_recorded"]
  const reasons = ["newer_schema", "locked_by_other_process", "not_a_history_file", "integrity_check_failed", "open_failed", "storage_stopped", "key_missing", "key_unavailable", "key_mismatch", "duplicate_source"]
  for (const state of states) for (const reason of reasons) for (const capturing of [false, true]) {
    const text = indicatorText({ state, reason, enabled: true, capturing, encrypted: true })
    assert.ok(baseline.includes(text.key), text.key)
    if (text.detail) assert.ok(baseline.includes(text.detail), text.detail)
  }
  for (const reason of ["entries_lost", "application_exit", "recording_disabled", "not_finished"]) assert.ok(baseline.includes(`incompleteReason.${reason}`), reason)
  for (const refusal of ["encounter_recording", "recording_active", "not_writable", "not_found"]) assert.ok(baseline.includes(`delete.refused.${refusal}`), refusal)
})

test("the indicator only maps the host state to text and tone", () => {
  assert.deepEqual(indicatorText({ state: "recording", enabled: true, capturing: true, encrypted: true }), { key: "indicator.recording", tone: "good" })
  assert.deepEqual(indicatorText({ state: "waiting", enabled: true, capturing: true, encrypted: true }), { key: "indicator.waitingCombat", tone: "neutral" })
  assert.deepEqual(indicatorText({ state: "waiting", enabled: true, capturing: false, encrypted: true }), { key: "indicator.ready", tone: "neutral" })
  assert.deepEqual(indicatorText({ state: "starting", enabled: true, capturing: true, encrypted: true }), { key: "indicator.starting", tone: "warn" })
  assert.deepEqual(indicatorText({ state: "disabled", enabled: false, capturing: true, encrypted: true }), { key: "indicator.disabled", tone: "neutral" })
  assert.deepEqual(indicatorText({ state: "unavailable", reason: "key_missing", enabled: true, capturing: false, encrypted: false }), { key: "indicator.unavailable", tone: "critical", detail: "unavailable.key_missing" })
  assert.deepEqual(indicatorText({ state: "write_failed", reason: "storage_stopped", enabled: true, capturing: true, encrypted: true }), { key: "indicator.writeFailed", tone: "critical", detail: "unavailable.storage_stopped" })
  assert.deepEqual(indicatorText({ state: "not_recorded", reason: "open_failed", enabled: true, capturing: true, encrypted: true }), { key: "indicator.notRecorded", tone: "warn", detail: "unavailable.open_failed" })
  assert.deepEqual(indicatorText(undefined), { key: "indicator.unknown", tone: "neutral" })
})

test("every Combat History settings string exists in every bundled language", () => {
  const manifest = JSON.parse(readFileSync(new URL("../../frontend/locales/manifest.json", import.meta.url), "utf8"))
  const read = locale => JSON.parse(readFileSync(new URL(`../../frontend/locales/${locale}/settings.json`, import.meta.url), "utf8"))
  const flatten = (value, prefix = "") => Object.entries(value).flatMap(([key, entry]) => typeof entry === "string" ? [prefix + key] : flatten(entry, `${prefix}${key}.`))
  const keys = locale => flatten(read(locale).historySettings).sort()
  const baseline = keys(manifest.fallbackLocale)
  assert.ok(baseline.includes("clearConfirm") && baseline.includes("encryptedHint") && baseline.includes("recordingHint"))
  for (const locale of manifest.supportedLocales) {
    assert.deepEqual(keys(locale), baseline, locale)
    assert.ok(read(locale).pages.history, locale)
  }
})
