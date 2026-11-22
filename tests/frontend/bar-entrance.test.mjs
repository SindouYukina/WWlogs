import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { barEntrance, motionVariables } from "../../frontend/src/design/motion/tokens.ts"

const source = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8")

test("the bar entrance is short, decelerating and staggered, and the same in every theme", () => {
  assert.ok(barEntrance.durationMs >= 550 && barEntrance.durationMs <= 750, `${barEntrance.durationMs} ms`)
  assert.ok(barEntrance.staggerMs >= 40 && barEntrance.staggerMs <= 60, `${barEntrance.staggerMs} ms`)
  // An ease-out cubic: fast start, long soft landing, and no control point that could overshoot the bar's value.
  assert.equal(barEntrance.easing, "cubic-bezier(0.33, 1, 0.68, 1)")
  // A long skill list starts its later rows with the last step, so the whole entrance stays short.
  assert.ok(barEntrance.durationMs + barEntrance.maxSteps * barEntrance.staggerMs <= 1200)
  const full = motionVariables()
  assert.equal(full["--ww-bar-enter-dur"], "650ms")
  assert.equal(full["--ww-bar-enter-stagger"], "50ms")
  assert.equal(full["--ww-ease-bar-enter"], barEntrance.easing)
  for (const flavor of ["electro", "aero", "havoc", "glacio", "fusion", "spectro"]) {
    assert.equal(motionVariables({ flavor })["--ww-bar-enter-dur"], "650ms", flavor)
  }
})

test("reduced motion makes the entrance instant and review speed slows it with everything else", () => {
  const reduced = motionVariables({ preference: "reduced" })
  assert.equal(reduced["--ww-bar-enter-dur"], "0ms")
  assert.equal(reduced["--ww-bar-enter-stagger"], "0ms")
  assert.equal(motionVariables({ scale: 2 })["--ww-bar-enter-dur"], "1300ms")
  assert.match(source("frontend/src/design/components.css"), /:root\[data-ww-motion="reduced"\] \.ww-bar\[data-enter="true"\] > \.ww-bar-trail,\r?\n:root\[data-ww-motion="reduced"\] \.ww-bar\[data-enter="true"\] > \.ww-bar-fill,\r?\n:root\[data-ww-motion="reduced"\] \.ww-bar\[data-enter="true"\] > \.ww-bar-edge \{ animation: none; \}/)
})

test("the entrance grows each layer left to right with transforms and holds nothing afterwards", () => {
  const css = source("frontend/src/design/components.css")
  assert.ok(css.includes("@keyframes ww-bar-grow { from { transform: scaleX(0); } }"))
  assert.ok(css.includes("@keyframes ww-bar-edge-grow { from { transform: translateX(0); } }"))
  assert.ok(css.includes(".ww-bar-trail, .ww-bar-fill { position: absolute; inset: 0; transform-origin: left center; transform: scaleX(var(--ww-share)); }"))
  const rules = css.split("\n").filter(line => line.includes('.ww-bar[data-enter="true"]') && line.includes("animation:") && !line.includes("animation: none"))
  assert.equal(rules.length, 2)
  for (const rule of rules) {
    // Backwards fill holds zero only through the stagger delay; a transform held afterwards would block later transitions.
    assert.match(rule, /var\(--ww-bar-enter-dur\) var\(--ww-ease-bar-enter\) backwards;/)
    assert.doesNotMatch(rule, /\bboth\b|\bforwards\b|infinite/)
    assert.match(rule, /animation-delay: calc\(var\(--ww-bar-index, 0\) \* var\(--ww-bar-enter-stagger\)\)/)
  }
})

test("a bar decides its entrance when it mounts and plays it to the end", () => {
  const bar = source("frontend/src/design/components/combat.tsx")
  // Latched at mount, capped, and never under reduced motion.
  assert.match(bar, /useState\(\(\) => enter === undefined \|\| motionReduced\(\) \? undefined : Math\.min\(Math\.max\(0, enter\), barEntrance\.maxSteps\)\)/)
  assert.match(bar, /data-enter=\{entrance !== undefined \|\| undefined\}/)
  // The attribute goes only when the grow animation ends or is cancelled, never on a timer.
  assert.match(bar, /addEventListener\("animationend", done\)/)
  assert.match(bar, /addEventListener\("animationcancel", done\)/)
  assert.match(bar, /event\.animationName === "ww-bar-grow"/)
  const hook = source("frontend/src/design/motion/useEntrance.ts")
  // The window closes right after the first commit with ready content; the bars mounted in it already hold their entrance.
  assert.match(hook, /if \(ready\) setOpen\(current => current === key \? null : current\)/)
  assert.doesNotMatch(hook, /setTimeout/)
})

test("only History opts into the entrance; the live Dashboard and Overlay bars appear at their value", () => {
  const bar = source("frontend/src/design/components/combat.tsx")
  const dashboard = source("frontend/src/views/DashboardView.tsx")
  // The Dashboard view itself renders the board and the table without enter.
  const live = dashboard.slice(dashboard.indexOf("export function DashboardView"))
  assert.match(live, /<RoleBoard rows=\{rows\}[^>]*numberMode=\{numberMode\} \/>/)
  assert.match(live, /<SkillTable breakdown=\{skills\} color=\{elementColor\(selected\?\.elementId\)\} \/>/)
  // The Overlay meter rows draw their bar inside CharacterDamageRow, which never plays the entrance.
  assert.match(bar, /<DamageBar share=\{row\.share\} fill="row" scope=\{scope\} \/>/)
  const analysis = source("frontend/src/views/HistoryAnalysis.tsx")
  assert.match(analysis, /numberMode="ease" enter=\{entering\} \/>/)
  assert.match(analysis, /enter=\{entering \? index : undefined\}/)
  // The entrance plays once per opened encounter, not on every tab switch, hover or filter change.
  assert.match(analysis, /const entering = useEntrance\(id, analysis\?\.state === "ready"\)/)
  const checkpoint = source("frontend/src/views/HistoryView.tsx")
  assert.match(checkpoint, /const entering = useEntrance\(summary\.id, true\)/)
  assert.equal(checkpoint.match(/enter=\{entering\}/g)?.length, 2)
  assert.doesNotMatch(checkpoint, /\benter \/>/)
})
