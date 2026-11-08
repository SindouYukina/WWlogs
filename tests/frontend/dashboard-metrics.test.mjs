import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { usableHealing } from "../../frontend/src/lib/snapshot.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const view = read("frontend/src/views/DashboardView.tsx")

test("the first card shows the encounter's peak 1-second DPS instead of the rolling 5 s and 10 s DPS", () => {
  assert.match(view, /note=\{view \? t\("peakNote", \{ value: peakText \}\) : undefined\}/)
  assert.match(view, /const peak = encounter\?\.peakDps/)
  assert.doesNotMatch(view, /rolling5sDps|rolling10sDps|rollingNote/)
})

test("the fourth card is the local healing, never the event count", () => {
  assert.match(view, /<StatCard index=\{3\} icon="heal" label=\{t\("healing"\)\} field="healing" status=\{healing\?\.status \?\? "unavailable"\} value=\{healingShown \? healing\.effective : undefined\}/)
  assert.match(view, /note=\{healingShown \? t\(encounter \? "healingNote" : "interval"\) : t\("healingUnavailable"\)\}/)
  assert.doesNotMatch(view, /eventsLabel|eventsNote|field="events"/)
  // Four summary cards, as before.
  assert.equal(view.match(/<StatCard index=\{\d\}/g).length, 4)
})

test("healing is shown only when it can be trusted, and unavailable data is never shown as zero", () => {
  assert.equal(usableHealing({ status: "available", effective: 0 }), true, "no healing observed in a complete encounter is a real zero")
  assert.equal(usableHealing({ status: "lower_bound", effective: 10 }), true)
  for (const status of ["unreliable", "unavailable", "invalid_duration"]) assert.equal(usableHealing({ status, effective: 10 }), false, status)
  assert.equal(usableHealing(undefined), false)
})

test("every bundled language names the peak and the healing card", () => {
  for (const locale of ["en", "ja", "ko", "zh-CN", "zh-TW"]) {
    const strings = JSON.parse(read(`frontend/locales/${locale}/dashboard.json`))
    assert.match(strings.peakNote, /\{\{value\}\}/, locale)
    for (const key of ["healing", "healingNote", "healingUnavailable"]) assert.ok(strings[key], `${locale} ${key}`)
    for (const key of ["rollingNote", "eventsLabel", "eventsNote"]) assert.equal(strings[key], undefined, `${locale} ${key} is no longer used`)
  }
})
