import type { HistoryContent, HistoryEncounterSummary, HistoryMode, HistoryName, HistoryStatus, HistoryZone } from "./history.ts"

// The History Library contracts the Go host returns (internal/desktop/history_library.go). Go selects, sorts, pages and
// classifies the records; the Master-backed filters (mode, stage, character, text) are resolved by Go too. This file only
// holds the query the view edits and lays out what Go returned.

/** How a stored record may be used, from its stored fields (internal/history/library.go). */
export type RecordClass = "complete" | "degraded" | "known_affected" | "incomplete" | "unreliable" | "unfinished"

export const recordClasses: readonly RecordClass[] = ["complete", "degraded", "known_affected", "incomplete", "unreliable", "unfinished"]

export type LibrarySort = "start_desc" | "start_asc" | "dps_desc" | "damage_desc" | "duration_desc"

export const librarySorts: readonly LibrarySort[] = ["start_desc", "start_asc", "dps_desc", "damage_desc", "duration_desc"]

export interface HistoryLibraryQuery {
  /** Calendar dates of the view's zone, inclusive. */
  from?: string
  to?: string
  gameVersions?: string[]
  classes?: RecordClass[]
  source?: "" | "live" | "replay"
  minSeconds?: number
  maxSeconds?: number
  sort?: LibrarySort
  offset?: number
  limit?: number
  /** Mode keys of the facets ("open_world", "category:<name>" or "content:<system>"), instance IDs, static RoleIds and free text. */
  modes?: string[]
  stages?: number[]
  roles?: number[]
  text?: string
}

export interface HistoryLibraryRole {
  /** The stored runtime RoleId; staticRoleId is the role a trial role belongs to (the art and name shown). */
  roleId: number
  staticRoleId: number
  elementId?: number
  name: HistoryName
  damage: number
}

export interface HistoryLibraryEntry extends HistoryEncounterSummary {
  class: RecordClass
  roles: HistoryLibraryRole[]
  vehicle?: boolean
}

export interface LibraryCount {
  value: string
  count: number
}

/**
 * One mode the library and career filters offer (Go historyModeFacetJSON): a named category, the open world, or a content
 * system the game names no system for, which is labelled by its system as in a row.
 */
export interface HistoryModeFacet {
  key: string
  kind: NonNullable<HistoryMode["kind"]>
  name?: string
  status: HistoryName["status"]
  system?: string
  count: number
}

export interface HistoryLibraryFacets {
  gameVersions: LibraryCount[]
  classes: LibraryCount[]
  sources: LibraryCount[]
  modes: HistoryModeFacet[]
  stages: Array<{ instanceId: number; name: HistoryName; content?: HistoryContent; count: number }>
  roles: Array<{ roleId: number; elementId?: number; name: HistoryName; count: number }>
  firstDate?: string
  lastDate?: string
}

export interface HistoryLibrary {
  status: HistoryStatus
  zone: HistoryZone
  total: number
  offset: number
  encounters: HistoryLibraryEntry[]
  facets: HistoryLibraryFacets
}

/** Where another page sends the History page: a date of the calendar, one stored encounter, or the library with a query. */
export type HistoryIntent =
  | { kind: "day"; date: string }
  | { kind: "encounter"; encounterId: number; date: string }
  | { kind: "library"; query: HistoryLibraryQuery }

/** The query a fresh library starts from: everything, newest first. */
export const defaultLibraryQuery: HistoryLibraryQuery = { sort: "start_desc" }

/** The query with paging removed: two queries that differ only in their page select the same records. */
export function libraryFilters(query: HistoryLibraryQuery): HistoryLibraryQuery {
  const { offset: _offset, limit: _limit, ...filters } = query
  return filters
}

/** Whether a query narrows the library at all (sorting does not). */
export function libraryFiltered(query: HistoryLibraryQuery): boolean {
  return Boolean(query.from || query.to || query.gameVersions?.length || query.classes?.length || query.source || query.minSeconds || query.maxSeconds ||
    query.modes?.length || query.stages?.length || query.roles?.length || query.text?.trim())
}

export type ClassTone = "neutral" | "good" | "warn" | "critical"

/** The tone a record class shows with: only a complete record is plain. */
export function classTone(value: RecordClass): ClassTone {
  switch (value) {
    case "complete": return "neutral"
    case "degraded": case "known_affected": case "incomplete": return "warn"
    case "unreliable": return "critical"
    case "unfinished": return "good"
  }
}

/** The listed encounters that may be chosen for deletion: every one except those still being recorded, which Go refuses. */
export function deletableIds(entries: readonly Pick<HistoryLibraryEntry, "id" | "state">[]): number[] {
  return entries.filter(entry => entry.state !== "open").map(entry => entry.id)
}

/** The chosen encounters with one of them added or taken out. */
export function toggleChosen(chosen: readonly number[], id: number): number[] {
  return chosen.includes(id) ? chosen.filter(other => other !== id) : [...chosen, id]
}

/** Whether none, some or all of the encounters that may be chosen are chosen. */
export function chosenState(deletable: readonly number[], chosen: readonly number[]): "none" | "some" | "all" {
  const marked = new Set(chosen)
  const count = deletable.filter(id => marked.has(id)).length
  return count === 0 ? "none" : count === deletable.length ? "all" : "some"
}
