import { Call } from "@wailsio/runtime"
import type { Snapshot } from "./snapshot"

// Calls into the Go Bridge (internal/desktop.Bridge) by fully qualified name; no generated bindings are needed.
const bridge = "wwlogs/internal/desktop.Bridge."

function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(bridge + method, ...args) as Promise<T>
}

export interface Adapter {
  ipv4: string
  description: string
}

export const host = {
  nextSnapshot: (after: number) => call<Snapshot>("NextSnapshot", after),
  currentSnapshot: () => call<Snapshot>("CurrentSnapshot"),
  listAdapters: () => call<Adapter[]>("ListAdapters"),
  startCapture: (localIPv4: string) => call<void>("StartCapture", { localIPv4 }),
  stopCapture: () => call<void>("StopCapture"),
  setOverlayVisible: (visible: boolean) => call<boolean>("SetOverlayVisible", visible),
  overlayVisible: () => call<boolean>("OverlayVisible"),
}
