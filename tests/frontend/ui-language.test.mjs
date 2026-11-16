import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import i18next from "../../frontend/node_modules/i18next/dist/esm/i18next.js"
import { applyUiLocale, snapshotLocale } from "../../frontend/src/i18n/ui-language.ts"

// Each snapshot carries the effective UI locale the host resolved (the owner's choice or the automatic language), and every
// window applies it to its own i18next instance (Phase 3A R06). These tests run the bundled i18next with the options of
// src/i18n/index.ts and every bundled resource.

const readJSON = path => JSON.parse(readFileSync(path, "utf8").replace(/^﻿/, ""))
const manifest = readJSON("frontend/locales/manifest.json")
const resources = Object.fromEntries(manifest.supportedLocales.map(locale => [locale,
  Object.fromEntries(manifest.namespaces.map(ns => [ns, readJSON(`frontend/locales/${locale}/${ns}.json`)]))]))

// One window: its i18next instance, its document root and the number of languageChanged announcements it received.
function openWindow(bootLocale) {
  const instance = i18next.createInstance()
  instance.init({ resources, lng: bootLocale, fallbackLng: manifest.fallbackLocale, supportedLngs: manifest.supportedLocales,
    load: "currentOnly", ns: manifest.namespaces, defaultNS: "common", initAsync: false, interpolation: { escapeValue: false } })
  const window = { instance, announcements: 0, langWrites: 0, lang: bootLocale }
  window.root = { get lang() { return window.lang }, set lang(value) { window.langWrites++; window.lang = value } }
  instance.on("languageChanged", () => window.announcements++)
  return window
}

const accept = (window, locale) => applyUiLocale(window.instance, window.root, snapshotLocale(locale, manifest.supportedLocales, manifest.fallbackLocale))

test("i18next announces a change to the language it already has, the cost the guard avoids", async () => {
  const window = openWindow("zh-TW")
  await window.instance.changeLanguage("zh-TW")
  assert.equal(window.announcements, 1)
})

test("a window changes its language only when a snapshot's locale differs from it", () => {
  const window = openWindow("zh-TW")
  const changes = ["zh-TW", "zh-TW", "zh-TW", "en", "en", "ja", "ja", "ja", "zh-TW"].map(locale => accept(window, locale))
  assert.deepEqual(changes, [false, false, false, true, false, true, false, false, true])
  assert.equal(window.announcements, 3)
  assert.equal(window.instance.language, "zh-TW")
  assert.equal(window.lang, "zh-TW")
  assert.equal(window.langWrites, 3, "the document language is written only when it changes")
})

test("a changed locale takes effect at once, in the same call, for every namespace", () => {
  const window = openWindow("zh-TW")
  assert.ok(accept(window, "en"))
  assert.equal(window.instance.language, "en")
  assert.equal(window.instance.t("analysis.effects.truncated", { ns: "history" }), resources.en.history.analysis.effects.truncated)
  assert.ok(accept(window, "ko"))
  assert.equal(window.instance.t("analysis.effects.truncated", { ns: "history" }), resources.ko.history.analysis.effects.truncated)
})

test("windows that started in different languages each follow the snapshots on their own", () => {
  const named = openWindow("ja") // opened with the saved language in its URL
  const plain = openWindow(manifest.fallbackLocale) // opened without one, so on the fallback
  for (const locale of ["ja", "ja", "zh-CN", "zh-CN"]) {
    accept(named, locale)
    accept(plain, locale)
  }
  assert.deepEqual([named.announcements, plain.announcements], [1, 2])
  assert.deepEqual([named.instance.language, plain.instance.language], ["zh-CN", "zh-CN"])
})

test("a locale that is not bundled shows the fallback", () => {
  assert.equal(snapshotLocale("fr", manifest.supportedLocales, manifest.fallbackLocale), manifest.fallbackLocale)
  assert.equal(snapshotLocale("zh-CN", manifest.supportedLocales, manifest.fallbackLocale), "zh-CN")
  const window = openWindow("ja")
  assert.ok(accept(window, "fr"))
  assert.equal(window.instance.language, manifest.fallbackLocale)
  assert.ok(!accept(window, "fr"))
})

test("the snapshot hook applies the locale through the guard only", () => {
  const source = readFileSync("frontend/src/lib/useSnapshot.ts", "utf8")
  assert.match(source, /applyUiLocale\(i18n,document\.documentElement,snapshotLocale\(next\.locale,manifest\.supportedLocales,manifest\.fallbackLocale\)\)/)
  assert.doesNotMatch(source, /changeLanguage\(|documentElement\.lang\s*=/)
})
