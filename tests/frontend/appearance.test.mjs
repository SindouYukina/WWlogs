import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync, readdirSync } from "node:fs"
import { defaultTheme, resolveTheme, themeCharacters, themeIds } from "../../frontend/src/design/themes/registry.ts"
import { flavors, motionVariables, springs } from "../../frontend/src/design/motion/tokens.ts"
import { springPosition } from "../../frontend/src/design/motion/spring.ts"
import { shellChanged, snapshotSupersedes } from "../../frontend/src/lib/snapshot.ts"

const themesCss = readFileSync("frontend/src/design/themes.css", "utf8")

test("electro is the default theme and unknown values fall back to it", () => {
  assert.equal(defaultTheme, "electro")
  assert.equal(resolveTheme(undefined), "electro")
  assert.equal(resolveTheme("from-a-newer-version"), "electro")
  for (const theme of themeIds) assert.equal(resolveTheme(theme), theme)
})

test("the Go settings list and the frontend registry name the same themes", () => {
  const go = readFileSync("internal/desktop/settings.go", "utf8")
  const list = /case ("electro"[^:]*):/.exec(go)[1].match(/"([a-z]+)"/g).map(value => value.replaceAll('"', ""))
  assert.deepEqual(list, [...themeIds])
})

test("each theme maps to its confirmed character and has art", () => {
  assert.deepEqual(themeCharacters, { electro: 1312, aero: 1409, havoc: 1508, glacio: 1107 })
  const art = readdirSync("frontend/src/design/themes/art")
  for (const theme of themeIds) {
    assert.ok(art.includes(`${theme}.webp`), theme)
    assert.ok(art.includes(`${theme}-thumb.webp`), theme)
  }
})

test("every theme sets the same design decisions", () => {
  const required = ["--ww-ink-h", "--ww-ink-c", "--ww-accent-h", "--ww-accent-c", "--ww-accent-2:", "--ww-indicator", "--ww-hero-number", "--ww-glow", "--ww-sheen", "--ww-card-edge", "--ww-card-corner", "--ww-chart-grid", "--ww-theme-ambient", "--ww-selection", "--ww-radius-scale"]
  const blocks = themesCss.split(/\n(?=\/\* [A-Z])/).filter(block => block.includes("--ww-ink-h"))
  assert.equal(blocks.length, 4)
  for (const block of blocks) for (const token of required) assert.ok(block.includes(token), `${token} missing in:\n${block.slice(0, 80)}`)
})

test("themes never redefine status or character series colors", () => {
  for (const token of ["--ww-good", "--ww-warn", "--ww-critical", "--ww-info", "--ww-idle", "--ww-element-"]) assert.ok(!themesCss.includes(`${token}`), token)
})

test("motion flavor changes the feel of interaction but never data motion", () => {
  for (const flavor of themeIds) {
    const variables = motionVariables({ flavor })
    const base = motionVariables({ flavor: "electro" })
    assert.equal(variables["--ww-spring-data"], base["--ww-spring-data"], `${flavor} data spring`)
    assert.equal(variables["--ww-spring-rank"], base["--ww-spring-rank"], `${flavor} rank spring`)
    assert.equal(variables["--ww-spring-data-dur"], base["--ww-spring-data-dur"])
  }
  assert.notEqual(motionVariables({ flavor: "havoc" })["--ww-spring-snappy"], motionVariables({ flavor: "aero" })["--ww-spring-snappy"])
  const peak = params => {
    let max = 0
    for (let t = 0; t < 3; t += 1 / 240) max = Math.max(max, springPosition(params, t))
    return max
  }
  assert.ok(peak(flavors.havoc.springs.lively) < 1.01, "havoc is decisive, with no visible overshoot")
  assert.ok(peak(flavors.aero.springs.lively) > peak(springs.gentle), "aero keeps its float")
})

test("reduced motion still zeroes every token under every flavor", () => {
  for (const flavor of themeIds) {
    const variables = motionVariables({ flavor, preference: "reduced" })
    for (const [name, value] of Object.entries(variables)) {
      if (name.includes("-dur") || name === "--ww-stagger") assert.equal(value, "0ms", `${flavor} ${name}`)
    }
  }
})

test("a heartbeat updates listening and theme without moving core sequence rules", () => {
  const base = { sequence: 5, localeRevision: 1, listening: { state: "active", reason: "receiving", since: "t0" }, theme: "electro", reduceMotion: false }
  assert.equal(snapshotSupersedes(base, base), false)
  assert.equal(shellChanged(base, base), false)
  assert.equal(shellChanged({ ...base, listening: { state: "stale", reason: "no_recent_frames", since: "t1" } }, base), true)
  assert.equal(shellChanged({ ...base, theme: "aero" }, base), true)
  assert.equal(shellChanged({ ...base, reduceMotion: true }, base), true)
  assert.equal(shellChanged({ ...base, listening: { ...base.listening, since: "t9" } }, base), false, "the timestamp alone causes no render")
})

test("translations name every listening state, reason and theme in every language", () => {
  const listening = readFileSync("internal/service/listening.go", "utf8")
  const reasons = [...listening.matchAll(/ListeningReason = "([a-z_]+)"/g)].map(match => match[1])
  const states = [...listening.matchAll(/ListeningState = "([a-z]+)"/g)].map(match => match[1])
  assert.ok(reasons.length >= 15 && states.length === 7)
  for (const locale of ["en", "zh-TW", "zh-CN", "ja", "ko"]) {
    const common = JSON.parse(readFileSync(`frontend/locales/${locale}/common.json`, "utf8").replace(/^﻿/, ""))
    const settings = JSON.parse(readFileSync(`frontend/locales/${locale}/settings.json`, "utf8").replace(/^﻿/, ""))
    for (const reason of reasons) assert.ok(common.listening.reason[reason], `${locale} ${reason}`)
    for (const state of states) assert.ok(common.listening.state[state], `${locale} ${state}`)
    for (const theme of themeIds) assert.ok(settings.themes[theme].name && settings.themes[theme].character, `${locale} ${theme}`)
  }
})

test("no font files or typography rules reach for a network or a developer path", () => {
  const css = readFileSync("frontend/src/design/typography.css", "utf8")
  assert.doesNotMatch(css, /https?:|fonts\.googleapis|local\(/)
  const faces = [...css.matchAll(/url\("\.\/fonts\/([^"]+)"\)/g)].map(match => match[1])
  const files = readdirSync("frontend/src/design/fonts")
  assert.ok(faces.length >= 5)
  for (const face of faces) assert.ok(files.includes(face), face)
  assert.doesNotMatch(css.replaceAll(/\/\*[\s\S]*?\*\//g, ""), /font-smoothing|filter:/, "no ineffective smoothing or blurring text filters")
})
