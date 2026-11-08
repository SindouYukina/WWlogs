import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { legendFocus, toggleLegendLock } from "../../frontend/src/design/components/legend-focus.ts"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const chart = read("frontend/src/design/components/LiveChart.tsx")
const css = read("frontend/src/design/components.css")

test("hover previews over the lock, and leaving returns to the lock", () => {
  const keys = ["team", "role-7-1205", "role-7-1409"]
  assert.equal(legendFocus(null, null, keys), null)
  assert.equal(legendFocus("role-7-1205", null, keys), "role-7-1205")
  assert.equal(legendFocus(null, "role-7-1409", keys), "role-7-1409")
  assert.equal(legendFocus("role-7-1205", "role-7-1409", keys), "role-7-1205", "a hover previews another series while one is locked")
  assert.equal(legendFocus("role-7-9999", "role-7-1409", keys), "role-7-1409", "a hover on a series that left the chart falls back to the lock")
  assert.equal(legendFocus(null, "role-7-9999", keys), null, "a lock on a series that left the chart focuses nothing")
})

test("a click locks, the same click releases, another click moves the lock", () => {
  assert.equal(toggleLegendLock(null, "a"), "a")
  assert.equal(toggleLegendLock("a", "a"), null)
  assert.equal(toggleLegendLock("a", "b"), "b")
})

test("the legend keeps colors and identities and resets only with a new scope", () => {
  assert.match(chart, /if \(focusScope !== scope\) \{\n\s+\/\/ A new encounter starts without the previous one's hover or lock\.\n\s+setFocusScope\(scope\)\n\s+setHovered\(null\)\n\s+setLocked\(null\)/)
  assert.match(chart, /stroke=\{s\.color\}/, "series keep the color the caller gives them")
  assert.match(chart, /<button key=\{s\.key\} type="button" className="ww-chart-legend-item" data-key=\{s\.key\} data-focused=\{s\.key === focus\} aria-pressed=\{s\.key === locked\}/)
  assert.match(chart, /if \(event\.key === "Escape"\) setLocked\(null\)/)
  assert.match(read("frontend/src/views/DashboardView.tsx"), /scope=\{timeline\?\.encounterId \?\? ""\}/)
})

test("focus changes no data and adds no animation loop", () => {
  // Paths are memoized on the data only; focus is not a dependency.
  assert.match(chart, /const paths = useMemo\(\(\) => new Map\(series\.map\(s => \[s\.key, linePath\(s\.values, slots, max, height\)\]\)\), \[series, slots, max, height\]\)/)
  assert.doesNotMatch(chart, /requestAnimationFrame|setInterval|setTimeout|\.animate\(/)
  // Focus animates only opacity, on each line's own SVG layer, timed by the motion tokens that reduced motion zeroes; the
  // compositor runs it without repainting a line. The thickening is a thicker copy fading in, not a stroke-width change.
  assert.match(css, /\.ww-chart-line \{[^}]*transition: d var\(--ww-dur-slow\) var\(--ww-ease-standard\); \}/)
  assert.match(css, /\.ww-chart-line-top \{ stroke-width: 3\.25; transition: none; \}/)
  // A hidden copy is not rendered at all; display flips at the ends of the fade, so it fades both ways.
  assert.match(css, /\.ww-chart-layer-top \{ display: none; opacity: 0; transition: opacity var\(--ww-dur-base\) var\(--ww-ease-standard\), display var\(--ww-dur-base\) allow-discrete; \}/)
  assert.match(css, /\.ww-chart-layer-top\[data-shown="true"\] \{ display: block; opacity: 1; \}\n@starting-style \{ \.ww-chart-layer-top\[data-shown="true"\] \{ opacity: 0; \} \}/)
  assert.doesNotMatch(css, /\.ww-chart[^{]*\{[^}]*transition:[^;}]*(?<![-\w])(stroke-width|color)\b/, "no chart transition restyles or repaints per frame")
  assert.match(css, /\.ww-chart-layer \{ pointer-events: none; transition: opacity var\(--ww-dur-base\) var\(--ww-ease-standard\); \}/)
  assert.match(css, /\.ww-chart\[data-focusing="true"\] \.ww-chart-layer\[data-focused="false"\] \{ opacity: 0\.2; \}/)
  assert.match(css, /\.ww-chart\[data-focusing="true"\] \.ww-chart-layer\[data-aggregate="true"\]\[data-focused="false"\] \{ opacity: 0\.42; \}/)
  assert.match(chart, /className="ww-chart-layer" data-key=\{s\.key\}/, "one SVG layer per line")
  // Every line has a thicker copy drawn after all lines; focus only shows or hides it, so nothing mounts or unmounts.
  assert.match(chart, /series\.map\(s => <svg key=\{`top-\$\{s\.key\}`\} className="ww-chart-layer ww-chart-layer-top" data-key=\{s\.key\} data-shown=\{s\.key === focus\}/)
  assert.doesNotMatch(css, /\.ww-chart[^{]*\{[^}]*(filter|drop-shadow|box-shadow: 0 0)/, "no blur or glow on the chart")
})
