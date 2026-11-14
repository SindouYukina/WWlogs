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
// No window shows the WebView2 context menu (reload, save as, print, inspect): it is browser chrome, not part of WWlogs.
// Wails leaves it on in its debug mode (the Development profile), and in Public over text fields and selected text, so the
// page turns it off itself, the same way in both profiles: one capture listener on the window reaches every element,
// portals and text fields included, before any other handler. DevTools and the Development remote debugging port are
// untouched. An Overlay also moves with a press anywhere on it, so no text or image drag starts there.
for (const eventType of surfaceWindow ? ["contextmenu", "dragstart"] : ["contextmenu"]) {
  window.addEventListener(eventType, event => event.preventDefault(), { capture: true })
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
