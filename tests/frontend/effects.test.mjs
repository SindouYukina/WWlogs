import test from "node:test"
import assert from "node:assert/strict"
import {effectRemaining,snapshotStale} from "../../frontend/src/lib/effects.ts"
import {readFileSync} from "node:fs"

const at=Date.parse("2026-11-05T00:00:00Z")
function snapshot(source="live") {return {publishedAt:new Date(at).toISOString(),service:{state:"running",session:{source}},view:{health:{context:{state:"VALID"}},effects:{status:"complete"}}}}
const effect={lifecycle:"active",remainingStatus:"estimated",remainingSeconds:12,showCountdown:true,holderRelation:"local_role",sourceRelation:"unknown",combatClass:"combat"}
test("countdown uses only Go capture time, including paced replay",()=>{
  for(const source of ["live","replay"]) {
    const s=snapshot(source)
    assert.equal(effectRemaining(effect,s,"connected",at+2000),12)
    assert.equal(effectRemaining({...effect,remainingSeconds:8},s,"connected",at+2000),8)
  }
})
test("the engine decides whether a countdown is trustworthy; the frontend adds only connection and freshness",()=>{
  const s=snapshot()
  assert.equal(snapshotStale(s,at+6000),true)
  for(const e of [{...effect,showCountdown:false},{...effect,showCountdown:undefined},{...effect,remainingSeconds:undefined}]) assert.equal(effectRemaining(e,s,"connected",at),undefined)
  assert.equal(effectRemaining(effect,s,"connected",at+6000),undefined)
  assert.equal(effectRemaining(effect,s,"disconnected",at),undefined)
  s.service.state="stopped"
  assert.equal(effectRemaining(effect,s,"connected",at),undefined)
})
test("display eligibility belongs to the engine: no frontend effect filter or classifier exists",()=>{
  const source=readFileSync("frontend/src/lib/effects.ts","utf8")
  assert.ok(!source.includes("relevantEffect"))
  assert.ok(!/combatClass|holderRelation/.test(source),"the frontend must not re-derive relevance from relations or classes")
})
