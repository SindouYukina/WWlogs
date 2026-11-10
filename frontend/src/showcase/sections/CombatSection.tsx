import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, Icon, Segmented, Slider } from "@/design"
import { displayedEncounter } from "@/lib/combat-data"
import { combatRows } from "@/lib/combat-view"
import { useRowNames } from "@/lib/format"
import { CombatOverlayPanel } from "@/views/CombatOverlayPanel"
import type { FeedControls } from "../useMockFeed"

/** The production Combat Overlay with scripted events beside it. Only the controls belong to the Showcase. */
export function CombatSection({ controls, numberMode, setNumberMode }: { controls: FeedControls; numberMode: "tween" | "roll"; setNumberMode: (mode: "tween" | "roll") => void }) {
  const { t } = useTranslation("showcase")
  const nameOf = useRowNames()
  const [alpha, setAlpha] = useState(0.74)
  const { apply, publishMs, setPublishMs, paused, setPaused } = controls
  const encounter = displayedEncounter(controls.data.snapshot?.view)
  const active = useMemo(() => (encounter ? combatRows(encounter) : []).filter(row => row.kind === "role"), [encounter])
  return <div className="sc-demo">
    <div className="sc-stage" data-backdrop="combat">
      <div className="sc-stage-overlay" style={{ width: 470 }}><CombatOverlayPanel data={controls.data} numberMode={numberMode} alpha={alpha} /></div>
    </div>
    <aside className="sc-controls">
      <h3>{t("controls.dataEvents")}</h3>
      <div className="sc-button-grid">
        {active.map(row => <Button key={row.key} size="sm" onClick={() => apply(feed => feed.burst(row.roleId ?? 0))}><Icon name="bolt" size={13} />{t("controls.burst", { name: nameOf(row) })}</Button>)}
        <Button size="sm" onClick={() => apply(feed => feed.swapRanks())}>{t("controls.swap")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.replaceRole())}>{t("controls.replaceRole")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.toggleVehicle())}>{t("controls.vehicle")}</Button>
      </div>
      <h3>{t("controls.lifecycle")}</h3>
      <div className="sc-button-grid">
        <Button size="sm" variant="primary" onClick={() => apply(feed => feed.resetEncounter())}><Icon name="reset" size={13} />{t("controls.reset")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.endEncounter())}>{t("controls.end")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.setIdle())}>{t("controls.idle")}</Button>
        <Button size="sm" onClick={() => setPaused(!paused)}><Icon name={paused ? "play" : "pause"} size={13} />{t(paused ? "controls.resume" : "controls.stall")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.toggleUntrusted())}>{t("controls.untrusted")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.toggleUnassigned())}>{t("controls.unassigned")}</Button>
      </div>
      <h3>{t("controls.publishRate")}</h3>
      <Segmented label={t("controls.publishRate")} value={String(publishMs)} options={[{ value: "100", label: "100 ms" }, { value: "250", label: "250 ms" }, { value: "500", label: "500 ms" }, { value: "1000", label: "1 s" }]} onChange={value => setPublishMs(Number(value))} />
      <h3>{t("controls.numberStyle")}</h3>
      <Segmented label={t("controls.numberStyle")} value={numberMode} options={[{ value: "tween", label: t("controls.tween") }, { value: "roll", label: t("controls.roll") }]} onChange={setNumberMode} />
      <h3>{t("controls.opacity")}</h3>
      <div className="sc-slider-row"><Slider label={t("controls.opacity")} min={20} max={100} value={Math.round(alpha * 100)} onChange={value => setAlpha(value / 100)} /><span className="ww-num">{Math.round(alpha * 100)}%</span></div>
      <p className="sc-note">{t("combat.note")}</p>
    </aside>
  </div>
}
