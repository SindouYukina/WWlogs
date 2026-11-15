import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

// The prompt of a Combat History that cannot be opened (frontend/src/views/HistoryRecovery.tsx) and the views that show it.
// The host decides whether a history may be rebuilt; the view only offers what it says, and deletes nothing without the
// second confirmation.

const source = name => readFileSync(new URL(`../../frontend/src/views/${name}`, import.meta.url), "utf8")
const recovery = source("HistoryRecovery.tsx")

test("the rebuild runs only from the second confirmation, and only when the host offers it", () => {
  // The host's rebuild is called in one place, run("rebuild"), which only the confirmation's danger button reaches.
  assert.equal(recovery.match(/\brebuild\(\)/g)?.length, 1)
  assert.equal(recovery.match(/run\("rebuild"\)/g)?.length, 1)
  const confirmButton = recovery.slice(recovery.indexOf('data-field="history-recovery-confirm-rebuild"') - 120, recovery.indexOf('data-field="history-recovery-confirm-rebuild"') + 120)
  assert.match(confirmButton, /variant="danger"[^>]*data-field="history-recovery-confirm-rebuild" onClick=\{\(\) => run\("rebuild"\)\}/)
  // The first button only opens the confirmation, and exists only when the host says a new history would solve it.
  assert.match(recovery, /\{rebuildable && <Button [^>]*data-field="history-recovery-rebuild" onClick=\{\(\) => \{ setFailed\(null\); setConfirming\(true\) \}\}>/)
  // Trying again is always there.
  assert.match(recovery, /data-field="history-recovery-retry" onClick=\{\(\) => run\("retry"\)\}/)
})

test("an unreadable history shows the prompt, never a reason or the first-run text", () => {
  const history = source("HistoryView.tsx")
  assert.match(history, /const empty = yearView !== null && yearView\.firstYear === 0 && !unavailable/)
  assert.match(history, /\{unavailable && <Reveal index=\{1\}><Card className="ww-history-recovery-card"><HistoryRecovery rebuildable=\{status\?\.rebuildable === true\}/)
  // The status line is for a history another WWlogs records into; an unavailable one has none.
  assert.match(history, /if \(!status \|\| status\.state !== "read_only"\) return null/)
  // Nor zero counts: the year, the day, the list and the comparison are not drawn for a history that cannot be opened.
  assert.match(history, /\{mode === "calendar" && !comparing && !unavailable && <Reveal index=\{1\}><Card className="ww-history-heat">/)
  assert.match(history, /\{!unavailable && <Reveal index=\{2\}><ContentSwitch /)
  assert.match(history, /\{compared\.length > 0 && !comparing && !unavailable && <div className="ww-compare-tray"/)
  const career = source("CareerView.tsx")
  assert.match(career, /const empty = career !== null && totals\?\.encounters === 0 && !unavailable/)
  assert.match(career, /\{!unavailable && <Reveal index=\{1\}><Card className="ww-history-filters">/)
  assert.match(career, /\{career && totals && !empty && !unavailable && <>/)
  assert.match(career, /<HistoryRecovery rebuildable=\{career\?\.status\.rebuildable === true\}/)
  const settings = source("SettingsView.tsx")
  assert.match(settings, /\{unavailable && <div className="ww-row-block"><HistoryRecovery rebuildable=\{indicator\?\.rebuildable === true\} retry=\{controller\.retryHistory\} rebuild=\{controller\.rebuildHistory\}/)
})

test("the prompt texts exist in every bundled locale and name no cause", () => {
  const manifest = JSON.parse(readFileSync(new URL("../../frontend/locales/manifest.json", import.meta.url), "utf8"))
  for (const locale of manifest.supportedLocales) {
    const texts = JSON.parse(readFileSync(new URL(`../../frontend/locales/${locale}/history.json`, import.meta.url), "utf8"))
    for (const key of ["title", "body", "bodyRetry", "detail", "retry", "retrying", "retryFailed", "rebuild", "confirmTitle", "confirmBody", "cancel", "confirm", "rebuilding", "rebuilt", "rebuildFailed"]) {
      assert.equal(typeof texts.recovery[key], "string", `${locale} recovery.${key}`)
    }
    assert.deepEqual(Object.keys(texts.unavailable).sort(), ["duplicate_source", "locked_by_other_process", "open_failed", "storage_stopped"], locale)
  }
})

// A capture that began while the history could not be used is "not recorded" (unavailable.open_failed): the indicator shows
// it only once the history can be used again (the host ranks an unavailable history first), for example right after a retry
// or a rebuild, so the text speaks of this capture and the next one, never of a history that cannot be opened.
test("a capture that began without its history speaks of that capture, not of the history", () => {
  const read = locale => JSON.parse(readFileSync(new URL(`../../frontend/locales/${locale}/history.json`, import.meta.url), "utf8")).unavailable.open_failed
  assert.match(read("zh-TW"), /這次擷取/)
  assert.match(read("zh-TW"), /下次開始擷取/)
  assert.doesNotMatch(read("zh-TW"), /無法開啟戰鬥歷史/)
  assert.match(read("en"), /this capture/)
  assert.match(read("en"), /next capture/)
  assert.doesNotMatch(read("en"), /could not be opened/)
})

// How the History is stored is WWlogs' own concern: no bundled text and no view tells the user about its encryption, its
// key or the machine it is bound to, and Settings has no storage section for it.
test("no user-visible text names how the History is stored", () => {
  const manifest = JSON.parse(readFileSync(new URL("../../frontend/locales/manifest.json", import.meta.url), "utf8"))
  const forbidden = /加密|解密|金鑰|密钥|密鑰|硬體識別|硬件识别|指紋|指纹|DPAPI|TPM|AES|XTS|HKDF|RSA|HWID|encrypt|decrypt|cipher|fingerprint|key deriv|key mismatch|keys?|暗号|復号|鍵|암호|복호|키|\.key/i
  const strings = (value, path) => typeof value === "string" ? [[path, value]] : Object.entries(value).flatMap(([key, entry]) => strings(entry, `${path}.${key}`))
  for (const locale of manifest.supportedLocales) for (const namespace of manifest.namespaces) {
    const texts = JSON.parse(readFileSync(new URL(`../../frontend/locales/${locale}/${namespace}.json`, import.meta.url), "utf8"))
    for (const [path, text] of strings(texts, `${locale}/${namespace}`)) assert.doesNotMatch(text, forbidden, path)
  }
  const settings = source("SettingsView.tsx")
  assert.doesNotMatch(settings, /historySettings\.sections\.storage|historySettings\.encrypt|history-encryption/)
  for (const view of ["HistoryView.tsx", "CareerView.tsx", "SettingsView.tsx", "HistoryRecovery.tsx", "DashboardView.tsx"]) {
    assert.doesNotMatch(source(view), /\.encrypted|keyProtection|localEncrypted/, view)
  }
})
