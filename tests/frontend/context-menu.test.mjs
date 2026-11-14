import assert from "node:assert/strict"
import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"
import test from "node:test"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")
const sources = dir => readdirSync(dir).flatMap(name => {
  const path = join(dir, name)
  return statSync(path).isDirectory() ? sources(path) : /\.(tsx?|css)$/.test(name) ? [path] : []
})

test("no window shows the WebView2 context menu, through one listener in the entry every window loads", () => {
  const main = read("frontend/src/main.tsx")
  // A top-level statement, under no profile or Showcase condition: the Desktop, each Overlay and the Development Showcase
  // alike, in both profiles, installed before React mounts anything.
  assert.match(main, /^for \(const eventType of surfaceWindow \? \["contextmenu", "dragstart"\] : \["contextmenu"\]\) \{\n  window\.addEventListener\(eventType, event => event\.preventDefault\(\), \{ capture: true \}\)\n\}$/m)
  assert.ok(main.indexOf("window.addEventListener(eventType") < main.indexOf("createRoot("), "installed before the first render")
  // Nowhere else: no page, portal or component handles the context menu on its own.
  for (const path of sources("frontend/src")) {
    if (path.endsWith("main.tsx")) continue
    assert.doesNotMatch(read(path), /contextmenu/i, path)
  }
})
