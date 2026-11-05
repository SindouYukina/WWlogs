import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"
import manifest from "./locales/manifest.json" with { type: "json" }

// One project for both windows: index.html selects the Desktop or Overlay view from ?view=.
export default defineConfig({
  plugins: [react(), tailwindcss(), {
    name: "bundled-locale-manifest",
    generateBundle() { this.emitFile({ type: "asset", fileName: "locales/manifest.json", source: JSON.stringify(manifest) }) },
  }],
  resolve: {
    alias: { "@": "/src" },
  },
  build: {
    // The build script passes --outDir to a temporary directory outside the repository.
    emptyOutDir: true,
    sourcemap: false,
  },
  server: { port: 9245, strictPort: true },
})
