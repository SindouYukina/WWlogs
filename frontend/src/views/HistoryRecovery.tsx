import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, Icon } from "@/design"
import type { HistoryRecoveryResult } from "@/lib/history"

export type RecoveryKind = "retry" | "rebuild"

/**
 * A Combat History that cannot be opened, as one neutral prompt: why it cannot be opened is the host's to keep and never
 * reaches the user. Trying again is always offered. Deleting the history and starting a new one is offered only when the host
 * says a new one would solve it (rebuildable), and runs only after a second confirmation that says what is lost.
 */
export function HistoryRecovery({ rebuildable, retry, rebuild, onRecovered }: {
  rebuildable: boolean
  retry: () => Promise<HistoryRecoveryResult>
  rebuild: () => Promise<HistoryRecoveryResult>
  onRecovered: (kind: RecoveryKind) => void
}) {
  const { t } = useTranslation("history")
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState<RecoveryKind | null>(null)
  const [failed, setFailed] = useState<RecoveryKind | null>(null)
  const run = (kind: RecoveryKind) => {
    setBusy(kind)
    setFailed(null)
    const request = kind === "retry" ? retry() : rebuild()
    request.then(result => {
      if (result.open) {
        setConfirming(false)
        onRecovered(kind)
      } else {
        setFailed(kind)
      }
    }).catch(() => setFailed(kind)).finally(() => {
      setBusy(null)
      if (kind === "rebuild") setConfirming(false)
    })
  }
  return <div className="ww-history-recovery" role="alert" data-field="history-recovery" data-rebuildable={rebuildable} data-confirming={confirming}>
    <div className="ww-history-recovery-head">
      <Icon name="alert" size={18} />
      <div className="ww-history-recovery-text">
        <h3>{t("recovery.title")}</h3>
        <p>{t(rebuildable ? "recovery.body" : "recovery.bodyRetry")}</p>
      </div>
    </div>
    {!confirming
      ? <div className="ww-history-recovery-actions">
        <Button size="sm" variant="primary" disabled={busy !== null} data-field="history-recovery-retry" onClick={() => run("retry")}>{t(busy === "retry" ? "recovery.retrying" : "recovery.retry")}</Button>
        {rebuildable && <Button size="sm" variant="ghost" disabled={busy !== null} data-field="history-recovery-rebuild" onClick={() => { setFailed(null); setConfirming(true) }}>{t("recovery.rebuild")}</Button>}
      </div>
      : <div className="ww-history-confirm" role="group" aria-label={t("recovery.confirmTitle")} data-field="history-recovery-confirm">
        <div className="ww-history-recovery-text">
          <h4>{t("recovery.confirmTitle")}</h4>
          <p>{t("recovery.confirmBody")}</p>
        </div>
        <div className="ww-history-confirm-actions">
          <Button size="sm" disabled={busy !== null} data-field="history-recovery-cancel" onClick={() => setConfirming(false)}>{t("recovery.cancel")}</Button>
          <Button size="sm" variant="danger" disabled={busy !== null} data-field="history-recovery-confirm-rebuild" onClick={() => run("rebuild")}>{t(busy === "rebuild" ? "recovery.rebuilding" : "recovery.confirm")}</Button>
        </div>
      </div>}
    {failed && <p className="ww-history-note" data-tone="critical" role="status" data-field="history-recovery-failed" data-kind={failed}>{t(failed === "rebuild" ? "recovery.rebuildFailed" : "recovery.retryFailed")}</p>}
  </div>
}
