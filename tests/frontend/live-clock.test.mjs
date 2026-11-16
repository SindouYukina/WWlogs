import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import { effectRemaining, liveClockActive, snapshotStale } from "../../frontend/src/lib/effects.ts"
import { dataChip, isFrozen } from "../../frontend/src/lib/combat-data.ts"

// The live wall clock (useClock, CombatData.now) feeds only the freshness checks. Every reader of it ignores it unless
// the service is running, so the clock ticks only then (Phase 3A R31). A reader that starts to depend on the clock
// outside "running" fails here.

const at = Date.parse("2026-11-16T12:00:00Z")
const view = { health: { dataIntegrity: "INTACT", context: { state: "VALID" } }, encounters: { recent: [] }, effects: { status: "complete" } }
const effect = { lifecycle: "active", remainingSeconds: 12, showCountdown: true }
const snapshotIn = state => ({ publishedAt: new Date(at).toISOString(), service: { state }, view })
const hour = 3_600_000

test("no reader of the live clock depends on it unless the service is running, and no countdown shows then", () => {
  for (const snapshot of [null, ...["stopped", "starting", "stopping", "error"].map(snapshotIn)]) {
    for (const connection of ["connecting", "connected", "disconnected"]) {
      for (const paused of [false, true]) {
        const label = `${snapshot?.service.state ?? "no snapshot"}, ${connection}, paused ${paused}`
        const early = { snapshot, connection, paused, now: at }
        const late = { ...early, now: at + hour }
        assert.equal(snapshotStale(snapshot, late.now), snapshotStale(snapshot, early.now), label)
        assert.equal(isFrozen(late), isFrozen(early), label)
        assert.deepEqual(dataChip(late), dataChip(early), label)
        assert.equal(effectRemaining(effect, snapshot, connection, late.now), effectRemaining(effect, snapshot, connection, early.now), label)
        assert.equal(effectRemaining(effect, snapshot, connection, early.now), undefined, label)
      }
    }
  }
})

test("while the service runs, the clock turns an old snapshot stale and stops its countdowns", () => {
  const snapshot = snapshotIn("running")
  const fresh = { snapshot, connection: "connected", now: at + 1000 }
  const old = { ...fresh, now: at + 6000 }
  assert.equal(snapshotStale(snapshot, fresh.now), false)
  assert.equal(snapshotStale(snapshot, old.now), true)
  assert.equal(isFrozen(fresh), false)
  assert.equal(isFrozen(old), true)
  assert.equal(dataChip(old).key, "stale")
  assert.equal(effectRemaining(effect, snapshot, "connected", fresh.now), 12)
  assert.equal(effectRemaining(effect, snapshot, "connected", old.now), undefined)
})

test("the clock runs exactly while the service runs", () => {
  assert.equal(liveClockActive(null), false)
  for (const state of ["stopped", "starting", "stopping", "error"]) assert.equal(liveClockActive(snapshotIn(state)), false, state)
  assert.equal(liveClockActive(snapshotIn("running")), true)
  const provider = readFileSync("frontend/src/lib/useLiveCombatData.ts", "utf8")
  assert.match(provider, /useClock\(liveClockActive\(snapshot\)\)/)
  assert.match(provider, /useEffect\(\(\) => \{\s*if \(!active\) return/)
  assert.match(provider, /\}, \[active\]\)/)
})
