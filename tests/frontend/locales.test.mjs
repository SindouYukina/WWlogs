import test from "node:test"
import assert from "node:assert/strict"
import {validateTranslation,manifest,validateLocales} from "../../frontend/locales/validate.mjs"

test("bundled resources match the manifest with complete keys and interpolation",()=>{
 const result=validateLocales()
 assert.equal(result.locales,manifest.supportedLocales.length)
 assert.equal(result.namespaces,manifest.namespaces.length)
 assert.equal(manifest.fallbackLocale,"en")
})
test("missing keys, extra keys and changed placeholders fail resource validation",()=>{
 const base={message:"Target {{name}}",empty:"None"}
 assert.doesNotThrow(()=>validateTranslation(base,{message:"目標 {{name}}",empty:"無"}))
 assert.throws(()=>validateTranslation(base,{message:"目標 {{name}}"}))
 assert.throws(()=>validateTranslation(base,{message:"目標 {{target}}",empty:"無"}))
 assert.throws(()=>validateTranslation(base,{...base,unexpected:"extra"}))
})

import enCommon from "../../frontend/locales/en/common.json" with {type:"json"}
test("transport failure and encounter-confidence machine codes have translations",()=>{
 for(const code of ["HEALTHY","GAP_DETECTED","RECOVERING","RECOVERED","UNRECOVERABLE","INFERRED","INTERRUPTED"]) assert.ok(enCommon.state[code],`missing status ${code}`)
})
