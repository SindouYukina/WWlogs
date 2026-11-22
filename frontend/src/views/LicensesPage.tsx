import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Button, Collapse, SettingsRow, SettingsSection } from "@/design"
import { fontLicenses, type FontLicense } from "@/lib/font-licenses"
import { gameMaterial, productLicense, sourceRepository } from "@/lib/legal-notices"

type OpenLink = (url: string) => Promise<void>

/** Hands a listed address to the browser and remembers whether that failed, so the page can say so. */
function useLinkOpener(openLink: OpenLink) {
  const [failed, setFailed] = useState(false)
  const follow = (url: string) => {
    setFailed(false)
    openLink(url).catch(() => setFailed(true))
  }
  return [follow, failed] as const
}

function FontLicenseCard({ font, openLink }: { font: FontLicense; openLink: OpenLink }) {
  const { t } = useTranslation("settings")
  const [open, setOpen] = useState(false)
  const [follow, linkFailed] = useLinkOpener(openLink)
  return <article className="ww-license" data-field={`license-${font.id}`}>
    <h3 className="ww-license-name">{font.name}</h3>
    <dl className="ww-license-facts">
      <dt>{t("licenses.usage")}</dt><dd>{t(`licenses.usageOf.${font.id}`)}</dd>
      <dt>{t("licenses.faces")}</dt><dd lang="en">{font.faces}</dd>
      <dt>{t("licenses.version")}</dt><dd className="ww-num">{font.version}</dd>
      <dt>{t("licenses.authors")}</dt><dd lang="en">{font.authors}</dd>
      <dt>{t("licenses.copyright")}</dt><dd lang="en" className="ww-selectable" data-field={`license-notice-${font.id}`}>{font.notice}</dd>
      <dt>{t("licenses.license")}</dt><dd lang="en">{font.licenseName}</dd>
      <dt>{t("licenses.links")}</dt>
      <dd className="ww-license-links">{font.links.map(link => <Button key={link.url} size="sm" variant="ghost" title={link.url} data-license-link={link.url} onClick={() => follow(link.url)}>{t(`licenses.link.${link.kind}`)}</Button>)}</dd>
    </dl>
    {linkFailed && <p className="ww-settings-notice" role="alert">{t("licenses.linkFailed")}</p>}
    <Button size="sm" aria-expanded={open} data-field={`license-toggle-${font.id}`} onClick={() => setOpen(!open)}>{t(open ? "licenses.hideText" : "licenses.showText")}</Button>
    <Collapse open={open}><pre className="ww-license-text ww-selectable" lang="en" tabIndex={open ? 0 : -1} data-field={`license-text-${font.id}`}>{font.text}</pre></Collapse>
  </article>
}

/** Who holds the rights to the game material WWlogs shows, and that WWlogs' own license does not cover it. */
function GameMaterialNotice({ openLink }: { openLink: OpenLink }) {
  const { t } = useTranslation("settings")
  const [follow, linkFailed] = useLinkOpener(openLink)
  const names = { holder: gameMaterial.holder, spdx: productLicense.spdx }
  const site = gameMaterial.officialSite.url
  return <SettingsSection title={t("sections.gameMaterial")}>
    <div className="ww-license-notice" data-field="license-game-material">
      <p className="ww-license-statement">{t("licenses.game.owner", names)}</p>
      <p className="ww-license-statement">{t("licenses.game.scope")}</p>
      <p className="ww-license-statement">{t("licenses.game.license", names)}</p>
      <p className="ww-license-statement">{t("licenses.game.unofficial", names)}</p>
      <Button size="sm" variant="ghost" title={site} data-license-link={site} onClick={() => follow(site)}>{t("licenses.game.officialSite")}</Button>
      {linkFailed && <p className="ww-settings-notice" role="alert">{t("licenses.linkFailed")}</p>}
    </div>
  </SettingsSection>
}

/** WWlogs' own license on the About page: its name and SPDX identifier, the source repository and the full license text. */
export function SoftwareLicenseSection({ openLink }: { openLink: OpenLink }) {
  const { t } = useTranslation("settings")
  const [open, setOpen] = useState(false)
  const [follow, linkFailed] = useLinkOpener(openLink)
  const repository = sourceRepository.url
  return <SettingsSection title={t("sections.softwareLicense")}>
    <p className="ww-license-intro">{t("softwareLicense.summary", { license: productLicense.name })}</p>
    <SettingsRow title={t("softwareLicense.name")}><span lang="en" data-field="app-license">{productLicense.name}</span></SettingsRow>
    <SettingsRow title={t("softwareLicense.spdx")}><span className="ww-selectable" lang="en" data-field="app-license-spdx">{productLicense.spdx}</span></SettingsRow>
    <SettingsRow title={t("softwareLicense.source")}>
      <span className="ww-row-pair">
        <span className="ww-selectable" lang="en" data-field="app-source">{repository}</span>
        <Button size="sm" variant="ghost" title={repository} data-license-link={repository} onClick={() => follow(repository)}>{t("softwareLicense.repository")}</Button>
      </span>
    </SettingsRow>
    {linkFailed && <p className="ww-settings-notice" role="alert">{t("licenses.linkFailed")}</p>}
    <SettingsRow title={t("softwareLicense.text")} description={t("softwareLicense.textHint")}>
      <Button size="sm" aria-expanded={open} data-field="app-license-toggle" onClick={() => setOpen(!open)}>{t(open ? "licenses.hideText" : "licenses.showText")}</Button>
    </SettingsRow>
    <Collapse open={open}><div className="ww-row-block"><pre className="ww-license-text ww-selectable" lang="en" tabIndex={open ? 0 : -1} data-field="app-license-text">{productLicense.text}</pre></div></Collapse>
  </SettingsSection>
}

/** Shows what the executable ships. Candidate fonts of the Development comparison tool are never listed here. */
export function LicensesPage({ openLink }: { openLink: OpenLink }) {
  const { t } = useTranslation("settings")
  return <div data-field="licenses">
    <GameMaterialNotice openLink={openLink} />
    <SettingsSection title={t("sections.fontLicenses")}>
      <p className="ww-license-intro">{t("licenses.intro")}</p>
      {fontLicenses.map(font => <FontLicenseCard key={font.id} font={font} openLink={openLink} />)}
    </SettingsSection>
  </div>
}
