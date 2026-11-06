# WWlogs Design System

One design system serves the Desktop window, the Combat Overlay and the Effects Overlay in both build profiles.

| Path | Responsibility |
| --- | --- |
| `tokens.css` | Every token, derived from a small set of theme inputs: colors, type scales, spacing, radii, surfaces, shadows and alpha steps. |
| `themes.css` | The theme inputs for the four official themes. Status and character series colors are never overridden here. |
| `themes/` | The theme ID registry and pre-processed character art. |
| `typography.css`, `fonts/` | Bundled WOFF2 fonts, language stacks, and the Display, UI and Data text roles. |
| `motion/tokens.ts` | The only place for durations, curves, spring parameters and per-theme motion flavors. They are installed as `--ww-*` CSS variables. |
| `appearance.ts` | Applies the persisted theme and motion preference to a window. |
| `motion/spring.ts` | Pure motion math: spring-to-`linear()` conversion, the counter follower, update planning and list presence. |
| `motion/ticker.ts` | The single JavaScript animation clock, used by text counters only. |
| `components.css` | Component styles. Values come from tokens. |
| `views.css` | Layout of the product views: the Desktop window, the Dashboard and the Overlay windows. |
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

## Themes

A theme is a set of tokens, not a set of components: palette, surfaces, ornament, corner scale, chart grid, ambient background and motion flavor. Add a theme by adding its inputs to `themes.css`, its ID to `themes/registry.ts` and `validTheme` in `internal/desktop/settings.go`, its art under `themes/art/`, its motion flavor, and its names to every locale's `settings.json`. Art is pre-processed outside the product (blur, masking, desaturation), so the page paints one static image. Character series colors follow the character in every theme, and data and rank springs are not themed.

## Typography

The product typeface is Inter with Noto Sans CJK, in both build profiles and on every surface. The Typography Showcase compares other candidates for development; it is not a product setting. The Licenses page in Settings lists exactly the fonts in `fonts/`, so a bundled font and its license file change together.

Use the roles `.ww-type-display`, `.ww-type-ui`, `.ww-type-small` and `.ww-type-data` (or `.ww-num`). Fonts come from the executable; the system is used only for glyphs outside the bundled subset, through the language's own system font so regional forms stay correct. `-webkit-font-smoothing` has no effect on Windows and must not be added. Avoid text filters and scale transforms on text. Licenses for bundled fonts are in `fonts/LICENSES/` and must accompany any release.

## Listening status

`ListeningStatus` is the single status display. Its input is `Snapshot.listening` from `internal/service/listening.go`, a classification that contains no key material. Status colors are independent of themes, and the component plays one settle animation per state change and never loops.

## Product views and data providers

`../views/` holds the one implementation of each product surface: `DashboardView`, `SettingsView`, `CombatOverlayPanel` and `EffectsOverlayPanel`, in `DesktopApp` and `OverlayWindow`. They read `CombatData` (`../lib/combat-data.ts`) and `SettingsController` (`../lib/settings-controller.ts`) and do not know where the values come from. The Live provider fills them from the Go host. A field with no backend source stays absent, and the view states that it is unavailable rather than showing a number.

Each window runs its own UI state and reads the one result the engine published; a hidden window holds no long-poll. The Go host owns Overlay window state (`internal/desktop/overlay.go`) and publishes it in the snapshot, so all windows agree and a change wakes them at once.

## Motion Showcase (Development only)

The Showcase lives in `../showcase/`. It adds a Mock provider, demo controls, a Typography Lab and frame statistics around the same product views. It has no copy of them. Public frontend builds define `__WWLOGS_SHOWCASE__` as false, so they contain neither its entry point nor its chunk, and `scripts/frontend.ps1` fails a Public build that does.

Build and open it in the real WebView2 host. `-asset-root` is an explicit path to a directory that contains `game/`, normally `assets`; without it, components show their fallbacks. Public executables carry the same images through the same resolver (`assets/README.md`).

```powershell
pwsh -NoProfile -File scripts/build.ps1 -Profile Development -Target Desktop
work/dist/WWlogs-Desktop-dev.exe -showcase -asset-root <directory>
```

Pass `-font-root <directory>` separately to load local typeface candidates. The root contains `manifest.json` and one directory per scheme; it must not be placed under `assets/`. Without it, only the bundled scheme and system baseline can be compared. The Typography section reports each scheme as Loaded, Partial coverage, Missing, Load failed or No font root, based on the file requests, the browser's decode and the manifest's character ranges. A CSS declaration or `document.fonts.check()` is not evidence that a font was used, so a scheme that did not load shows no specimen and is not applied to the page. Add `-showcase-overlays` to also open its real transparent Overlay windows. For fast iteration without the Go host, run `npm run dev` in `frontend/`, set `WWLOGS_ASSET_ROOT` for game images and `WWLOGS_FONT_ROOT` to `work/fonts/candidates`, and open `http://localhost:9245/?view=showcase`.
