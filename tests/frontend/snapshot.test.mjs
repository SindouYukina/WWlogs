import assert from "node:assert/strict"
import test from "node:test"
import { dpsText, roleLabel, dataState } from "../../frontend/src/lib/snapshot.ts"

test("DPS uses only the engine's status, never an untrusted numeric value", () => {
  const sample = { value: 12345, damage: 24690, durationSeconds: 2 }
  assert.equal(dpsText({ ...sample, status: "available" }), "12,345")
  assert.equal(dpsText({ ...sample, status: "lower_bound" }), "≥ 12,345")
  for (const status of ["unreliable", "unavailable", "insufficient_data", "invalid_duration"]) {
    assert.equal(dpsText({ ...sample, status }), status)
  }
  assert.equal(dpsText(undefined), "—")
  assert.equal(dpsText({ ...sample, status: "available", value: NaN }), "unavailable")
})

test("unverified names retain UNKNOWN and the Role ID", () => {
  assert.equal(roleLabel({ roleId: 99, name: "Guess", nameStatus: "CORRELATED" }), "UNKNOWN (99)")
  assert.equal(roleLabel({ roleId: 99, name: "Verified name", nameStatus: "VERIFIED" }), "Verified name")
})

test("missing, degraded, suspect and stopped snapshots stay distinct", () => {
  assert.equal(dataState(null), "no_data")
  const snapshot = { service: { state: "running" }, view: null }
  assert.equal(dataState(snapshot), "no_data")
  snapshot.view = { health: { dataIntegrity: "INTACT", context: { state: "VALID" } }, encounters: { recent: [] } }
  assert.equal(dataState(snapshot), "no_encounter")
  snapshot.view.health.dataIntegrity = "DEGRADED"
  assert.equal(dataState(snapshot), "degraded")
  snapshot.view.health.dataIntegrity = "UNRELIABLE"
  assert.equal(dataState(snapshot), "untrusted")
  snapshot.view.health.dataIntegrity = "INTACT"
  snapshot.view.health.context.state = "SUSPECT"
  assert.equal(dataState(snapshot), "untrusted")
  snapshot.service.state = "stopped"
  assert.equal(dataState(snapshot), "not_capturing")
})

import { snapshotSupersedes } from "../../frontend/src/lib/snapshot.ts"
test("late core and locale responses cannot overwrite newer state",()=>{
  const current={sequence:10,localeRevision:3}
  assert.equal(snapshotSupersedes({sequence:10,localeRevision:4},current),true)
  assert.equal(snapshotSupersedes({sequence:11,localeRevision:3},current),true)
  assert.equal(snapshotSupersedes({sequence:11,localeRevision:2},current),false)
  assert.equal(snapshotSupersedes({sequence:9,localeRevision:4},current),false)
  assert.equal(snapshotSupersedes(current,current),false)
  assert.equal(snapshotSupersedes({sequence:11,localeRevision:3},current,4),false)
})
