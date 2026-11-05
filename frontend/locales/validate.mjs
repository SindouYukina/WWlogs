import { readFileSync, readdirSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const root=dirname(fileURLToPath(import.meta.url))
const read=name=>JSON.parse(readFileSync(join(root,name),"utf8").replace(/^\uFEFF/,""))
export const manifest=read("manifest.json")
function flatten(value,prefix="",result={}) {
  for(const [key,entry] of Object.entries(value)) {
    const path=prefix ? `${prefix}.${key}` : key
    if(typeof entry==="string" && entry!=="") result[path]=entry
    else if(entry && typeof entry==="object" && !Array.isArray(entry)) flatten(entry,path,result)
    else throw Error(`Invalid translation: ${path}`)
  }
  return result
}
const parameters=text=>[...text.matchAll(/{{\s*([\w]+)\s*}}/g)].map(m=>m[1]).sort().join(",")
export function validateTranslation(baseline,values,label="translation") {
  if(Object.keys(values).sort().join()!==Object.keys(baseline).sort().join()) throw Error(`Translation keys differ: ${label}`)
  for(const key of Object.keys(baseline)) if(typeof values[key]!=="string"||values[key]===""||parameters(values[key])!==parameters(baseline[key])) throw Error(`Interpolation differs: ${label}/${key}`)
}
export function validateLocales() {
  const locales=manifest.supportedLocales
  if(!Array.isArray(locales)||new Set(locales).size!==locales.length||!locales.includes(manifest.fallbackLocale)) throw Error("Invalid language manifest")
  if(Object.keys(manifest.languages).sort().join()!==[...locales].sort().join()) throw Error("Language metadata differs from manifest")
  const directories=readdirSync(root,{withFileTypes:true}).filter(e=>e.isDirectory()).map(e=>e.name).sort()
  if(directories.join()!==[...locales].sort().join()) throw Error("Language folders differ from manifest")
  let count=0
  for(const ns of manifest.namespaces) {
    const baseline=flatten(read(`${manifest.fallbackLocale}/${ns}.json`))
    count+=Object.keys(baseline).length
    for(const locale of locales) {
      if(!manifest.languages[locale].label||!manifest.languages[locale].masterLocale) throw Error(`Missing metadata: ${locale}`)
      const values=flatten(read(`${locale}/${ns}.json`))
      validateTranslation(baseline,values,`${locale}/${ns}`)
      const files=readdirSync(join(root,locale)).filter(e=>e.endsWith(".json")).sort()
      if(files.join()!==manifest.namespaces.map(n=>n+".json").sort().join()) throw Error(`Unexpected namespaces: ${locale}`)
    }
  }
  return {locales:locales.length,namespaces:manifest.namespaces.length,keys:count}
}
console.log("Bundled localization validation: PASS",JSON.stringify(validateLocales()))
