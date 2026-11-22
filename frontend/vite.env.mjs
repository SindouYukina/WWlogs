// Node-only build helpers, kept out of vite.config.ts so the frontend needs no Node type definitions.
import { createReadStream, existsSync, readFileSync } from "node:fs"
import { join } from "node:path"

/** The Motion Showcase exists only in Development builds. A build that names no profile is Public. */
export function showcaseEnabled(command) {
  return command === "serve" || process.env.WWLOGS_FRONTEND_PROFILE === "development"
}

/**
 * WWlogs' license for Settings > About: the LICENSE file at the repository root, unmodified, with the LF line endings of
 * the FSF original whatever the checkout uses. It becomes a build constant because the dev server loads no file outside
 * the frontend, and widening its allow list to the repository would also expose the private directories there.
 */
export function productLicenseText() {
  return readFileSync(new URL("../LICENSE", import.meta.url), "utf8").replace(/\r\n/g, "\n")
}

/**
 * Emits the files the Go host reads from the frontend archive: the language manifest, and a copy of each language's
 * startup namespace, the few texts the host shows before any window exists (a launch that cannot start).
 */
export function hostLocaleFiles(manifest) {
  return {
    name: "bundled-locale-manifest",
    generateBundle() {
      this.emitFile({ type: "asset", fileName: "locales/manifest.json", source: JSON.stringify(manifest) })
      for (const locale of manifest.supportedLocales) {
        const text = readFileSync(new URL(`./locales/${locale}/startup.json`, import.meta.url), "utf8").replace(/^\uFEFF/, "")
        this.emitFile({ type: "asset", fileName: `locales/${locale}/startup.json`, source: JSON.stringify(JSON.parse(text)) })
      }
    },
  }
}

/**
 * Compares the npm packages a bundle's modules come from with the frontend components of the library license manifest
 * (src/lib/library-licenses/manifest.json). A component marked "generated" contributes generated code (styles, bundler
 * helpers) rather than modules, so it is never expected among the module paths. Returns the packages bundled without a
 * listed license and the listed packages the bundle no longer contains.
 */
export function libraryLicenseGaps(moduleIds, components) {
  const bundled = new Set()
  for (const id of moduleIds) {
    const match = /node_modules[\\/]((?:@[^\\/]+[\\/])?[^\\/]+)[\\/]/.exec(id)
    if (match) bundled.add(match[1].replaceAll("\\", "/"))
  }
  const listed = components.filter(component => component.group === "frontend")
  return {
    unlisted: [...bundled].filter(name => !listed.some(component => component.name === name)).sort(),
    unbundled: listed.filter(component => !component.generated && !bundled.has(component.name)).map(component => component.name).sort(),
  }
}

/** Fails a production build whose bundle and the library license manifest disagree, so no bundled package goes unlisted. */
export function libraryLicenseGuard() {
  const manifest = JSON.parse(readFileSync(new URL("./src/lib/library-licenses/manifest.json", import.meta.url), "utf8"))
  return {
    name: "library-license-guard",
    apply: "build",
    generateBundle(_options, bundle) {
      const moduleIds = Object.values(bundle).flatMap(output => output.type === "chunk" ? output.moduleIds ?? Object.keys(output.modules ?? {}) : [])
      const { unlisted, unbundled } = libraryLicenseGaps(moduleIds, manifest.components)
      if (unlisted.length) this.error(`Bundled packages without a license in src/lib/library-licenses/manifest.json: ${unlisted.join(", ")}`)
      if (unbundled.length) this.error(`Listed in src/lib/library-licenses/manifest.json but no longer bundled: ${unbundled.join(", ")}`)
    },
  }
}

// The dev server has no Go host, so game images and preview fonts come from separate roots named explicitly by their
// environment variables. Missing roots return 404 and components show their fallbacks.
const folders = {
  "character-avatar": ["characters", "avatar.webp"],
  "character-portrait": ["characters", "portrait.webp"],
  buff: ["buffs", "icon.webp"],
  skill: ["skills", "icon.webp"],
  "skill-config": ["skills/by-config", "icon.webp"],
}

const fontTypes = { ".json": "application/json", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf" }

export function previewGameAssets() {
  return {
    name: "preview-game-assets",
    apply: "serve",
    configureServer(server) {
      const root = process.env.WWLOGS_FONT_ROOT
      server.middlewares.use("/preview-fonts", (request, response) => {
        const name = (request.url ?? "").split("?")[0]
        const match = /^\/([a-z-]+\/)?[a-zA-Z0-9._-]+\.(json|woff2|ttf|otf)$/.exec(name)
        const file = root && match ? join(root, name.slice(1)) : ""
        if (!file || !existsSync(file)) {
          response.statusCode = 404
          response.end()
          return
        }
        response.setHeader("Content-Type", fontTypes[name.slice(name.lastIndexOf("."))])
        createReadStream(file).pipe(response)
      })
      server.middlewares.use("/game-assets", (request, response) => {
        const root = process.env.WWLOGS_ASSET_ROOT
        const match = /^\/([a-z-]+)\/([0-9]+)$/.exec(request.url ?? "")
        const folder = match && folders[match[1]]
        const file = root && folder ? join(root, "game", folder[0], match[2], folder[1]) : ""
        if (!file || !existsSync(file)) {
          response.statusCode = 404
          response.end()
          return
        }
        response.setHeader("Content-Type", "image/webp")
        createReadStream(file).pipe(response)
      })
    },
  }
}
