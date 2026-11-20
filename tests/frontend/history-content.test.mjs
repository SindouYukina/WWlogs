import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { contentLayerText, modeText, namedContentLayers, shownContentLayers, stageText } from "../../frontend/src/lib/history.ts"

// How the History views label what Go resolved about an encounter's instance (internal/desktop/history.go): the kind of
// play, the stage name and the content layers. Go decides every state; these helpers only choose the words.

const manifest = JSON.parse(readFileSync(new URL("../../frontend/locales/manifest.json", import.meta.url), "utf8"))
const history = locale => JSON.parse(readFileSync(new URL(`../../frontend/locales/${locale}/history.json`, import.meta.url), "utf8").replace(/^﻿/, ""))

// A minimal t over one locale's history namespace: dotted keys, {{name}} interpolation and defaultValue.
function translator(locale) {
  const resources = history(locale)
  return (key, options = {}) => {
    const value = key.split(".").reduce((node, part) => node?.[part], resources)
    const text = typeof value === "string" ? value : options.defaultValue ?? key
    return text.replace(/{{\s*(\w+)\s*}}/g, (_, name) => String(options[name] ?? ""))
  }
}

const matrix = {
  system: "wheel-tower",
  layers: [
    { dimension: "activity", state: "RUNTIME_DEPENDENT", candidates: 2 },
    { dimension: "cycle", state: "RUNTIME_DEPENDENT", candidates: 9 },
    { dimension: "stage", state: "NO_OFFICIAL_NAME" },
    { dimension: "mode", state: "OFFICIAL", name: "穩態協議", status: "VERIFIED" },
  ],
}

test("a content system without a game name is labelled as a WWlogs classification, in every bundled language", () => {
  for (const locale of manifest.supportedLocales) {
    const t = translator(locale)
    const resources = history(locale)
    for (const system of ["wheel-tower", "boss-rush", "tower-defense", "role-trial"]) {
      const label = modeText({ kind: "content_system", system, status: "VERIFIED" }, t)
      assert.equal(label, resources.contentSystem[system], `${locale} ${system}`)
      assert.ok(label && !label.startsWith("contentSystem"), `${locale} ${system}`)
    }
    // A system the bundled resources do not label yet still reads as other content, never as a raw key.
    assert.equal(modeText({ kind: "content_system", system: "future-system", status: "VERIFIED" }, t), resources.contentSystemOther, locale)
    for (const state of ["NO_OFFICIAL_NAME", "UNRESOLVED", "NAME_MISSING"]) assert.match(resources.contentSystemNote[state], /WWlogs/, `${locale} ${state}`)
    assert.equal(modeText({ kind: "open_world", status: "VERIFIED" }, t), resources.modeOpenWorld)
  }
  const t = translator("zh-TW")
  assert.equal(modeText({ kind: "category", name: "逆境深塔", status: "VERIFIED" }, t), "逆境深塔")
  assert.equal(modeText({ status: "UNKNOWN" }, t), undefined)
  assert.equal(modeText(undefined, t), undefined)
})

test("a WWlogs label never repeats a game's category name", () => {
  // The Adventure Guide names categories such as 討伐強敵 / Boss Challenge and 終焉矩陣 / Endstate Matrix; a WWlogs label
  // that read the same would pass for the game's own name.
  const gameNames = new Set(["討伐強敵", "讨伐强敌", "Boss Challenge", "強敵討伐", "강적 토벌", "終焉矩陣", "终焉矩阵", "Endstate Matrix", "終焉マトリクス", "종말 매트릭스"])
  for (const locale of manifest.supportedLocales) {
    for (const label of Object.values(history(locale).contentSystem)) assert.ok(!gameNames.has(label), `${locale} ${label}`)
  }
})

test("content layers are shown with their dimension; a value the History cannot choose says so", () => {
  const t = translator("zh-TW")
  assert.deepEqual(shownContentLayers(matrix).map(layer => layer.dimension), ["activity", "cycle", "mode"])
  assert.deepEqual(namedContentLayers(matrix).map(layer => layer.dimension), ["mode"])
  assert.equal(contentLayerText(matrix.layers[3], t), "模式：穩態協議")
  assert.equal(contentLayerText(matrix.layers[0], t), "活動：無法確定（2 種可能）")
  assert.equal(contentLayerText(matrix.layers[0], translator("en")), "Event: undetermined (2 possible)")
  assert.deepEqual(shownContentLayers(undefined), [])
  // NO_OFFICIAL_NAME, UNRESOLVED and NAME_MISSING layers name nothing and are not shown.
  const quiet = { system: "role-trial", layers: [{ dimension: "stage", state: "UNRESOLVED" }, { dimension: "role", state: "NAME_MISSING" }, { dimension: "season", state: "NO_OFFICIAL_NAME" }] }
  assert.deepEqual(shownContentLayers(quiet), [])
})

test("the stage is the stage name; without one, the named content layers stand in, each labelled with its dimension", () => {
  const t = translator("zh-TW")
  assert.equal(stageText({ name: "V1-1-4", status: "VERIFIED" }, { system: "tower-of-adversity", layers: [{ dimension: "stage", state: "OFFICIAL", name: "殘響之塔", status: "VERIFIED" }] }, t), "V1-1-4")
  assert.equal(stageText({ status: "UNKNOWN" }, matrix, t), "模式：穩態協議")
  const hologram = { system: "tactical-hologram", layers: [{ dimension: "series", state: "OFFICIAL", name: "全像戰略·演武", status: "VERIFIED" }, { dimension: "stage", state: "RUNTIME_DEPENDENT", candidates: 3 }] }
  assert.equal(stageText({ status: "UNKNOWN" }, hologram, t), "系列：全像戰略·演武")
  const trial = { system: "role-trial", layers: [{ dimension: "stage", state: "UNRESOLVED" }, { dimension: "role", state: "OFFICIAL", name: "莉莉", status: "VERIFIED" }] }
  assert.equal(stageText({ status: "UNKNOWN" }, trial, t), "試用角色：莉莉")
  assert.equal(stageText({ status: "UNKNOWN" }, { system: "boss-rush", layers: [{ dimension: "stage", state: "NO_OFFICIAL_NAME" }] }, t), undefined)
  assert.equal(stageText({ status: "UNKNOWN" }, undefined, t), undefined)
})

test("the History views name modes and stages through the shared components only", () => {
  for (const path of ["HistoryView.tsx", "HistoryLibrary.tsx", "HistoryCompare.tsx", "CareerView.tsx"]) {
    const view = readFileSync(new URL(`../../frontend/src/views/${path}`, import.meta.url), "utf8")
    assert.match(view, /from "\.\/HistoryNames"/, path)
    // A view that chose between the open world and a category name itself would miss content systems.
    assert.doesNotMatch(view, /kind === "open_world" \? t\("modeOpenWorld"\)/, path)
  }
})
