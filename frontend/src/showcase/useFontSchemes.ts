import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { fetchFontManifest, loadingStatus, requiredFaces, schemeIds, schemeStatusFrom, schemeUsable, type FontFaceInfo, type FontFaceOutcome, type FontManifest, type SchemeId, type SchemeStatus } from "./typefaces"

export type { SchemeId } from "./typefaces"

function urlOf(scheme: SchemeId, face: FontFaceInfo): string {
  return `/preview-fonts/${scheme}/${face.file}`
}

/** One candidate file: a 404 is Missing, while a refused request or a file the browser cannot decode is a load failure. */
async function loadCandidateFace(scheme: SchemeId, face: FontFaceInfo, added: FontFace[]): Promise<FontFaceOutcome> {
  let response: Response
  try {
    response = await fetch(urlOf(scheme, face), { cache: "no-store" })
  } catch {
    return "failed"
  }
  if (response.status === 404) return "missing"
  if (!response.ok) return "failed"
  try {
    const instance = new FontFace(face.family, await response.arrayBuffer(), { weight: face.weight, display: "swap" })
    await instance.load()
    document.fonts.add(instance)
    added.push(instance)
    return "loaded"
  } catch {
    return "failed"
  }
}

/** The bundled scheme ships in the build, so only its decode can fail; ask the browser for each required family. */
async function loadBundledFamily(family: string): Promise<FontFaceOutcome> {
  try {
    return (await document.fonts.load(`400 16px "${family}"`, "A永")).length > 0 ? "loaded" : "failed"
  } catch {
    return "failed"
  }
}

export interface FontSchemes {
  manifest: FontManifest | null
  /** The manifest request finished, whatever its result. */
  ready: boolean
  statuses: Readonly<Record<SchemeId, SchemeStatus>>
  ensure: (scheme: SchemeId) => void
  usable: (scheme: SchemeId) => boolean
  /** Reads the directory again, for when files were added or removed while the Showcase is open. */
  recheck: () => void
}

const bundledFamilies: Record<string, readonly string[]> = {
  "zh-TW": ["WW Inter", "WW Noto TC"],
  "zh-CN": ["WW Inter", "WW Noto SC"],
  ja: ["WW Inter", "WW Noto JP"],
  ko: ["WW Inter", "WW Noto KR"],
  en: ["WW Inter", "WW Noto TC"],
}

function initialStatuses(): Record<SchemeId, SchemeStatus> {
  const result = {} as Record<SchemeId, SchemeStatus>
  for (const id of schemeIds) result[id] = id === "system" ? { kind: "baseline", missing: [], failed: [] } : { ...loadingStatus }
  return result
}

/** Loads a scheme's files for the active language on demand and reports what really happened. */
export function useFontSchemes(locale: string): FontSchemes {
  const [manifest, setManifest] = useState<FontManifest | null>(null)
  const [ready, setReady] = useState(false)
  const [statuses, setStatuses] = useState(initialStatuses)
  const [generation, setGeneration] = useState(0)
  const started = useRef(new Set<string>())
  const added = useRef<FontFace[]>([])
  const wanted = useRef(new Set<SchemeId>())
  useEffect(() => {
    let cancelled = false
    setReady(false)
    void fetchFontManifest().then(result => {
      if (cancelled) return
      setManifest(result)
      setReady(true)
    })
    return () => { cancelled = true }
  }, [generation])
  const load = useCallback((scheme: SchemeId) => {
    if (scheme === "system" || !ready) return
    const key = `${generation}:${scheme}:${locale}`
    if (started.current.has(key)) return
    started.current.add(key)
    const publish = (status: SchemeStatus) => setStatuses(current => ({ ...current, [scheme]: status }))
    publish({ ...loadingStatus })
    if (scheme === "inter-noto") {
      void Promise.all((bundledFamilies[locale] ?? bundledFamilies.en!).map(loadBundledFamily)).then(outcomes => {
        publish(outcomes.every(outcome => outcome === "loaded") ? { kind: "loaded", missing: [], failed: [] } : { kind: "failed", missing: [], failed: [...(bundledFamilies[locale] ?? [])] })
      })
      return
    }
    if (!manifest?.[scheme]) {
      publish({ kind: "no-root", missing: [], failed: [] })
      return
    }
    const required = requiredFaces(manifest, scheme, locale)
    const loaded = added.current
    void Promise.all(required.map(async face => [face.file, await loadCandidateFace(scheme, face, loaded)] as const)).then(results => {
      publish(schemeStatusFrom(required, new Map(results)))
    })
  }, [generation, locale, manifest, ready])
  const ensure = useCallback((scheme: SchemeId) => {
    wanted.current.add(scheme)
    load(scheme)
  }, [load])
  useEffect(() => {
    // The manifest arrived, the language changed or a recheck ran: reload what is in use.
    for (const scheme of wanted.current) load(scheme)
  }, [load])
  const recheck = useCallback(() => {
    for (const face of added.current) document.fonts.delete(face)
    added.current = []
    started.current.clear()
    setStatuses(initialStatuses())
    setGeneration(value => value + 1)
  }, [])
  return useMemo(() => ({ manifest, ready, statuses, ensure, recheck, usable: (scheme: SchemeId) => schemeUsable(statuses[scheme]) }), [manifest, ready, statuses, ensure, recheck])
}
