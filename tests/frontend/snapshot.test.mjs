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

import {damageContributors} from "../../frontend/src/lib/snapshot.ts"
test("motorcycle is a separate contributor using Go shares without a synthetic Role ID",()=>{
 const role={playerId:1,roleId:42,damageDealt:75,damageTaken:9,damageShare:0.75}
 const vehicle={kind:"motorcycle",evidence:"CORRELATED",damageDealt:25,outgoingEvents:2,damageShare:0.25}
 const rows=damageContributors([role],vehicle)
 assert.equal(rows.length,2)
 assert.equal(rows[0].role,role)
 assert.equal(rows[1].vehicle,vehicle)
 assert.equal(rows[1].role,undefined)
 assert.equal(rows[1].damageShare,0.25)
 assert.equal(rows[0].damageShare,0.75)
 assert.equal(damageContributors([],vehicle)[0].key,"vehicle-motorcycle")
 assert.equal(damageContributors([role]).length,1)
})

import { defaultUpdateInterval, normalizeUpdateInterval, shellChanged, updateIntervals, withShell } from "../../frontend/src/lib/snapshot.ts"

test("only the selectable update intervals are accepted; anything else is the 250 ms default", () => {
  assert.deepEqual([...updateIntervals], [250, 500, 750, 1000])
  for (const value of updateIntervals) assert.equal(normalizeUpdateInterval(value), value)
  for (const value of [undefined, null, 0, 100, 125, 2000, "250"]) assert.equal(normalizeUpdateInterval(value), defaultUpdateInterval)
  assert.equal(defaultUpdateInterval, 250)
})

test("a display setting change on a heartbeat updates the shell without replacing core data", () => {
  const current = { sequence: 9, localeRevision: 1, view: { marker: "core" }, updateIntervalMs: 250, animationFps: 60, theme: "electro" }
  const heartbeat = { ...current, view: { marker: "must not replace" }, updateIntervalMs: 1000, animationFps: 30 }
  assert.equal(shellChanged(heartbeat, current), true)
  const next = withShell(current, heartbeat)
  assert.equal(next.view, current.view)
  assert.equal(next.updateIntervalMs, 1000)
  assert.equal(next.animationFps, 30)
  assert.equal(shellChanged({ ...current }, current), false)
})
