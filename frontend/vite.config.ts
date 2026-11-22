import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"
import manifest from "./locales/manifest.json" with { type: "json" }
import { hostLocaleFiles, libraryLicenseGuard, previewGameAssets, productLicenseText, showcaseEnabled } from "./vite.env.mjs"

// One project for all windows: index.html selects the view from ?view=.
// With the Showcase disabled, dead-code elimination removes its chunk from the output entirely.
export default defineConfig(({ command }) => ({
  define: {
    __WWLOGS_SHOWCASE__: JSON.stringify(showcaseEnabled(command)),
    __WWLOGS_LICENSE_TEXT__: JSON.stringify(productLicenseText()),
  },
  plugins: [react(), tailwindcss(), previewGameAssets(), libraryLicenseGuard(), hostLocaleFiles(manifest)],
  resolve: {
    alias: { "@": "/src" },
  },
  build: {
    // The build script passes --outDir to a temporary directory outside the repository.
    emptyOutDir: true,
    sourcemap: false,
  },
  server: { port: 9245, strictPort: true },
}))
