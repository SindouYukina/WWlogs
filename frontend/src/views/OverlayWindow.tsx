import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/design"
import type { CombatData } from "@/lib/combat-data"
import { host } from "@/lib/host"
import type { OverlayKind } from "@/lib/snapshot"
import { useLiveCombatData } from "@/lib/useLiveCombatData"
import { CombatOverlayPanel } from "./CombatOverlayPanel"
import { EffectsOverlayPanel } from "./EffectsOverlayPanel"

export interface OverlaySurfaceProps {
  surface: OverlayKind
  data: CombatData
  /** Percent; undefined keeps the design default. */
  opacity?: number
  /** Edit Mode: the window accepts input and shows that it can be moved. */
  editing?: boolean
  /** Dragging without Edit Mode chrome, for review windows. */
  draggable?: boolean
  onFinishEditing?: () => void
  numberMode?: "tween" | "roll"
}

/** One Overlay window's content, shared by the production window and the Development review windows. */
export function OverlaySurface({ surface, data, opacity, editing = false, draggable = false, onFinishEditing, numberMode }: OverlaySurfaceProps) {
  const { t } = useTranslation("overlay")
  const canDrag = editing || draggable
  useEffect(() => {
    // Wails reads this from the computed style at mouse down, so the window cannot be dragged outside Edit Mode.
    document.body.dataset.editing = String(canDrag)
  }, [canDrag])
  const alpha = opacity === undefined ? undefined : opacity / 100
  return <div className="ww-root ww-overlay-surface" data-surface={surface} data-editing={canDrag} data-field="overlay" data-locale={data.snapshot?.locale}>
    {surface === "combat" ? <CombatOverlayPanel data={data} alpha={alpha} numberMode={numberMode} /> : <EffectsOverlayPanel data={data} alpha={alpha} fitHeight />}
    {editing && <div className="ww-overlay-edit" data-field="edit-bar">
      <span className="ww-truncate">{t("drag")}</span>
      <Button size="sm" variant="primary" onClick={onFinishEditing}>{t("finishEditing")}</Button>
    </div>}
  </div>
}

/** The production Overlay window. Its window state comes from the same host snapshot as its data. */
export function OverlayWindow({ surface }: { surface: OverlayKind }) {
  const { data } = useLiveCombatData()
  const state = data.snapshot?.overlays?.[surface]
  return <OverlaySurface surface={surface} data={data} opacity={state?.opacity} editing={state?.editing === true} onFinishEditing={() => void host.updateOverlay(surface, { editing: false })} />
}
