# Windows build

Use the Go toolchain specified in `go.mod` and the exact Node.js and npm versions in `frontend/package.json`. For a portable Windows setup, unpack the matching Node.js x64 distribution under `%LOCALAPPDATA%\WWlogs\toolchain`, then install the pinned npm release into that Node directory with `npm install --global --prefix <node-directory> npm@<pinned-version>`. The build runs `npm ci` in a temporary directory using the committed lockfile. Both profiles use the same dependencies. The Wails CLI is optional. If used, match its version to the Go module and `@wailsio/runtime`.

For VS Code, install the recommended TypeScript 7 extension and run `npm ci` in `frontend` to use the workspace language service. The production build installs its own dependencies from the lockfile.

## Application icon

`icon.ico` is the canonical, Git-managed WWlogs logo for both build profiles and both executable targets. Keep this source asset here, outside disposable output directories; do not add version suffixes or check in generated `.syso` files.

Build with `scripts/build.ps1 -Target Desktop -Profile Development` (or `Public`; omit `-Target Desktop` for the console analyzer). The shared helper uses `winres` to compile the ICO and WWlogs version information into a temporary Windows amd64 COFF object. The Desktop build shares the same image frames between icon group 3 and Wails' native window class icon group 32512. The helper verifies the linked resources before installing the EXE. No external ICO is needed at runtime.

The temporary `cmd/<target>/wwlogs_windows_amd64.syso` is ignored and removed on exit. Builds of the same target are exclusive: an existing resource causes a failure instead of reusing or overwriting it. After a forcibly interrupted build, remove that generated file only after confirming no build is running. Go overlays cannot supply this resource because Go's object packer requires a physical file.

The existing build cleans only temporary generated resources and frontend output. Keep source assets in `build/windows` and use the build helper for application executables. Windows may cache icons for overwritten EXE paths. Use a new output filename for visual acceptance.
