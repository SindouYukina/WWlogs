import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { easeNumber, planNumberUpdate, planSelectionUpdate, reconcilePresence, springCurve, springPosition, stepCritical } from "../../frontend/src/design/motion/spring.ts"
import { durations, motionVariables, selectionNumberMs, springs } from "../../frontend/src/design/motion/tokens.ts"
import { combatRows, countdownFraction, countdownState, effectRow } from "../../frontend/src/lib/combat-view.ts"

test("spring curves start at 0, end at 1 and stay within a compact CSS value", () => {
  for (const [name, params] of Object.entries(springs)) {
    const curve = springCurve(params)
    assert.match(curve.easing, /^linear\(0, .*, 1\)$/, name)
    assert.ok(curve.durationMs > 100 && curve.durationMs < 3000, `${name} duration ${curve.durationMs}`)
    assert.ok(curve.easing.length < 900, `${name} easing is ${curve.easing.length} chars`)
    const stops = curve.easing.slice(7, -1).split(", ").slice(1, -1).map(stop => parseFloat(stop.split(" ")[1]))
    for (let i = 1; i < stops.length; i++) assert.ok(stops[i] > stops[i - 1], `${name} stops must increase`)
  }
})

test("data bars and rank motion never mislead with a large overshoot", () => {
  const peak = params => {
    let max = 0
    for (let t = 0; t < 3; t += 1 / 240) max = Math.max(max, springPosition(params, t))
    return max
  }
  assert.ok(peak(springs.data) <= 1.0005, "a data bar must not overstate its value")
  assert.ok(peak(springs.rank) < 1.04, "rank swaps settle without a bounce that reads as data")
  assert.ok(peak(springs.lively) > 1.01, "the lively spring is the one with visible character")
})

test("reduced motion and review speed apply to every timing variable", () => {
  const reduced = motionVariables({ preference: "reduced" })
  for (const name of Object.keys(durations)) assert.equal(reduced[`--ww-dur-${name}`], "0ms")
  for (const name of Object.keys(springs)) assert.equal(reduced[`--ww-spring-${name}-dur`], "0ms")
  assert.equal(motionVariables({ scale: 2 })["--ww-dur-base"], `${durations.base * 2}ms`)
})

test("an animated counter never displays a value above the engine's latest value", () => {
  let state = { position: 0, velocity: 0 }
  let target = 0
  const steps = [50000, 0, 0, 1200000, 0, 300, 300, 0, 0, 0]
  for (const increase of steps) {
    target += increase
    for (let frame = 0; frame < 15; frame++) {
      state = stepCritical(state, target, 11, 1 / 60)
      assert.ok(state.position <= target + 1e-6, `position ${state.position} passed ${target}`)
    }
  }
  for (let frame = 0; frame < 240; frame++) state = stepCritical(state, target, 11, 1 / 60)
  assert.ok(Math.abs(state.position - target) < 1, "the counter settles on the exact value")
})

test("the follower is stable after a very long frame", () => {
  const state = stepCritical({ position: 0, velocity: 0 }, 1000, 11, 10)
  assert.ok(Math.abs(state.position - 1000) < 1e-3 && Number.isFinite(state.velocity))
})

test("number updates: resets, regressions, stale data and missing data are shown, not animated", () => {
  assert.equal(planNumberUpdate(100, 200, true, false), "animate")
  assert.equal(planNumberUpdate(100, 200, false, false), "snap", "a new encounter does not animate across scopes")
  assert.equal(planNumberUpdate(100, 50, true, false), "snap", "a decrease is a fact, not a transition")
  assert.equal(planNumberUpdate(100, 200, true, true), "snap", "stale data does not pretend to be live")
  assert.equal(planNumberUpdate(undefined, 200, true, false), "snap")
  assert.equal(planNumberUpdate(100, undefined, true, false), "hide")
  assert.equal(planNumberUpdate(100, Number.NaN, true, false), "hide")
})

test("a selection number moves to a lower answer as it does to a higher one; scope, stale and missing data snap or hide", () => {
  assert.equal(planSelectionUpdate(100, 200, true, false), "animate")
  assert.equal(planSelectionUpdate(100, 50, true, false), "animate", "a smaller range is another selection, not a reset")
  assert.equal(planSelectionUpdate(100, 200, false, false), "snap", "a new encounter does not animate across scopes")
  assert.equal(planSelectionUpdate(100, 200, true, true), "snap")
  assert.equal(planSelectionUpdate(undefined, 200, true, false), "snap")
  assert.equal(planSelectionUpdate(100, undefined, true, false), "hide")
  assert.equal(planSelectionUpdate(100, Number.NaN, true, false), "hide")
})

test("a selection number reaches the exact answer within its short duration and never leaves [from, to]", () => {
  // Short enough that the final value is read about one fifth of a second after an answer arrives.
  assert.ok(selectionNumberMs >= 120 && selectionNumberMs <= 240, `${selectionNumberMs} ms`)
  for (const [from, to] of [[0, 1234567], [987654, 12], [500, 500], [-3, 7]]) {
    let previous = from
    for (let elapsed = 0; elapsed <= selectionNumberMs + 40; elapsed += 1000 / 60) {
      const value = easeNumber(from, to, elapsed, selectionNumberMs)
      assert.ok(value >= Math.min(from, to) - 1e-9 && value <= Math.max(from, to) + 1e-9, `${value} outside ${from}..${to}`)
      assert.ok(to >= from ? value >= previous - 1e-9 : value <= previous + 1e-9, "monotonic")
      previous = value
    }
    assert.equal(easeNumber(from, to, selectionNumberMs, selectionNumberMs), to, "the last frame is the answer itself")
  }
  // An ease-out: most of the way there by the middle of the move.
  assert.ok(easeNumber(0, 100, selectionNumberMs / 2, selectionNumberMs) > 80)
  assert.equal(easeNumber(0, 100, 50, 0), 100)
})

