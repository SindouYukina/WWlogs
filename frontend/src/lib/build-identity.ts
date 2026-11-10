/**
 * Display rules for the build identity the host reports in every snapshot (Go internal/buildinfo is the only source).
 * Nothing here invents a value: a missing or unavailable field is shown as such, never replaced.
 */
export interface AppIdentity {
  version: string
  /** Official lifetime Build Number; 0 together with official false marks an unofficial build. */
  buildNumber: number
  official: boolean
  commit: string
  /** "true" when tracked sources were modified at build time, "false", or "unknown". */
  dirty: string
  profile: string
}

export type IdentityText =
  | { kind: "value"; value: string }
  | { kind: "label"; key: string }

const unavailable = "UNAVAILABLE"

/** The product version as MAJOR.MINOR.PATCH, without the tag prefix. */
export function versionText(app: AppIdentity): IdentityText {
  if (!app.version || app.version === unavailable) return { kind: "label", key: "buildUnavailable" }
  return { kind: "value", value: app.version.replace(/^v/, "") }
}

/** The official Build Number, or the unofficial label; an unofficial build never shows a number. */
export function buildNumberText(app: AppIdentity): IdentityText {
  if (!app.official || !Number.isSafeInteger(app.buildNumber) || app.buildNumber < 1) return { kind: "label", key: "buildUnofficial" }
  return { kind: "value", value: String(app.buildNumber) }
}

export function profileKey(app: AppIdentity): string | undefined {
  if (app.profile === "development") return "buildProfileDevelopment"
  if (app.profile === "public") return "buildProfilePublic"
  return undefined
}

/** The abbreviated source commit and, separately, the working-tree note that qualifies it. */
export function revisionText(app: AppIdentity): { revision: IdentityText; note?: string } {
  if (!/^[0-9a-f]{40}$/.test(app.commit)) return { revision: { kind: "label", key: "buildUnavailable" } }
  const note = app.dirty === "true" ? "buildModified" : app.dirty === "false" ? undefined : "buildStateUnknown"
  return { revision: { kind: "value", value: app.commit.slice(0, 12) }, note }
}
