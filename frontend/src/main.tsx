import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import "./index.css"
import "./i18n"
import { applyAppearance } from "@/design/appearance"
import type { OverlayKind } from "@/lib/snapshot"
import { DesktopApp } from "@/views/DesktopApp"
import { OverlayWindow } from "@/views/OverlayWindow"

// The default theme applies before the first paint; the first snapshot then carries the saved one.
applyAppearance(undefined, false)

// All windows load this page. The Go host opens the Desktop window at ?view=desktop and each Overlay at
// ?view=overlay&surface=combat or effects.
const parameters = new URLSearchParams(window.location.search)
const showcase = __WWLOGS_SHOWCASE__ && parameters.get("view") === "showcase"
const surfaceWindow = parameters.get("view") === "overlay" || (showcase && parameters.has("surface"))
const surface: OverlayKind = parameters.get("surface") === "effects" ? "effects" : "combat"
document.body.dataset.view = surfaceWindow ? "overlay" : showcase ? "showcase" : "desktop"
if (surfaceWindow) {
  for (const eventType of ["contextmenu", "dragstart"]) {
    document.body.addEventListener(eventType, event => event.preventDefault())
  }
}

const root = createRoot(document.getElementById("root")!)
if (__WWLOGS_SHOWCASE__ && showcase) {
  // Development only: Public builds define the flag as false and contain neither this import nor its chunk.
  void import("./showcase/ShowcaseApp").then(({ ShowcaseApp }) => root.render(<StrictMode><ShowcaseApp /></StrictMode>))
} else {
  root.render(
    <StrictMode>
      {surfaceWindow ? <OverlayWindow surface={surface} /> : <DesktopApp />}
    </StrictMode>,
  )
}
