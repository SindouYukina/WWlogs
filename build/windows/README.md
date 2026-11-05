# Windows application icon

`icon.ico` is the canonical, Git-managed WWlogs logo for both build profiles and both executable targets. Keep this source asset here, outside disposable output directories; do not add version suffixes or check in generated `.syso` files.

Build with `scripts/build.ps1 -Target Desktop -Profile Development` (or `Public`; omit `-Target Desktop` for the console analyzer). The shared helper uses `winres` to compile the ICO and WWlogs version information into a temporary Windows amd64 COFF object. The Desktop build shares the same image frames between icon group 3 and Wails' native window class icon group 32512. The helper verifies the linked resources before installing the EXE. No external ICO is needed at runtime.

The temporary `cmd/<target>/wwlogs_windows_amd64.syso` is ignored and removed on exit. Builds of the same target are exclusive: an existing resource causes a failure instead of reusing or overwriting it. After a forcibly interrupted build, remove that generated file only after confirming no build is running. Go overlays cannot supply this resource because Go's object packer requires a physical file.

The existing build cleans only temporary generated resources and frontend output. Keep source assets in `build/windows` and use the build helper for application executables. Windows may cache icons for overwritten EXE paths. Use a new output filename for visual acceptance.
