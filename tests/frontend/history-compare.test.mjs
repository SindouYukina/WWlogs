import assert from "node:assert/strict"
import test from "node:test"
import { compareLimit, toggleCompared } from "../../frontend/src/lib/history-compare.ts"

// The comparison set only keeps which stored encounters were chosen; Go answers everything about them.

const entry = (id) => ({ id, date: "2026-11-01", clock: `10:0${id}:00` })

test("an encounter joins the set once and leaves it again", () => {
  let set = toggleCompared([], entry(1))
  set = toggleCompared(set, entry(2))
  assert.deepEqual(set.map(chosen => chosen.id), [1, 2])
  set = toggleCompared(set, entry(1))
  assert.deepEqual(set.map(chosen => chosen.id), [2])
})

test("a full set refuses another encounter and keeps its order", () => {
  assert.equal(compareLimit, 4)
  let set = []
  for (let id = 1; id <= compareLimit + 1; id++) set = toggleCompared(set, entry(id))
  assert.deepEqual(set.map(chosen => chosen.id), [1, 2, 3, 4])
  // Removing one makes room again.
  set = toggleCompared(toggleCompared(set, entry(3)), entry(5))
  assert.deepEqual(set.map(chosen => chosen.id), [1, 2, 4, 5])
})
