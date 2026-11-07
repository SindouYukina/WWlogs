# Windows build

Use the Go toolchain specified in `go.mod` and the exact Node.js and npm versions in `frontend/package.json`. For a portable Windows setup, unpack the matching Node.js x64 distribution under `%LOCALAPPDATA%\WWlogs\toolchain`, then install the pinned npm release into that Node directory with `npm install --global --prefix <node-directory> npm@<pinned-version>`. The build runs `npm ci` in a temporary directory using the committed lockfile. Both profiles use the same dependencies. The Wails CLI is optional. If used, match its version to the Go module and `@wailsio/runtime`.

For VS Code, install the recommended TypeScript 7 extension and run `npm ci` in `frontend` to use the workspace language service. The production build installs its own dependencies from the lockfile.

`scripts/build.ps1` is the one build entry point. One invocation is one build run: `-Profile Both` builds Development and Public together, `-Profile Development` or `-Profile Public` builds one. Completed executables go to the Git-ignored `bin/development/WWlogs.exe` and `bin/public/WWlogs.exe` (the console analyzer target, `-Target Capture`, writes `WWlogs-Capture.exe` beside them). `-OutputRoot` moves both profile directories; `-Output` names the file of a single-profile run.

## Build Number

The official Build Number is a lifetime-increasing integer that identifies one completed build run. It is independent of the product version, branch, commit and executable count: every executable of one run carries the same number, and a rebuild of the same commit gets the next one. `scripts/embed-key.go` is the only allocator. It locks the owner's Git-ignored `.local/build-state.json` for the whole run, builds and verifies every requested executable in a staging file beside its output, installs them together, and only then records the number. A failed or partial run installs nothing, keeps the previous executables and does not advance the counter. If a run is killed while installing, the next run skips that number rather than issuing it twice. A missing counter makes the build Unofficial; a corrupt counter, or a missing one next to its backup `build-state.backup.json`, stops the build instead of starting again at 1. `-InitializeBuildNumber` is the owner-directed first use and is refused once a counter exists or existed. `-Unofficial` builds without the counter; `scripts/verify.ps1` builds this way. A checkout without the counter always builds Unofficial executables and can never produce an official number.

The executable reports its identity with `-version` (`WWlogs version`, `Build number`, `Build profile`, `Commit`, `Working tree modified`) and in Settings > About. The Windows version resource keeps MAJOR.MINOR.PATCH in its numeric fields and writes the full identity into the `ProductVersion` and `Comments` strings, so a Build Number above 65535 is never truncated.

## Application icon

`icon.ico` is the canonical, Git-managed WWlogs logo for both build profiles and both executable targets. Keep this source asset here, outside disposable output directories; do not add version suffixes or check in generated `.syso` files.

Build with `scripts/build.ps1 -Profile Development` (or `Public` or `Both`; add `-Target Capture` for the console analyzer). The shared helper uses `winres` to compile the ICO and WWlogs version information into a temporary Windows amd64 COFF object. The Desktop build shares the same image frames between icon group 3 and Wails' native window class icon group 32512. The helper verifies the linked resources before installing the EXE. No external ICO is needed at runtime.

The temporary `cmd/<target>/wwlogs_windows_amd64.syso` is ignored and removed on exit. Builds of the same target are exclusive: an existing resource causes a failure instead of reusing or overwriting it. After a forcibly interrupted build, remove that generated file only after confirming no build is running. Go overlays cannot supply this resource because Go's object packer requires a physical file.

The existing build cleans only temporary generated resources and frontend output. Keep source assets in `build/windows` and use the build helper for application executables. Windows may cache icons for overwritten EXE paths. Use a new output filename for visual acceptance.
