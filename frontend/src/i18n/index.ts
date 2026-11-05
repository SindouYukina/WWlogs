import i18n from "i18next"
import { initReactI18next } from "react-i18next"
import manifest from "../../locales/manifest.json"

// Vite bundles approved JSON resources; no network backend, file scanner, or language detector is installed.
const modules=import.meta.glob("../../locales/*/*.json",{eager:true,import:"default"})
const resources: Record<string,Record<string,object>>={}
for(const locale of manifest.supportedLocales) {
  const namespaces: Record<string,object>={}
  for(const ns of manifest.namespaces) {
    const resource=modules[`../../locales/${locale}/${ns}.json`]
    if(!resource) throw new Error("A bundled translation resource is missing")
    namespaces[ns]=resource as object
  }
  resources[locale]=namespaces
}
void i18n.use(initReactI18next).init({resources,lng:manifest.fallbackLocale,fallbackLng:manifest.fallbackLocale,supportedLngs:manifest.supportedLocales,load:"currentOnly",ns:manifest.namespaces,defaultNS:"common",initAsync:false,interpolation:{escapeValue:false},react:{useSuspense:false}})
export { manifest }
export default i18n

export function statusText(value: string | undefined): string {
  if(!value) return i18n.t("unknown")
  const key=`state.${value}`
  return i18n.exists(key) ? i18n.t(key) : i18n.t("unknown")
}
