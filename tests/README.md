# Product Tests

All WWlogs product test source files, test helpers, and fixtures belong in this `tests/` directory. Do not put test code in `cmd/` or `internal/`.

- `capture/` and `keyembed/` contain white-box tests. `go-overlay.json` maps these files from their physical locations under `tests/` to the product packages under test, and hides their original paths from `tests/` package discovery.
- `versioninfo/` contains product tests that use exported APIs.
- `.vscode/settings.json` and `scripts/test.ps1` use the same overlay so that `gopls`, Go tests, and editor diagnostics resolve files consistently.

Run product tests from the repository root:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1
```

The test script forwards standard `go test` flags to the test command. For example:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1 -run TestCurrentMetadataIsComplete
```

The test entry point explicitly lists the required `cmd`, `internal`, and `tests` packages. It does not recursively scan `work/` or other unrelated repository directories.
