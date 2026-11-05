import { useEffect, useRef, useState } from "react"
import { host } from "./host"

// Poll actual native visibility: closing the Overlay hides it outside the Desktop controls.
// Revisions prevent an older poll from overwriting a newer user command.
export function useOverlay() {
  const [visible, setVisible] = useState<boolean | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const revision = useRef(0)
  const changing = useRef(false)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      const version = revision.current
      if (!changing.current) {
        try {
          const actual = await host.overlayVisible()
          if (!cancelled && version === revision.current) { setVisible(actual); setError("") }
        } catch {
          if (!cancelled && version === revision.current) { setVisible(null); setError("Overlay status is unavailable.") }
        }
      }
      if (!cancelled) timer = setTimeout(poll, 1000)
    }
    void poll()
    return () => { cancelled = true; mounted.current = false; clearTimeout(timer) }
  }, [])
  const toggle = async () => {
    if (changing.current || visible === null) return
    changing.current = true
    revision.current++
    setPending(true)
    setError("")
    try {
      const actual = await host.setOverlayVisible(!visible)
      if (mounted.current) setVisible(actual)
    } catch (error: unknown) {
      if (mounted.current) { setVisible(null); setError(String(error)) }
    } finally {
      changing.current = false
      if (mounted.current) setPending(false)
    }
  }
  return { visible, pending, error, toggle }
}
