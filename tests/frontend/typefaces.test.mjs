import assert from "node:assert/strict"
import test from "node:test"
import { fetchFontManifest, requiredFaces, schemeFamilies, schemeStatusFrom, schemeUsable, uncoveredCharacters } from "../../frontend/src/showcase/typefaces.ts"

const face = (file, family = "MiSans", ranges) => ({ file, family, ranges })

test("a scheme is loaded only when every required file was fetched and decoded", () => {
  const required = [face("a.ttf"), face("b.ttf")]
  assert.equal(schemeStatusFrom(required, new Map()).kind, "loading")
  assert.equal(schemeStatusFrom(required, new Map([["a.ttf", "loaded"]])).kind, "loading")
  assert.equal(schemeStatusFrom(required, new Map([["a.ttf", "loaded"], ["b.ttf", "loaded"]])).kind, "loaded")
})

test("a missing or corrupt file is named and is never success", () => {
  const required = [face("a.ttf"), face("b.ttf")]
  const missing = schemeStatusFrom(required, new Map([["a.ttf", "loaded"], ["b.ttf", "missing"]]))
  assert.deepEqual([missing.kind, missing.missing], ["missing", ["b.ttf"]])
  assert.equal(schemeUsable(missing), false)
  const failed = schemeStatusFrom(required, new Map([["a.ttf", "failed"], ["b.ttf", "loaded"]]))
  assert.deepEqual([failed.kind, failed.failed], ["failed", ["a.ttf"]])
  assert.equal(schemeUsable(failed), false)
  assert.equal(schemeStatusFrom([], new Map()).kind, "missing", "a manifest that lists no face for this language is not success")
})

test("only a real load or the declared system baseline may be shown under a scheme's name", () => {
  for (const kind of ["loaded", "baseline"]) assert.equal(schemeUsable({ kind, missing: [], failed: [] }), true)
  for (const kind of ["loading", "no-root", "missing", "failed"]) assert.equal(schemeUsable({ kind, missing: [], failed: [] }), false)
})

test("required faces follow the active language", () => {
  const manifest = { misans: { faces: [face("tc.ttf", "MiSans TC"), face("sc.ttf", "MiSans")] }, sarasa: { faces: [face("tc.woff2", "Sarasa UI TC"), face("k.woff2", "Sarasa UI K")] } }
  assert.deepEqual(requiredFaces(manifest, "misans", "zh-TW").map(item => item.file), ["tc.ttf", "sc.ttf"])
  assert.deepEqual(requiredFaces(manifest, "misans", "ko").map(item => item.file), ["sc.ttf"])
  assert.deepEqual(requiredFaces(manifest, "sarasa", "ko").map(item => item.file), ["k.woff2"])
  assert.deepEqual(requiredFaces(null, "misans", "ko"), [])
  assert.deepEqual(schemeFamilies("system", "ja"), [], "the baseline requires nothing")
})

test("coverage reports what the faces do not map, and unknown is not full", () => {
  const latin = face("a.ttf", "A", [[0x41, 0x5a], [0x61, 0x7a]])
  const cjk = face("b.ttf", "B", [[0x4e00, 0x9fff]])
  assert.deepEqual(uncoveredCharacters("Ab 永", [latin, cjk]), [])
  assert.deepEqual(uncoveredCharacters("Ab 한글 永", [latin, cjk]), ["한", "글"])
  assert.deepEqual(uncoveredCharacters("한한한", [latin]), ["한"], "each character is listed once")
  assert.deepEqual(uncoveredCharacters("𠀋", [face("c.ttf", "C", [[0x2000b, 0x2000b]])]), [], "supplementary code points count as one character")
  assert.equal(uncoveredCharacters("A", [face("d.ttf", "D")]), undefined)
  assert.equal(uncoveredCharacters("A", []), undefined)
})

test("a manifest that cannot be read counts as no candidate directory", async () => {
  const json = body => async () => new Response(body, { status: 200 })
  assert.equal(await fetchFontManifest(async () => new Response("", { status: 404 })), null)
  assert.equal(await fetchFontManifest(json("<!doctype html><html></html>")), null, "an HTML fallback page is not a manifest")
  assert.equal(await fetchFontManifest(async () => { throw new Error("offline") }), null)
  assert.deepEqual(await fetchFontManifest(json(`{"misans":{"faces":[]}}`)), { misans: { faces: [] } })
})
