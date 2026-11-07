import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { adapterProblem, captureBusy } from "../../frontend/src/lib/capture-environment.ts"

const read = path => readFileSync(path, "utf8")
const manifest = JSON.parse(read("frontend/locales/manifest.json"))
const locale = (code, namespace) => JSON.parse(read(`frontend/locales/${code}/${namespace}.json`))

test("the saved interface problem is reported only once Npcap is ready", () => {
  const adapters = [{ ipv4: "192.0.2.10", description: "" }]
  assert.equal(adapterProblem(null, adapters, ""), null, "no check has finished")
  assert.equal(adapterProblem("npcap_missing", [], ""), null, "the environment notice explains it")
  assert.equal(adapterProblem("ready", adapters, ""), "unset")
  assert.equal(adapterProblem("ready", adapters, "192.0.2.11"), "unavailable", "never replaced by another address")
  assert.equal(adapterProblem("ready", adapters, "192.0.2.10"), null)
})

test("the interface is locked from start until release", () => {
  for (const state of ["starting", "running", "stopping"]) assert.equal(captureBusy(state), true, state)
  for (const state of ["stopped", "error", undefined]) assert.equal(captureBusy(state), false, String(state))
})

test("General lists language, interface, launch start and update interval in that order", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  const general = view.slice(view.indexOf("function GeneralPage"), view.indexOf("function AppearancePage"))
  const order = ['data-field="locale"', "<AdapterRow", 'data-field="auto-start-capture"', 'data-field="update-interval"'].map(marker => general.indexOf(marker))
  assert.ok(order.every(index => index >= 0), JSON.stringify(order))
  assert.deepEqual([...order].sort((a, b) => a - b), order)
  const row = view.slice(view.indexOf("function AdapterRow"), view.indexOf("function GeneralPage"))
  assert.ok(row.includes("settings-environment-recheck") && row.includes("controller.checkEnvironment"), "the recheck stays with the interface")
  assert.ok(row.includes("disabled={controller.captureBusy"), "a running capture locks the interface")
})

test("the separate capture page and its navigation are gone", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  assert.ok(!view.includes("CapturePage") && !/"capture"/.test(view), "no capture page, route or sidebar entry")
  for (const code of manifest.supportedLocales) {
    const settings = locale(code, "settings")
    assert.equal(settings.groups.capture, undefined, code)
    assert.equal(settings.pages.capture, undefined, code)
    assert.equal(settings.chooseAtLaunch, undefined, code)
    for (const key of ["defaultAdapter", "adapterHint", "adapterChoose", "adapterLocked", "autoStartCapture", "autoStartCaptureHint"]) assert.ok(settings[key], `${code}: ${key}`)
    const dashboard = locale(code, "dashboard")
    for (const key of ["startHint", "interfaceUnset", "interfaceUnavailable", "openSettings"]) assert.ok(dashboard[key], `${code}: ${key}`)
  }
})

test("the header reads status, Overlays, then the capture button, without an interface picker", () => {
  const app = read("frontend/src/views/DesktopApp.tsx")
  const tools = app.slice(app.indexOf('className="ww-app-tools"'), app.indexOf("</header>"))
  const order = ['data-field="listening"', 'className="ww-app-overlays"', 'className="ww-app-capture"'].map(marker => tools.indexOf(marker))
  assert.ok(order.every(index => index >= 0), JSON.stringify(order))
  assert.deepEqual([...order].sort((a, b) => a - b), order)
  assert.ok(!tools.includes("<Select"), "the interface is chosen in Settings")
  assert.ok(tools.includes('<Tooltip text={t("startHint")} trigger="child"'), "the start button carries the login hint")
  const stop = tools.slice(tools.indexOf('data-field="capture-stop"') - 200, tools.indexOf('data-field="capture-stop"'))
  assert.ok(!stop.includes("Tooltip"), "stopping is never obstructed by the hint")
  assert.ok(!app.includes("startCapture(adapter") || app.includes("const adapter = controller.defaultAdapter"), "manual start uses the saved interface")
})

test("the launch start has one owner in the host", () => {
  const appGo = read("internal/desktop/app.go")
  assert.ok(appGo.includes("bridge.startAtLaunch(") && appGo.includes("bridge.startup.claim()"))
  const bridge = read("internal/desktop/bridge.go")
  const start = bridge.slice(bridge.indexOf("func (b *Bridge) StartCapture"), bridge.indexOf("func (b *Bridge) startCapture"))
  const stop = bridge.slice(bridge.indexOf("func (b *Bridge) StopCapture"), bridge.indexOf("func (b *Bridge) ServiceShutdown"))
  assert.ok(start.includes("b.startup.claim()") && stop.includes("b.startup.claim()"), "a manual start or stop settles the launch start")
  assert.ok(!read("frontend/src/views/DesktopApp.tsx").includes("autoStartCapture"), "the frontend never starts a capture by itself")
})
