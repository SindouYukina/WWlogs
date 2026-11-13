# Version readiness gate

The gate checks that a game-version adaptation is complete in every domain AGENTS.md section 4.4 requires: from the
client's definitions, through the Master and the image pack, to the shared Go decisions the Overlay and History show. It
replaces "the decoder runs and the tests pass" with explicit, reproducible checks that fail on real omissions.

The private adaptation procedure (`work/version-update/PROCEDURE.md`, owner-local) says when to run it; this file says
how it works.

## States

- `STATIC_READY`: every mandatory static, research and evidence check is `PASS` (or `NOT_APPLICABLE` with a reason).
  An `UNVERIFIED` mandatory check (an input was not supplied) blocks it like a `FAIL`.
- `LIVE_VERIFIED`: the owner's Live/Npcap acceptance record for this game version passed. It is reported separately and
  never implied by `STATIC_READY`; a Live pass of another version or build never carries over.

Check statuses are `PASS`, `FAIL`, `UNVERIFIED` and `NOT_APPLICABLE`. A check that did not run is never `PASS`.

## Running it

```powershell
# 1. A verification run that records what it executed.
pwsh -NoProfile -File scripts/verify.ps1 -PcapDirectory logs -EvidenceOut <evidence dir>
# 2. The gate over the tracked tree, the private research inputs and that evidence.
pwsh -NoProfile -File scripts/readiness.ps1 -Out <report dir> -Evidence <evidence dir> -ResearchInputs <inputs.json> [-LiveAcceptance <live.json>]
```

The report directory receives `readiness.json` (machine-readable) and `readiness.md` (the Core Acceptance Matrix: one
row per domain, then every check with its coverage and evidence). Exit code 0 is `STATIC_READY`, 1 is not, 2 means the
gate itself could not run. Without `-Evidence` and `-ResearchInputs` the gate still runs every tracked check, and the
others are `UNVERIFIED`.

Inputs:

| Input | Kind | Content |
| --- | --- | --- |
| Source tree, `data/master_data.db`, `assets/manifest.json` and `assets/game/**` | tracked | always read |
| `tests/capture/golden_registry.json` | tracked | the Golden captures the corpus requires for the current game version |
| Research inputs file | private | `{"protocolSchema": "<client schema from protojs_schema.py>", "clientReferences": "<client reference inventory>"}`; relative paths resolve from the repository root |
| `-Evidence` directory | run evidence | `verify-results.json`, `golden-coverage.json`, `history-summary.json` from `verify.ps1 -EvidenceOut` |
| `-LiveAcceptance` | owner record | `{"gameVersion", "clientVersion", "buildNumber", "executableSha256", "date", "operator", "result", "checks"}` |

The client reference inventory lists every pack path the current client references and the explicit exclusions (with
reasons) of references the product never requests: `{"client", "resource", "paths": [...], "exclusions": [{"path",
"source", "reason"}]}`.

## Checks

