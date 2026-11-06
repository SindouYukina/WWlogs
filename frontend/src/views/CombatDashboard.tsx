import { useTranslation } from "react-i18next"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { dpsText, numberText, damageContributors, type DamageContributor, type DPS, type Role, type IntegrityStatus, type ViewSnapshot } from "@/lib/snapshot"
import i18n, { statusText } from "@/i18n"

export function roleName(role:Role):string {
  return role.nameStatus==="VERIFIED"&&role.name ? role.name : i18n.t("unknownRole",{id:role.roleId})
}

export function contributorName(row:DamageContributor):string {
  return row.role?roleName(row.role):i18n.t(row.vehicle?.kind==="motorcycle"?"motorcycle":"vehicle")
}

export function IntegrityBadge({status}:{status?:IntegrityStatus}) {
  const {t}=useTranslation("common")
  return <Badge variant="outline" data-status={status} className={status==="UNRELIABLE"?"border-destructive/40 text-destructive":status==="DEGRADED"?"border-warning/40 text-warning":status==="INTACT"?"border-emerald-400/30 text-emerald-300":"text-muted-foreground"}>{status ? statusText(status) : t("waiting")}</Badge>
}

export function DPSMetric({label,dps,field,prominent=false}:{label:string;dps?:DPS;field:string;prominent?:boolean}) {
  const {i18n}=useTranslation("common")
  const valid=dps?.status==="available"||dps?.status==="lower_bound"
  return <div className={prominent?"metric metric-primary":"metric"}>
    <div className="eyebrow">{label}</div>
    <div className={`metric-value ${!valid?"text-muted-foreground":dps?.status==="lower_bound"?"text-warning":""}`} data-field={field} data-status={dps?.status??"unavailable"}>
      {valid?dpsText(dps,i18n.language):dps?statusText(dps.status):"—"}
    </div>
    <div className="metric-note" data-field={`${field}-status`}>{statusText(dps?.status)}{dps?.confidence?` · ${statusText(dps.confidence)}`:""}</div>
  </div>
}

