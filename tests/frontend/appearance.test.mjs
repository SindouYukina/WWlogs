import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync, readdirSync, statSync } from "node:fs"
import { DatabaseSync } from "node:sqlite"
import { defaultTheme, resolveTheme, themeCharacters, themeIds } from "../../frontend/src/design/themes/registry.ts"
import { flavors, motionVariables, springs } from "../../frontend/src/design/motion/tokens.ts"
import { springPosition } from "../../frontend/src/design/motion/spring.ts"
import { shellChanged, snapshotSupersedes, withShell } from "../../frontend/src/lib/snapshot.ts"

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
  assert.deepEqual(themeCharacters, { electro: 1312, aero: 1409, havoc: 1508, glacio: 1107, fusion: 1209, spectro: 1512 })
  assert.deepEqual([...themeIds], ["electro", "aero", "havoc", "glacio", "fusion", "spectro"], "picker order: Spectro is the sixth card, on the right")
  const art = readdirSync("frontend/src/design/themes/art")
  for (const theme of themeIds) {
    assert.ok(art.includes(`${theme}.webp`), theme)
    assert.ok(art.includes(`${theme}-thumb.webp`), theme)
  }
})

test("every theme sets the same design decisions", () => {
  const required = ["--ww-ink-h", "--ww-ink-c", "--ww-accent-h", "--ww-accent-c", "--ww-accent-2:", "--ww-indicator", "--ww-hero-number", "--ww-glow", "--ww-sheen", "--ww-card-edge", "--ww-card-corner", "--ww-chart-grid", "--ww-theme-ambient", "--ww-selection", "--ww-radius-scale"]
  const blocks = themesCss.split(/\n(?=\/\* [A-Z])/).filter(block => block.includes("--ww-ink-h"))
  assert.equal(blocks.length, themeIds.length)
  for (const theme of themeIds) assert.ok(blocks.some(block => block.includes(`data-ww-theme="${theme}"`)), theme)
  for (const block of blocks) for (const token of required) assert.ok(block.includes(token), `${token} missing in:\n${block.slice(0, 80)}`)
})

