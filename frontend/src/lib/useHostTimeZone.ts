import { useEffect, useState } from "react"
import { hostTimeZone } from "./history"

/**
 * The host's IANA time zone, read again whenever the window regains focus or becomes visible. Changing the Windows time zone
 * happens in another window, so returning to WWlogs is when a new zone can first matter; nothing polls in between. WebView2
 * follows a Windows time zone change on its own, and the History queries follow the name this returns.
 */
export function useHostTimeZone(): string {
  const [zone, setZone] = useState(hostTimeZone)
  useEffect(() => {
    const check = () => setZone(hostTimeZone())
    window.addEventListener("focus", check)
    document.addEventListener("visibilitychange", check)
    return () => {
      window.removeEventListener("focus", check)
      document.removeEventListener("visibilitychange", check)
    }
  }, [])
  return zone
}
