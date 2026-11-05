import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { dpsText, numberText, roleLabel, type DPS, type IntegrityStatus, type ViewSnapshot } from "@/lib/snapshot"

export function IntegrityBadge({ status }: { status?: IntegrityStatus }) {
  return <Badge variant="outline" className={status === "UNRELIABLE" ? "border-destructive/40 text-destructive" : status === "DEGRADED" ? "border-warning/40 text-warning" : status === "INTACT" ? "border-emerald-400/30 text-emerald-300" : "text-muted-foreground"}>{status ?? "No data"}</Badge>
}

export function DPSMetric({ label, dps, field, prominent = false }: { label: string; dps?: DPS; field: string; prominent?: boolean }) {
  const valid = dps?.status === "available" || dps?.status === "lower_bound"
  return <div className={prominent ? "metric metric-primary" : "metric"}>
    <div className="eyebrow">{label}</div>
    <div className={`metric-value ${!valid ? "text-muted-foreground" : dps?.status === "lower_bound" ? "text-warning" : ""}`} data-field={field}>{dpsText(dps)}</div>
    <div className="metric-note" data-field={`${field}-status`}>
      {dps ? <><span>{dps.status.replaceAll("_", " ")}</span>{dps.reason && <span> · {dps.reason.replaceAll("_", " ")}</span>}{dps.confidence && <span> · {dps.confidence.replaceAll("_", " ")}</span>}</> : "Waiting for combat data"}
    </div>
  </div>
}

