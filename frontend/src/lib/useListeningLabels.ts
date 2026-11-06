import { useCallback } from "react"
import { useTranslation } from "react-i18next"
import type { ThemeId } from "@/design/themes/registry.ts"
import type { ListeningInfo } from "./snapshot"

/** Turns the host's classification into translated text. An unknown reason from a newer host shows the state alone. */
export function useListeningLabels() {
  const { t, i18n } = useTranslation("common")
  return useCallback((listening: ListeningInfo) => {
    const reasonKey = `listening.reason.${listening.reason}`
    return { label: t(`listening.state.${listening.state}`), detail: i18n.exists(reasonKey, { ns: "common" }) ? t(reasonKey) : undefined }
  }, [t, i18n])
}

export function useThemeDescriptions() {
  const { t } = useTranslation("settings")
  return useCallback((theme: ThemeId) => ({ name: t(`themes.${theme}.name`), character: t(`themes.${theme}.character`) }), [t])
}
