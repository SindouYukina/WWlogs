import { useEffect, useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { Button, Card, CharacterAvatar, Chip, Select, Tooltip } from "@/design"
import { usableDps } from "@/lib/combat-data"
import { useFormatters } from "@/lib/format"
import { classTone, defaultLibraryQuery, libraryFiltered, librarySorts, recordClasses, type HistoryLibrary as Library, type HistoryLibraryEntry, type HistoryLibraryQuery, type LibrarySort, type RecordClass } from "@/lib/history-library"
import { host } from "@/lib/host"
import { dpsText } from "@/lib/snapshot"

// The History Library: every stored encounter across dates, filtered and sorted by Go. The view edits the query and lays
// out the page; it never filters, sorts or classifies records itself.

/** How many encounters one page of the library asks for. */
const libraryPage = 50

export interface HistoryLibraryProps {
  zone: string
  /** Changes when the history may have changed; the library then asks again. */
  refreshKey: string
  query: HistoryLibraryQuery
  onQuery: (query: HistoryLibraryQuery) => void
  onOpen: (id: number) => void
  /** Encounters chosen for comparison, and how many may be chosen. */
  compare?: readonly number[]
  compareLimit?: number
  onCompare?: (id: number) => void
}

function ModeName({ entry }: { entry: HistoryLibraryEntry }) {
  const { t } = useTranslation("history")
  if (!entry.scene.known || !entry.mode?.kind) return <span className="ww-history-unknown">{t("unidentified")}</span>
  return <span data-mode-kind={entry.mode.kind}>{entry.mode.kind === "open_world" ? t("modeOpenWorld") : entry.mode.name}{entry.mode.status === "CORRELATED" && <span className="ww-history-mark" title={t("correlatedNote")}>≈</span>}</span>
}

function StageName({ entry }: { entry: HistoryLibraryEntry }) {
  const { t } = useTranslation("history")
  if (!entry.scene.known || !entry.sceneName.name) return <span className="ww-history-unknown">{t("unidentified")}</span>
  return <span title={entry.sceneName.status === "CORRELATED" ? t("correlatedNote") : undefined}>{entry.sceneName.name}{entry.sceneName.status === "CORRELATED" && <span className="ww-history-mark">≈</span>}</span>
}

export function ClassChip({ value }: { value: RecordClass }) {
  const { t } = useTranslation("history")
  return <span data-field="record-class" data-class={value} title={t(`library.classNote.${value}`)}><Chip tone={classTone(value)} dot>{t(`library.class.${value}`)}</Chip></span>
}

/** The roles that dealt confirmed damage, as avatars: the stored contributors, never a team composition. */
function RoleAvatars({ entry }: { entry: HistoryLibraryEntry }) {
  return <span className="ww-history-roles" data-field="roles">
    {entry.roles.map(role => <Tooltip key={role.roleId} text={role.name.name ?? String(role.staticRoleId)} trigger="child" placement="top">
      <span data-role-id={role.roleId} data-static-role-id={role.staticRoleId}><CharacterAvatar roleId={role.staticRoleId} elementId={role.elementId} size={22} /></span>
    </Tooltip>)}
  </span>
}

export function HistoryLibraryView({ zone, refreshKey, query, onQuery, onOpen, compare = [], compareLimit = 0, onCompare }: HistoryLibraryProps) {
  const { t, i18n } = useTranslation(["history", "common"])
  const format = useFormatters()
  const [page, setPage] = useState<Library | null>(null)
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)
  const [text, setText] = useState(query.text ?? "")
  // A typed search reaches Go once typing pauses.
  useEffect(() => {
    if ((query.text ?? "") === text) return
    const timer = setTimeout(() => onQuery({ ...query, text: text.trim() || undefined }), 300)
    return () => clearTimeout(timer)
  }, [text, query, onQuery])

  useEffect(() => {
    let current = true
    setLoading(true)
    host.historyLibrary({ ...query, offset: 0, limit: libraryPage }, zone).then(view => {
      if (!current) return
      setFailed(false)
      setPage(view)
    }).catch(() => { if (current) setFailed(true) }).finally(() => { if (current) setLoading(false) })
    return () => { current = false }
  }, [query, zone, refreshKey])

  const more = () => {
    if (!page || loading) return
    setLoading(true)
    host.historyLibrary({ ...query, offset: page.encounters.length, limit: libraryPage }, zone).then(next => {
      setPage(previous => previous ? { ...next, offset: 0, encounters: [...previous.encounters, ...next.encounters] } : next)
    }).catch(() => setFailed(true)).finally(() => setLoading(false))
  }

  const facets = page?.facets
  const any = { value: "", label: t("library.any") }
  const versionOptions = useMemo(() => [any, ...(facets?.gameVersions ?? []).map(v => ({ value: v.value, label: `${v.value || t("library.versionUnknown")} (${format.integer(v.count)})` }))], [facets, i18n.language])
  const modeOptions = useMemo(() => [any, ...(facets?.modes ?? []).map(m => ({ value: m.key, label: `${m.kind === "open_world" ? t("modeOpenWorld") : m.name} (${format.integer(m.count)})` }))], [facets, i18n.language])
  const stageOptions = useMemo(() => [any, ...(facets?.stages ?? []).filter(s => s.name.name).map(s => ({ value: String(s.instanceId), label: `${s.name.name} (${format.integer(s.count)})` }))], [facets, i18n.language])
  const roleOptions = useMemo(() => [any, ...(facets?.roles ?? []).map(r => ({ value: String(r.roleId), label: `${r.name.name ?? r.roleId} (${format.integer(r.count)})` }))], [facets, i18n.language])
  const classOptions = useMemo(() => [any, ...recordClasses.map(c => ({ value: c, label: t(`library.class.${c}`) }))], [i18n.language])
  const sourceOptions = useMemo(() => [any, { value: "live", label: t("source.live") }, { value: "replay", label: t("source.replay") }], [i18n.language])
  const sortOptions = useMemo(() => librarySorts.map(s => ({ value: s, label: t(`library.sort.${s}`) })), [i18n.language])
  const set = (change: Partial<HistoryLibraryQuery>) => onQuery({ ...query, ...change })
  const open = (id: number) => (event: KeyboardEvent) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpen(id) } }

  return <div className="ww-history-library" data-field="history-library">
    <Card className="ww-history-filters">
      <div className="ww-history-filter-grid" data-field="library-filters">
        <label className="ww-history-filter"><span>{t("library.from")}</span><input className="ww-input" type="date" value={query.from ?? ""} max={query.to || undefined} data-field="library-from" onChange={event => set({ from: event.target.value || undefined })} /></label>
        <label className="ww-history-filter"><span>{t("library.to")}</span><input className="ww-input" type="date" value={query.to ?? ""} min={query.from || undefined} data-field="library-to" onChange={event => set({ to: event.target.value || undefined })} /></label>
        <div className="ww-history-filter"><span>{t("columns.version")}</span><Select label={t("columns.version")} value={query.gameVersions?.[0] ?? ""} options={versionOptions} onChange={value => set({ gameVersions: value ? [value] : undefined })} /></div>
        <div className="ww-history-filter"><span>{t("columns.mode")}</span><Select label={t("columns.mode")} value={query.modes?.[0] ?? ""} options={modeOptions} onChange={value => set({ modes: value ? [value] : undefined })} /></div>
        <div className="ww-history-filter"><span>{t("columns.scene")}</span><Select label={t("columns.scene")} value={query.stages?.[0] !== undefined ? String(query.stages[0]) : ""} options={stageOptions} onChange={value => set({ stages: value ? [Number(value)] : undefined })} /></div>
        <div className="ww-history-filter"><span>{t("library.character")}</span><Select label={t("library.character")} value={query.roles?.[0] !== undefined ? String(query.roles[0]) : ""} options={roleOptions} onChange={value => set({ roles: value ? [Number(value)] : undefined })} /></div>
        <div className="ww-history-filter"><span>{t("library.classLabel")}</span><Select label={t("library.classLabel")} value={query.classes?.[0] ?? ""} options={classOptions} onChange={value => set({ classes: value ? [value as RecordClass] : undefined })} /></div>
        <div className="ww-history-filter"><span>{t("library.source")}</span><Select label={t("library.source")} value={query.source ?? ""} options={sourceOptions} onChange={value => set({ source: (value || undefined) as HistoryLibraryQuery["source"] })} /></div>
        <label className="ww-history-filter"><span>{t("library.minMinutes")}</span><input className="ww-input ww-num" type="number" min={0} step={1} inputMode="numeric" value={query.minSeconds ? String(query.minSeconds / 60) : ""} data-field="library-min" onChange={event => set({ minSeconds: Number(event.target.value) > 0 ? Number(event.target.value) * 60 : undefined })} /></label>
        <label className="ww-history-filter"><span>{t("library.maxMinutes")}</span><input className="ww-input ww-num" type="number" min={0} step={1} inputMode="numeric" value={query.maxSeconds ? String(query.maxSeconds / 60) : ""} data-field="library-max" onChange={event => set({ maxSeconds: Number(event.target.value) > 0 ? Number(event.target.value) * 60 : undefined })} /></label>
        <label className="ww-history-filter ww-history-filter-wide"><span>{t("library.search")}</span><input className="ww-input" type="search" value={text} placeholder={t("library.searchHint")} autoComplete="off" spellCheck={false} maxLength={64} data-field="library-text" onChange={event => setText(event.target.value)} /></label>
        <div className="ww-history-filter"><span>{t("library.sortLabel")}</span><Select label={t("library.sortLabel")} value={query.sort ?? "start_desc"} options={sortOptions} onChange={value => set({ sort: value as LibrarySort })} /></div>
      </div>
      <div className="ww-history-filter-foot">
        <span className="ww-history-total" data-field="library-total" data-total={page?.total ?? 0}>{page ? t("library.total", { n: format.integer(page.total) }) : ""}</span>
        <Button size="sm" variant="ghost" disabled={!libraryFiltered(query)} data-field="library-reset" onClick={() => { setText(""); onQuery({ ...defaultLibraryQuery, sort: query.sort }) }}>{t("library.reset")}</Button>
      </div>
      <p className="ww-history-note">{t("library.rolesNote")}</p>
    </Card>
    {failed && <p className="ww-history-note" data-tone="critical" role="alert">{t("loadFailed")}</p>}
    <Card className="ww-history-day">
      {page && page.total === 0 ? <p className="ww-dash-empty-note" data-field="library-empty">{t(libraryFiltered(query) ? "library.emptyFiltered" : "emptyHistory")}</p> :
        <div className="ww-dash-skills-wrap" style={{ maxHeight: "none" }}><table className="ww-history-table" data-field="library-list" data-total={page?.total ?? 0}>
          <thead><tr>
            {compareLimit > 0 && <th className="ww-history-compare-column" aria-label={t("compare.select")} />}
            <th>{t("library.date")}</th><th className="ww-history-version-column">{t("columns.version")}</th><th>{t("columns.mode")}</th><th>{t("columns.scene")}</th>
            <th>{t("library.characters")}</th><th className="ww-num">{t("columns.duration")}</th><th className="ww-num">{t("library.damage")}</th><th className="ww-num">{t("columns.dps")}</th><th>{t("library.classLabel")}</th>
          </tr></thead>
          <tbody>{page?.encounters.map(entry => {
            const chosen = compare.includes(entry.id)
            return <tr key={entry.id} className="ww-history-row" tabIndex={0} role="button" data-encounter-id={entry.id} data-class={entry.class} aria-label={t("openEncounter", { time: entry.start.clock, date: entry.localDate })}
              onClick={() => onOpen(entry.id)} onKeyDown={open(entry.id)}>
              {compareLimit > 0 && <td className="ww-history-compare-column" onClick={event => event.stopPropagation()}>
                <input type="checkbox" className="ww-check" checked={chosen} disabled={!chosen && compare.length >= compareLimit} aria-label={t("compare.select")} data-field="compare-select" onChange={() => onCompare?.(entry.id)} />
              </td>}
              <td className="ww-num ww-history-time" data-field="start">{entry.localDate} {entry.start.clock}</td>
              <td className="ww-history-version-column">{entry.gameVersion ? <span className="ww-history-version">{entry.gameVersion}</span> : <span className="ww-history-unknown" title={t("versionUnknown")}>—</span>}</td>
              <td data-field="mode"><ModeName entry={entry} /></td>
              <td data-field="scene"><StageName entry={entry} /></td>
              <td><RoleAvatars entry={entry} /></td>
              <td className="ww-num" data-field="duration">{entry.durationSeconds === undefined ? "—" : format.clock(entry.durationSeconds)}</td>
              <td className="ww-num" data-field="damage">{entry.damageDealt === undefined || entry.state !== "finished" ? "—" : `${entry.class === "degraded" || entry.class === "known_affected" ? "≥ " : ""}${format.integer(entry.damageDealt)}`}</td>
              <td className="ww-num" data-field="dps">{usableDps(entry.dps) ? dpsText(entry.dps, i18n.language) : "—"}</td>
              <td><ClassChip value={entry.class} /></td>
            </tr>
          })}</tbody>
        </table></div>}
      {page && page.total > page.encounters.length && <div className="ww-history-more">
        <span>{t("shownOf", { shown: format.integer(page.encounters.length), total: format.integer(page.total) })}</span>
        <Button size="sm" disabled={loading} onClick={more} data-field="library-more">{t("loadMore")}</Button>
      </div>}
    </Card>
  </div>
}
