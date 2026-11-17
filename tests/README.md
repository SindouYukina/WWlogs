# Product Tests

All WWlogs product test source files, test helpers, and fixtures belong in this `tests/` directory. Do not put test code in `cmd/` or `internal/`.

- `capture/` and `keyembed/` contain white-box tests. `go-overlay.json` maps these files from their physical locations under `tests/` to the product packages under test, and hides their original paths from `tests/` package discovery.
- `versioninfo/` contains product tests that use exported APIs.
- `architecture/` contains the architecture contracts: static checks of the product source of both build profiles (see below).
- `buildrun/` tests the official Build Number allocator and the all-or-nothing installation of a build run, each against its own temporary counter directory.
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

## Architecture contracts

`architecture/` reads the source of both build profiles with `go/build`, `go/ast` and `go/types` and checks the
boundaries the shared core depends on. It needs only tracked files and the Go toolchain, and runs with the other
product tests (`scripts/test.ps1`, `scripts/verify.ps1`).

- **Profile files**: a file belongs to a profile by its build constraint, `wwdev` is the only profile tag, and the name
  says the same (`*_development.go`, `*_public.go`).
- **Shared references**: shared code reaches profile code only through declarations both profiles have (a Development
  hook and its Public no-op), and both profiles type-check.
- **One-way hooks**: the two forms of a split declaration have the same signature and return nothing. A split value (an
  entry point's options, a capability gate, the build profile) is listed in `reviewedProfileValues` with the reason it
  cannot change combat results. A split type is empty in the Public profile.
- **Development writes**: Development code changes core memory only as a local copy; through a pointer, slice or map it
  writes only Development and research data.
- **Core fields**: shared code reads every field of the core `processor` in the Public profile; Development state
  belongs in `processor.dev`.
- **Research schema**: the research protocol schema and the generic decoder's records stay out of shared code.
- **Development surface**: the Development diagnostic flags of v0.8.0 are still defined, and only in Development files;
  the Development executables link the PCAP writer package and no Public executable does.

Each contract has a negative test that breaks it in memory (or, for the linked packages, through a temporary overlay)
and requires the contract to report it; no file on disk changes. A static check sees
the source text only. Core state changed through a core method call, a diagnostic failure that stops processing, and
equal results in both profiles are covered by behavior tests (`capture/diagnostic_isolation_dev_test.go`,
`capture/client_push_dev_test.go`, and the replay steps of `scripts/verify.ps1`).

A failing contract is a design signal: move the code to the place its responsibility belongs. When a reviewed
architecture change needs a different contract, change the contract in the same commit and say why; a reviewed exception
names its case and why it cannot change combat results. Never delete or relax a check only to make it pass.

## Golden facts and the regression corpus

Replay regression runs real captures through the product (`scripts/verify.ps1 -PcapDirectory <dir>`). The captures are
private and never part of the repository; the facts they must show are tracked in the collectors under `tests/capture`
(`semantic_replay_dev_test.go` and the other `*Collector` tests).

- **A Golden fact needs evidence outside the product's decoders**: the generic schema decode of the client's own
  `Protocol.js`, the server's request and response pairing, the target's Life attribute steps, the server's
  `JoinSceneNotify` and `EntityType`, the client's own reports, or the owner's observation in game. Name the check in the
  test comment and in the private run record. A value only WWlogs produces is a Replay Observation: log it, never pin it.
- **Never update an expected value to make a change pass.** Decide first which side is wrong: a decoder fix (pin the
  corrected value with its independent check and keep the old one in the run record), a wrong Golden, a changed
  corpus, changed game semantics (a new version's facts replace the old ones; retired-version captures are research
  evidence only), or an obsolete test. Write the decision and its evidence down before changing the fact.
- **The corpus is the owner's choice.** `tests/capture/golden_registry.json` names the captures of the current game
  version and which ones are required. A required capture must be executed with every fact checked, from the file with
  the SHA-256 the registry pins; the version readiness gate (`scripts/readiness`) fails otherwise. Supplementary
  captures keep their facts for when they are present; their absence is reported, not hidden.
- `verify.ps1 -EvidenceOut <dir>` records what actually ran: `golden-coverage.json` lists each executed capture, its
  SHA-256, how many facts of each kind it was checked against and whether they held, and the captures whose facts were
  skipped because they were absent.
- Synthetic fixtures (frames built in the test) cover what the corpus cannot show on demand: transport loss and
  reordering, RequestId wrap boundaries, reconnects, key collisions, time stops. They test the rule, not the game; a
  game fact still needs a capture and an independent check.