| Check | Domain | Kind | What it states | What it catches |
| --- | --- | --- | --- | --- |
| `protocol.single-native-decoder` | protocol | static | One generated wire-number file, `currentGameVersion` defined once, no translation/legacy/fallback layer, no wire number assigned elsewhere | a retired protocol layer or a second numbers table left in the product |
| `protocol.version-format` | protocol | static | The decoded version is one major.minor and matches the protocol's client label | a patch component or a mismatched client label |
| `protocol.schema-keys` | protocol | research | Every generated constant resolves, by the client's minified key, to the same number in the current client schema; the source hashes match | numbers not regenerated after a client update |
| `master.integrity` | master | static | `PRAGMA integrity_check` is ok and `foreign_key_check` is empty | a damaged or inconsistent Master |
| `master.loads` | master | static | `capture.LoadMasterData` opens the Master in all five bundled Master locales | a Master the product refuses (the loader is fail-closed on broken relations) |
| `master.relations` | master | static | Trial parents, role skill groups, buff cues, guide categories and entrances resolve | a lookup that no longer resolves |
| `master.role-names` | master | static | Every playable role is named in every locale | a new role whose text the client has not filled in |
| `characters.art` | characters | static | Every playable role has avatar and portrait in the pack | a new role without art (Global 3.8, role 1512, Build 28) |
| `characters.trial-parent` | characters | static | Every trial of a playable role resolves to it through `DisplayResolver.StaticRole` and finds its art | a trial version without its parent's art |
| `characters.skill-icons` | characters | static | Every UI skill of a playable role has its icon | a new role's skill icons missing |
| `characters.skill-names` | characters | static | Every playable role names its UI skills in every locale | a new role with empty skill text |
| `effects.hud-classification` | effects | static | Every buff with a client HUD cue (type 2/14/24/37 with a Path) gets the product's buff, debuff or neutral classification, matching the cue's direction parameter | a HUD status the product drops or misclassifies (Build 31: Lily's arrowless statuses) |
| `effects.hud-icons` | effects | static | Every HUD buff has its icon in the pack | a HUD status without an icon |
| `assets.manifest-integrity` | assets | static | Every manifest image exists with its SHA-256 and size; no unlisted image | a damaged or partially installed pack |
| `assets.manifest-version` | assets | static | The pack's resource version belongs to the decoded game version | a pack not refreshed with the protocol |
| `assets.client-references` | assets | research | The pack holds exactly the client-referenced images the product requests | images the client added or dropped |
| `assets.embedded-parity` | assets | evidence | Both desktop profiles embed every distinct manifest image (`verify.ps1`) | a build that lost images |
| `content.guide-categories` | content | static | Every Adventure Guide category the client's detection rows use is named in every locale | a new mode without a name |
| `content.instance-modes` | content | static | Every category with an instance only it names resolves through `DisplayResolver.InstanceMode` | a new mode the resolver cannot place |
| `content.unresolved-categories` | content | static, advisory | Categories the client data names only ambiguously | reported limitation, never a blocker |
| `runtime.history-version-source` | runtime | static | No product or frontend source keeps a game version literal of its own | History or a view recording an old version |
| `runtime.profile-parity` | runtime | evidence | Public and Development statistics, History and Master answers are equal (`verify.ps1`) | a profile split in core semantics |
| `runtime.history-version-replay` | runtime | evidence | Every encounter the History replay stored carries the decoded version | stale History game versions |
| `verification.regression-suite` | verification | evidence | Every `verify.ps1` step ran and passed | skipped or failing regression |
| `verification.golden-coverage` | verification | evidence | Every required Golden capture ran from the registered file, checked its facts and passed | a Golden that silently stopped running |
| `verification.failure-injection` | verification | evidence | The gate's own failure-injection tests ran in both profiles | a gate that no longer detects omissions |
| `verification.live-acceptance` | verification | live | The owner's Live acceptance of this version | `LIVE / Npcap: NOT RUN` |

Where the product decides, the gate asks the product: effect HUD eligibility is `capture.MasterData.EffectDefinition`
(the definition both the Overlay and History use), trial parents are `DisplayResolver.StaticRole`, and modes are
`DisplayResolver.InstanceMode`. The client side of each comparison (which buffs the HUD draws, which roles are playable,
which categories the guide uses) is read independently from the Master tables built from the client.

## Failure injection

`tests/readiness` runs the gate on the tracked repository (no static gap allowed) and on isolated defective copies, each
of which must fail with the named check and finding: the new role's art and skill icons removed from a copy of the
manifest; a product classification that drops a HUD status or every arrowless (neutral) status; a new guide category with
an exclusive instance relation and no name, and a trial role with a missing parent, in a temporary copy of the Master; a
History version literal with the protocol at 3.9 and a replay whose encounters lack the version; a retired translation
layer and a stray wire number in a source copy, and a client schema with one renumbered command; a required Golden
capture absent, without facts, failed, or from another file; and client reference drift. Clean controls of the same
inputs pass. Nothing is written to the tracked Master, manifest or any user data.

## Troubleshooting

| Result | Cause | Repair |
| --- | --- | --- |
| `protocol.schema-keys` FAIL with hash mismatch | the constants come from another client build | regenerate with `generate_numbers.py update` (procedure S3) |
| `characters.art` / `skill-icons` FAIL | the image pack was not refreshed for new roles | refresh the pack (procedure S7) and `go run scripts/asset-manifest.go` |
| `effects.hud-classification` FAIL | the HUD rule in `internal/capture/effect_hud_cues.go` / `effect_display.go` no longer matches the client | compare with the client's `BuffItem*.js` and fix the shared Go rule, never per effect |
| `content.instance-modes` FAIL | a new guide relation or category the resolver does not read | rebuild the game-mode tables and, with client evidence, extend `internal/capture/instance_display.go` |
| `runtime.history-version-source` FAIL | a version literal outside `protocol_numbers.go` | take the version from `capture.CurrentGameVersion()` through the stored protocol session |
| `verification.golden-coverage` FAIL | a required capture was not passed to `verify.ps1`, its file changed, or a fact failed | pass the corpus with `-PcapDirectory`; a changed file needs a new registry entry and independent facts |
| any evidence check UNVERIFIED | `-Evidence` missing or from a run without `-PcapDirectory` | rerun `verify.ps1 -PcapDirectory logs -EvidenceOut <dir>` |
| `master.loads` slow | each locale loads the full Master (about 10 s each on the development PC) | expected; results are remembered within one gate process |