test("History selection numbers ease; the live Dashboard keeps its counters", () => {
  const read = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8")
  const number = read("frontend/src/design/components/AnimatedNumber.tsx")
  assert.match(number, /if \(mode === "ease"\) return <EaseNumber/)
  assert.match(number, /planSelectionUpdate\(previous\.current\.value, value, previous\.current\.scope === scope, stale\)/)
  assert.match(number, /planned === "animate" && motionReduced\(\) \? "snap" : planned/)
  const analysis = read("frontend/src/views/HistoryAnalysis.tsx")
  assert.equal(analysis.match(/numberMode="tween"/g), null)
  assert.equal(analysis.match(/numberMode="ease"/g)?.length, 5)
  const dashboard = read("frontend/src/views/DashboardView.tsx")
  // The ranking's damage line follows the board's own mode only when that mode is ease.
  assert.match(dashboard, /mode=\{numberMode === "ease" \? "ease" : "tween"\}/)
})

const keyOf = item => item.key

test("presence keeps removed items for their exit, then drops them; a returning key revives", () => {
  const a = { key: "a" }, b = { key: "b" }
  let entries = reconcilePresence([], [a, b], keyOf, 0, 200)
  assert.deepEqual(entries.map(e => [e.key, e.phase]), [["a", "enter"], ["b", "enter"]])
  entries = reconcilePresence(entries, [b], keyOf, 100, 200)
  assert.deepEqual(entries.map(e => [e.key, e.phase]), [["a", "exit"], ["b", "enter"]])
  assert.equal(entries[0].removeAt, 300)
  assert.deepEqual(reconcilePresence(entries, [b], keyOf, 250, 200).map(e => e.key), ["a", "b"], "still exiting")
  assert.deepEqual(reconcilePresence(entries, [b], keyOf, 300, 200).map(e => e.key), ["b"], "exit finished")
  const revived = reconcilePresence(entries, [a, b], keyOf, 150, 200)
  assert.equal(revived.find(e => e.key === "a").phase, "enter")
  assert.equal(revived.find(e => e.key === "a").removeAt, undefined)
})

function encounter(overrides = {}) {
  return {
    dps: { status: "available", durationSeconds: 10, damage: 0, value: 0 },
    roles: [
      { playerId: 1, roleId: 1205, name: "A", nameStatus: "VERIFIED", damageDealt: 100, damageShare: 0.25, damageTaken: 0, rank: 1, dps: 10 },
      { playerId: 1, roleId: 1506, name: "", nameStatus: "UNKNOWN", damageDealt: 300, damageShare: 0.75, damageTaken: 5, rank: 0, dps: 30 },
    ],
    ...overrides,
  }
}

test("combat rows follow the engine's rank, keep identity and never invent a name", () => {
  const rows = combatRows(encounter())
  assert.deepEqual(rows.map(row => [row.key, row.rank]), [["role-1-1506", 0], ["role-1-1205", 1]])
  assert.equal(rows[0].name, null, "an unverified name stays null with its raw role ID")
  assert.equal(rows[0].roleId, 1506)
  assert.equal(rows[1].name, "A")
  assert.equal(rows[0].dps, 30)
})

test("a row's DPS is the engine's value and nothing is worked out here", () => {
  const withoutDps = encounter({ roles: [{ playerId: 1, roleId: 1205, name: "A", nameStatus: "VERIFIED", damageDealt: 100, damageTaken: 0, rank: 0 }] })
  assert.equal(combatRows(withoutDps)[0].dps, undefined, "the engine reported none, so an available encounter DPS adds none")
  const odd = encounter({ dps: { status: "unreliable", durationSeconds: 10 }, roles: [{ playerId: 1, roleId: 1205, name: "A", nameStatus: "VERIFIED", damageDealt: 100, damageTaken: 0, rank: 0, dps: 7 }] })
  assert.equal(combatRows(odd)[0].dps, 7, "the value is passed through, not derived from damage and duration")
})

test("a vehicle is a separate contributor, not a synthetic role", () => {
  const rows = combatRows(encounter({ vehicle: { kind: "motorcycle", damageDealt: 200, damageShare: 0.3, rank: 1, dps: 20 } }))
  const vehicle = rows.find(row => row.kind === "vehicle")
  assert.equal(vehicle.roleId, undefined)
  assert.equal(vehicle.name, null)
})

test("a countdown is shown only for what the engine reported", () => {
  assert.deepEqual(countdownState({ durationSeconds: 10 }, undefined), { kind: "unknown" })
  assert.deepEqual(countdownState({}, 4), { kind: "remaining-only", remaining: 4 })
  assert.deepEqual(countdownState({ durationSeconds: 0 }, 4), { kind: "remaining-only", remaining: 4 })
  assert.equal(countdownFraction({ kind: "unknown" }), undefined)
  assert.equal(countdownFraction({ kind: "remaining-only", remaining: 4 }), undefined, "no proportion without a total")
  assert.equal(countdownFraction({ kind: "timed", remaining: 5, duration: 10 }), 0.5)
  assert.equal(countdownFraction({ kind: "timed", remaining: 15, duration: 10 }), 1, "never above full")
})

test("no-timer effects stay unknown", () => {
  const base = { key: "k", polarity: "unclassified", display: { eligibility: "eligible" }, firstObservedAt: "t0", lastUpdatedAt: "t0", remainingStatus: "unknown" }
  assert.deepEqual(effectRow(base, undefined).countdown, { kind: "unknown" })
})
