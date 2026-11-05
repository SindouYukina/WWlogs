import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import "./index.css"
import { useSnapshot } from "@/lib/useSnapshot"
import { DesktopView } from "@/views/DesktopView"
import { OverlayView } from "@/views/OverlayView"

// Both windows load this page; the Go host opens the Desktop window at ?view=desktop and the Overlay at ?view=overlay.
const viewName = new URLSearchParams(window.location.search).get("view") === "overlay" ? "overlay" : "desktop"
document.body.dataset.view = viewName

function App() {
  const { snapshot, connection } = useSnapshot()
  return viewName === "overlay"
    ? <OverlayView snapshot={snapshot} connection={connection} />
    : <DesktopView snapshot={snapshot} connection={connection} />
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
