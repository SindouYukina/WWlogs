import { memo } from "react"

// A small inline icon set. An icon library would add a dependency for fewer than twenty glyphs.
const paths = {
  chevron: "M6 9l6 6 6-6",
  check: "M5 12.5l4.5 4.5L19 7.5",
  info: "M12 21a9 9 0 100-18 9 9 0 000 18zM12 11v5M12 8h.01",
  sliders: "M4 7h10M18 7h2M14 5v4M4 17h2M10 17h10M6 15v4",
  overlay: "M4 6a2 2 0 012-2h12a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2zM8 9h8M8 13h5",
  spark: "M12 3v5M12 16v5M3 12h5M16 12h5M6 6l3 3M15 15l3 3M18 6l-3 3M9 15l-3 3",
  target: "M12 20a8 8 0 100-16 8 8 0 000 16zM12 15a3 3 0 100-6 3 3 0 000 6zM12 2v3M12 19v3M2 12h3M19 12h3",
  link: "M10 14a4 4 0 005.7 0l3-3a4 4 0 00-5.7-5.7l-1 1M14 10a4 4 0 00-5.7 0l-3 3a4 4 0 005.7 5.7l1-1",
  capture: "M4 12h3l2-6 4 12 3-9 1 3h3",
  palette: "M12 3a9 9 0 100 18c1.5 0 2-1 2-2s-1-2 0-3 2-.5 3-.5c2 0 4-1.5 4-3.5 0-5-4-9-9-9zM7.5 11h.01M10 7.5h.01M15 8h.01",
  globe: "M12 21a9 9 0 100-18 9 9 0 000 18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18",
  license: "M7 3h7l4 4v14H7zM14 3v4h4M10 12h5M10 16h5",
  about: "M12 21a9 9 0 100-18 9 9 0 000 18zM12 16v-5M12 8h.01",
  grid: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
  chart: "M4 20V4M4 20h16M8 16l3-5 3 3 5-7",
  alert: "M12 4l9 16H3zM12 10v4M12 17h.01",
  clock: "M12 21a9 9 0 100-18 9 9 0 000 18zM12 7v5l3 2",
  bolt: "M13 3L5 14h6l-1 7 8-11h-6z",
  shield: "M12 3l7 3v5c0 5-3 8-7 10-4-2-7-5-7-10V6z",
  list: "M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01",
  question: "M9 9a3 3 0 115 2c-1 1-2 1.5-2 3M12 18h.01",
  play: "M7 5l12 7-12 7z",
  pause: "M8 5v14M16 5v14",
  reset: "M4 12a8 8 0 108-8H7M7 4L4 7l3 3",
} as const

export type IconName = keyof typeof paths

export const Icon = memo(function Icon({ name, size = 18 }: { name: IconName; size?: number }) {
  return <svg className="ww-icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true"><path d={paths[name]} /></svg>
})
