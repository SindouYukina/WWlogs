import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { host, type Adapter, type Settings } from "@/lib/host"
import { useOverlay } from "@/lib/useOverlay"
import { dataState, type Connection, type Snapshot } from "@/lib/snapshot"
import { snapshotStale } from "@/lib/effects"
import { manifest, statusText } from "@/i18n"
import { ListeningStatus, ThemeBackdrop, ThemePicker, Toggle, resolveTheme, type ThemeId } from "@/design"
import { useListeningLabels, useThemeDescriptions } from "@/lib/useListeningLabels"
import { CombatDashboard } from "./CombatDashboard"
import { StateBanner } from "./StateBanner"

function AdapterSelect({adapters,value,onChange,disabled,id,allowEmpty=false}:{adapters:Adapter[];value:string;onChange:(value:string)=>void;disabled?:boolean;id:string;allowEmpty?:boolean}) {
  const {t}=useTranslation(["dashboard","settings","common"])
  const missing=value!==""&&!adapters.some(adapter=>adapter.ipv4===value)
  return <select id={id} className="adapter-select" value={value} disabled={disabled} onChange={event=>onChange(event.target.value)}>
    {(allowEmpty||value==="")&&<option value="">{allowEmpty?t("chooseAtLaunch",{ns:"settings"}):t("selectInterface")}</option>}
    {missing&&<option value={value}>{value} · {statusText("unavailable")}</option>}
    {adapters.map(adapter=><option key={adapter.ipv4} value={adapter.ipv4}>{adapter.ipv4} — {adapter.description||t("network")}</option>)}
  </select>
}

