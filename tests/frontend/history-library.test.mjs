import assert from "node:assert/strict"
import test from "node:test"
import { chosenState, classTone, defaultLibraryQuery, deletableIds, libraryFiltered, libraryFilters, recordClasses, toggleChosen } from "../../frontend/src/lib/history-library.ts"

test("only a complete record shows plain; every class has a tone", () => {
  assert.equal(classTone("complete"), "neutral")
  for (const value of recordClasses) assert.ok(["neutral", "good", "warn", "critical"].includes(classTone(value)))
  assert.equal(classTone("known_affected"), "warn")
  assert.equal(classTone("unreliable"), "critical")
})

test("sorting and paging do not count as filtering", () => {
  assert.equal(libraryFiltered(defaultLibraryQuery), false)
  assert.equal(libraryFiltered({ sort: "dps_desc", offset: 50, limit: 50 }), false)
  assert.equal(libraryFiltered({ text: "  " }), false)
  assert.equal(libraryFiltered({ text: "Lily" }), true)
  assert.equal(libraryFiltered({ roles: [1512] }), true)
  assert.deepEqual(libraryFilters({ from: "2026-11-01", offset: 50, limit: 50 }), { from: "2026-11-01" })
})

test("choosing encounters to delete skips the ones still recording and has no comparison limit", () => {
  const entries = [{ id: 1, state: "finished" }, { id: 2, state: "open" }, { id: 3, state: "abandoned" }, { id: 4, state: "finished" }, { id: 5, state: "finished" }, { id: 6, state: "finished" }]
  // Go refuses deleting an encounter in progress, so the list never offers it; finished and abandoned ones may go.
  assert.deepEqual(deletableIds(entries), [1, 3, 4, 5, 6])
  let chosen = []
  for (const id of [1, 3, 4, 5, 6]) chosen = toggleChosen(chosen, id)
  // More than the four a comparison takes.
  assert.deepEqual(chosen, [1, 3, 4, 5, 6])
  assert.equal(chosenState(deletableIds(entries), chosen), "all")
  chosen = toggleChosen(chosen, 4)
  assert.deepEqual(chosen, [1, 3, 5, 6])
  assert.equal(chosenState(deletableIds(entries), chosen), "some")
  assert.equal(chosenState(deletableIds(entries), []), "none")
  // A choice outside the deletable list (an encounter no longer listed) does not count.
  assert.equal(chosenState([1, 3], [9]), "none")
  assert.equal(chosenState([], []), "none")
})
