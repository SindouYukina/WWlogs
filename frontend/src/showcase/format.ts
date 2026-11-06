import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import type { CombatRowModel } from "@/lib/combat-view"
import { masterRoles } from "./master-fixture.ts"

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

export function roleElement(roleId: number | undefined): number | undefined {
  return masterRoles.find(role => role.roleId === roleId)?.elementId
}

const elementColor: Record<number, string> = {
  1: "var(--ww-element-glacio)",
  2: "var(--ww-element-fusion)",
  3: "var(--ww-element-electro)",
  4: "var(--ww-element-aero)",
  5: "var(--ww-element-spectro)",
  6: "var(--ww-element-havoc)",
}

export function roleColor(roleId: number | undefined): string {
  return elementColor[roleElement(roleId) ?? -1] ?? "var(--ww-element-unknown)"
}

/** Master names by locale. A missing name falls back to the same UNKNOWN form the production views use. */
export function useRoleNames() {
  const { t, i18n } = useTranslation(["common"])
  const locale = i18n.language
  return useMemo(() => (row: Pick<CombatRowModel, "kind" | "roleId">) => {
    if (row.kind === "vehicle") return t("motorcycle", { ns: "common" })
    const role = masterRoles.find(candidate => candidate.roleId === row.roleId)
    const names = role?.names as Readonly<Record<string, string>> | undefined
    return names?.[locale] ?? names?.en ?? t("unknownRole", { ns: "common", id: row.roleId })
  }, [t, locale])
}