export function DesktopView({snapshot,connection,changeLocale}:{snapshot:Snapshot|null;connection:Connection;changeLocale:(locale:string)=>Promise<void>}) {
  const {t}=useTranslation(["dashboard","common","settings"])
  const c=(key:string)=>t(key,{ns:"common"})
  const s=(key:string)=>t(key,{ns:"settings"})
  const [page,setPage]=useState<"dashboard"|"settings">("dashboard")
  const [adapters,setAdapters]=useState<Adapter[]>([])
  const [selected,setSelected]=useState("")
  const [settings,setSettings]=useState<Settings>({version:1,defaultAdapterIPv4:"",showOverlayOnStartup:false,locale:manifest.fallbackLocale})
  const [saved,setSaved]=useState<Settings|null>(null)
  const [warning,setWarning]=useState("")
  const [error,setError]=useState(false)
  const [settingsError,setSettingsError]=useState(false)
  const [saveMessage,setSaveMessage]=useState(false)
  const [loading,setLoading]=useState(true)
  const [saving,setSaving]=useState(false)
  const [refreshing,setRefreshing]=useState(false)
  const [action,setAction]=useState<"start"|"stop"|null>(null)
  const [languagePending,setLanguagePending]=useState(false)
  const [now,setNow]=useState(Date.now())
  const actionLock=useRef(false)
  const saveLock=useRef(false)
  const languageLock=useRef(false)
  const overlay=useOverlay()
  const describeListening=useListeningLabels()
  const describeTheme=useThemeDescriptions()
  const theme=resolveTheme(snapshot?.theme)
  const listening=snapshot?.listening?describeListening(snapshot.listening):null
  const appearance=(nextTheme:ThemeId,reduceMotion:boolean)=>{setSettingsError(false);host.setAppearance(nextTheme,reduceMotion).catch(()=>setSettingsError(true))}
  const service=snapshot?.service
  const busy=service?.state==="starting"||service?.state==="running"||service?.state==="stopping"
  const connected=connection==="connected"
  const selectedAvailable=adapters.some(adapter=>adapter.ipv4===selected)
  const dirty=saved!==null&&(settings.defaultAdapterIPv4!==saved.defaultAdapterIPv4||settings.showOverlayOnStartup!==saved.showOverlayOnStartup)

  useEffect(()=>{
    let cancelled=false
    void Promise.allSettled([host.listAdapters(),host.getSettings()]).then(([interfaces,preferences])=>{
      if(cancelled)return
      setAdapters(interfaces.status==="fulfilled"?interfaces.value??[]:[])
      if(interfaces.status==="rejected")setError(true)
      if(preferences.status==="fulfilled") {
        setSettings(preferences.value.settings);setSaved(preferences.value.settings)
        setWarning(preferences.value.warning??"");setSelected(preferences.value.settings.defaultAdapterIPv4)
      } else setSettingsError(true)
      setLoading(false)
    })
    const timer=setInterval(()=>setNow(Date.now()),1000)
    return()=>{cancelled=true;clearInterval(timer)}
  },[])

  const captureAction=async(kind:"start"|"stop")=>{
    if(actionLock.current)return
    actionLock.current=true;setAction(kind);setError(false)
    try { if(kind==="start")await host.startCapture(selected);else await host.stopCapture() }
    catch {setError(true)}
    finally {actionLock.current=false;setAction(null)}
  }
  const refresh=async()=>{
    if(refreshing)return
    setRefreshing(true);setError(false)
    try{setAdapters(await host.listAdapters()??[])}catch{setError(true)}finally{setRefreshing(false)}
  }
  const save=async()=>{
    if(saveLock.current||!saved)return
    saveLock.current=true;setSaving(true);setSettingsError(false);setSaveMessage(false)
    try{const result=await host.saveSettings(settings);setSettings(result.settings);setSaved(result.settings);setWarning(result.warning??"");setSaveMessage(true)}
    catch{setSettingsError(true)}finally{saveLock.current=false;setSaving(false)}
  }
  const language=async(locale:string)=>{
    if(languageLock.current)return
    languageLock.current=true;setLanguagePending(true);setSettingsError(false)
    try{await changeLocale(locale)}catch{setSettingsError(true)}finally{languageLock.current=false;setLanguagePending(false)}
  }

  return <div className={page==="dashboard"?"desktop-shell ww-has-backdrop":"desktop-shell"}>
    <ThemeBackdrop theme={theme} intensity={page==="dashboard"?"art":"ambient"}/>
    <header className="app-header">
      <div className="flex items-center gap-3"><div className="app-mark" aria-hidden="true">W</div><div><h1 className="text-lg font-semibold tracking-tight">WWlogs</h1><div className="text-xs text-muted-foreground">{c("appSubtitle")}</div></div></div>
      <div className="flex flex-wrap items-center justify-end gap-3">{snapshot?.listening&&listening&&<span data-field="listening" data-state={snapshot.listening.state} data-reason={snapshot.listening.reason}><ListeningStatus state={snapshot.listening.state} label={listening.label} detail={listening.detail}/></span>}<span className="text-xs text-muted-foreground" data-field="app">{service?`${service.app.version} · ${service.app.profile}`:c("connecting")}</span><StateBanner connection={connection} state={dataState(snapshot)} snapshot={snapshot} stale={snapshotStale(snapshot,now)}/></div>
    </header>
    <div className="workspace">
      <nav className="page-nav" aria-label={c("dashboard")}>
        <Button variant={page==="dashboard"?"secondary":"ghost"} aria-current={page==="dashboard"?"page":undefined} onClick={()=>setPage("dashboard")}>{c("dashboard")}</Button>
        <Button variant={page==="settings"?"secondary":"ghost"} aria-current={page==="settings"?"page":undefined} onClick={()=>setPage("settings")}>{c("settings")}{dirty?" ·":""}</Button>
        <div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground"><span className={`status-dot ${service?.state==="running"?"bg-emerald-400":"bg-muted-foreground"}`}/><span data-field="service-state" data-state={service?.state}>{statusText(service?.state)}</span></div>
      </nav>
      {warning&&<div className="notice text-warning" role="status">{s(warning==="overlay_save_failed"?"saveFailed":"invalid")}</div>}
      {overlay.warning==="overlay_save_failed"&&<div className="notice text-warning" role="alert">{s("saveFailed")}</div>}
      <section aria-label={t("network")}><Card><CardContent className="pt-5">
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-56 flex-1"><label className="eyebrow mb-2 block" htmlFor="capture-adapter">{t("network")}</label><AdapterSelect id="capture-adapter" adapters={adapters} value={selected} onChange={setSelected} disabled={busy||action!==null||loading||refreshing}/></div>
          <Button variant="outline" disabled={!connected||refreshing||loading||busy||action!==null} onClick={()=>void refresh()}>{t(refreshing?"refreshing":"refresh")}</Button>
          <Button className="bg-primary text-primary-foreground hover:opacity-90" disabled={!connected||loading||busy||action!==null||!selectedAvailable} onClick={()=>void captureAction("start")}>{action==="start"||service?.state==="starting"?statusText("starting"):t("start")}</Button>
          <Button variant="secondary" disabled={!connected||service?.state!=="running"||action!==null} onClick={()=>void captureAction("stop")}>{action==="stop"||service?.state==="stopping"?statusText("stopping"):t("stop")}</Button>
          <Button variant="outline" disabled={!connected||overlay.pending||overlay.visible===null} onClick={()=>void overlay.toggle()}>{t(overlay.visible?"hideOverlay":"showOverlay")}</Button>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-muted-foreground"><span data-field="session">{service?.session?t("session",{id:service.session.captureSessionId,source:statusText(service.session.source==="live"?"liveSource":"replay")}):t("noSession")}</span><span data-field="overlay-state">{overlay.visible===null?c("unknown"):t(overlay.visible?"overlayVisible":"overlayHidden")}</span></div>
        {!loading&&adapters.length===0&&<p className="mt-3 text-sm text-warning">{t("noInterfaces")}</p>}
        {selected!==""&&!selectedAvailable&&!loading&&<p className="mt-3 text-sm text-warning">{t("missingInterface")}</p>}
        {(error||overlay.error)&&<p role="alert" className="mt-3 text-sm text-destructive">{c("operationFailed")}</p>}
        {service?.lastError&&<p role="alert" className="mt-3 text-sm text-destructive">{c("captureFailed")}</p>}
      </CardContent></Card></section>
      <main>{page==="dashboard"?<CombatDashboard diagnostics={service?.app.profile==="development"} view={snapshot?.view??null} stopped={service?.state==="stopped"||service?.state==="error"}/>:<div className="mx-auto max-w-3xl space-y-5">
        <div><h2 className="text-xl font-semibold">{s("title")}</h2><p className="mt-1 text-sm text-muted-foreground">{s("description")}</p></div>
        <Card><CardHeader><CardTitle>{s("appearance")}</CardTitle></CardHeader><CardContent className="space-y-4">
          <div data-field="theme" data-theme={theme}><ThemePicker label={s("theme")} value={theme} describe={describeTheme} onChange={next=>appearance(next,snapshot?.reduceMotion===true)}/><p className="mt-2 text-xs text-muted-foreground">{s("themeHint")}</p></div>
          <div className="flex items-center justify-between gap-4 border-t pt-4"><div><div className="text-sm font-medium">{s("reduceMotion")}</div><p className="mt-1 text-xs text-muted-foreground">{s("reduceMotionHint")}</p></div><Toggle label={s("reduceMotion")} checked={snapshot?.reduceMotion===true} onChange={value=>appearance(theme,value)}/></div>
        </CardContent></Card>
        <Card><CardHeader><CardTitle>{s("language")}</CardTitle></CardHeader><CardContent>
          <label className="sr-only" htmlFor="locale">{s("language")}</label><select id="locale" className="adapter-select" data-field="locale" disabled={!connected||languagePending} value={snapshot?.locale??manifest.fallbackLocale} onChange={event=>void language(event.target.value)}>{manifest.supportedLocales.map(locale=><option key={locale} value={locale}>{manifest.languages[locale as keyof typeof manifest.languages].label}</option>)}</select><p className="mt-2 text-xs text-muted-foreground">{s("languageHint")}</p>
        </CardContent></Card>
        <Card><CardHeader><CardTitle>{s("startup")}</CardTitle></CardHeader><CardContent className="space-y-6">
          <div><label className="mb-2 block text-sm font-medium" htmlFor="default-adapter">{s("defaultAdapter")}</label><AdapterSelect id="default-adapter" adapters={adapters} value={settings.defaultAdapterIPv4} onChange={value=>{setSettings({...settings,defaultAdapterIPv4:value});setSaveMessage(false)}} disabled={loading||saving||saved===null} allowEmpty/><p className="mt-2 text-xs text-muted-foreground">{s("adapterHint")}</p></div>
          <label className="flex items-start gap-3 text-sm"><input type="checkbox" className="mt-1 size-4 accent-primary" checked={settings.showOverlayOnStartup} disabled={loading||saving||saved===null} onChange={event=>{setSettings({...settings,showOverlayOnStartup:event.target.checked});setSaveMessage(false)}}/><span>{s("showOnStartup")}<span className="mt-1 block text-xs text-muted-foreground">{s("visibilityHint")}</span></span></label>
          <div><Button variant="outline" disabled={!connected} onClick={()=>void host.resetOverlayPosition().catch(()=>setSettingsError(true))}>{s("resetOverlay")}</Button><p className="mt-2 text-xs text-muted-foreground">{s("geometryHint")}</p></div>
          <div className="flex items-center gap-3 border-t pt-5"><Button disabled={!connected||saving||loading||saved===null||(!dirty&&!warning)} onClick={()=>void save()}>{s(saving?"saving":"save")}</Button>{dirty&&<Badge variant="outline">{s("unsaved")}</Badge>}</div>
          {saveMessage&&<p role="status" className="text-sm text-emerald-300">{s("saved")}</p>}{settingsError&&<p role="alert" className="text-sm text-destructive">{s("saveFailed")}</p>}
        </CardContent></Card><p className="text-xs text-muted-foreground">{s("storage")}</p>
      </div>}</main>
      <footer className="flex flex-wrap justify-between gap-2 border-t pt-4 text-xs text-muted-foreground"><span>{t("footer")}</span><span data-field="sequence">{t("snapshot",{id:snapshot?.sequence??"—"})}{service?` · ${service.app.commit.slice(0,12)}`:""}</span></footer>
    </div>
  </div>
}
