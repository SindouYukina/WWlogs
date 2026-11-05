import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// One project for both windows: index.html selects the Desktop or Overlay view from ?view=.
export default defineConfig({
  plugins: [react(), tailwindcss()],
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
