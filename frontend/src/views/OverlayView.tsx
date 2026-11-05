import { dataState, dpsText, numberText, roleLabel, type Connection, type Snapshot } from "@/lib/snapshot"
import { StateBanner } from "./StateBanner"

// OverlayView is the Phase 3 overlay: the same snapshot as the Desktop view, drawn on a transparent window. Layout,
// dragging, and click-through belong to later phases.
export function OverlayView({ snapshot, connection }: { snapshot: Snapshot | null; connection: Connection }) {
  const view = snapshot?.view ?? null
  const encounter = view?.encounters.current ?? view?.encounters.recent.at(-1)
  return (
    <div className="m-2 flex flex-col gap-1 rounded-lg bg-black/60 p-3 text-sm text-white" data-field="overlay">
      <div className="flex items-center justify-between gap-2">
        <span className="font-semibold">WWlogs</span>
        <StateBanner connection={connection} state={dataState(snapshot)} snapshot={snapshot} />
      </div>
      <div className="flex justify-between">
        <span>Encounter DPS</span>
        <span data-field="encounter-dps">{dpsText(encounter?.dps ?? view?.statistics.encounterDps)}</span>
      </div>
      <div className="flex justify-between">
        <span>Rolling 5 s</span>
        <span>{dpsText(view?.statistics.rolling5sDps)}</span>
      </div>
      {(encounter?.roles ?? view?.roles ?? []).map((role) => (
        <div key={`${role.playerId}-${role.roleId}`} className="flex justify-between text-xs">
          <span>{roleLabel(role)}</span>
          <span>{numberText(role.damageDealt)}</span>
        </div>
      ))}
      <div className="flex justify-between text-xs text-white/60">
        <span data-field="sequence">#{snapshot?.sequence ?? 0}</span>
        <span data-field="damage-dealt">{numberText(view?.statistics.damageDealt)}</span>
      </div>
    </div>
  )
}
