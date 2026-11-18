import test from "node:test"
import assert from "node:assert/strict"
import {effectRemaining,effectsOverlayMode,snapshotStale} from "../../frontend/src/lib/effects.ts"
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
test("the Effects Overlay mode keeps content and Edit Mode apart",()=>{
  // 1. No effect outside Edit Mode: hidden. 2. Effects outside Edit Mode: visible.
  assert.equal(effectsOverlayMode(0,false),"hidden")
  assert.equal(effectsOverlayMode(1,false),"visible")
  assert.equal(effectsOverlayMode(37,false),"visible")
  // 3. No effect in Edit Mode: the preview. 4. Effects in Edit Mode: still Edit Mode, drawn with the effects.
  assert.equal(effectsOverlayMode(0,true),"edit-preview")
  assert.equal(effectsOverlayMode(5,true),"edit-preview")
})
test("leaving Edit Mode and a combat's effects coming and going switch the mode with nothing left behind",()=>{
  // [cells, editing] over time, and the mode each must give.
  const timeline=[
    [0,false,"hidden"],[0,true,"edit-preview"],[0,false,"hidden"], // 5. leave Edit Mode with no effect
    [0,true,"edit-preview"],[2,true,"edit-preview"],[2,false,"visible"], // 6. leave Edit Mode with effects
    [3,false,"visible"],[1,false,"visible"],[0,false,"hidden"],[4,false,"visible"],[0,false,"hidden"], // 7. a combat
  ]
  for(const [cells,editing,mode] of timeline) assert.equal(effectsOverlayMode(cells,editing),mode,`${cells} cells, editing ${editing}`)
})
test("display eligibility belongs to the engine: no frontend effect filter or classifier exists",()=>{
  const source=readFileSync("frontend/src/lib/effects.ts","utf8")
  assert.ok(!source.includes("relevantEffect"))
  assert.ok(!/combatClass|holderRelation/.test(source),"the frontend must not re-derive relevance from relations or classes")
})