test("every token a theme uses exists", () => {
  const defined = new Set()
  for (const file of ["tokens.css", "themes.css"]) {
    for (const match of readFileSync(`frontend/src/design/${file}`, "utf8").matchAll(/(--ww-[a-z0-9-]+)\s*:/g)) defined.add(match[1])
  }
  for (const match of themesCss.matchAll(/var\((--ww-[a-z0-9-]+)/g)) assert.ok(defined.has(match[1]), match[1])
})

test("the ambient backdrop stays free of hard edges and is dithered", () => {
  for (const match of themesCss.matchAll(/--ww-theme-ambient: ([^;]+);/g)) assert.doesNotMatch(match[1], /linear-gradient/, "a theme ambient is soft light, never a drawn line")
  const css = readFileSync("frontend/src/design/components.css", "utf8")
  assert.match(css, /\.ww-backdrop\[data-intensity="ambient"\] \{ background: url\("\.\/themes\/art\/dither\.webp"\)/)
})

test("theme art keeps its dither without growing out of proportion", () => {
  const dir = "frontend/src/design/themes/art"
  for (const theme of themeIds) {
    const art = statSync(`${dir}/${theme}.webp`).size
    // A low-quality encode removes the dither and brings the steps back; these files were 40 KB when they showed them.
    assert.ok(art > 150_000 && art < 500_000, `${theme} art is ${art} bytes`)
    assert.ok(statSync(`${dir}/${theme}-thumb.webp`).size < 40_000, `${theme} thumbnail`)
  }
  assert.ok(statSync(`${dir}/dither.webp`).size < 8_000)
})

test("the theme picker keeps every theme in one row", () => {
  const picker = /\.ww-theme-picker \{[^}]*\}/.exec(readFileSync("frontend/src/design/components.css", "utf8"))[0]
  assert.match(picker, new RegExp(`grid-template-columns: repeat\\(${themeIds.length},`))
  assert.doesNotMatch(picker, /auto-fill|auto-fit/, "a wrapping grid would add a second row")
  const css = readFileSync("frontend/src/design/components.css", "utf8")
  for (const theme of themeIds) assert.ok(css.includes(`[data-theme-preview="${theme}"]`), `${theme} has swatches`)
})

test("a narrow window scrolls the theme row inside itself instead of wrapping or shrinking the names", () => {
  const css = readFileSync("frontend/src/design/components.css", "utf8")
  const picker = /\.ww-theme-picker \{[^}]*\}/.exec(css)[0]
  assert.match(picker, /repeat\(\d+, minmax\(var\(--ww-theme-card-min-width\), 1fr\)\)/, "cards share the width down to a readable minimum")
  assert.match(picker, /overflow-x: auto/, "below that minimum the row scrolls inside the picker, not the page")
  assert.match(picker, /margin: calc\(-1 \* var\(--ww-theme-picker-bleed\)\); padding: var\(--ww-theme-picker-bleed\)/, "the bleed keeps the hover lift and focus ring clear of the scroll clip")
  assert.match(picker, /scroll-padding-inline: var\(--ww-theme-picker-bleed\)/)
  const tokens = readFileSync("frontend/src/design/tokens.css", "utf8")
  const minimum = Number(/--ww-theme-card-min-width: (\d+)px;/.exec(tokens)[1])
  // The widest names measured at this width (Cartethyia, Spectro) need about 53 px of the 66 px text column.
  assert.ok(minimum >= 112 && minimum <= 140, `minimum card width ${minimum}px`)
  assert.match(css, /\.ww-theme-card-text > strong, \.ww-theme-card-text > small \{ overflow: hidden; white-space: nowrap; text-overflow: ellipsis; \}/)
  assert.match(css, /\.ww-theme-card-swatches > i \{ flex: none;/, "the three dots never squeeze together")
  assert.match(css, /\.ww-theme-card\[aria-checked="true"\]:focus-visible \{ box-shadow: inset 0 0 0 2px var\(--ww-indicator\), var\(--ww-focus-ring\); \}/, "the selected card still shows focus")
  const picker_tsx = readFileSync("frontend/src/design/components/appearance.tsx", "utf8")
  assert.match(picker_tsx, /onFocus=\{event => event\.currentTarget\.scrollIntoView\(\{ block: "nearest", inline: "nearest" \}\)\}/, "a card reached with the keyboard scrolls fully into view")
})

test("the Spectro theme leads with a soft gold, keeps clear of the caution yellow and adds Lily's hair as its second note", () => {
  const block = themesCss.slice(themesCss.indexOf('[data-ww-theme="spectro"]'), themesCss.indexOf("::selection"))
  const tokens = readFileSync("frontend/src/design/tokens.css", "utf8")
  const color = (source, name) => {
    const [L, C, h] = new RegExp(`${name}: oklch\\(([0-9.]+) ([0-9.]+) ([0-9.]+)\\)`).exec(source).slice(1).map(Number)
    return { L, C, h, a: C * Math.cos((h * Math.PI) / 180), b: C * Math.sin((h * Math.PI) / 180) }
  }
  const distance = (x, y) => Math.hypot(x.L - y.L, x.a - y.a, x.b - y.b)
  const accent = color(block, "--ww-accent")
  const warn = color(tokens, "--ww-warn")
  assert.ok(accent.h >= 60 && accent.h <= 95, "the lead color is gold")
  assert.ok(accent.C <= 0.13 && accent.C < warn.C, "softer than the caution yellow, never neon")
  assert.ok(distance(accent, warn) >= 0.06, `gold sits apart from the caution yellow (${distance(accent, warn).toFixed(3)})`)
  const hero = color(block, "--ww-hero-number")
  assert.ok(hero.L >= 0.9 && hero.C <= 0.06, "the brightest gold is a pale lily light")
  const second = color(block, "--ww-accent-2")
  assert.ok(second.h >= 300 || second.h <= 20, "the second note is her lavender-pink hair")
  assert.equal(/--ww-indicator: ([^;]+);/.exec(block)[1], "var(--ww-accent)")
  // Gold at the shared accent lightness reads as ochre, so this theme sets every accent step itself.
  for (const step of ["--ww-accent:", "--ww-accent-strong", "--ww-accent-bright", "--ww-accent-soft", "--ww-accent-line", "--ww-on-accent", "--ww-accent-fill-top", "--ww-accent-fill-bottom", "--ww-accent-fill-hover"]) assert.ok(block.includes(step), step)
})

test("every theme and character name comes from the Master in every language", () => {
  const db = new DatabaseSync("data/master_data.db", { readOnly: true })
  try {
    const manifest = JSON.parse(readFileSync("frontend/locales/manifest.json", "utf8").replace(/^\uFEFF/, ""))
    for (const locale of manifest.supportedLocales) {
      const masterLocale = manifest.languages[locale].masterLocale
      const settings = JSON.parse(readFileSync(`frontend/locales/${locale}/settings.json`, "utf8").replace(/^\uFEFF/, ""))
      for (const theme of themeIds) {
        const role = String(themeCharacters[theme])
        const element = db.prepare("select element_id from roleinfo where id = ?").get(role).element_id
        const character = db.prepare("select name from roleinfo_locale where id = ? and locale = ?").get(role, masterLocale).name
        const name = db.prepare("select name from elementinfo_locale where id = ? and locale = ?").get(element, masterLocale).name
        assert.deepEqual(settings.themes[theme], { name, character }, `${locale} ${theme}`)
      }
    }
  } finally {
    db.close()
  }
})

test("the Fusion theme is the character first and the warm note second", () => {
  const block = themesCss.slice(themesCss.indexOf('[data-ww-theme="fusion"]'))
  const hue = name => Number(new RegExp(`${name}: oklch\\([0-9.]+ [0-9.]+ ([0-9.]+)`).exec(block)[1])
  assert.equal(/--ww-accent-h: (\d+)/.exec(block)[1], "284", "the leading color comes from her hair and coat")
  assert.ok(hue("--ww-accent-2") < 80, "the second color is the warm one")
  assert.ok(hue("--ww-indicator") > 270 && hue("--ww-hero-number") > 270, "selection and hero numbers stay cold")
})

test("theme names match the game's own names in every language", () => {
  const expected = { "zh-TW": ["熱熔", "莫寧"], "zh-CN": ["热熔", "莫宁"], en: ["Fusion", "Mornye"], ja: ["焦熱", "モーニエ"], ko: ["용융", "모니에"] }
  for (const [locale, [name, character]] of Object.entries(expected)) {
    const settings = JSON.parse(readFileSync(`frontend/locales/${locale}/settings.json`, "utf8").replace(/^\uFEFF/, ""))
    assert.deepEqual(settings.themes.fusion, { name, character }, locale)
  }
  const spectro = { "zh-TW": ["衍射", "莉莉"], "zh-CN": ["衍射", "莉莉"], en: ["Spectro", "Lily"], ja: ["回折", "リリー"], ko: ["회절", "릴리"] }
  for (const [locale, [name, character]] of Object.entries(spectro)) {
    const settings = JSON.parse(readFileSync(`frontend/locales/${locale}/settings.json`, "utf8").replace(/^\uFEFF/, ""))
    assert.deepEqual(settings.themes.spectro, { name, character }, locale)
  }
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

test("a heartbeat carries a Combat History indicator change, such as switching recording off", () => {
  const history = { state: "waiting", enabled: true, capturing: false, encrypted: true }
  const base = { sequence: 5, localeRevision: 1, theme: "electro", reduceMotion: false, history }
  assert.equal(shellChanged({ ...base, history: { ...history } }, base), false)
  const off = { ...base, history: { ...history, state: "disabled", enabled: false } }
  assert.equal(shellChanged(off, base), true)
  assert.deepEqual(withShell(base, off).history, off.history)
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
