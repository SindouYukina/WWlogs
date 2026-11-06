import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { classifyGameAssetFailure, gameAssetUrl } from "../../frontend/src/design/assets.ts"
import { adapterUsable, environmentNotice } from "../../frontend/src/lib/capture-environment.ts"

const manifest = JSON.parse(readFileSync("frontend/locales/manifest.json", "utf8"))
const read = path => readFileSync(path, "utf8")

test("each state has its own notice and no state reads as a missing IPv4 address", () => {
  assert.equal(environmentNotice(null), null)
  assert.equal(environmentNotice("ready"), null)
  const keys = ["npcap_missing", "npcap_unavailable", "no_interfaces", "no_ipv4", "check_failed"].map(state => environmentNotice(state).key)
  assert.equal(new Set(keys).size, keys.length)
  assert.deepEqual(keys, ["missing", "unavailable", "noInterfaces", "noIpv4", "checkFailed"])
})

test("an unknown state from a newer host reads as a failed check, never as ready", () => {
  assert.equal(environmentNotice("something_new").key, "checkFailed")
})

test("only Npcap's own problems offer its installer", () => {
  assert.equal(environmentNotice("npcap_missing").offersDownload, true)
  assert.equal(environmentNotice("npcap_unavailable").offersDownload, true)
  assert.equal(environmentNotice("no_interfaces").offersDownload, false)
  assert.equal(environmentNotice("no_ipv4").offersDownload, false)
  assert.equal(environmentNotice("check_failed").offersDownload, false)
})

test("capture can start only from a finished check that found the address", () => {
  const adapters = [{ ipv4: "192.0.2.10", description: "" }]
  assert.equal(adapterUsable("ready", false, adapters, "192.0.2.10"), true)
  assert.equal(adapterUsable("ready", true, adapters, "192.0.2.10"), false, "a check is running")
  assert.equal(adapterUsable(null, false, adapters, "192.0.2.10"), false, "no check has finished")
  assert.equal(adapterUsable("npcap_unavailable", false, adapters, "192.0.2.10"), false, "a stale list")
  assert.equal(adapterUsable("ready", false, adapters, "192.0.2.11"), false, "the address is gone")
  assert.equal(adapterUsable("ready", false, [], ""), false)
})

test("every locale carries the whole environment notice text", () => {
  const required = ["checking", "recheck", "download", "openPage", "opening", "resolving", "latestVersion", "sourceUnconfirmed", "openFailed", "afterOpen"]
  const groups = ["missing", "unavailable", "noInterfaces", "noIpv4", "checkFailed"]
  assert.equal(manifest.supportedLocales.length, 5)
  for (const locale of manifest.supportedLocales) {
    const environment = JSON.parse(read(`frontend/locales/${locale}/dashboard.json`)).environment
    for (const key of required) assert.ok(environment[key], `${locale}: ${key}`)
    for (const group of groups) assert.ok(environment[group].title && environment[group].body, `${locale}: ${group}`)
    assert.ok(environment.latestVersion.includes("{{version}}"), locale)
    assert.ok(JSON.parse(read(`frontend/locales/${locale}/settings.json`)).loadFailed, locale)
  }
})

test("capture problems cannot raise the settings save error", () => {
  const controller = read("frontend/src/lib/settings-controller.ts")
  const check = controller.slice(controller.indexOf("const checkEnvironment"), controller.indexOf("useEffect("))
  assert.ok(check.includes("check_failed"))
  assert.ok(!check.includes("setFailed"), "a failed check must not report a failed save")
  const load = controller.slice(controller.indexOf("const loadPreferences"), controller.indexOf("const checkEnvironment"))
  assert.ok(load.includes("setLoadFailed(true)") && !load.includes("setFailed("), "a failed load is its own error")
})

test("the notice carries its recheck and the views share one implementation", () => {
  const notice = read("frontend/src/views/CaptureEnvironmentNotice.tsx")
  assert.ok(notice.includes("environment-recheck") && notice.includes("npcap-download"))
  assert.ok(!/[一-鿿]/.test(notice), "UI text belongs in the bundled translations")
  assert.ok(read("frontend/src/views/DesktopApp.tsx").includes("<CaptureEnvironmentNotice"))
  assert.ok(!read("frontend/src/showcase/useMockSettings.ts").includes("CaptureEnvironmentNotice"))
})

test("a failed image separates a missing ID from a delivery failure", async () => {
  let requests = 0
  const answer = status => async () => { requests += 1; return { status } }
  assert.equal(await classifyGameAssetFailure(gameAssetUrl("buff", 1), answer(404)), "missing")
  assert.equal(await classifyGameAssetFailure(gameAssetUrl("buff", 2), answer(500)), "failed")
  assert.equal(await classifyGameAssetFailure(gameAssetUrl("buff", 3), async () => { throw new Error("host gone") }), "failed")
  assert.equal(await classifyGameAssetFailure(gameAssetUrl("buff", 4), answer(200)), "failed", "an undecodable image is a failure")
  assert.equal(await classifyGameAssetFailure(gameAssetUrl("buff", 1), answer(500)), "missing", "the answer is remembered")
  assert.equal(requests, 3)
})
