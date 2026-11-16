import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync, readdirSync } from "node:fs"
import { join, relative } from "node:path"
import { fileURLToPath } from "node:url"

// Every literal translation key of the frontend source exists in the English resources of a namespace its file uses
// (Phase 3A R07, R09). validate.mjs keeps every language identical to English, so a key missing from English is missing
// from every language, and the UI would show the raw key. Keys built at run time (t(variable), template literals) are not
// checked here.

const frontend = fileURLToPath(new URL("../../frontend/", import.meta.url))
const readJSON = path => JSON.parse(readFileSync(path, "utf8").replace(/^﻿/, ""))
const manifest = readJSON(join(frontend, "locales", "manifest.json"))

function flatten(value, prefix = "", result = new Set()) {
  for (const [key, entry] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (entry && typeof entry === "object") flatten(entry, path, result)
    else result.add(path)
  }
  return result
}

// English resources by namespace: the bundled namespaces and the Development showcase's own one.
const resources = new Map(manifest.namespaces.map(ns => [ns, flatten(readJSON(join(frontend, "locales", "en", `${ns}.json`)))]))
resources.set("showcase", flatten(readJSON(join(frontend, "src", "showcase", "locales", "en.json"))))

const pluralSuffixes = ["zero", "one", "two", "few", "many", "other"]
// A key resolves when it names a string, one of its plural forms, or an object read whole (returnObjects).
function resolves(keys, key) {
  if (keys.has(key) || pluralSuffixes.some(suffix => keys.has(`${key}_${suffix}`))) return true
  for (const candidate of keys) if (candidate.startsWith(`${key}.`)) return true
  return false
}

function sourceFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return sourceFiles(path)
    return /\.(ts|tsx)$/.test(entry.name) && !entry.name.endsWith(".d.ts") ? [path] : []
  })
}

// The namespaces of a file: those of its useTranslation calls (none named: the default "common"); a file without one
// (a helper that receives t) may use any namespace.
function namespacesOf(source) {
  const found = new Set()
  for (const match of source.matchAll(/useTranslation\(([^)]*)\)/g)) {
    const named = [...match[1].matchAll(/"([\w-]+)"/g)].map(m => m[1])
    for (const ns of named.length ? named : ["common"]) found.add(ns)
  }
  if (found.size === 0) return [...resources.keys()]
  if (/\bi18n\.t\(/.test(source)) found.add("common") // the default namespace
  return [...found]
}

// Literal keys: t("key"), t("key", { ns: "x" }), t("ns:key") and i18n.t("key").
function literalKeys(source) {
  const keys = []
  for (const match of source.matchAll(/\bt\(\s*"([^"]+)"\s*(?:,\s*\{([^}]*)\})?/g)) {
    const option = match[2] && /\bns:\s*"([\w-]+)"/.exec(match[2])
    keys.push({ key: match[1], ns: option ? option[1] : undefined })
  }
  return keys
}

function missingKeys() {
  const missing = []
  let checked = 0
  for (const path of sourceFiles(join(frontend, "src"))) {
    const source = readFileSync(path, "utf8")
    const namespaces = namespacesOf(source)
    for (const { key, ns } of literalKeys(source)) {
      checked++
      const prefixed = /^([\w-]+):(.+)$/.exec(key)
      const [candidates, bare] = prefixed && resources.has(prefixed[1]) ? [[prefixed[1]], prefixed[2]] : [ns ? [ns] : namespaces, key]
      if (!candidates.some(name => resources.has(name) && resolves(resources.get(name), bare))) {
        missing.push({ entry: `${relative(frontend, path).replaceAll("\\", "/")}: ${key}`, namespaces: candidates.join(", ") })
      }
    }
  }
  return { missing, checked }
}

// Keys known to be missing ("file: key"), each with the change that adds it. An entry that resolves must be removed.
const knownMissing = new Map([
  ["src/views/HistoryAnalysis.tsx: analysis.effects.notStored", "Phase 3A R07: added by the v0.8.0 frontend targeted cleanup"],
])

test("every literal translation key of the frontend exists in English", () => {
  const { missing, checked } = missingKeys()
  assert.ok(checked > 500, `read ${checked} literal keys; the reader no longer finds the frontend's t() calls`)
  const unexpected = [...new Set(missing.filter(m => !knownMissing.has(m.entry)).map(m => `${m.entry} (${m.namespaces})`))].sort()
  assert.deepEqual(unexpected, [], `translation keys missing from the English resources:\n${unexpected.join("\n")}`)
  const fixed = [...knownMissing.keys()].filter(entry => !missing.some(m => m.entry === entry))
  assert.deepEqual(fixed, [], `listed as missing but resolved now; remove from knownMissing:\n${fixed.join("\n")}`)
})

test("the key reader follows namespaces, prefixes, plurals and whole objects", () => {
  assert.deepEqual(literalKeys(`t("a.b") t("c", { ns: "history" }) t(\`x\${y}\`) t(key) i18n.t("unknown")`), [
    { key: "a.b", ns: undefined }, { key: "c", ns: "history" }, { key: "unknown", ns: undefined },
  ])
  assert.deepEqual(namespacesOf(`useTranslation(["history", "common"])`), ["history", "common"])
  assert.deepEqual(namespacesOf(`useTranslation()`), ["common"])
  assert.deepEqual(namespacesOf(`function helper(t) {}`), [...resources.keys()])
  const keys = new Set(["items_one", "items_other", "group.inner", "plain"])
  assert.ok(resolves(keys, "items") && resolves(keys, "group") && resolves(keys, "plain"))
  assert.ok(!resolves(keys, "missing") && !resolves(keys, "gro"))
})
