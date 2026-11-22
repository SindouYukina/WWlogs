import assert from "node:assert/strict"
import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"
import test from "node:test"

const read = path => readFileSync(path, "utf8")
const sources = dir => readdirSync(dir).flatMap(name => {
  const path = join(dir, name)
  return statSync(path).isDirectory() ? (name === "showcase" ? [] : sources(path)) : /\.(tsx?|css)$/.test(name) ? [path] : []
})

test("product text is not drag-selectable, editable fields and marked values are", () => {
  const css = read("frontend/src/design/components.css")
  assert.ok(css.includes('body[data-view="desktop"], body[data-view="overlay"], .ww-root { -webkit-user-select: none; user-select: none; }'))
  assert.ok(css.includes('.ww-root input, .ww-root textarea, .ww-root [contenteditable="true"], .ww-root [contenteditable="plaintext-only"], .ww-root .ww-selectable { -webkit-user-select: text; user-select: text; }'))
  assert.ok(css.includes("body[data-view] img { -webkit-user-drag: none; }"))
})

test("the copyable values are exactly the build identity, copyright notice, license texts and notices, license identifier, source address, diagnostics and raw skill IDs", () => {
  const about = read("frontend/src/views/SettingsView.tsx")
  for (const field of ["app-version", "app-build-number", "app-revision"]) assert.match(about, new RegExp(`className="ww-num ww-selectable" data-field="${field}"`), field)
  const licenses = read("frontend/src/views/LicensesPage.tsx")
  assert.ok(licenses.includes('className="ww-license-text ww-selectable"') && licenses.includes('className="ww-selectable" data-field={`license-notice-${font.id}`}'))
  for (const field of ["app-license-spdx", "app-source"]) assert.ok(licenses.includes(`className="ww-selectable" lang="en" data-field="${field}"`), field)
  assert.ok(licenses.includes('className="ww-license-text ww-selectable" lang="en" tabIndex={open ? 0 : -1} data-field="app-license-text"'))
  assert.ok(licenses.includes('className="ww-license-copyright ww-selectable" lang="en" data-field="app-copyright"'))
  assert.ok(licenses.includes('className="ww-license-notices ww-selectable" data-field={`library-notice-${library.id}`}') && licenses.includes('className="ww-license-text ww-selectable" lang="en" tabIndex={open ? 0 : -1} data-field={`library-text-${library.id}-${file.name}`}'))
  const dashboard = read("frontend/src/views/DashboardView.tsx")
  assert.ok(dashboard.includes('className="ww-dash-facts ww-selectable"') && dashboard.includes('className="ww-dash-skill-id ww-num ww-selectable"'))
  const marked = sources("frontend/src").flatMap(path => read(path).match(/ww-selectable"/g) ?? [])
  assert.equal(marked.length, 13, "no other text was made selectable")
})

test("selection is not suppressed by intercepting pointer events", () => {
  for (const path of sources("frontend/src")) {
    const text = read(path)
    assert.ok(!/addEventListener\(\s*["'](selectstart|dragstart|mousedown)["']/.test(text), path)
    assert.ok(!/on(SelectStart|DragStart)=/.test(text), path)
  }
})

test("the interface select keeps its original sizing: at least 160 px, up to 280 px for a chosen interface", () => {
  assert.ok(read("frontend/src/design/components.css").includes(".ww-row-pair .ww-select-trigger { flex: 0 1 280px; min-width: 160px; }"))
})
