import { useSyncExternalStore } from "react"

// What one timeline card shares between its tracks while the pointer moves: the range being dragged on the DPS plot and
// the moment under the pointer there. Only the overlays that draw them subscribe, so a drag or a hover re-renders a few
// overlay elements and never the tracks, the numbers or the tabs. Presentation only: nothing here is a statistic.

export interface TimelineSyncState {
  /** The range being dragged, drawn over every track until the drag is committed or cancelled. */
  preview: { fromMs: number; toMs: number } | null
  /** The moment under the pointer on the DPS plot, drawn as a guide over the tracks. */
  hoverMs: number | null
}

export interface TimelineSync {
  get(): TimelineSyncState
  set(patch: Partial<TimelineSyncState>): void
  subscribe(listener: () => void): () => void
}

export function createTimelineSync(): TimelineSync {
  let state: TimelineSyncState = { preview: null, hoverMs: null }
  const listeners = new Set<() => void>()
  return {
    get: () => state,
    set(patch) {
      const next = { ...state, ...patch }
      if (next.preview === state.preview && next.hoverMs === state.hoverMs) return
      state = next
      for (const listener of listeners) listener()
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
  }
}

const idle: TimelineSyncState = { preview: null, hoverMs: null }
const never = () => () => {}

/** One field of a card's shared pointer state; without a sync it is idle. */
export function useTimelineSync<K extends keyof TimelineSyncState>(sync: TimelineSync | undefined, key: K): TimelineSyncState[K] {
  return useSyncExternalStore(sync ? sync.subscribe : never, () => (sync ? sync.get() : idle)[key])
}
