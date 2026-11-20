import { useTranslation } from "react-i18next"
import { contentLayerText, modeText, namedContentLayers, shownContentLayers, stageText, type HistoryContent, type HistoryMode, type HistoryName } from "@/lib/history"
import { LinkedName } from "./HistoryAnalysis"

// Where an encounter was fought, as every History view names it. Go resolves the kind of play, the stage name and the
// content layers of the instance (internal/desktop/history.go); these components label and lay them out, and never put a
// name of one layer where another belongs.

/**
 * The kind of play: a category Go named, or the open world or a content system the game gives no system name, labelled
 * here; the content system's label is marked as a WWlogs classification.
 */
export function ModeName({ mode, known = true }: { mode: HistoryMode | undefined; known?: boolean }) {
  const { t } = useTranslation("history")
  const text = known ? modeText(mode, t) : undefined
  if (!mode?.kind || !text) return <span className="ww-history-unknown">{t("unidentified")}</span>
  if (mode.kind === "content_system") {
    const note = t(`contentSystemNote.${mode.nameStatus ?? "NO_OFFICIAL_NAME"}`)
    return <span data-mode-kind={mode.kind} data-system={mode.system} title={note}>
      <span className="ww-truncate" data-status={mode.status}>{text}</span><span className="ww-history-mark" aria-label={note}>*</span>
    </span>
  }
  return <span data-mode-kind={mode.kind}><LinkedName name={{ name: text, status: mode.status }} fallback={t("unidentified")} title={t("correlatedNote")} /></span>
}

/**
 * The stage: its own name; without one, the content layers Go named, each with its dimension; else the plain word with the
 * raw instance ID.
 */
export function StageName({ name, content, instanceId, known = true }: { name: HistoryName; content?: HistoryContent; instanceId?: number; known?: boolean }) {
  const { t } = useTranslation("history")
  if (!known) return <span className="ww-history-unknown">{t("unidentified")}</span>
  const text = name.name ? undefined : stageText(name, content, t)
  if (text) {
    const correlated = namedContentLayers(content).some(layer => layer.status !== "VERIFIED")
    return <span className="ww-truncate" data-field="stage-content" title={t("stageContentNote")}>{text}{correlated && <span className="ww-history-mark" title={t("correlatedNote")} aria-label={t("correlatedNote")}>≈</span>}</span>
  }
  return <LinkedName name={name} fallback={t("unidentified")} id={instanceId} title={t("correlatedNote")} />
}

/** Every content layer the History shows, each with its dimension: a name, or how many values it cannot choose between. */
export function ContentLayers({ content }: { content?: HistoryContent }) {
  const { t } = useTranslation("history")
  const layers = shownContentLayers(content)
  if (!layers.length) return <span className="ww-history-unknown">—</span>
  return <span data-field="content-layers">{layers.map((layer, index) => <span key={layer.dimension} data-dimension={layer.dimension} data-state={layer.state}>
    {index > 0 && " · "}{contentLayerText(layer, t)}
    {layer.state === "RUNTIME_DEPENDENT" && <span className="ww-history-mark" title={t("contentLayerOpenNote")} aria-label={t("contentLayerOpenNote")}>?</span>}
    {layer.status === "CORRELATED" && <span className="ww-history-mark" title={t("correlatedNote")} aria-label={t("correlatedNote")}>≈</span>}
  </span>)}</span>
}
