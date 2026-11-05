import { Badge } from "@/components/ui/badge"
import type { Connection, DataState, Snapshot } from "@/lib/snapshot"

// StateBanner names why the numbers may be missing or untrusted. Both views use it, so the wording stays the same.
export function StateBanner({ connection, state, snapshot }: { connection: Connection; state: DataState; snapshot: Snapshot | null }) {
  if (connection === "disconnected") {
    return <Badge variant="destructive" data-field="banner">Disconnected from the WWlogs host</Badge>
  }
  if (connection === "connecting") {
    return <Badge variant="secondary" data-field="banner">Connecting…</Badge>
  }
  switch (state) {
    case "not_capturing":
      return (
        <Badge variant="secondary" data-field="banner">
          {snapshot?.service.state === "error" ? `Capture error: ${snapshot.service.lastError ?? "unknown"}` : `Not capturing (${snapshot?.service.state ?? "stopped"})`}
        </Badge>
      )
    case "no_data":
      return <Badge variant="secondary" data-field="banner">Waiting for data</Badge>
    case "no_encounter":
      return <Badge variant="outline" data-field="banner">No encounter yet</Badge>
    case "untrusted":
      return <Badge variant="destructive" data-field="banner">Data not reliable: transport or attribution loss</Badge>
    case "degraded":
      return <Badge variant="outline" className="border-warning/40 text-warning" data-field="banner">Degraded · lower bounds</Badge>
    default:
      return <Badge variant="outline" data-field="banner">Live</Badge>
  }
}
