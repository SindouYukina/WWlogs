import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { dataState, numberText, damageContributors, type Connection, type Effect, type Snapshot } from "@/lib/snapshot"
import { effectRemaining, relevantEffect, snapshotStale } from "@/lib/effects"
import { statusText } from "@/i18n"
import { DPSMetric, IntegrityBadge, roleName, contributorName } from "./CombatDashboard"
import { ListeningStatus } from "@/design"
import { useListeningLabels } from "@/lib/useListeningLabels"
import { StateBanner } from "./StateBanner"

export function OverlayView({snapshot,connection}:{snapshot:Snapshot|null;connection:Connection}) {
  const {t,i18n}=useTranslation(["overlay","common","dashboard"])
  const [now,setNow]=useState(Date.now())
  useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),1000);return()=>clearInterval(timer)},[])
  const c=(key:string)=>t(key,{ns:"common"})
  const num=(value:number|undefined)=>numberText(value,i18n.language)
  const state=(value:string)=>i18n.exists(`state.${value}`,{ns:"overlay"})?t(`state.${value}`):c("unknown")
  const diagnostics=snapshot?.service.app.profile==="development"
  const view=snapshot?.view
  const encounter=view?.encounters.current??view?.encounters.recent.at(-1)
  const contributors=damageContributors(encounter?.roles??[],encounter?.vehicle)
  const effects=(view?.effects.active??[]).filter(relevantEffect)
  const ended=(view?.effects.recentEnded??[]).filter(relevantEffect).slice(-3).reverse()
  const scope=`${view?.captureSessionId}.${view?.statisticsIntervalId}`
  const duration=encounter?.elapsedSeconds
  const elapsed=duration!==undefined&&duration>=0?`${Math.floor(duration/60)}:${String(Math.floor(duration%60)).padStart(2,"0")}`:"—"
  const describeListening=useListeningLabels()
  const listening=snapshot?.listening?describeListening(snapshot.listening):null
  const effectName=(effect:Effect)=>effect.termName&&effect.termStatus==="VERIFIED"?effect.termName:t("unknownEffect",{id:effect.effectId??"—"})
  const target=(effect:Effect)=>{
    if(effect.holderName&&effect.holderNameStatus==="VERIFIED")return effect.holderName
    const role=effect.holderRoleId===undefined?undefined:view?.roles.find(role=>role.roleId===effect.holderRoleId)
    return role?roleName(role):effect.holderRoleId!==undefined?t("unknownRole",{ns:"common",id:effect.holderRoleId}):state(effect.holderRelation)
  }
  return <div className="combat-overlay" data-field="overlay" data-locale={snapshot?.locale}>
    <div className="overlay-content">
      {snapshot?.listening&&listening&&<div className="overlay-listening" data-field="listening" data-state={snapshot.listening.state}><ListeningStatus state={snapshot.listening.state} label={listening.label} detail={listening.detail} variant="compact"/></div>}
      <StateBanner connection={connection} state={dataState(snapshot)} snapshot={snapshot} stale={snapshotStale(snapshot,now)}/>
      <div className="overlay-encounter-title"><span>{t(view?.encounters.current?"currentEncounter":encounter?"lastEncounter":"encounter",{ns:"dashboard"})} · {statusText(view?.encounters.state)}</span><span data-field="encounter-elapsed">{t("duration",{ns:"common",time:elapsed})}</span></div>
      <DPSMetric label={c("encounterDps")} dps={encounter?.dps??view?.statistics.encounterDps} field="encounter-dps" prominent/>
      <div className="overlay-rolling"><DPSMetric label={c("rolling5")} dps={view?.statistics.rolling5sDps} field="rolling5-dps"/><DPSMetric label={c("rolling10")} dps={view?.statistics.rolling10sDps} field="rolling10-dps"/></div>
      <div className="overlay-total"><span>{t("confirmedDamage",{ns:"dashboard"})} · {t("interval",{ns:"dashboard"})}</span><strong data-field="damage-dealt">{num(view?.statistics.damageDealt)}</strong></div>
      <div className="overlay-health"><IntegrityBadge status={encounter?.integrity.status??view?.health.dataIntegrity}/><span data-field="context">{c("context")}: {statusText(view?.health.context.state)}</span><span data-field="transport">{c("transport")}: {statusText(view?.health.transport.state)}</span></div>
      {view?.health.dataIntegrity!==undefined&&view.health.dataIntegrity!=="INTACT"&&<p className="overlay-warning">{t(view.health.dataIntegrity==="DEGRADED"?"degradedNote":"unreliableNote",{ns:"dashboard"})}</p>}
      <section className="overlay-section" aria-label={t("roles",{ns:"dashboard"})}>
        <h2>{t("roles",{ns:"dashboard"})}</h2><div data-field="roles">
          {contributors.map(row=><div className="overlay-role" data-field={row.vehicle?"vehicle":undefined} key={`${scope}-${encounter?.encounterId}-${row.key}`}>
            <div className="overlay-role-heading"><span className="truncate" title={contributorName(row)}>{contributorName(row)}</span><span>{num(row.damageDealt)} <small>{row.damageShare===undefined?"—":new Intl.NumberFormat(i18n.language,{style:"percent",maximumFractionDigits:1}).format(row.damageShare)}</small></span></div>
            <div className="contribution-track" aria-hidden="true"><div style={{width:`${Math.min(100,(row.damageShare??0)*100)}%`}}/></div>
            {row.damageTaken>0&&<div className="overlay-role-taken">{c("taken")}: {num(row.damageTaken)}</div>}
          </div>)}
          {contributors.length===0&&<p className="overlay-empty">{t("noRoles",{ns:"dashboard"})}</p>}
          {diagnostics&&encounter&&<div className="overlay-unassigned" data-field="unassigned"><span>{c("unassigned")}</span><span>{num(encounter.unassignedRole.damageDealt)} · {t("events",{ns:"dashboard",count:encounter.unassignedRole.outgoingEvents})} · {encounter.unassignedRoleShare===undefined?"—":new Intl.NumberFormat(i18n.language,{style:"percent",maximumFractionDigits:1}).format(encounter.unassignedRoleShare)}</span></div>}
        </div>
      </section>
      {view&&(view.unassignedRole.outgoingEvents>0||view.statistics.unattributed.outgoingEvents>0)&&<p className="overlay-warning" data-field="attribution-incomplete">{c("attributionIncomplete")}</p>}
      {diagnostics&&<div className="overlay-unassigned" data-field="unknown-ownership">{t("unknownOwnership",{ns:"dashboard"})}: {num(view?.statistics.unattributed.outgoingDamage)}</div>}
      <section className="overlay-section" aria-label={t("effects")}>
        <h2>{t("effects")}</h2><p className="overlay-timer-hint">{t("timerHint")}</p>
        <div className="overlay-effects" data-field="effects">{effects.slice(0,12).map(effect=>{
          const remaining=effectRemaining(effect,snapshot,connection,now)
          const updated=effect.lastUpdatedAt!==effect.firstObservedAt
          return <div className="overlay-effect" key={`${scope}-${effect.key}`} data-effect={effect.key}>
            <div className="overlay-effect-heading"><span className="truncate" title={effectName(effect)}>{effectName(effect)}</span><strong data-field="effect-remaining" data-status={remaining===undefined?"unknown":"estimated"}>{remaining===undefined?"—":`~ ${t("seconds",{ns:"common",value:new Intl.NumberFormat(i18n.language,{maximumFractionDigits:1}).format(remaining)})}`}</strong></div>
            {effect.termName&&effect.termStatus!=="VERIFIED"&&<p className="overlay-candidate">{effect.termName} · {t("termCandidate")}</p>}
            <div className="overlay-effect-meta"><span>{state(effect.polarity)}</span><span>{t("stacks",{count:effect.stackCount??c("unknown")})}</span><span>{t("target",{name:target(effect)})}</span></div>
            <div className="overlay-effect-meta"><span>{state(effect.lifecycle==="active"&&updated?"updated":effect.lifecycle)}</span><span>{state(effect.evidence)}</span></div>
          </div>
        })}</div>
        {effects.length===0&&<p className="overlay-empty">{t("noEffects")}</p>}
        {(effects.length>12||(view?.effects.omitted??0)>0)&&<p className="overlay-empty">{t("omitted",{count:Math.max(0,effects.length-12)+(view?.effects.omitted??0)})}</p>}
        {ended.length>0&&<details className="overlay-ended"><summary>{t("recent")}</summary>{ended.map(effect=><div key={`${scope}-${effect.key}-${effect.endedAt}`}><span className="truncate">{effectName(effect)}</span><span>{state(effect.lifecycle)}</span></div>)}</details>}
      </section>
      {diagnostics&&<footer className="overlay-footer"><span data-field="sequence">{t("snapshot",{ns:"dashboard",id:snapshot?.sequence??"—"})}</span></footer>}
    </div>
  </div>
}
