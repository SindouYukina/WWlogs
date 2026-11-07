import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

const read = path => readFileSync(path, "utf8").replace(/\r\n/g, "\n")

test("the header emblem has one source and one component", () => {
  const emblem = read("frontend/src/design/components/BrandEmblem.tsx")
  assert.ok(emblem.includes("../brand/header_emblem.png"))
  for (const path of ["frontend/src/views/DesktopApp.tsx", "frontend/src/showcase/ShowcaseApp.tsx"]) {
    const source = read(path)
    assert.ok(source.includes("<BrandEmblem />"), path)
    assert.ok(!/ww-app-mark|sc-mark/.test(source), path)
  }
  const png = readFileSync("frontend/src/design/brand/header_emblem.png")
  assert.deepEqual([...png.subarray(0, 4)], [0x89, 0x50, 0x4e, 0x47])
  assert.equal(png.readUInt32BE(16), png.readUInt32BE(20), "square, so the emblem keeps its ratio")
})
