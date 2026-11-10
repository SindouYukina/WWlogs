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
  // The main value is the server-confirmed healing magnitude; the note is the HP actually restored. The frontend only reads both.
  assert.match(view, /<StatCard index=\{3\} icon="heal" label=\{t\("healing"\)\} field="healing" status=\{healing\?\.status \?\? "unavailable"\} value=\{healingShown \? healing\.reported : undefined\}/)
  assert.match(view, /note=\{healingShown \? t\("healingNote", \{ value: healingFormat\(healing\.effective\) \}\) : t\("healingUnavailable"\)\}/)
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
    assert.match(strings.healingNote, /\{\{value\}\}/, `${locale} healingNote is the restored HP value`)
    assert.notEqual(strings.healing, strings.healingNote.replace("{{value}}", "").trim(), `${locale}: the two healing figures have different names`)
    for (const key of ["healing", "healingNote", "healingUnavailable"]) assert.ok(strings[key], `${locale} ${key}`)
    for (const key of ["rollingNote", "eventsLabel", "eventsNote"]) assert.equal(strings[key], undefined, `${locale} ${key} is no longer used`)
  }
})

test("the ranking notes read the engine's facts for the scope on screen and never merge them", () => {
  const overlay = read("frontend/src/views/CombatOverlayPanel.tsx")
  for (const source of [view, overlay]) {
    assert.match(source, /const completeness = \(encounter \?\? view\)\?\.completeness/, "the facts of the encounter on screen, else the interval")
    assert.doesNotMatch(source, /statistics\.unattributed\.outgoingEvents/, "unknown ownership is not a gap in role attribution")
    assert.doesNotMatch(source, /unassignedRole\.outgoingEvents|unassigned\?\.outgoingEvents/, "the engine decides attribution")
    assert.match(source, /completeness\?\.roleAttributionComplete === false/)
    assert.match(source, /unknownOwnershipNote/)
  }
  assert.match(view, /data-field="attribution-incomplete"/)
  assert.match(view, /data-field="unknown-ownership-note"/)
})

test("the ranking is the engine's: rows arrive ordered and rated, and the views draw them as given", () => {
  const adapter = read("frontend/src/lib/combat-view.ts")
  assert.doesNotMatch(adapter, /localeCompare|usableDuration|intervalRows|\.damage - |damageDealt \/|\/ duration/, "no ranking or rate is worked out in the adapter")
  assert.match(adapter, /dps: role\.dps/)
  assert.match(adapter, /rank: role\.rank/)
  assert.match(adapter, /dps: vehicle\.dps/)
  assert.match(adapter, /rank: vehicle\.rank/)
  assert.doesNotMatch(read("frontend/src/lib/snapshot.ts"), /damageContributors/, "the unused second ranking is gone")
  const overlay = read("frontend/src/views/CombatOverlayPanel.tsx")
  const panel = read("frontend/src/design/components/combat.tsx")
  for (const source of [view, overlay]) assert.match(source, /const scope = encounter \?\? view; return scope \? combatRows\(scope\) : \[\]/, "one scope gives every number of the board")
  assert.doesNotMatch(panel, /sort\(\(a, b\) => a\.rank/, "the Overlay draws the order it is given")
  assert.doesNotMatch(view, /sort\(\(a, b\) => a\.rank/, "the Dashboard draws the order it is given")
})

test("every bundled language says unknown ownership is outside the total", () => {
  const manifest = JSON.parse(read("frontend/locales/manifest.json"))
  for (const locale of manifest.supportedLocales) {
    const common = JSON.parse(read(`frontend/locales/${locale}/common.json`))
    assert.match(common.unknownOwnershipNote, /\{\{value\}\}/, locale)
    assert.ok(common.attributionIncomplete.length > 0, locale)
  }
})

test("the four summary cards carry the encounter header's scope, so they do not repeat it", () => {
  const copy = {
    "zh-TW": { dps: "DPS", damage: "總傷害", damageNote: "已確認的輸出傷害", taken: "承受傷害", takenNote: "包含護盾吸收", peak: "峰值 DPS：{{value}}", healing: "治療量", healingNote: "實際恢復生命值：{{value}}" },
  }
  for (const locale of Object.keys(copy)) {
    const dashboard = JSON.parse(read(`frontend/locales/${locale}/dashboard.json`))
    const common = JSON.parse(read(`frontend/locales/${locale}/common.json`))
    assert.equal(common.encounterDps, copy[locale].dps)
    assert.equal(dashboard.peakNote, copy[locale].peak)
    assert.equal(dashboard.confirmedDamage, copy[locale].damage)
    assert.equal(dashboard.damageNote, copy[locale].damageNote)
    assert.equal(dashboard.damageTaken, copy[locale].taken)
    assert.equal(dashboard.takenNote, copy[locale].takenNote)
    assert.equal(dashboard.healing, copy[locale].healing)
    assert.equal(dashboard.healingNote, copy[locale].healingNote)
  }
  for (const locale of ["en", "ja", "ko", "zh-CN", "zh-TW"]) {
    const dashboard = JSON.parse(read(`frontend/locales/${locale}/dashboard.json`))
    const common = JSON.parse(read(`frontend/locales/${locale}/common.json`))
    const shown = [common.encounterDps, dashboard.peakNote, dashboard.confirmedDamage, dashboard.damageNote, dashboard.damageTaken, dashboard.takenNote, dashboard.healing, dashboard.healingNote]
    for (const text of shown) assert.doesNotMatch(text, /本場|本机|本機|本场|Local\b|local\b|この戦闘|ローカル|이번 전투|로컬|in this encounter/, `${locale}: ${text}`)
  }
})

test("the three smaller summary numbers share the DPS number's centre line and the DPS card keeps its own layout", () => {
  const css = read("frontend/src/design/views.css")
  assert.match(css, /\.ww-dash-stat-wrap:not\(\.ww-dash-stat-hero\) \.ww-dash-stat-value \{ display: flex; align-items: center; min-height: calc\(var\(--ww-text-hero\) \* 1\.05\); \}/)
  assert.match(css, /\.ww-dash-stat-hero \.ww-dash-stat-value \{ font-size: var\(--ww-text-hero\); line-height: 1\.05;/, "the reserved line is exactly the DPS number's line")
})

test("a skill row without an engine name keeps the unconfirmed label, and a named row never shows an id for it", () => {
  const row = view.slice(view.indexOf('data-field="unassigned-skill"'), view.indexOf("</tbody>"))
  assert.match(row, /\{unassigned\.name \?\? t\("unassignedSkill"\)\}/, "the fallback label remains for a row without a reliable name")
  assert.doesNotMatch(row, /skillId|damageId|DamageId/, "a damage id is never shown as if it were a skill id")
  assert.match(row, /derivedUnassignedNoteKey\[unassigned\.derived\]/, "a derived name carries the evidence note")
})
