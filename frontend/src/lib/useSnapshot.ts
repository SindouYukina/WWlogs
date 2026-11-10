import { useCallback, useEffect, useRef, useState } from "react"
import { host } from "./host"
import type { Connection, Snapshot } from "./snapshot"
import { shellChanged, snapshotSupersedes, withShell } from "./snapshot"
import i18n, { manifest } from "@/i18n"
import { applyAppearance } from "@/design/appearance"

/** Resolves when the window is visible. A hidden window holds no long-poll, so it costs the host nothing. */
function whenVisible(): Promise<void> {
  if (!document.hidden) return Promise.resolve()
  return new Promise(resolve => {
    const onChange = () => {
      if (document.hidden) return
      document.removeEventListener("visibilitychange", onChange)
      resolve()
    }
    document.addEventListener("visibilitychange", onChange)
  })
}

// A single long-poll per visible window consumes the shared Go publisher. Locale revisions also order same-sequence
// copies. Statistics are never computed here: every window reads the one result the engine published.
export function useSnapshot() {
  const [snapshot,setSnapshot]=useState<Snapshot|null>(null)
  const [connection,setConnection]=useState<Connection>("connecting")
  const latest=useRef({sequence:0,localeRevision:0})
  const minimumRevision=useRef(0)
  const mounted=useRef(false)
  const accept=useCallback((next:Snapshot)=>{
    const previous=latest.current
    applyAppearance(next.theme,next.reduceMotion===true)
    if(!snapshotSupersedes(next,previous,minimumRevision.current)) {
      // A heartbeat keeps the core sequence, but listening state, appearance and window state may still have moved.
      setSnapshot(current=>current&&next.sequence===current.sequence&&shellChanged(next,current)?withShell(current,next):current)
      return
    }
    const locale=manifest.supportedLocales.includes(next.locale) ? next.locale : manifest.fallbackLocale
    // All resources are eager; language and names become visible in the same React batch.
    void i18n.changeLanguage(locale)
    document.documentElement.lang=locale
    latest.current={sequence:next.sequence,localeRevision:next.localeRevision}
    setSnapshot(next)
  },[])
  const changeLocale=useCallback(async(locale:string)=>{
    const settings=await host.setLocale(locale)
    if(!mounted.current) return
    minimumRevision.current=Math.max(minimumRevision.current,settings.localeRevision)
    accept(await host.currentSnapshot())
  },[accept])
  useEffect(()=>{
    mounted.current=true
    let cancelled=false
    const loop=async()=>{
      while(!cancelled) {
        await whenVisible()
        if(cancelled) return
        try {
          const next=await host.nextSnapshot(latest.current.sequence)
          if(cancelled) return
          setConnection("connected")
          // A reply that arrives just after hiding is dropped; the current state is fetched again when shown.
          if(!document.hidden) accept(next)
        } catch {
          if(cancelled) return
          setConnection("disconnected")
          await new Promise(resolve=>setTimeout(resolve,1000))
        }
      }
    }
    void loop()
    return()=>{cancelled=true;mounted.current=false}
  },[accept])
  return {snapshot,connection,changeLocale}
}
