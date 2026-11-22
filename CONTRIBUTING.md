# Contributing to WWlogs

Thank you for your interest in WWlogs. This guide explains which contributions are welcome, how to prepare a pull request, and what the official project accepts.

## Contributions that are welcome

- **Bug fixes**: problems that have an issue or a clear way to reproduce them.
- **Feature suggestions**: open an issue with the feature request form first, so that the idea can be discussed before anyone writes code.
- **Translation fixes**: the app's languages live in `frontend/locales/<language>/`. The README translations follow the Traditional Chinese `README.md`, which is the master; a fix keeps the same structure in every language.
- **Documentation improvements**: corrections and clarifications of the README files and the other documents.

For a larger change, please open an issue before you start, so that you do not spend time on something that does not fit the project.

## How contributions work

The maintainer manages the WWlogs repository and its `main` branch. Contributors do not push to it: every change comes in as a pull request from your own fork, and the maintainer reviews it and decides whether and when to integrate it into the current development line.

1. Fork the repository on GitHub and clone your fork:

   ```powershell
   git clone https://github.com/<your-account>/WWlogs.git
   cd WWlogs
   ```

2. Create a branch for your change:

   ```powershell
   git switch -c fix/short-description
   ```

3. Make the change, keep it to one topic, and commit it with a clear message that says what changed and why.
4. Run the checks described below.
5. Push the branch to your fork and open a pull request against the default branch of `SindouYukina/WWlogs`. Fill in the pull request template.

## Building and testing

The requirements are listed under "Building from source" in the [README](README.en.md#building-from-source): Windows x64, PowerShell 7, the Go and Node.js versions in `go.mod` and `frontend/package.json`, and, for builds, an external key that you prepare yourself.

- `pwsh -File scripts/verify.ps1 -Mode Public` is the verification anyone can run from a checkout: formatting, `go vet`, the Go tests of both build profiles (including the architecture contract tests), the frontend behavior tests, the frontend typecheck and build, and the version control of the game assets. Checks that need the maintainer's private material are reported as `SKIP`, so the best result is `PASS WITH PRIVATE CHECKS SKIPPED`.
- `pwsh -File scripts/test.ps1` runs only the Go tests of both profiles; it is quicker while you work.
- `pwsh -File scripts/build.ps1 -Profile Both` builds the Public and Development executables. It needs the external key; see the README.

Before integrating a change, the maintainer also runs the complete verification with the private material, including the replay regression over archived captures. A pull request does not need to run those checks.

## What a pull request should include

- what the change does and why, and the issue it addresses;
- the main changes and the modules they touch;
- how you tested it, with the commands and their results;
- whether it changes a stored data format, Master data or lookups, parsing or the way statistics are calculated, or the runtime core;
- known limitations.

The pull request template asks for these points.

## What the official project accepts

- Follow the existing architecture and code style. Go code is formatted with `gofmt`, and code, comments and identifiers are in English.
- Keep pull requests focused. Large refactoring that the change does not need is not accepted.
- Parsing correctness must not regress: damage, attribution, encounters, effects and statistics must stay as accurate as before, and a change to them needs evidence.
- Do not make tests pass by skipping verification, hiding errors, weakening assertions or changing how statistics are defined.
- The official project does not add community DPS leaderboards or other comparisons between players, official plain-text export of combat logs, or official transfer of combat history between PCs.
- Do not submit private keys, packet captures, combat history files, game client files or data extracted from them, or any player's personal information, such as game UIDs and player names.
- The Master data (`data/master_data.db`) and the game image pack are maintained by the maintainer. Please report a missing or wrong name or icon in an issue instead of editing them.

These points describe what the official WWlogs project accepts. They do not restrict your rights under the AGPL-3.0-only license: you may modify and distribute WWlogs under its terms.

## License of contributions

WWlogs is licensed under AGPL-3.0-only. By submitting a pull request, you agree that your contribution is licensed under the same license.

## Reporting problems

- Bugs and feature requests: use the issue forms.
- Security problems: follow [SECURITY.md](SECURITY.md), and do not post the details in a public issue.
