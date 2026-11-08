import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { commitNumberEntry, previewNumberEntry, readNumberEntry, stepNumber } from "../../frontend/src/lib/number-entry.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const min = 75
const max = 200

test("typed and pasted text is read as a whole number or rejected", () => {
  assert.deepEqual(readNumberEntry(""), { kind: "empty" })
  assert.deepEqual(readNumberEntry("   "), { kind: "empty" })
  assert.deepEqual(readNumberEntry("150"), { kind: "number", value: 150 })
  assert.deepEqual(readNumberEntry(" 150 "), { kind: "number", value: 150 }, "a paste with spaces around it")
  assert.deepEqual(readNumberEntry("150%"), { kind: "number", value: 150 }, "a pasted percent sign")
  assert.deepEqual(readNumberEntry("１５０％"), { kind: "number", value: 150 }, "full-width digits from an IME")
  assert.deepEqual(readNumberEntry("125.4"), { kind: "number", value: 125 })
  assert.deepEqual(readNumberEntry("125.5"), { kind: "number", value: 126 })
  assert.deepEqual(readNumberEntry("125."), { kind: "number", value: 125 })
  assert.deepEqual(readNumberEntry("-20"), { kind: "number", value: -20 })
  assert.equal(Object.is(readNumberEntry("-0.4").value, 0), true, "no negative zero")
  for (const text of ["abc", "1e2", "12a", "-", ".", "1,5", "0x80", "1 5 0", "150%%"]) assert.deepEqual(readNumberEntry(text), { kind: "invalid" }, text)
})

test("only a complete number within the range previews while typing", () => {
  // Typing 150 passes through 1 and 15, which must not move the window.
  assert.equal(previewNumberEntry("1", min, max), undefined)
  assert.equal(previewNumberEntry("15", min, max), undefined)
  assert.equal(previewNumberEntry("150", min, max), 150)
  assert.equal(previewNumberEntry("75", min, max), 75)
  assert.equal(previewNumberEntry("200", min, max), 200)
  assert.equal(previewNumberEntry("74", min, max), undefined)
  assert.equal(previewNumberEntry("201", min, max), undefined)
  assert.equal(previewNumberEntry("200.6", min, max), undefined, "a decimal that rounds out of range")
  assert.equal(previewNumberEntry("-80", min, max), undefined)
  assert.equal(previewNumberEntry("", min, max), undefined)
  assert.equal(previewNumberEntry("x", min, max), undefined)
})

test("Enter or leaving the field clamps a number and keeps the earlier value for anything else", () => {
  assert.equal(commitNumberEntry("137", 100, min, max), 137, "1% precision")
  assert.equal(commitNumberEntry("40", 100, min, max), 75)
  assert.equal(commitNumberEntry("-5", 100, min, max), 75)
  assert.equal(commitNumberEntry("999", 100, min, max), 200)
  assert.equal(commitNumberEntry("199.6", 100, min, max), 200)
  assert.equal(commitNumberEntry("", 120, min, max), 120)
  assert.equal(commitNumberEntry("abc", 120, min, max), 120)
})

test("the step buttons move to the next multiple of the step and stop at the range", () => {
  assert.equal(stepNumber(137, 5, 1, min, max), 140)
  assert.equal(stepNumber(137, 5, -1, min, max), 135)
  assert.equal(stepNumber(140, 5, 1, min, max), 145)
  assert.equal(stepNumber(140, 5, -1, min, max), 135)
  assert.equal(stepNumber(200, 5, 1, min, max), 200)
  assert.equal(stepNumber(75, 5, -1, min, max), 75)
  assert.equal(stepNumber(76, 5, -1, min, max), 75)
})

test("the number field keeps unfinished text, restores on Escape and never rewrites while typing", () => {
  const source = read("frontend/src/design/components/controls.tsx")
  const field = source.slice(source.indexOf("export function NumberField"), source.indexOf("export interface TooltipProps"))
  assert.match(field, /value=\{text \?\? String\(value\)\}/, "typed text wins over the current value")
  assert.match(field, /onBlur=\{commit\}/)
  assert.match(field, /event\.key === "Enter"/)
  assert.match(field, /event\.key === "Escape"/)
  assert.match(field, /inputMode="numeric"/)
  assert.doesNotMatch(field, /type="number"/, "a number input clears unfinished or pasted text it cannot parse")
})

test("both Overlays offer the same live scale control and the surface zooms as a whole", () => {
  const settings = read("frontend/src/views/SettingsView.tsx")
  assert.match(settings, /<Slider label=\{t\("scale"\)\} min=\{minOverlayScale\} max=\{maxOverlayScale\} step=\{5\} value=\{scale\} onChange=\{setScale\} \/>/)
  assert.match(settings, /<NumberField value=\{scale\} unit="%" step=\{5\} min=\{minOverlayScale\} max=\{maxOverlayScale\} onChange=\{setScale\}/)
  assert.match(settings, /const minOverlayScale = 75\nconst maxOverlayScale = 200\n/)
  const views = read("frontend/src/design/views.css")
  assert.match(views, /\.ww-overlay-surface \{[^}]*zoom: var\(--ww-overlay-scale, 1\); height: calc\(100vh \/ var\(--ww-overlay-scale, 1\)\);/)
  const panel = read("frontend/src/views/EffectsOverlayPanel.tsx")
  assert.match(panel, /const zoom = panel\.currentCSSZoom \|\| 1/, "the effect grid is measured in the panel's own pixels")
})
