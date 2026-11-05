import { useEffect, useState } from "react"
import { host } from "./host"
import type { Connection, Snapshot } from "./snapshot"

// useSnapshot keeps the newest snapshot. Each window long-polls the host on its own (NextSnapshot returns at least
// once a second), so a busy window just receives fewer snapshots and never slows the capture or the other window.
export function useSnapshot(): { snapshot: Snapshot | null; connection: Connection } {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [connection, setConnection] = useState<Connection>("connecting")

  useEffect(() => {
    let cancelled = false
    let after = 0
    const loop = async () => {
      while (!cancelled) {
        try {
          const next = await host.nextSnapshot(after)
          if (cancelled) return
          setConnection("connected")
          if (next.sequence !== after) {
            after = next.sequence
            setSnapshot(next)
          }
        } catch {
          if (cancelled) return
          setConnection("disconnected")
          await new Promise((resolve) => setTimeout(resolve, 1000))
        }
      }
    }
    void loop()
    return () => {
      cancelled = true
    }
  }, [])

  return { snapshot, connection }
}
