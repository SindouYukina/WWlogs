// The capture environment states reported by the Go host (internal/capture/environment.go), and what the UI does with
// each. Kept free of React and of the host so the rules can be tested directly.
export type EnvironmentState = "ready" | "npcap_missing" | "npcap_unavailable" | "no_interfaces" | "no_ipv4" | "check_failed"

export interface Adapter {
  ipv4: string
  description: string
}

export interface CaptureEnvironment {
  state: EnvironmentState
  adapters: Adapter[]
  interfaceCount: number
}

export interface NpcapDownload {
  version: string
  url: string
  direct: boolean
}

export interface EnvironmentNotice {
  /** The translation group under dashboard `environment`. */
  key: "missing" | "unavailable" | "noInterfaces" | "noIpv4" | "checkFailed"
  /** Only Npcap's own problems offer its installer; a disabled network adapter is not fixed by reinstalling Npcap. */
  offersDownload: boolean
}

const notices: Record<Exclude<EnvironmentState, "ready">, EnvironmentNotice> = {
  npcap_missing: { key: "missing", offersDownload: true },
  npcap_unavailable: { key: "unavailable", offersDownload: true },
  no_interfaces: { key: "noInterfaces", offersDownload: false },
  no_ipv4: { key: "noIpv4", offersDownload: false },
  check_failed: { key: "checkFailed", offersDownload: false },
}

/** What to tell the user. Null before the first result and when capture can start; an unknown state is a failed check. */
export function environmentNotice(state: EnvironmentState | null): EnvironmentNotice | null {
  if (state === null || state === "ready") return null
  return notices[state] ?? notices.check_failed
}

/** Capture can start only from a finished check that found this address, so a stale list can never start it. */
export function adapterUsable(state: EnvironmentState | null, checking: boolean, adapters: readonly Adapter[], ipv4: string): boolean {
  return state === "ready" && !checking && adapters.some(adapter => adapter.ipv4 === ipv4)
}
