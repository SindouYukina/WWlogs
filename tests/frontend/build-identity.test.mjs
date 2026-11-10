import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { buildNumberText, profileKey, revisionText, versionText } from "../../frontend/src/lib/build-identity.ts"

const commit = "0123456789abcdef0123456789abcdef01234567"
const official = { version: "v0.6.0", buildNumber: 128, official: true, commit, dirty: "false", profile: "public" }

test("an official build shows its version, number, type and revision", () => {
  assert.deepEqual(versionText(official), { kind: "value", value: "0.6.0" })
  assert.deepEqual(buildNumberText(official), { kind: "value", value: "128" })
  assert.equal(profileKey(official), "buildProfilePublic")
  assert.equal(profileKey({ ...official, profile: "development" }), "buildProfileDevelopment")
  assert.deepEqual(revisionText(official), { revision: { kind: "value", value: "0123456789ab" }, note: undefined })
})

test("an unofficial build never shows a number", () => {
  for (const app of [{ ...official, official: false, buildNumber: 0 }, { ...official, official: false }, { ...official, buildNumber: 0 }]) {
    assert.deepEqual(buildNumberText(app), { kind: "label", key: "buildUnofficial" })
  }
})

test("modified, unknown and unavailable sources are labelled, not hidden", () => {
  assert.equal(revisionText({ ...official, dirty: "true" }).note, "buildModified")
  assert.equal(revisionText({ ...official, dirty: "unknown" }).note, "buildStateUnknown")
  assert.deepEqual(revisionText({ ...official, commit: "UNAVAILABLE" }).revision, { kind: "label", key: "buildUnavailable" })
  assert.deepEqual(versionText({ ...official, version: "UNAVAILABLE" }), { kind: "label", key: "buildUnavailable" })
})

test("the Go identity and the frontend type carry the same fields", () => {
  const go = readFileSync("internal/service/capture_service.go", "utf8")
  const block = /type AppIdentity struct \{([\s\S]*?)\n\}/.exec(go)[1]
  const goFields = [...block.matchAll(/json:"([a-zA-Z]+)"/g)].map(match => match[1]).sort()
  const ts = readFileSync("frontend/src/lib/build-identity.ts", "utf8")
  const tsBlock = /export interface AppIdentity \{([\s\S]*?)\n\}/.exec(ts)[1]
  const tsFields = [...tsBlock.matchAll(/^\s+([a-zA-Z]+):/gm)].map(match => match[1]).sort()
  assert.deepEqual(tsFields, goFields)
})

test("the About page shows no hard-coded build values", () => {
  const view = readFileSync("frontend/src/views/SettingsView.tsx", "utf8")
  // Line endings depend on the checkout (core.autocrlf), so the match accepts CRLF and LF.
  const about = /function AboutPage[\s\S]*?\r?\n\}\r?\n/.exec(view)[0]
  assert.doesNotMatch(about, /\b0\.6\.0\b|\b128\b|"v\d/)
  for (const field of ["app-version", "app-build-number", "app-profile", "app-revision"]) assert.match(about, new RegExp(`data-field="${field}"`))
})