export function CombatDashboard({ view, stopped }: { view: ViewSnapshot | null; stopped: boolean }) {
  const encounter = view?.encounters.current ?? view?.encounters.recent.at(-1)
  const integrity = view?.statistics.integrity
  const total = view?.statistics.damageDealt
  const roles = [...(view?.roles ?? [])].sort((a, b) => b.damageDealt - a.damageDealt)
  const share = (damage: number) => total !== undefined && total > 0 ? damage / total * 100 : undefined
  return <div className="space-y-5">
    <Card className="overflow-hidden">
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <div><div className="eyebrow">{view?.encounters.current ? "Current encounter" : encounter ? "Last encounter" : "Encounter"}</div><CardTitle className="mt-1 text-xl" data-field="encounter-state">{view ? view.encounters.state.replaceAll("_", " ") : "No encounter"}</CardTitle></div>
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground"><span>{encounter ? `Encounter #${encounter.sequence} · ${encounter.outcome}` : "Waiting for an encounter"}</span><IntegrityBadge status={encounter?.integrity.status} /></div>
      </CardHeader>
      <CardContent>
        <DPSMetric label="Encounter DPS" dps={encounter?.dps ?? view?.statistics.encounterDps} field="encounter-dps" prominent />
        <div className="mt-4 grid gap-4 border-t pt-4 sm:grid-cols-3">
          <DPSMetric label="Rolling 5 s" dps={view?.statistics.rolling5sDps} field="rolling5-dps" />
          <DPSMetric label="Rolling 10 s" dps={view?.statistics.rolling10sDps} field="rolling10-dps" />
          <DPSMetric label="Damage-span DPS" dps={view?.statistics.damageSpanDps} field="damage-span-dps" />
        </div>
        <p className="mt-4 text-xs text-muted-foreground">{stopped && view ? "Capture ended · showing the final snapshot. " : ""}Rolling and damage-span values cover the capture interval. DPS status and confidence come from the combat engine.</p>
      </CardContent>
    </Card>

    <div className="grid gap-4 sm:grid-cols-2">
      <Card><CardContent className="pt-6"><div className="eyebrow">Confirmed local damage · interval</div><div className="total-value text-emerald-300" data-field="damage-dealt">{numberText(total)}</div><div className="metric-note">{numberText(view?.statistics.outgoingEvents)} confirmed outgoing events</div></CardContent></Card>
      <Card><CardContent className="pt-6"><div className="eyebrow">Local damage taken · interval</div><div className="total-value" data-field="damage-taken">{numberText(view?.statistics.damageTaken)}</div><div className="metric-note">{numberText(view?.statistics.incomingEvents)} confirmed incoming events</div></CardContent></Card>
    </div>

    <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.65fr)_minmax(270px,1fr)]">
      <Card>
        <CardHeader><CardTitle>Role contributions</CardTitle><p className="text-xs text-muted-foreground">Confirmed local damage in this capture interval. Shares include UnassignedRole.</p></CardHeader>
        <CardContent className="overflow-x-auto">
          <table className="role-table" data-field="roles">
            <thead><tr><th>Role</th><th>Damage / share</th><th>Direct / summon</th><th>Taken</th></tr></thead>
            <tbody>{roles.map((role) => {
              const percent = share(role.damageDealt)
              return <tr key={`${role.playerId}-${role.roleId}`}><td><span className="font-medium">{roleLabel(role)}</span><div className="mt-1 text-xs text-muted-foreground">Role {role.roleId}</div></td><td><div className="font-medium tabular-nums">{numberText(role.damageDealt)} <span className="text-xs text-muted-foreground">{percent === undefined ? "—" : `${percent.toFixed(1)}%`}</span></div><div className="contribution-track" aria-hidden="true"><div style={{ width: `${Math.min(100, percent ?? 0)}%` }} /></div></td><td className="tabular-nums text-muted-foreground">{numberText(role.directDamage)}<div>{numberText(role.summonDamage)}</div></td><td className="tabular-nums">{numberText(role.damageTaken)}</td></tr>
            })}
            {view && <tr><td><span className="font-medium">UnassignedRole</span><div className="mt-1 text-xs text-muted-foreground">Local owner confirmed</div></td><td data-field="unassigned">{numberText(view.unassignedRole.damageDealt)} <span className="text-xs text-muted-foreground">{share(view.unassignedRole.damageDealt)?.toFixed(1) ?? "—"}{total ? "%" : ""}</span></td><td className="text-xs text-muted-foreground">Role unresolved</td><td>{numberText(view.unassignedRole.damageTaken)}</td></tr>}
            {!view && <tr><td colSpan={4} className="empty-state">Start capture to see role contributions.</td></tr>}
            {view && roles.length === 0 && <tr><td colSpan={4} className="empty-state">No resolved role contributions yet.</td></tr>}
            </tbody>
          </table>
          <div className="mt-4 rounded-lg border border-dashed p-3 text-xs text-muted-foreground"><span className="font-medium text-foreground">Unknown ownership</span><div className="mt-1">Outgoing {numberText(view?.statistics.unattributed.outgoingDamage)} · Incoming {numberText(view?.statistics.unattributed.incomingDamage)}</div><p className="mt-1">Excluded from confirmed local totals. Unknown names retain their raw Role ID.</p></div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><div className="flex items-center justify-between gap-2"><CardTitle>Data integrity</CardTitle><span data-field="integrity"><IntegrityBadge status={view?.health.dataIntegrity} /></span></div><p className="text-xs text-muted-foreground">Capture interval · reported by Go</p></CardHeader>
        <CardContent className="space-y-4 text-sm">
          <p className={integrity?.status === "UNRELIABLE" ? "text-destructive" : integrity?.status === "DEGRADED" ? "text-warning" : "text-muted-foreground"}>{!view ? "No capture data is available." : integrity?.status === "INTACT" ? "No loss or context downgrade reported in this interval." : integrity?.status === "DEGRADED" ? "Some events may be missing. Eligible DPS values are lower bounds." : "Encounter boundaries or loss extent are uncertain. Unreliable DPS is withheld."}</p>
          <dl className="detail-list"><dt>Context</dt><dd data-field="context" className={view?.health.context.state === "SUSPECT" ? "text-warning" : ""}>{view?.health.context.state ?? "—"}</dd><dt>Transport</dt><dd data-field="transport">{view?.health.transport.state ?? "—"}</dd><dt>Transport losses</dt><dd>{numberText(integrity?.transportLosses)}</dd><dt>Unrecovered losses</dt><dd>{numberText(integrity?.unrecoveredLosses)}</dd><dt>Downgraded events</dt><dd>{numberText(integrity?.contextDowngradedEvents)}</dd></dl>
          {view?.health.context.state === "SUSPECT" && <p className="rounded-md border border-warning/30 p-3 text-xs text-warning">Context SUSPECT · local attribution may be incomplete. Confidence is determined by the engine.</p>}
          {(integrity?.risks.length ?? 0) > 0 && <div className="border-t pt-3"><div className="eyebrow">Reported risks</div><ul className="mt-2 space-y-1 text-xs text-warning">{integrity?.risks.map((risk) => <li key={risk}>{risk.replaceAll("_", " ")}</li>)}</ul></div>}
          <details className="border-t pt-3"><summary className="cursor-pointer text-xs text-muted-foreground">Capture details</summary><dl className="detail-list mt-3 text-xs"><dt>Kernel drops</dt><dd>{view?.health.capture.kernelDropped.status === "available" ? numberText(view.health.capture.kernelDropped.value) : view?.health.capture.kernelDropped.status ?? "—"}</dd><dt>RPC paired / requests</dt><dd>{view ? `${view.health.rpcPairing.paired} / ${view.health.rpcPairing.requests}` : "—"}</dd><dt>Finished encounters</dt><dd data-field="encounters-finished">{numberText(view?.encounters.completedTotal)}</dd><dt>Statistics interval</dt><dd data-field="interval">{view ? `${view.captureSessionId}.${view.statisticsIntervalId}` : "—"}</dd></dl></details>
        </CardContent>
      </Card>
    </div>
  </div>
}
