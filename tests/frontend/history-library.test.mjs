import assert from "node:assert/strict"
import test from "node:test"
import { classTone, defaultLibraryQuery, libraryFiltered, libraryFilters, recordClasses } from "../../frontend/src/lib/history-library.ts"

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
