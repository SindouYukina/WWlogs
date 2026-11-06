import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import "./index.css"
import "./i18n"
import { useSnapshot } from "@/lib/useSnapshot"
import { DesktopView } from "@/views/DesktopView"
import { OverlayView } from "@/views/OverlayView"

// Both windows load this page; the Go host opens the Desktop window at ?view=desktop and the Overlay at ?view=overlay.
const parameters = new URLSearchParams(window.location.search)
const viewName = parameters.get("view") === "overlay" ? "overlay" : "desktop"
const showcase = __WWLOGS_SHOWCASE__ && parameters.get("view") === "showcase"
document.body.dataset.view = showcase ? (parameters.has("surface") ? "showcase-overlay" : "showcase") : viewName
if (viewName === "overlay") {
  for (const eventType of ["contextmenu", "dragstart"]) {
    document.body.addEventListener(eventType, event => event.preventDefault())
  }
}

function App() {
  const { snapshot, connection, changeLocale } = useSnapshot()
  return viewName === "overlay"
    ? <OverlayView snapshot={snapshot} connection={connection} />
    : <DesktopView snapshot={snapshot} connection={connection} changeLocale={changeLocale} />
}

const root = createRoot(document.getElementById("root")!)
if (__WWLOGS_SHOWCASE__ && showcase) {
  // Development only: Public builds define the flag as false and contain neither this import nor its chunk.
  void import("./showcase/ShowcaseApp").then(({ ShowcaseApp }) => root.render(<StrictMode><ShowcaseApp /></StrictMode>))
} else {
  root.render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}
