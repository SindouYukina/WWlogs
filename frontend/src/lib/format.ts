import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import type { CombatRowModel } from "./combat-view"

export function useFormatters() {
  const { i18n } = useTranslation()
  const locale = i18n.language
  return useMemo(() => {
    const integer = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 })
    const compact = new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 })
    const percent = new Intl.NumberFormat(locale, { style: "percent", minimumFractionDigits: 1, maximumFractionDigits: 1 })
    return {
      integer: (value: number) => integer.format(value),
      compact: (value: number) => compact.format(value),
      percent: (value: number) => percent.format(value),
      seconds: (value: number) => value.toFixed(1),
      clock: (seconds: number | undefined) => seconds === undefined ? "—" : `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(Math.floor(seconds % 60)).padStart(2, "0")}`,
    }
  }, [locale])
}

const elementColors: Record<number, string> = {
  1: "var(--ww-element-glacio)",
  2: "var(--ww-element-fusion)",
  3: "var(--ww-element-electro)",
  4: "var(--ww-element-aero)",
  5: "var(--ww-element-spectro)",
  6: "var(--ww-element-havoc)",
}

/** A series color follows the character's Master element, never its rank. An unknown element stays neutral. */
export function elementColor(elementId: number | undefined): string {
  return elementColors[elementId ?? -1] ?? "var(--ww-element-unknown)"
}

/** The display name a row carries from the Master projection. A missing name shows UNKNOWN with the raw ID, never a guess. */
export function useRowNames() {
  const { t } = useTranslation("common")
  return useMemo(() => (row: Pick<CombatRowModel, "kind" | "roleId" | "name">) => {
    if (row.kind === "vehicle") return t("motorcycle")
    return row.name ?? t("unknownRole", { id: row.roleId })
  }, [t])
}
