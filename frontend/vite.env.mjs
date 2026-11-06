// Node-only build helpers, kept out of vite.config.ts so the frontend needs no Node type definitions.
import { createReadStream, existsSync } from "node:fs"
import { join } from "node:path"

/** The Motion Showcase exists only in Development builds. A build that names no profile is Public. */
export function showcaseEnabled(command) {
  return command === "serve" || process.env.WWLOGS_FRONTEND_PROFILE === "development"
}

// Mirrors internal/assets paths. The dev server has no Go host, so images come from a directory named explicitly by
// WWLOGS_ASSET_ROOT; without it every image request is a 404 and components show their fallbacks.
const folders = {
  "character-avatar": ["characters", "avatar.webp"],
  "character-portrait": ["characters", "portrait.webp"],
  buff: ["buffs", "icon.webp"],
  skill: ["skills", "icon.webp"],
}

export function previewGameAssets() {
  return {
    name: "preview-game-assets",
    apply: "serve",
    configureServer(server) {
      const root = process.env.WWLOGS_ASSET_ROOT
      server.middlewares.use("/game-assets", (request, response) => {
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
