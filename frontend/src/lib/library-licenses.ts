// The third-party software WWlogs executables contain, with every license file that came with each component: the
// unmodified files from its source at the pinned version, in library-licenses/<id>/, bundled into the executable so they
// can be read offline. They are never translated. library-licenses/manifest.json lists the components; tests compare it
// with go.mod, the lockfile, the modules the desktop executable links and the upstream files, and the frontend build
// fails when it bundles a package the manifest does not list (vite.env.mjs).
import manifest from "./library-licenses/manifest.json"

const texts = import.meta.glob<string>("./library-licenses/*/*", { query: "?raw", import: "default", eager: true })

export type LibraryGroup = "go" | "frontend"

export interface LibraryLicenseFile {
  name: string
  text: string
}

export interface LibraryLicense {
  id: string
  group: LibraryGroup
  /** The module path, package name or "Go", as the component names itself. */
  name: string
  /** The pinned version; empty for Go, whose standard library comes from the toolchain of the build. */
  version: string
  /** The SPDX identifier of the component's own license. */
  license: string
  /** The copyright lines of its license file. */
  notices: readonly string[]
  files: readonly LibraryLicenseFile[]
}

/** The order the Licenses page shows the groups in. */
export const libraryGroups: readonly LibraryGroup[] = ["go", "frontend"]

export const libraryLicenses: readonly LibraryLicense[] = manifest.components.map(component => ({
  id: component.id,
  group: component.group as LibraryGroup,
  name: component.name,
  version: component.version,
  license: component.license,
  notices: component.notices,
  files: component.files.map(name => ({ name, text: texts[`./library-licenses/${component.id}/${name}`] ?? "" })),
}))
