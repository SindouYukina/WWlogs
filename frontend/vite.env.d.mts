import type { Plugin } from "vite"

export function showcaseEnabled(command: string): boolean
export function productLicenseText(): string
export function libraryLicenseGaps(moduleIds: Iterable<string>, components: ReadonlyArray<{ group: string; name: string; generated?: boolean }>): { unlisted: string[]; unbundled: string[] }
export function libraryLicenseGuard(): Plugin
export function previewGameAssets(): Plugin
