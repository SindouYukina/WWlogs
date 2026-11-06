import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { BuffStatusItem, Button, Chip, StackList } from "@/design"
import { effectRow, type EffectRowModel } from "@/lib/combat-view"
import { effectRemaining, relevantEffect, snapshotStale } from "@/lib/effects"
import type { Effect } from "@/lib/snapshot"
import type { FeedControls } from "../useMockFeed"
import { useFormatters } from "../format"
import { masterEffects, masterRoles } from "../master-fixture.ts"

interface EffectView {
  effect: Effect
  row: EffectRowModel
}

const keyOf = (item: EffectView) => item.effect.key

/** Rows come from the same trust gate as production: a stale or suspect snapshot yields no countdown. */
export function EffectsPanel({ controls }: { controls: FeedControls }) {
  const { t, i18n } = useTranslation(["showcase", "overlay"])
  const format = useFormatters()
  const { snapshot } = controls.output
  const connection = "connected" as const
  const stale = snapshotStale(snapshot, controls.now) || controls.paused
  const items = useMemo<EffectView[]>(() => (snapshot.view?.effects.active ?? []).filter(relevantEffect).map(effect => {
    const remaining = stale ? undefined : effectRemaining(effect, snapshot, connection, controls.now)
    return { effect, row: { ...effectRow(effect, remaining), stale } }
  }), [snapshot, stale, controls.now])
  const holder = (effect: Effect) => {
    const names = masterRoles.find(role => role.roleId === effect.holderRoleId)?.names as Readonly<Record<string, string>> | undefined
    return names?.[i18n.language] ?? names?.en
  }
  const term = (effect: Effect) => {
    const master = masterEffects.find(candidate => candidate.effectId === effect.effectId)
    const names = master && "termNames" in master ? master.termNames as Readonly<Record<string, string>> : undefined
    return names?.[i18n.language] ?? names?.en
  }
  return <div className="ww-effects">
    <div className="ww-effects-head"><span>{t("overlay:effects")}</span><span className="ww-num">{items.length}</span></div>
    {items.length === 0 && <div className="ww-combat-empty" style={{ background: "var(--ww-surface-overlay)", borderRadius: "var(--ww-radius-md)" }}>{t("overlay:noEffects")}</div>}
    <StackList items={items} keyOf={keyOf} rowHeight={38} gap={5} renderItem={({ effect, row }) => {
      // Correlated glossary terms are shown as a related term beside the raw ID, never as the effect's confirmed name.
      const related = effect.termStatus === "CORRELATED" ? term(effect) : undefined
      const meta = [related && t("effects.related", { term: related }), holder(effect)].filter(Boolean).join(" · ")
      const countdown = row.countdown
      return <BuffStatusItem effect={row} title={`#${effect.effectId}`} unnamed meta={meta} tickMs={controls.publishMs} remainingText={countdown.kind === "unknown" ? "—" : format.seconds(countdown.remaining)} />
    }} />
  </div>
}

export function EffectsSection({ controls }: { controls: FeedControls }) {
  const { t } = useTranslation("showcase")
  const { apply, paused, setPaused } = controls
  return <div className="sc-demo">
    <div className="sc-stage" data-backdrop="effects">
      <div className="sc-stage-overlay" style={{ width: 300 }}><EffectsPanel controls={controls} /></div>
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
