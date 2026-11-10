import { memo, useEffect, useRef, useState } from "react"
import { themeArt, themeThumb } from "../themes/art"
import { themeIds, type ThemeId } from "../themes/registry.ts"
import { Icon } from "./Icon"

export interface ThemeBackdropProps {
  theme: ThemeId
  /** "art" shows the character scene for the Dashboard; "ambient" is the faint gradient for Settings. */
  intensity: "art" | "ambient"
}

const crossfadeMs = 480

/**
 * A static layer behind content. Switching themes fades the new image over the old one once it has decoded, then drops
 * the old layer so only one image stays in memory. A settled backdrop has no animation and no repaint.
 */
export const ThemeBackdrop = memo(function ThemeBackdrop({ theme, intensity }: ThemeBackdropProps) {
  const [layers, setLayers] = useState<ThemeId[]>([theme])
  const latest = useRef(theme)
  useEffect(() => {
    latest.current = theme
    if (intensity !== "art") return
    let timer: ReturnType<typeof setTimeout> | undefined
    const image = new Image()
    image.src = themeArt[theme]
    const ready = () => {
      if (latest.current !== theme) return
      setLayers(current => current.at(-1) === theme ? current : [...current.slice(-1), theme])
      timer = setTimeout(() => setLayers(current => current.slice(-1)), crossfadeMs)
    }
    image.decode().then(ready, ready)
    return () => {
      if (timer) clearTimeout(timer)
    }
  }, [theme, intensity])
  if (intensity === "ambient") return <div className="ww-backdrop" data-intensity="ambient" aria-hidden="true" />
  return <div className="ww-backdrop" data-intensity="art" aria-hidden="true">
    {layers.map(layer => <div key={layer} className="ww-backdrop-art" style={{ backgroundImage: `url("${themeArt[layer]}")` }} />)}
    <div className="ww-backdrop-shade" />
  </div>
})

export interface ThemePickerProps {
  value: ThemeId
  onChange: (theme: ThemeId) => void
  label: string
  /** Theme display name and character name, both from locale resources. */
  describe: (theme: ThemeId) => { name: string; character: string }
}

/** Selection applies at once, so the preview is the whole window. */
export function ThemePicker({ value, onChange, label, describe }: ThemePickerProps) {
  return <div className="ww-theme-picker" role="radiogroup" aria-label={label}>
    {themeIds.map(theme => {
      const { name, character } = describe(theme)
      return <button key={theme} type="button" role="radio" aria-checked={theme === value} className="ww-theme-card" data-theme-preview={theme} onClick={() => onChange(theme)}>
        <span className="ww-theme-card-art" style={{ backgroundImage: `url("${themeThumb[theme]}")` }}>
          {theme === value && <span className="ww-theme-card-check"><Icon name="check" size={13} /></span>}
        </span>
        <span className="ww-theme-card-text"><strong>{name}</strong><small>{character}</small></span>
        <span className="ww-theme-card-swatches" aria-hidden="true"><i /><i /><i /></span>
      </button>
    })}
  </div>
}

export type ListeningStateName = "off" | "waiting" | "establishing" | "active" | "stale" | "error" | "closed"

export interface ListeningStatusProps {
  state: ListeningStateName
  label: string
  /** The reason in plain language; shown in full form and as the tooltip in compact form. */
  detail?: string
  variant?: "full" | "compact" | "dot"
}

const statusIcons: Partial<Record<ListeningStateName, "alert" | "check" | "clock" | "pause" | "bolt">> = {
  active: "check",
  stale: "clock",
  error: "alert",
  establishing: "bolt",
}

/**
 * One status display for every window. A change of state plays one settle animation, restarted by remounting the ring;
 * nothing here loops, so a healthy session draws no frames. Color comes from the status palette, never the theme.
 */
export const ListeningStatus = memo(function ListeningStatus({ state, label, detail, variant = "full" }: ListeningStatusProps) {
  const icon = statusIcons[state]
  return <span className="ww-listening" data-state={state} data-variant={variant} role="status" title={detail ? `${label} · ${detail}` : label}>
    <span className="ww-listening-mark" aria-hidden="true">
      <span key={state} className="ww-listening-ring" />
      {variant !== "dot" && icon ? <Icon name={icon} size={variant === "compact" ? 10 : 12} /> : <span className="ww-listening-dot" />}
    </span>
    {variant !== "dot" && <span className="ww-listening-label">{label}</span>}
    {variant === "full" && detail && <span className="ww-listening-detail">{detail}</span>}
  </span>
})
