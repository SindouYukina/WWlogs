import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { host, type Adapter } from "@/lib/host"
import { dataState, dpsText, numberText, roleLabel, type Connection, type Snapshot } from "@/lib/snapshot"
import { StateBanner } from "./StateBanner"

// DesktopView is the Phase 3 control and status page: capture control, overlay control, and the published state.
// It is a functional check of the data chain, not the final dashboard.
export function DesktopView({ snapshot, connection }: { snapshot: Snapshot | null; connection: Connection }) {
  const [adapters, setAdapters] = useState<Adapter[]>([])
  const [selected, setSelected] = useState("")
  const [overlayShown, setOverlayShown] = useState(false)
  const [actionError, setActionError] = useState("")
  const service = snapshot?.service
  const view = snapshot?.view ?? null
  const busy = service?.state === "starting" || service?.state === "running" || service?.state === "stopping"

  useEffect(() => {
    host.listAdapters().then((list) => {
      setAdapters(list ?? [])
      if (list && list[0]) setSelected((current) => current || list[0]!.ipv4)
    }).catch((error: unknown) => setActionError(String(error)))
  }, [])

  // The overlay can also be hidden from its own window (a close request hides it), so read the real state.
  useEffect(() => {
    const refresh = () => host.overlayVisible().then(setOverlayShown).catch(() => undefined)
    refresh()
    const timer = setInterval(refresh, 1000)
    return () => clearInterval(timer)
  }, [])

  const act = (action: () => Promise<unknown>) => {
    setActionError("")
    action().catch((error: unknown) => setActionError(String(error)))
  }

  return (
    <div className="flex min-h-screen flex-col gap-4 p-6">
      <header className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">WWlogs</h1>
        <span className="text-sm text-muted-foreground" data-field="app">
          {service ? `${service.app.version} · ${service.app.profile}` : ""}
        </span>
        <StateBanner connection={connection} state={dataState(snapshot)} snapshot={snapshot} />
      </header>

      <Card>
        <CardHeader><CardTitle>Capture</CardTitle></CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3 text-sm">
          <select
            className="h-9 rounded-md border bg-background px-2"
            value={selected}
            onChange={(event) => setSelected(event.target.value)}
            disabled={busy}
          >
            {adapters.length === 0 && <option value="">No Npcap interface</option>}
            {adapters.map((adapter) => (
              <option key={adapter.ipv4} value={adapter.ipv4}>{`${adapter.ipv4} — ${adapter.description}`}</option>
            ))}
          </select>
          <Button disabled={busy || selected === ""} onClick={() => act(() => host.startCapture(selected))}>Start</Button>
          <Button variant="secondary" disabled={!busy} onClick={() => act(() => host.stopCapture())}>Stop</Button>
          <Button variant="outline" onClick={() => act(async () => setOverlayShown(await host.setOverlayVisible(!(await host.overlayVisible()))))}>
            {overlayShown ? "Hide overlay" : "Show overlay"}
          </Button>
          <span data-field="service-state">State: {service?.state ?? "—"}</span>
          <span data-field="sequence">Snapshot #{snapshot?.sequence ?? 0}</span>
          {service?.session && <span data-field="session">Session {service.session.captureSessionId} ({service.session.source})</span>}
          {actionError !== "" && <span className="text-destructive">{actionError}</span>}
        </CardContent>
      </Card>

      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader><CardTitle>Statistics</CardTitle></CardHeader>
          <CardContent className="grid grid-cols-2 gap-1 text-sm">
            <span>Damage dealt</span><span data-field="damage-dealt">{numberText(view?.statistics.damageDealt)}</span>
            <span>Damage taken</span><span data-field="damage-taken">{numberText(view?.statistics.damageTaken)}</span>
            <span>Encounter DPS</span><span data-field="encounter-dps">{dpsText(view?.statistics.encounterDps)}</span>
            <span>Rolling 5 s</span><span>{dpsText(view?.statistics.rolling5sDps)}</span>
            <span>Rolling 10 s</span><span>{dpsText(view?.statistics.rolling10sDps)}</span>
            <span>Damage span</span><span>{dpsText(view?.statistics.damageSpanDps)}</span>
            <span>Unattributed out</span><span>{numberText(view?.statistics.unattributed.outgoingDamage)}</span>
            <span>Unassigned role</span><span data-field="unassigned">{numberText(view?.unassignedRole.damageDealt)}</span>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>Health</CardTitle></CardHeader>
          <CardContent className="grid grid-cols-2 gap-1 text-sm">
            <span>Integrity</span><span data-field="integrity">{view?.health.dataIntegrity ?? "—"}</span>
            <span>Transport</span><span>{view?.health.transport.state ?? "—"}</span>
            <span>Context</span><span>{view?.health.context.state ?? "—"}</span>
            <span>Kernel drops</span><span>{view ? `${view.health.capture.kernelDropped.value} (${view.health.capture.kernelDropped.status})` : "—"}</span>
            <span>RPC paired</span><span>{view ? `${view.health.rpcPairing.paired} / ${view.health.rpcPairing.requests}` : "—"}</span>
            <span>Interval</span><span data-field="interval">{view ? `${view.captureSessionId}.${view.statisticsIntervalId}` : "—"}</span>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>Encounter</CardTitle></CardHeader>
          <CardContent className="grid grid-cols-2 gap-1 text-sm">
            <span>State</span><span data-field="encounter-state">{view?.encounters.state ?? "—"}</span>
            <span>Current</span><span>{view?.encounters.current?.encounterId ?? "—"}</span>
            <span>Integrity</span><span>{view?.encounters.current?.integrity.status ?? "—"}</span>
            <span>Finished</span><span data-field="encounters-finished">{numberText(view?.encounters.completedTotal)}</span>
            <span>Active effects</span><span data-field="effects">{numberText(view?.effects.active.length)}</span>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader><CardTitle>Roles</CardTitle></CardHeader>
        <CardContent>
          <table className="w-full text-sm" data-field="roles">
            <thead className="text-left text-muted-foreground">
              <tr><th>Role</th><th>Dealt</th><th>Direct</th><th>Summon</th><th>Taken</th></tr>
            </thead>
            <tbody>
              {(view?.roles ?? []).map((role) => (
                <tr key={`${role.playerId}-${role.roleId}`}>
                  <td>{roleLabel(role)}</td>
                  <td>{numberText(role.damageDealt)}</td>
                  <td>{numberText(role.directDamage)}</td>
                  <td>{numberText(role.summonDamage)}</td>
                  <td>{numberText(role.damageTaken)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  )
}
