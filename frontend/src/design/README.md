# WWlogs Design System

One design system serves the Desktop window, the Combat Overlay and the Effects Overlay in both build profiles.

| Path | Responsibility |
| --- | --- |
| `tokens.css` | The only place for raw colors, type scales, spacing, radii, surfaces, shadows and alpha steps. |
| `motion/tokens.ts` | The only place for durations, curves and spring parameters. They are installed as `--ww-*` CSS variables at startup. |
| `motion/spring.ts` | Pure motion math: spring-to-`linear()` conversion, the counter follower, update planning and list presence. |
| `motion/ticker.ts` | The single JavaScript animation clock, used by text counters only. |
| `components.css` | Component styles. Values come from tokens. |
| `components/` | Shared React components. They take display text and engine-derived values as props. |
| `../lib/combat-view.ts` | Adapts engine snapshots to row models without adding information. |

## Rules

- Do not add raw colors or literal timings to components. Add or change a token.
- Do not add a runtime animation library, icon library or chart library without bundle and WebView2 measurements.
- High-frequency motion animates `transform` and `opacity` only. Layout-affecting transitions are limited to user-triggered changes such as shared indicators and section expansion.
- A settled window must schedule no work: no infinite animations except a bounded expiry cue, and no frame loop while values are unchanged.
- Do not use `backdrop-filter` on Overlay surfaces. It cannot sample behind a transparent window.
- Motion presents engine values and never changes them. A counter never passes the engine value. Encounter resets, decreases and stale data snap. A countdown moves between engine values and stops when snapshots stop.
- Do not invent names, polarity, timers or identities for visual completeness. Unknown values keep their raw IDs and neutral styling.
- Respect `prefers-reduced-motion`; `installMotionTokens` zeroes every motion token for it.
- Development and Public builds share these components. Do not add profile-specific variants.

## Motion Showcase (Development only)

The Showcase lives in `../showcase/`. It uses mock data shaped like the engine Snapshot and the production adapter and trust-gating code. Public frontend builds define `__WWLOGS_SHOWCASE__` as false, so they contain neither its entry point nor its chunk, and `scripts/frontend.ps1` fails a Public build that does.

Build and open it in the real WebView2 host. `-asset-root` is an explicit path to a directory that contains `game/`; without it, components show their fallbacks.

```powershell
pwsh -NoProfile -File scripts/build.ps1 -Profile Development -Target Desktop
work/dist/WWlogs-Desktop-dev.exe -showcase -asset-root <directory>
```

Add `-showcase-overlays` to also open its real transparent Overlay windows. For fast iteration without the Go host, run `npm run dev` in `frontend/`, set `WWLOGS_ASSET_ROOT` to the same directory, and open `http://localhost:9245/?view=showcase`.
