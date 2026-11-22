import type { ThemeId } from "./registry.ts"
import aero from "./art/aero.webp"
import aeroThumb from "./art/aero-thumb.webp"
import electro from "./art/electro.webp"
import electroThumb from "./art/electro-thumb.webp"
import fusion from "./art/fusion.webp"
import fusionThumb from "./art/fusion-thumb.webp"
import glacio from "./art/glacio.webp"
import glacioThumb from "./art/glacio-thumb.webp"
import havoc from "./art/havoc.webp"
import havocThumb from "./art/havoc-thumb.webp"
import spectro from "./art/spectro.webp"
import spectroThumb from "./art/spectro-thumb.webp"

// Static, pre-processed art. Blur, masking and desaturation were done at asset build time, so the page paints one image.
export const themeArt: Readonly<Record<ThemeId, string>> = { electro, aero, havoc, glacio, fusion, spectro }
export const themeThumb: Readonly<Record<ThemeId, string>> = { electro: electroThumb, aero: aeroThumb, havoc: havocThumb, glacio: glacioThumb, fusion: fusionThumb, spectro: spectroThumb }
