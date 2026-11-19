# Product Tests

All WWlogs product test source files, test helpers, and fixtures belong in this `tests/` directory. Do not put test code in `cmd/` or `internal/`.

- `capture/`, `devcapture/`, `devdesktop/` and `keyembed/` contain white-box tests. `go-overlay.json` maps these files from their physical locations under `tests/` to the product packages under test, and hides their original paths from `tests/` package discovery.
- `capture/` tests the shared core (`internal/capture`) in both build profiles; `devcapture/` tests the Development capture module (`internal/devcapture`: research and diagnostic outputs, the command census, the live spool). A test file of one package cannot be imported by another, so `devcapture/capture_helpers_test.go` copies the `capture/` helpers the Development tests use, each group named after its original; change a copy with its original.
- A synthetic Master (a fixture SQLite file built in a test) must provide the runtime schema
  (`internal/capture/master_schema.go`): the loader and the display resolver refuse any other. `completeMasterFixture`
  (`capture/master_schema_test.go`, with copies in `devcapture/` and `desktop/`) adds the tables, view and columns a
  fixture lacks, empty, after the test created what it needs.
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

`architecture/` reads the source of both build profiles with `go/build`, `go/ast`, `go/types` and `go list` and checks
the architecture of the two profiles: one shared core (`internal/capture`) and one implementation of every product
capability, Development capabilities in ordinary Development modules (`internal/devcapture`, `internal/devdesktop`)
that only the Development composition links, and profile differences only in a few build composition boundaries
(`cmd/capture`, `cmd/desktop`, `internal/buildinfo`). It needs only tracked files and the Go toolchain, and runs with
the other product tests (`scripts/test.ps1`, `scripts/verify.ps1`).

- **Profile files**: a file belongs to a profile by its build constraint, `wwdev` is the only profile tag, and the name
  says the same (`*_development.go`, `*_public.go`).
- **Composition boundaries**: profile-specific files exist only in the packages listed in `compositionBoundaries`; a
  Development capability goes into a Development module, never into a profile split of a feature package.
- **Wiring only**: the profile files of a boundary declare constants and variables that only name what a profile links,
  with the same names and types in both profiles; no function, method, type or computed value.
- **Development modules**: no Public executable links a Development module or a Development-only dependency (the PCAP
  writer, the version resource reader), the Development executables still link them, every package only Development
  links is declared, and only Development modules and the Development files of a boundary import a Development module.
- **No Development implementation in the core**: every function, method and type of the core is used by the core or by
  a package outside the Development modules; the few observation entry points only Development calls are reviewed.
- **One-way observation**: the interfaces the core calls into Development code (`Observer`, the census observers,
  `SessionDiagnostics`) return nothing the core could decide with and receive values, not pointers (a pointer would
  let the observer write the core value and moves it to the heap even in Public); the few results and pointers they
  have are reviewed.
- **Development writes**: Development code changes core memory only as a local copy; through a pointer, slice or map it
  writes only its own data, apart from the reviewed configuration of an entry point.
- **Core fields**: the core reads every field of the core `Processor`; Development state belongs in the Development
  module.
- **Research schema**: the research protocol schema and the generic decoder's records are declared in the Development
  capture module and nowhere in the core.
- **Development surface**: the Development diagnostic flags of v0.8.0 are still defined in their Development modules,
  and no Public command line defines them.

Each contract has a negative test that breaks it in memory (or, for a violation across packages, through a temporary
overlay) and requires the contract to report it; no file on disk changes. A static check sees the source text only.
Core state changed through a core method call, a diagnostic failure that stops processing, and equal results with and
without the Development observer are covered by behavior tests (`devcapture/diagnostic_isolation_dev_test.go`,
`devcapture/client_push_dev_test.go`, `capture/processor_state_test.go`, whose names the contracts check, and the replay
steps of `scripts/verify.ps1`). `scripts/verify.ps1` also audits the built Public executables: no Development flag, no
Development string and no symbol of a Development module.

A failing contract is a design signal: move the code to the place its responsibility belongs. When a reviewed
architecture change needs a different contract, change the contract in the same commit and say why; a reviewed exception
names its case and why it cannot change combat results. Never delete or relax a check only to make it pass.

## Golden facts and the regression corpus

Replay regression runs real captures through the product (`scripts/verify.ps1 -PcapDirectory <dir>`). The captures are
private and never part of the repository; the facts they must show are tracked in the collectors under `tests/capture` and `tests/devcapture`
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
  the SHA-256 the registry pins; the version readiness gate (`scripts/readiness`) fails otherwise, and so does the
  semantic replay step of a Maintainer `verify.ps1 -PcapDirectory` run. Supplementary captures keep their facts for when
  they are present; their absence is reported, not hidden.
- `verify.ps1 -EvidenceOut <dir>` records what actually ran: `golden-coverage.json` lists each executed capture, its
  SHA-256, how many facts of each kind it was checked against and whether they held, and the captures whose facts were
  skipped because they were absent.
- `verify.ps1` has two modes. `-Mode Maintainer` (the default) is the maintainer's complete verification: it also runs
  the checks that need private material, the Master contract (`work/acceptance/master/master_contract.json`, or the file
  `WWLOGS_MASTER_CONTRACT` names), the owner-local key and, with `-PcapDirectory`, the capture corpus, and a missing or
  unreadable private input fails it; it never turns into Public. `-Mode Public` runs only what tracked files and public
  tools allow (formatting, vet, both profiles' tests, the asset check, the frontend build), reads no private material and
  reports every other step SKIP.
- `verify.ps1` reports every step it declares as PASS, FAIL, BLOCKED (required but unable to run), NOT RUN (only for the
  captures of an absent `-PcapDirectory`) or SKIP (Public mode only), and only PASS counts as passed. A Maintainer run
  exits 0 only when every step passed; a Public run exits 0 when every step it runs passed and says
  `PASS WITH PRIVATE CHECKS SKIPPED`; both exit 1 on a failed or blocked step and 2 when steps were NOT RUN. The go test
  steps count the tests that passed and those that skipped themselves. A collector counts as run only when its test ran,
  passed and wrote its outputs: a collector skips itself without its environment and still exits 0.
- `tests/verify/rules.tests.ps1` tests the rules `verify.ps1` decides with (`scripts/verify/rules.ps1`): where the Master
  contract is, how a go test stream reads, when a required check and the required Golden captures count as run, and the
  verdict of each mode. `verify.ps1` runs it as a step.
- **Wall-clock outputs are samples, never facts.** A replay without pacing publishes host views by wall time, so the
  same packets give fewer views from a faster executable. The host session collector checks every view a session
  published against the processor after the same packets and against the publish contract, and lists the view counts
  in `wallClockFields`; compare them with the contract, never for equality.
- Synthetic fixtures (frames built in the test) cover what the corpus cannot show on demand: transport loss and
  reordering, RequestId wrap boundaries, reconnects, key collisions, time stops. They test the rule, not the game; a
  game fact still needs a capture and an independent check.
