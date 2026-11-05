# Windows application icon

`icon.ico` is the canonical, Git-managed WWlogs logo for both build profiles and both executable targets. Keep this source asset here, outside disposable output directories; do not add version suffixes or check in generated `.syso` files.

Build with `scripts/build.ps1 -Target Desktop -Profile Development` (or `Public`; omit `-Target Desktop` for the console analyzer). The shared build helper uses Wails v3's resource generator dependency, `winres`, to compile the ICO into a temporary Windows amd64 COFF object with Wails icon group ID 3. Go links it into the selected entry point, then the helper verifies every icon resource in the actual EXE before installing it. Missing or invalid icons fail the build. No external ICO is needed at runtime.

The temporary `cmd/<target>/wwlogs_windows_amd64.syso` is ignored and removed on exit. Builds of the same target are exclusive: an existing resource causes a failure instead of reusing or overwriting it. After a forcibly interrupted build, remove that generated file only after confirming no build is running. Go overlays cannot supply this resource because Go's object packer requires a physical file.

The existing build cleans only temporary generated resources and frontend output. Do not place source assets in `bin`, clean `build/windows`, or bypass the helper with a plain `go build` when producing application executables. Icon-only generation preserves existing manifest and DPI behavior. Windows may cache icons for overwritten EXE paths; use a new output filename for visual acceptance without clearing the user's icon cache.
