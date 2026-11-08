import test from "node:test"
import assert from "node:assert/strict"
import {effectRemaining,snapshotStale} from "../../frontend/src/lib/effects.ts"
import {readFileSync} from "node:fs"

const at=Date.parse("2026-11-05T00:00:00Z")
function snapshot(source="live") {return {publishedAt:new Date(at).toISOString(),service:{state:"running",session:{source}},view:{health:{context:{state:"VALID"}},effects:{status:"complete"}}}}
const effect={lifecycle:"active",remainingStatus:"estimated",remainingSeconds:12,holderRelation:"local_role",sourceRelation:"unknown",combatClass:"combat"}
test("countdown uses only Go capture time, including paced replay",()=>{
  for(const source of ["live","replay"]) {
    const s=snapshot(source)
    assert.equal(effectRemaining(effect,s,"connected",at+2000),12)
    assert.equal(effectRemaining({...effect,remainingSeconds:8},s,"connected",at+2000),8)
  }
})
test("stale context, stale snapshot, removed effects and missing duration never show a precise timer",()=>{
  const s=snapshot()
  assert.equal(snapshotStale(s,at+6000),true)
  for(const e of [{...effect,stale:true},{...effect,lifecycle:"confirmed_removed"},{...effect,remainingStatus:"unknown"},{...effect,remainingSeconds:undefined}]) assert.equal(effectRemaining(e,s,"connected",at),undefined)
  assert.equal(effectRemaining(effect,s,"connected",at+6000),undefined)
  assert.equal(effectRemaining({...effect,maintained:true},s,"connected",at),undefined,"a maintained modifier shows no refresh period as a countdown")
  assert.equal(effectRemaining(effect,{...s,view:{...s.view,effects:{...s.view.effects,clockStalled:true}}},"connected",at),undefined,"a stalled capture clock shows no countdown")
  assert.equal(effectRemaining(effect,s,"disconnected",at),undefined)
  s.view.health.context.state="SUSPECT"
  assert.equal(effectRemaining(effect,s,"connected",at),undefined)
  s.view.health.context.state="VALID";s.service.state="stopped"
  assert.equal(effectRemaining(effect,s,"connected",at),undefined)
})
test("display eligibility belongs to the engine: no frontend effect filter or classifier exists",()=>{
  const source=readFileSync("frontend/src/lib/effects.ts","utf8")
  assert.ok(!source.includes("relevantEffect"))
  assert.ok(!/combatClass|holderRelation/.test(source),"the frontend must not re-derive relevance from relations or classes")
})
