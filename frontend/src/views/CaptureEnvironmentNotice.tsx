import { useTranslation } from "react-i18next"
import { Button } from "@/design"
import { environmentNotice } from "@/lib/capture-environment"
import type { SettingsController } from "@/lib/settings-controller"

/** Says why capture cannot start, with the action that fixes it right beside the message. */
export function CaptureEnvironmentNotice({ controller }: { controller: SettingsController }) {
  const { t } = useTranslation("dashboard")
  const notice = environmentNotice(controller.environment)
  if (!notice) return null
  const { info, resolving, opening, opened, openFailed } = controller.npcapDownload
  const unconfirmed = info !== null && !info.direct
  const detail = !notice.offersDownload ? null
    : openFailed ? t("environment.openFailed")
    : opened ? t("environment.afterOpen")
    : unconfirmed ? t("environment.sourceUnconfirmed")
    : info?.version ? t("environment.latestVersion", { version: info.version })
    : resolving ? t("environment.resolving")
    : null
  return <div className="ww-env-notice" role="alert" data-field="capture-environment" data-state={controller.environment}>
    <div className="ww-env-notice-text">
      <strong data-field="environment-title">{t(`environment.${notice.key}.title`)}</strong>
      <span>{t(`environment.${notice.key}.body`)}</span>
      {detail && <span className="ww-env-notice-detail" data-field="environment-detail">{detail}</span>}
    </div>
    <div className="ww-env-notice-actions">
      {notice.offersDownload && <Button size="sm" variant="primary" disabled={opening} data-field="npcap-download" onClick={controller.openNpcapDownload}>{t(opening ? "environment.opening" : unconfirmed ? "environment.openPage" : "environment.download")}</Button>}
      <Button size="sm" disabled={controller.checking} data-field="environment-recheck" onClick={controller.checkEnvironment}>{t(controller.checking ? "environment.checking" : "environment.recheck")}</Button>
    </div>
  </div>
}
