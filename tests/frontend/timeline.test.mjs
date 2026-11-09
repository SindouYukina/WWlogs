import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { combatRows } from "../../frontend/src/lib/combat-view.ts"
import { contributorKey, minimumTimelineSlots, teamSeriesKey, timelineFromView, unassignedSeriesKey } from "../../frontend/src/lib/timeline.ts"

const point = (startSeconds, durationSeconds, state, parts) => {
  const damage = parts.reduce((sum, value) => sum + value, 0)
  return { startSeconds, durationSeconds, state, damage, dps: durationSeconds > 0 ? damage / durationSeconds : null, contributorDamage: parts }
}

const timeline = (overrides = {}) => ({
  encounterId: "3-1-2", final: true, intervalSeconds: 1, startedAt: "", endAt: "", durationSeconds: 3.5, damageDealt: 0,
  contributors: [{ kind: "role", playerId: 7, roleId: 1102 }, { kind: "vehicle", vehicleKind: "motorcycle" }, { kind: "unassigned" }],
  points: [point(0, 1, "complete", [100, 0, 20]), point(1, 1, "complete", [0, 0, 0]), point(2, 1, "complete", [0, 300, 0]), point(3, 0.5, "partial", [50, 0, 0])],
  ...overrides,
})

const view = (overrides = {}) => ({ encounters: { recent: [{ encounterId: "3-1-2" }] }, timeline: timeline(overrides) })

test("no snapshot or no encounter gives an empty chart, never a placeholder curve", () => {
  for (const [value, id] of [[null, undefined], [{ encounters: { recent: [] } }, undefined], [view(), "another"]]) {
    const result = timelineFromView(value, id)
    assert.equal(result.series.size, 0)
    assert.equal(result.slots, minimumTimelineSlots)
  }
})

test("the team line is the engine's interval DPS, zero seconds stay zero, the final partial uses its real duration", () => {
  const result = timelineFromView(view(), "3-1-2")
  assert.deepEqual(result.series.get(teamSeriesKey), [120, 0, 300, 100])
  assert.equal(result.slotSeconds, 1)
  assert.equal(result.final, true)
  assert.equal(result.slots, minimumTimelineSlots)
})

test("contributor lines split the team line exactly and use the ranking row keys", () => {
  const result = timelineFromView(view(), "3-1-2")
  assert.deepEqual(result.series.get("role-7-1102"), [100, 0, 0, 100])
  assert.deepEqual(result.series.get("vehicle-motorcycle"), [0, 0, 300, 0])
  assert.deepEqual(result.series.get(unassignedSeriesKey), [20, 0, 0, 0])
  const team = result.series.get(teamSeriesKey)
  for (let slot = 0; slot < team.length; slot++) {
    const sum = ["role-7-1102", "vehicle-motorcycle", unassignedSeriesKey].reduce((total, key) => total + result.series.get(key)[slot], 0)
    assert.ok(Math.abs(sum - team[slot]) < 1e-9)
  }
  const rows = combatRows({ dps: { status: "available", durationSeconds: 3.5 }, roles: [{ playerId: 7, roleId: 1102, damageDealt: 150, directDamage: 150, summonDamage: 0, damageTaken: 0, nameStatus: "UNKNOWN", name: "", rank: 1 }], vehicle: { kind: "motorcycle", damageDealt: 300, rank: 0 } })
  for (const row of rows) assert.ok(result.series.has(row.key), `${row.key} has no series`)
  assert.equal(contributorKey({ kind: "unassigned" }), unassignedSeriesKey)
})

test("an interval still in progress is not drawn", () => {
  const result = timelineFromView(view({ final: false, points: [point(0, 1, "complete", [10, 0, 0]), point(1, 0.4, "in_progress", [5, 0, 0])] }), "3-1-2")
  assert.deepEqual(result.series.get(teamSeriesKey), [10, null])
})

test("a wider display interval labels slots by its length and long encounters keep every point", () => {
  const points = Array.from({ length: 90 }, (_, index) => point(index * 5, 5, "complete", [50, 0, 0]))
  const result = timelineFromView(view({ intervalSeconds: 5, points }), "3-1-2")
  assert.equal(result.slotSeconds, 5)
  assert.equal(result.slots, 90)
  assert.ok(result.series.get(teamSeriesKey).every(value => value === 10))
})

test("the live provider takes the timeline from the engine snapshot, not from a timer or mock data", () => {
  const provider = readFileSync("frontend/src/lib/useLiveCombatData.ts", "utf8")
  assert.match(provider, /timelineFromView\(view, displayedEncounter\(view\)\?\.encounterId\)/)
  const adapter = readFileSync("frontend/src/lib/timeline.ts", "utf8")
  assert.doesNotMatch(adapter, /Math\.random|Date\.now|setInterval|showcase|mock/i)
})
