import assert from "node:assert/strict"
import test from "node:test"
import { correlationClasses, correlationQuery, correlationScopes, fullQuery } from "../../frontend/src/lib/history-analysis.ts"

// The correlation panel only picks whose hits Go places; it never places a hit itself.

test("an effect offers its holder's hits only when Go named the holder", () => {
  assert.deepEqual(correlationScopes({ kind: "role", contributor: "role-7-1505" }), ["selection", "holder"])
  assert.deepEqual(correlationScopes({ kind: "role" }), ["selection"])
  assert.deepEqual(correlationScopes({ kind: "enemy", enemy: 0 }), ["selection", "target"])
  assert.deepEqual(correlationScopes({ kind: "team" }), ["selection"])
  assert.deepEqual(correlationScopes({ kind: "unknown" }), ["selection"])
  assert.deepEqual(correlationClasses, ["confirmed", "stated", "unknown", "outside"])
})

test("a scope narrows the workspace query without moving its range", () => {
  const base = { ...fullQuery, fromMs: 2000, toMs: 4000, contributor: "role-7-1206", enemy: 1, skillRow: "role-7-1206:120601" }
  assert.deepEqual(correlationQuery(base, "selection", {}), base)
  const holder = correlationQuery(base, "holder", { contributor: "role-7-1505" })
  assert.equal(holder.contributor, "role-7-1505")
  assert.equal(holder.skillRow, "")
  assert.equal(holder.enemy, 1)
  assert.equal(holder.fromMs, 2000)
  const target = correlationQuery(base, "target", { enemy: 0 })
  assert.equal(target.enemy, 0)
  assert.equal(target.contributor, "role-7-1206")
  assert.equal(target.toMs, 4000)
})