export function CombatDashboard({view,stopped,diagnostics=false}:{view:ViewSnapshot|null;stopped:boolean;diagnostics?:boolean}) {
  const {t,i18n}=useTranslation(["dashboard","common"])
  const c=(key:string)=>t(key,{ns:"common"})
  const num=(value:number|undefined)=>numberText(value,i18n.language)
  const encounter=view?.encounters.current??view?.encounters.recent.at(-1)
  const integrity=view?.statistics.integrity
  const contributors=damageContributors(view?.roles??[],view?.vehicle)
  return <div className="space-y-5">
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <div><div className="eyebrow">{t(view?.encounters.current?"currentEncounter":encounter?"lastEncounter":"encounter")}</div><CardTitle className="mt-1 text-xl" data-field="encounter-state">{view?statusText(view.encounters.state):c("noEncounter")}</CardTitle></div>
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground"><span>{encounter?`${t("encounterNumber",{id:encounter.sequence})} · ${statusText(encounter.outcome)}`:c("noEncounter")}</span><IntegrityBadge status={encounter?.integrity.status}/></div>
      </CardHeader>
      <CardContent>
        <DPSMetric label={c("encounterDps")} dps={encounter?.dps??view?.statistics.encounterDps} field="encounter-dps" prominent/>
        <div className="mt-4 grid gap-4 border-t pt-4 sm:grid-cols-3">
          <DPSMetric label={c("rolling5")} dps={view?.statistics.rolling5sDps} field="rolling5-dps"/>
          <DPSMetric label={c("rolling10")} dps={view?.statistics.rolling10sDps} field="rolling10-dps"/>
          <DPSMetric label={c("damageSpan")} dps={view?.statistics.damageSpanDps} field="damage-span-dps"/>
        </div>
        <p className="mt-4 text-xs text-muted-foreground">{stopped&&view?`${t("final")} · `:""}{t("scopeNote")}</p>
      </CardContent>
    </Card>
    <div className="grid gap-4 sm:grid-cols-2">
      <Card><CardContent className="pt-6"><div className="eyebrow">{t("confirmedDamage")} · {t("interval")}</div><div className="total-value" style={{color:"var(--ww-hero-number)"}} data-field="damage-dealt">{num(view?.statistics.damageDealt)}</div><div className="metric-note">{t("events",{count:view?.statistics.outgoingEvents??0})}</div></CardContent></Card>
      <Card><CardContent className="pt-6"><div className="eyebrow">{t("damageTaken")} · {t("interval")}</div><div className="total-value" data-field="damage-taken">{num(view?.statistics.damageTaken)}</div><div className="metric-note">{t("events",{count:view?.statistics.incomingEvents??0})}</div></CardContent></Card>
    </div>
    <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.65fr)_minmax(270px,1fr)]">
      <Card><CardHeader><CardTitle>{t("roles")}</CardTitle><p className="text-xs text-muted-foreground">{t("interval")}</p></CardHeader>
        <CardContent className="overflow-x-auto"><table className="role-table" data-field="roles">
          <thead><tr><th>{c("roleTitle")}</th><th>{t("share")}</th><th>{t("directSummon")}</th><th>{c("taken")}</th></tr></thead>
          <tbody>{contributors.map(row=><tr key={row.key} data-field={row.vehicle?"vehicle":undefined}>
            <td className="font-medium">{contributorName(row)}<div className="mt-1 text-xs text-muted-foreground">{row.role?t("role",{ns:"common",id:row.role.roleId}):c("vehicle")}</div></td>
            <td><div className="tabular-nums">{num(row.damageDealt)} <span className="text-xs text-muted-foreground">{row.damageShare===undefined?"—":new Intl.NumberFormat(i18n.language,{style:"percent",maximumFractionDigits:1}).format(row.damageShare)}</span></div><div className="contribution-track" aria-hidden="true"><div style={{width:`${Math.min(100,(row.damageShare??0)*100)}%`}}/></div></td>
            <td className="tabular-nums text-muted-foreground">{row.role?num(row.role.directDamage):"—"}<div>{row.role?num(row.role.summonDamage):"—"}</div></td><td>{row.vehicle?"—":num(row.damageTaken)}</td>
          </tr>)}
          {diagnostics&&view&&<tr><td>{c("unassigned")}<div className="mt-1 text-xs text-muted-foreground">{t("localOwner")}</div></td><td data-field="unassigned">{num(view.unassignedRole.damageDealt)}<div>{t("events",{count:view.unassignedRole.outgoingEvents})} · {view.unassignedRoleShare===undefined?"—":new Intl.NumberFormat(i18n.language,{style:"percent",maximumFractionDigits:1}).format(view.unassignedRoleShare)}</div></td><td>—</td><td>{num(view.unassignedRole.damageTaken)}</td></tr>}
          {contributors.length===0&&<tr><td colSpan={4} className="empty-state">{t("noRoles")}</td></tr>}
          </tbody></table>
          {view&&(view.unassignedRole.outgoingEvents>0||view.statistics.unattributed.outgoingEvents>0)&&<p className="mt-3 text-xs text-warning" data-field="attribution-incomplete">{c("attributionIncomplete")}</p>}
          {diagnostics&&<div data-field="unknown-ownership" className="mt-4 rounded-lg border border-dashed p-3 text-xs text-muted-foreground"><span className="font-medium text-foreground">{t("unknownOwnership")}</span><div className="mt-1">{t("unknownTotals",{out:num(view?.statistics.unattributed.outgoingDamage),in:num(view?.statistics.unattributed.incomingDamage)})}</div><p className="mt-1">{t("unknownNote")}</p></div>}
        </CardContent>
      </Card>
      <Card><CardHeader><div className="flex items-center justify-between gap-2"><CardTitle>{c("integrity")}</CardTitle><span data-field="integrity"><IntegrityBadge status={view?.health.dataIntegrity}/></span></div><p className="text-xs text-muted-foreground">{t("interval")}</p></CardHeader>
        <CardContent className="space-y-4 text-sm">
          <p className="text-muted-foreground">{!view?c("waiting"):t(integrity?.status==="INTACT"?"intactNote":integrity?.status==="DEGRADED"?"degradedNote":"unreliableNote")}</p>
          <dl className="detail-list"><dt>{c("context")}</dt><dd data-field="context">{statusText(view?.health.context.state)}</dd><dt>{c("transport")}</dt><dd data-field="transport">{statusText(view?.health.transport.state)}</dd><dt>{t("transportLosses")}</dt><dd>{num(integrity?.transportLosses)}</dd><dt>{t("unrecovered")}</dt><dd>{num(integrity?.unrecoveredLosses)}</dd><dt>{t("downgraded")}</dt><dd>{num(integrity?.contextDowngradedEvents)}</dd></dl>
          {view?.health.context.state==="SUSPECT"&&<p className="rounded-md border border-warning/30 p-3 text-xs text-warning">{t("suspectNote")}</p>}
          {diagnostics&&<details className="border-t pt-3"><summary className="cursor-pointer text-xs text-muted-foreground">{t("details")}</summary><dl className="detail-list mt-3 text-xs"><dt>{t("drops")}</dt><dd>{view?.health.capture.kernelDropped.status==="available"?num(view.health.capture.kernelDropped.value):c("unknown")}</dd><dt>{t("paired")}</dt><dd>{view?`${view.health.rpcPairing.paired} / ${view.health.rpcPairing.requests}`:"—"}</dd><dt>{t("finished")}</dt><dd data-field="encounters-finished">{num(view?.encounters.completedTotal)}</dd><dt>{t("interval")}</dt><dd data-field="interval">{view?`${view.captureSessionId}.${view.statisticsIntervalId}`:"—"}</dd></dl></details>}
        </CardContent>
      </Card>
    </div>
  </div>
}
