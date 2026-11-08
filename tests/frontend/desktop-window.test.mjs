import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")

test("Settings offers one button that returns the main window to its default size and position", () => {
  const view = read("frontend/src/views/SettingsView.tsx")
  const general = view.slice(view.indexOf("function GeneralPage"), view.indexOf("function AppearancePage"))
  assert.match(general, /<SettingsSection title=\{t\("sections\.window"\)\}>/)
  assert.match(general, /data-field="reset-main-window" onClick=\{controller\.resetDesktopWindow\}>\{t\("resetMainWindow"\)\}/)
  assert.match(read("frontend/src/lib/settings-controller.ts"), /resetDesktopWindow: \(\) => guard\(host\.resetDesktopWindow\(\)\)/)
  assert.match(read("frontend/src/lib/host.ts"), /resetDesktopWindow: \(\) => call<void>\("ResetDesktopWindow"\)/)
  for (const locale of ["en", "ja", "ko", "zh-CN", "zh-TW"]) {
    const strings = JSON.parse(read(`frontend/locales/${locale}/settings.json`))
    for (const key of ["mainWindow", "mainWindowNote", "resetMainWindow"]) assert.ok(strings[key], `${locale} ${key}`)
  }
})

test("a failed window save is reported like any other save failure", () => {
  assert.match(read("frontend/src/views/SettingsView.tsx"), /controller\.warning === "overlay_save_failed" \|\| controller\.warning === "window_save_failed" \? "saveFailed"/)
})
