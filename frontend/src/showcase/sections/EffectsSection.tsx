import { useTranslation } from "react-i18next"
import { Button, Chip } from "@/design"
import { EffectsOverlayPanel } from "@/views/EffectsOverlayPanel"
import type { FeedControls } from "../useMockFeed"

/** The production Effects Overlay with scripted events beside it. Only the controls belong to the Showcase. */
export function EffectsSection({ controls }: { controls: FeedControls }) {
  const { t } = useTranslation("showcase")
  const { apply, paused, setPaused } = controls
  return <div className="sc-demo">
    <div className="sc-stage" data-backdrop="effects">
      <div className="sc-stage-overlay" style={{ width: 300 }}><EffectsOverlayPanel data={controls.data} /></div>
    </div>
    <aside className="sc-controls">
      <h3>{t("controls.effectEvents")}</h3>
      <div className="sc-button-grid">
        <Button size="sm" variant="primary" onClick={() => apply(feed => { feed.addEffect("timed") })}>{t("controls.addEffect")}</Button>
        <Button size="sm" onClick={() => apply(feed => { feed.addEffect("unknown") })}>{t("controls.addUnknown")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.refreshEffect())}>{t("controls.refresh")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.stackEffect())}>{t("controls.stack")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.removeEffect())}>{t("controls.remove")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.addMany(8))}>{t("controls.addMany")}</Button>
        <Button size="sm" onClick={() => apply(feed => feed.clearEffects())}>{t("controls.clear")}</Button>
        <Button size="sm" onClick={() => setPaused(!paused)}>{t(paused ? "controls.resume" : "controls.stall")}</Button>
      </div>
      <div className="sc-chip-row"><Chip tone="warn">{t("effects.noFakeNames")}</Chip></div>
      <p className="sc-note">{t("effects.note")}</p>
    </aside>
  </div>
}
