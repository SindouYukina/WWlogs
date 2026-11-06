import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, Collapse, SettingsSection } from "@/design"
import { fontLicenses, type FontLicense } from "@/lib/font-licenses"

function FontLicenseCard({ font, openLink }: { font: FontLicense; openLink: (url: string) => Promise<void> }) {
  const { t } = useTranslation("settings")
  const [open, setOpen] = useState(false)
  const [linkFailed, setLinkFailed] = useState(false)
  const follow = (url: string) => {
    setLinkFailed(false)
    openLink(url).catch(() => setLinkFailed(true))
  }
  return <article className="ww-license" data-field={`license-${font.id}`}>
    <h3 className="ww-license-name">{font.name}</h3>
    <dl className="ww-license-facts">
      <dt>{t("licenses.usage")}</dt><dd>{t(`licenses.usageOf.${font.id}`)}</dd>
      <dt>{t("licenses.faces")}</dt><dd lang="en">{font.faces}</dd>
      <dt>{t("licenses.version")}</dt><dd className="ww-num">{font.version}</dd>
      <dt>{t("licenses.authors")}</dt><dd lang="en">{font.authors}</dd>
      <dt>{t("licenses.copyright")}</dt><dd lang="en" data-field={`license-notice-${font.id}`}>{font.notice}</dd>
      <dt>{t("licenses.license")}</dt><dd lang="en">{font.licenseName}</dd>
      <dt>{t("licenses.links")}</dt>
      <dd className="ww-license-links">{font.links.map(link => <Button key={link.url} size="sm" variant="ghost" title={link.url} data-license-link={link.url} onClick={() => follow(link.url)}>{t(`licenses.link.${link.kind}`)}</Button>)}</dd>
    </dl>
    {linkFailed && <p className="ww-settings-notice" role="alert">{t("licenses.linkFailed")}</p>}
    <Button size="sm" aria-expanded={open} data-field={`license-toggle-${font.id}`} onClick={() => setOpen(!open)}>{t(open ? "licenses.hideText" : "licenses.showText")}</Button>
    <Collapse open={open}><pre className="ww-license-text" lang="en" tabIndex={open ? 0 : -1} data-field={`license-text-${font.id}`}>{font.text}</pre></Collapse>
  </article>
}

/** Shows what the executable ships. Candidate fonts of the Development comparison tool are never listed here. */
export function LicensesPage({ openLink }: { openLink: (url: string) => Promise<void> }) {
  const { t } = useTranslation("settings")
  return <div data-field="licenses">
    <SettingsSection title={t("sections.fontLicenses")}>
      <p className="ww-license-intro">{t("licenses.intro")}</p>
      {fontLicenses.map(font => <FontLicenseCard key={font.id} font={font} openLink={openLink} />)}
    </SettingsSection>
  </div>
}
