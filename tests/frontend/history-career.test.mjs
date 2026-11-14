import assert from "node:assert/strict"
import test from "node:test"
import { careerLibraryQuery, careerQuery, careerStateQuery, defaultCareerState, maxCalendarDays, rangeHeatmapLayout } from "../../frontend/src/lib/history-career.ts"

// The Career page only names its range presets and lays out Go's dates; every total and date comes from Go.

test("a preset asks Go for its dates instead of computing them", () => {
  assert.deepEqual(careerQuery("7d", {}, {}), { lastDays: 7 })
  assert.deepEqual(careerQuery("30d", {}, { gameVersions: ["3.8"] }), { gameVersions: ["3.8"], lastDays: 30 })
  assert.deepEqual(careerQuery("all", { from: "2026-01-01" }, {}), {})
  assert.deepEqual(careerQuery("custom", { from: "2026-10-01", to: "" }, {}), { from: "2026-10-01" })
  assert.deepEqual(careerStateQuery(defaultCareerState), {})
  assert.deepEqual(careerStateQuery({ preset: "7d", gameVersion: "3.8", mode: "open_world", stage: 8 }), { gameVersions: ["3.8"], modes: ["open_world"], stages: [8], lastDays: 7 })
})

test("the library opened from a career keeps Go's dates and the page's filters; a picked place replaces the page's", () => {
  const career = { from: "2026-10-15", to: "2026-10-21" }
  const state = { preset: "7d", gameVersion: "3.8", mode: "category:Tower", stage: 20016 }
  assert.deepEqual(careerLibraryQuery(career, state), { sort: "start_desc", from: "2026-10-15", to: "2026-10-21", gameVersions: ["3.8"], modes: ["category:Tower"], stages: [20016] })
  assert.deepEqual(careerLibraryQuery(career, state, { role: 1512 }).roles, [1512])
  const byMode = careerLibraryQuery(career, state, { mode: "open_world" })
  assert.deepEqual(byMode.modes, ["open_world"])
  assert.equal(byMode.stages, undefined)
  const byStage = careerLibraryQuery(career, state, { stage: 8 })
  assert.deepEqual(byStage.stages, [8])
  assert.equal(byStage.modes, undefined)
  // An open range stays open: the library is not given dates Go did not answer with.
  const all = careerLibraryQuery({ from: "", to: "" }, { preset: "all" })
  assert.deepEqual(all, { sort: "start_desc" })
})

test("a range heatmap lays out exactly the range's dates, Sunday first, months in order", () => {
  // 2026-09-29 is a Tuesday; 2026-10-05 a Monday.
  const layout = rangeHeatmapLayout("2026-09-29", "2026-10-05", [{ date: "2026-09-30", encounters: 2 }, { date: "2026-10-03", encounters: 8 }, { date: "2026-12-01", encounters: 5 }])
  assert.equal(layout.days, 7)
  assert.equal(layout.weeks.length, 2)
  assert.equal(layout.weeks[0][0], null)
  assert.equal(layout.weeks[0][1], null)
  assert.equal(layout.weeks[0][2].date, "2026-09-29")
  assert.equal(layout.weeks[1][1].date, "2026-10-05")
  assert.equal(layout.weeks[1][2], null)
  // A date outside the range is not counted, and a date without a stored encounter is level 0, not invented activity.
  assert.equal(layout.total, 10)
  assert.equal(layout.max, 8)
  assert.equal(layout.weeks[0][3].level, 1)
  assert.equal(layout.weeks[0][6].level, 4)
  assert.equal(layout.weeks[0][4].count, 0)
  assert.equal(layout.weeks[0][4].level, 0)
  // The range starts in September and October starts in the first week: both are labelled, in order.
  assert.deepEqual(layout.months, [8, 9])
  assert.deepEqual(layout.monthStarts, [0, 0])
})

test("a range longer than the calendar shows its last dates", () => {
  const layout = rangeHeatmapLayout("2024-01-01", "2026-10-31", [{ date: "2024-01-02", encounters: 3 }, { date: "2026-10-31", encounters: 1 }])
  assert.equal(layout.days, maxCalendarDays)
  assert.equal(layout.total, 1)
  const first = layout.weeks.flat().find(cell => cell !== null)
  assert.equal(first.date, "2025-10-26")
  assert.ok(layout.weeks.length <= 54)
})
