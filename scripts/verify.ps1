<#
.SYNOPSIS
  One local verification entry point for both build profiles, in a Maintainer or a Public mode.

.DESCRIPTION
  Runs formatting, vet, and unit tests for the Public and Development profiles, builds both executables with
  embed-key, audits the public executable's output surface, and checks the build identity. These throw-away builds
  are Unofficial: verification never reads or advances the owner's official build counter.
  The steps are declared below ($declaredSteps), and every run reports every one of them as PASS (it executed and
  passed), FAIL (it executed and failed), BLOCKED (it was required but could not run: a missing tool or input, or a
  failed step it depends on), NOT RUN (the caller did not ask for it) or SKIP (Public mode: it needs private material).
  Nothing is reported as PASS unless it executed; a test collector counts as executed only when its test ran and passed
  and wrote its outputs. The go test steps count the tests that passed and the ones that skipped themselves.

  -Mode Maintainer (the default) is the maintainer's complete verification. It also runs the checks that need private
  material: the Master contract (work/acceptance/master/master_contract.json, or the file WWLOGS_MASTER_CONTRACT names)
  checked against the tracked Master, the owner-local key the builds embed, and, with -PcapDirectory, the archived
  capture corpus, whose required Golden captures (tests/capture/golden_registry.json) must all run and pass. A missing
  or unreadable private input BLOCKS its steps and fails the run: Maintainer mode never turns into Public mode.
  -Mode Public verifies a checkout with tracked files and public tools only. It reads no private material: the
  inherited WWLOGS_* inputs (other than WWLOGS_NODE_HOME) are cleared for the run, and every step that needs private
  material is SKIP. Its best verdict is "PASS WITH PRIVATE CHECKS SKIPPED", never a Maintainer PASS.

  NOT RUN is reserved for the archived captures when -PcapDirectory is not given. Pass -PcapDirectory (Maintainer mode)
  to add the replay regression, the public output audit, the encounter replay (determinism and partition), the host
  session equivalence and the transport perturbation replay through an embedded-key development test binary; a given
  directory without a capture of the current game version blocks those steps.

  Exit code 0: Maintainer, every declared step passed; Public, every step it runs passed. 1: a step failed or was
  blocked. 2: nothing failed, but steps were NOT RUN, so the verification is incomplete.

  -EvidenceOut writes what the run executed for the version readiness gate (scripts/readiness.ps1):
  verify-results.json (the mode and every step's state), golden-coverage.json (the Golden captures the semantic
  regression ran) and history-summary.json (the Public History replay summary).
#>
param(
    # Maintainer: the complete verification with the private material. Public: tracked files and public tools only.
    [ValidateSet('Maintainer', 'Public')]
    [string]$Mode = 'Maintainer',
    # Directory that contains archived *.pcap files. Local and private; never read from the repository. Maintainer only.
    [string]$PcapDirectory = '',
    # Local interface address used only to decide packet direction during offline replay.
    [string]$ReplayIp = '192.168.1.2',
    # Directory for the machine-readable run evidence; outside the repository or in an ignored location.
    [string]$EvidenceOut = ''
)

$ErrorActionPreference = 'Stop'
# Build with the system Go only: never switch to, or download, another toolchain for a newer go.mod requirement.
$env:GOTOOLCHAIN = 'local'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
. (Join-Path $PSScriptRoot 'verify/rules.ps1')
$overlay = Join-Path $repoRoot 'tests/go-overlay.json'
$results = [ordered]@{}
$testPackages = @('./cmd/capture', './internal/buildinfo', './internal/capture', './internal/devcapture', './internal/history', './internal/service', './internal/desktop', './internal/devdesktop', './internal/keyembed', './internal/instance', './tests/architecture', './tests/assets', './tests/buildrun', './tests/readiness', './tests/versioninfo')
if ($Mode -eq 'Public' -and $PcapDirectory -ne '') {
    throw '-PcapDirectory is private material: Public mode reads none. Replay the corpus with -Mode Maintainer.'
}
if ($EvidenceOut -ne '') {
    $EvidenceOut = [IO.Path]::GetFullPath($EvidenceOut)
    [void][IO.Directory]::CreateDirectory($EvidenceOut)
}

# Every step of a complete verification, in report order. A step a run did not reach is still reported (Set-Unreached).
$buildSteps = @('build (Public)', 'build (Development)', 'build identity', 'public flags', 'public binary strings', 'public binary symbols')
$desktopSteps = @('frontend typecheck + build', 'desktop build (Public + Development)', 'desktop public flags + strings', 'desktop embedded images', 'master: embedded bytes + profile lookup parity')
$replaySteps = @('replay: public output audit + statistics equality', 'replay: encounter determinism + partition', 'replay: host session equivalence', 'replay: transport perturbation', 'replay: effect and skill semantics', 'replay: combat history fidelity')
$declaredSteps = @('gofmt', 'go vet (Public)', 'go test (Public)', 'go vet (Development)', 'go test (Development)', 'master: private contract', 'verify: mode and result rules', 'game assets: version control') + $buildSteps + $desktopSteps + $replaySteps
# The steps that need private material, with what they need. Public mode reports them SKIP without running them.
$keySteps = $buildSteps + @($desktopSteps | Where-Object { $_ -ne 'frontend typecheck + build' })
$privateNeeds = [ordered]@{ 'master: private contract' = 'the Master contract' }
foreach ($name in $keySteps) { $privateNeeds[$name] = 'the owner-local key' }
foreach ($name in $replaySteps) { $privateNeeds[$name] = 'the owner-local key and the capture corpus' }
# The private Master checks of Maintainer mode: tests of the core that read the Master contract. Each must pass in both
# profiles; one that skips, fails or does not report fails the step.
$privateMasterTests = @('TestTrackedMasterSatisfiesItsContract', 'TestMasterContractIsConsistent', 'TestMasterRuntimeSchemaAgreesWithTheContract')

function Set-Result([string]$Name, [string]$State, [string]$Detail = '') {
    if ($Name -notin $declaredSteps) { throw "verify.ps1 reports an undeclared step: $Name" }
    if ($State -notin @('PASS', 'FAIL', 'BLOCKED', 'NOT RUN', 'SKIP')) { throw "verify.ps1 reports an unknown state $State for $Name" }
    if ($State -eq 'SKIP' -and $Mode -ne 'Public') { throw "verify.ps1 skipped $Name in $Mode mode, which skips nothing" }
    $results[$Name] = "$State$(if ($Detail) { " - $Detail" })"
    Write-Host ("{0,-9} {1}{2}" -f $State, $Name, $(if ($Detail) { "  ($Detail)" } else { '' }))
}

# Set-Unreached reports the steps that cannot run after this point (NOT RUN for an input the caller did not give, BLOCKED
# otherwise); a step that already has a result keeps it.
function Set-Unreached([string[]]$Names, [string]$State, [string]$Reason) {
    foreach ($name in $Names) {
        if (-not $results.Contains($name)) { Set-Result $name $State $Reason }
    }
}

# Invoke-Collector runs one replay collector of a test binary and reports what it actually executed: a collector skips
# itself without its environment, and a test filter that matches nothing exits 0 too, so the exit code alone is no proof.
# Problem is empty only when the test ran, passed, and wrote at least ExpectedOutputs files into OutputDirectory.
function Invoke-Collector([string]$Binary, [string]$Test, [string]$OutputDirectory = '', [int]$ExpectedOutputs = 0, [string]$OutputFilter = '*') {
    $output = @(& $Binary '-test.run' "^$Test$" '-test.count' '1' '-test.v' 2>&1 | ForEach-Object { "$_" })
    $code = $LASTEXITCODE
    $text = $output -join "`n"
    $problem = ''
    if ($code -ne 0) {
        $problem = "exit code $code"
    } elseif ($text -notmatch "(?m)^--- PASS: $Test \(" -or $text -match "(?m)^--- SKIP: $Test \(" -or $text -match 'no tests to run') {
        $problem = "$Test did not run"
    } elseif ($OutputDirectory -ne '') {
        $written = @(Get-ChildItem -LiteralPath $OutputDirectory -Filter $OutputFilter -File -ErrorAction SilentlyContinue).Count
        if ($written -lt $ExpectedOutputs) { $problem = "$Test wrote $written of $ExpectedOutputs outputs" }
    }
    $summary = @($output | Where-Object { $_ -notmatch '^(=== (RUN|PAUSE|CONT)|\s+--- PASS)' })
    return [pscustomobject]@{ Problem = $problem; Tail = (($summary | Select-Object -Last 20) -join ' ') }
}

function Invoke-Go([string[]]$GoArguments) {
    $output = & go @GoArguments 2>&1
    return [pscustomobject]@{ Code = $LASTEXITCODE; Output = ($output | Out-String) }
}

# Invoke-GoTest runs go test -json and reads which tests passed, failed and skipped themselves (Read-VerifyGoTestJson).
function Invoke-GoTest([string[]]$GoArguments) {
    $lines = @(& go @(@('test', '-json') + $GoArguments) 2>&1 | ForEach-Object { "$_" })
    $code = $LASTEXITCODE
    return [pscustomobject]@{ Code = $code; Tests = (Read-VerifyGoTestJson $lines) }
}

# Strings of Development diagnostic, research and output code that no Public executable may contain: spool and
# output names, Development record types, the command census, the research schema, the research wire path of Buff
# rows and the Development summary lines.
$developmentStrings = @('combat-events.jsonl', 'dps-effects.jsonl', 'buff-events.jsonl', 'status.json', 'capture-spool', 'LIVE_DIAGNOSTIC_SNAPSHOT', 'event-jsonl-out', 'pcapgo', 'encounter-events.jsonl', 'BATTLE_STATE_SIGNAL', 'COMBAT_EVENT_RECORD', 'ENCOUNTER_TRANSITION',
    'PRESENT BUT UNDECODED', 'describes game version', '(CombatReceiveData[])', 'transport_health kcp_streams=')

# Get-DevelopmentSymbols lists the Development module packages whose symbols an executable links (go tool nm).
function Get-DevelopmentSymbols([string]$Executable) {
    $symbols = & go tool nm $Executable 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0 -or $symbols.Length -lt 1000) { return @("symbol table unreadable") }
    return @('wwlogs/internal/devcapture.', 'wwlogs/internal/devdesktop.', 'github.com/google/gopacket/pcapgo.') | Where-Object { $symbols.Contains($_) }
}

# The mode's inputs. Maintainer resolves its private Master contract here, before anything runs; Public sets aside every
# inherited WWLOGS_* input (restored at the end) so that no test or collector reads private material in a Public run.
$startDirectory = (Get-Location).ProviderPath
$contract = $null
$clearedEnvironment = @{}
if ($Mode -eq 'Maintainer') {
    $contract = Resolve-VerifyMasterContract $repoRoot $env:WWLOGS_MASTER_CONTRACT $startDirectory
    Write-Host "Mode: Maintainer (Master contract: $($contract.Shown), $($contract.Source)$(if ($PcapDirectory) { "; capture corpus: $PcapDirectory" } else { '; no capture corpus' }))"
} else {
    foreach ($variable in @(Get-ChildItem Env: | Where-Object { $_.Name -like 'WWLOGS_*' -and $_.Name -ne 'WWLOGS_NODE_HOME' })) {
        $clearedEnvironment[$variable.Name] = $variable.Value
        Remove-Item -LiteralPath "Env:$($variable.Name)"
    }
    Write-Host "Mode: Public (tracked files and public tools only; the checks that need private material are SKIP)"
    foreach ($name in $privateNeeds.Keys) { Set-Result $name 'SKIP' "Public mode: needs $($privateNeeds[$name])" }
}

Push-Location $repoRoot
try {
    # 1. gofmt. A CRLF checkout must not read as a formatting difference, so gofmt -l checks byte copies with every CR
    # removed (ISO-8859-1 maps bytes one to one, and CR never occurs inside a UTF-8 multi-byte sequence). File content
    # never passes through a PowerShell pipe, so the console code page cannot change the result; gofmt prints only the
    # relative paths of the copies.
    # Git lists tracked and new unignored files, and it is the one tool this script can already rely on.
    $goFiles = @(& git ls-files --cached --others --exclude-standard -- cmd internal scripts tests | Where-Object { $_ -like '*.go' -and (Test-Path -LiteralPath $_) } | Sort-Object)
    $bytewise = [Text.Encoding]::GetEncoding(28591)
    $gofmtDirectory = Join-Path ([IO.Path]::GetTempPath()) ("wwlogs-gofmt-" + [Guid]::NewGuid().ToString('N'))
    try {
        foreach ($file in $goFiles) {
            $copy = Join-Path $gofmtDirectory $file
            [void][IO.Directory]::CreateDirectory((Split-Path -Parent $copy))
            $content = $bytewise.GetString([IO.File]::ReadAllBytes((Join-Path $repoRoot $file))).Replace("`r", '')
            [IO.File]::WriteAllBytes($copy, $bytewise.GetBytes($content))
        }
        Push-Location $gofmtDirectory
        try {
            $gofmtOutput = @(& gofmt -l . 2>&1 | ForEach-Object { "$_" })
            $gofmtCode = $LASTEXITCODE
        } finally { Pop-Location }
    } finally {
        Remove-Item -LiteralPath $gofmtDirectory -Recurse -Force -ErrorAction SilentlyContinue
    }
    $unformatted = @($gofmtOutput | ForEach-Object { $_ -replace '\\', '/' -replace '^\./', '' } | Sort-Object)
    if ($goFiles.Count -eq 0) {
        Set-Result 'gofmt' 'FAIL' 'no Go files listed'
    } elseif ($gofmtCode -ne 0) {
        Set-Result 'gofmt' 'FAIL' (($gofmtOutput -join ' ').Trim())
    } elseif ($unformatted.Count -eq 0) {
        Set-Result 'gofmt' 'PASS' "$($goFiles.Count) files"
    } else {
        Set-Result 'gofmt' 'FAIL' ($unformatted -join ', ')
    }

    # 2. go vet and go test for each profile. The tests run the same way in both modes: without the Master contract, so a
    # test that needs private material skips itself here and is counted as skipped; the private Master checks run in
    # their own step.
    $packages = @('./cmd/...', './internal/...', './tests/...')
    $goTests = [ordered]@{}
    $contractOverride = $env:WWLOGS_MASTER_CONTRACT
    Remove-Item Env:WWLOGS_MASTER_CONTRACT -ErrorAction SilentlyContinue
    try {
        foreach ($profile in @(@{ Name = 'Public'; Tags = @() }, @{ Name = 'Development'; Tags = @('-tags', 'wwdev') })) {
            $vet = Invoke-Go (@('vet', "-overlay=$overlay") + $profile.Tags + $packages)
            Set-Result "go vet ($($profile.Name))" $(if ($vet.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($vet.Code -ne 0) { $vet.Output.Trim() })
            $test = Invoke-GoTest (@('-count=1', "-overlay=$overlay") + $profile.Tags + $testPackages)
            $goTests[$profile.Name] = $test.Tests
            $counted = "$($test.Tests.Passed) passed, $($test.Tests.Skipped) skipped, $($test.Tests.Failed) failed"
            if ($test.Code -eq 0 -and $test.Tests.Failed -eq 0 -and $test.Tests.FailedPackages.Count -eq 0 -and $test.Tests.Passed -gt 0) {
                Set-Result "go test ($($profile.Name))" 'PASS' $counted
            } else {
                $failure = if ($test.Tests.FailureText) { $test.Tests.FailureText } else { "exit code $($test.Code)" }
                Set-Result "go test ($($profile.Name))" 'FAIL' "$counted; $failure"
            }
        }
    } finally {
        if ($null -ne $contractOverride) { $env:WWLOGS_MASTER_CONTRACT = $contractOverride }
    }

    # The private Master checks (Maintainer): the core's tests that read the Master contract, in both profiles.
    if ($Mode -eq 'Maintainer') {
        if ($contract.Problem) {
            Set-Result 'master: private contract' 'BLOCKED' "$($contract.Problem); Maintainer mode requires it"
        } else {
            $previous = $env:WWLOGS_MASTER_CONTRACT
            $env:WWLOGS_MASTER_CONTRACT = $contract.Path
            try {
                $contractRuns = [ordered]@{}
                $contractFailures = @()
                foreach ($profile in @(@{ Name = 'Public'; Tags = @() }, @{ Name = 'Development'; Tags = @('-tags', 'wwdev') })) {
                    $run = Invoke-GoTest (@('-count=1', "-overlay=$overlay") + $profile.Tags + @('-run', "^($($privateMasterTests -join '|'))$", './internal/capture'))
                    $contractRuns[$profile.Name] = $run.Tests
                    if ($run.Tests.FailureText) { $contractFailures += $run.Tests.FailureText }
                }
            } finally {
                if ($null -ne $previous) { $env:WWLOGS_MASTER_CONTRACT = $previous } else { Remove-Item Env:WWLOGS_MASTER_CONTRACT -ErrorAction SilentlyContinue }
            }
            $contractProblems = @(Get-VerifyRequiredTestProblems $contractRuns 'wwlogs/internal/capture' $privateMasterTests)
            if ($contractProblems.Count -eq 0) {
                Set-Result 'master: private contract' 'PASS' "$($privateMasterTests.Count) checks in both profiles against $($contract.Shown)"
            } else {
                Set-Result 'master: private contract' 'FAIL' (($contractProblems + $contractFailures) -join '; ')
            }
        }
    }

    # The rules this script applies (modes, verdicts, the private and Golden checks) are tested before they decide.
    $rulesTest = Join-Path $repoRoot 'tests/verify/rules.tests.ps1'
    $rulesOutput = @(& ([Diagnostics.Process]::GetCurrentProcess().MainModule.FileName) -NoProfile -NonInteractive -File $rulesTest 2>&1 | ForEach-Object { "$_" })
    $rulesCode = $LASTEXITCODE
    $rulesLine = @($rulesOutput | Where-Object { $_ -match '^verify rules: ' } | Select-Object -Last 1)
    if ($rulesCode -eq 0 -and $rulesLine.Count -eq 1 -and $rulesLine[0] -match '^verify rules: (\d+) of \1 checks passed') {
        Set-Result 'verify: mode and result rules' 'PASS' ($rulesLine[0] -replace '^verify rules: ', '')
    } else {
        Set-Result 'verify: mode and result rules' 'FAIL' "exit code $rulesCode; $(($rulesOutput | Select-Object -Last 20) -join ' ')"
    }

    # Product images are under version control, and nothing else under assets/ is. Untracked or changed images fail here
    # or in the pack, so a clean clone can always build the same images.
    $assetManifest = Get-Content -LiteralPath 'assets/manifest.json' -Raw | ConvertFrom-Json
    $listedAssets = @($assetManifest.entries | ForEach-Object { "assets/$($_.path)" }) + @('assets/README.md', 'assets/manifest.json')
    $trackedAssets = @(& git ls-files -- assets)
    $assetProblems = @()
    $untrackedAssets = @($listedAssets | Where-Object { $_ -notin $trackedAssets })
    $unlistedAssets = @($trackedAssets | Where-Object { $_ -notin $listedAssets })
    if ($untrackedAssets.Count -gt 0) { $assetProblems += "$($untrackedAssets.Count) listed files are not tracked, first $($untrackedAssets[0])" }
    if ($unlistedAssets.Count -gt 0) { $assetProblems += "$($unlistedAssets.Count) tracked files are not listed, first $($unlistedAssets[0])" }
    foreach ($local in @('assets/game/local.tmp', 'assets/game/local.png', 'work/tools/fonts/candidates/misans/local.ttf', 'work/tools/fonts/src/misans/local.zip')) {
        & git check-ignore -q -- $local
        if ($LASTEXITCODE -ne 0) { $assetProblems += "$local is not ignored" }
    }
    if (@(& git ls-files -- work 'assets/**/*.ttf' 'assets/**/*.otf' 'assets/**/*.woff2').Count -gt 0) { $assetProblems += 'font candidates or work files are tracked' }
    Set-Result 'game assets: version control' $(if ($assetProblems.Count -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($assetProblems.Count -gt 0) { $assetProblems -join '; ' } else { "$($assetManifest.entries.Count) images" })

    # 3. Build both executables into a throw-away directory and audit them.
    $buildDirectory = Join-Path ([IO.Path]::GetTempPath()) ("wwlogs-verify-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $buildDirectory | Out-Null
    try {
        # The frontend is typechecked and built outside the repository with the system Node.js and npm. It needs no
        # private material, so it runs in both modes.
        $frontendDist = Join-Path $buildDirectory 'frontend-dist'
        $frontendDevelopmentDist = Join-Path $buildDirectory 'frontend-dist-development'
        $frontendOutput = ''
        $frontendStopped = $false
        try {
            $frontendOutput = & (Join-Path $PSScriptRoot 'frontend.ps1') -Action Build -OutDir $frontendDist -Profile Public 2>&1 | Out-String
            $frontendOutput += & (Join-Path $PSScriptRoot 'frontend.ps1') -Action Build -OutDir $frontendDevelopmentDist -Profile Development 2>&1 | Out-String
        } catch {
            # frontend.ps1 stops with a terminating error: it is this step's result, not the end of the run.
            $frontendOutput += $_.Exception.Message
            $frontendStopped = $true
        }
        $frontendBuilt = -not $frontendStopped -and (Test-Path -LiteralPath (Join-Path $frontendDist 'index.html')) -and (Test-Path -LiteralPath (Join-Path $frontendDevelopmentDist 'index.html'))
        if ($frontendBuilt) {
            Set-Result 'frontend typecheck + build' 'PASS'
        } else {
            # A missing Node.js or npm blocks the desktop steps: the product cannot be built without them.
            $state = if ($frontendOutput -match 'was not found on PATH') { 'BLOCKED' } else { 'FAIL' }
            Set-Result 'frontend typecheck + build' $state $frontendOutput.Trim()
        }

        # The executables embed the owner-local key, so only Maintainer mode builds them (Public mode reported their
        # steps SKIP before anything ran). The restricted embed-key step alone probes and reads that key input; without
        # it Maintainer mode is blocked, never reduced.
        $missingKeyMessage = 'owner-local private key is not present'
        $keyStatus = if ($Mode -eq 'Maintainer') { Invoke-Go @('run', './scripts/embed-key.go', '-check-key') } else { $null }
        if ($Mode -eq 'Maintainer' -and $keyStatus.Code -ne 0) {
            if ($keyStatus.Output.Contains($missingKeyMessage)) {
                Set-Unreached ($keySteps + $replaySteps) 'BLOCKED' "$missingKeyMessage; Maintainer mode requires it"
            } else {
                Set-Unreached ($keySteps + $replaySteps) 'BLOCKED' "the key check failed: $($keyStatus.Output.Trim())"
            }
        } elseif ($Mode -eq 'Maintainer') {
            $publicExe = Join-Path $buildDirectory 'WWlogs.exe'
            $developmentExe = Join-Path $buildDirectory 'WWlogs-dev.exe'
            $publicBuild = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'public', '-o', $publicExe)
            $developmentBuild = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'development', '-o', $developmentExe)
            $publicKeyMissing = $publicBuild.Output.Contains($missingKeyMessage)
            $developmentKeyMissing = $developmentBuild.Output.Contains($missingKeyMessage)
            Set-Result 'build (Public)' $(if ($publicKeyMissing) { 'BLOCKED' } elseif ($publicBuild.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($publicKeyMissing) { $missingKeyMessage } elseif ($publicBuild.Code -ne 0) { $publicBuild.Output.Trim() })
            Set-Result 'build (Development)' $(if ($developmentKeyMissing) { 'BLOCKED' } elseif ($developmentBuild.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($developmentKeyMissing) { $missingKeyMessage } elseif ($developmentBuild.Code -ne 0) { $developmentBuild.Output.Trim() })
            if ($publicKeyMissing -or $developmentKeyMissing) {
                Set-Unreached ($keySteps + $replaySteps) 'BLOCKED' "$missingKeyMessage; Maintainer mode requires it"
            } elseif ($publicBuild.Code -ne 0 -or $developmentBuild.Code -ne 0) {
                Set-Unreached ($keySteps + $replaySteps) 'BLOCKED' 'blocked: a console build failed'
            }

            if ($publicBuild.Code -eq 0 -and $developmentBuild.Code -eq 0) {
                # Build identity: profile from compiled constants, SHA-256 against an independent tool.
                $publicVersion = (& $publicExe -version) -join "`n"
                $developmentVersion = (& $developmentExe -version) -join "`n"
                $developmentHash = (Get-FileHash -LiteralPath $developmentExe -Algorithm SHA256).Hash.ToLowerInvariant()
                $identityOk = ($publicVersion -match 'Build profile: public') -and ($developmentVersion -match 'Build profile: development') -and
                    ($developmentVersion -match "Executable SHA-256: $developmentHash") -and ($publicVersion -notmatch 'Executable SHA-256') -and
                    ($publicVersion -match 'Commit: [0-9a-f]{40}') -and ($publicVersion -match 'Build number: unofficial') -and
                    ($developmentVersion -match 'Build number: unofficial')
                Set-Result 'build identity' $(if ($identityOk) { 'PASS' } else { 'FAIL' })

                # Public output surface: no diagnostic flags, no diagnostic strings, no PCAP writer symbols.
                # Help is read through a pipe: PowerShell neither waits for nor captures a GUI-subsystem executable
                # called inside an expression, and a missing help text must fail instead of passing the flag check.
                $help = & $publicExe -h 2>&1 | Out-String
                $flagLeak = @('pcap-out', 'event-jsonl-out', 'buff-event-out', 'status-file', 'research', 'live-diagnostic-out', 'frame-archive-out', 'protocol-schema', 'encounter-out') | Where-Object { $help -match "-$_\b" }
                $helpRead = $help -match '(?m)^\s+-version\b'
                Set-Result 'public flags' $(if ($helpRead -and -not $flagLeak) { 'PASS' } else { 'FAIL' }) $(if (-not $helpRead) { 'help text not captured' } elseif ($flagLeak) { "exposes: $($flagLeak -join ', ')" })
                $bytes = [IO.File]::ReadAllBytes($publicExe)
                $latin = [Text.Encoding]::GetEncoding(28591).GetString($bytes)
                $stringLeak = @($developmentStrings | Where-Object { $latin.Contains($_) })
                Set-Result 'public binary strings' $(if (-not $stringLeak) { 'PASS' } else { 'FAIL' }) $(if ($stringLeak) { "contains: $($stringLeak -join ', ')" })
                # Symbols: no function or type of a Development module is linked into the Public executable.
                $symbolLeak = @(Get-DevelopmentSymbols $publicExe)
                Set-Result 'public binary symbols' $(if (-not $symbolLeak) { 'PASS' } else { 'FAIL' }) $(if ($symbolLeak) { "links: $($symbolLeak -join ', ')" })

                # Desktop host: the built frontend is embedded into both profiles, and the Public desktop executable is
                # audited like the console one.
                if (-not $frontendBuilt) {
                    Set-Unreached $desktopSteps 'BLOCKED' 'blocked: the frontend build did not complete'
                } else {
                    $desktopPublicExe = Join-Path $buildDirectory 'WWlogs-Desktop.exe'
                    $desktopDevelopmentExe = Join-Path $buildDirectory 'WWlogs-Desktop-dev.exe'
                    $desktopPublic = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'public', '-target', 'desktop', '-frontend-dist-public', $frontendDist, '-o', $desktopPublicExe)
                    $desktopDevelopment = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'development', '-target', 'desktop', '-frontend-dist-development', $frontendDevelopmentDist, '-o', $desktopDevelopmentExe)
                    Set-Result 'desktop build (Public + Development)' $(if ($desktopPublic.Code -eq 0 -and $desktopDevelopment.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($desktopPublic.Code -ne 0) { $desktopPublic.Output.Trim() } elseif ($desktopDevelopment.Code -ne 0) { $desktopDevelopment.Output.Trim() })
                    if ($desktopPublic.Code -eq 0) {
                        $desktopHelp = & $desktopPublicExe -h 2>&1 | Out-String
                        $desktopFlagLeak = @('replay', 'replay-pacing', 'autostart', 'capture-diagnostics', 'webview-debug-port', 'overlay', 'showcase', 'showcase-overlays', 'asset-root', 'font-root', 'master-db', 'webview-args', 'simulate-capture-environment', 'simulate-npcap-offline', 'record-browser-url', 'simulate-ui-languages', 'history-db') | Where-Object { $desktopHelp -match "-$_\b" }
                        $desktopLatin = [Text.Encoding]::GetEncoding(28591).GetString([IO.File]::ReadAllBytes($desktopPublicExe))
                        $desktopStringLeak = @(($developmentStrings + @('remote-debugging-port=%d')) | Where-Object { $desktopLatin.Contains($_) })
                        $desktopSymbolLeak = @(Get-DevelopmentSymbols $desktopPublicExe)
                        $desktopProblems = @()
                        if ($desktopHelp -notmatch '(?m)^\s+-version\b') { $desktopProblems += 'help text not captured' }
                        if ($desktopFlagLeak) { $desktopProblems += "flags: $($desktopFlagLeak -join ', ')" }
                        if ($desktopStringLeak) { $desktopProblems += "strings: $($desktopStringLeak -join ', ')" }
                        if ($desktopSymbolLeak) { $desktopProblems += "symbols: $($desktopSymbolLeak -join ', ')" }
                        Set-Result 'desktop public flags + strings' $(if ($desktopProblems.Count -eq 0) { 'PASS' } else { 'FAIL' }) ($desktopProblems -join '; ')

                        # Both desktop profiles must carry the same distinct manifest images.
                        $blobPattern = [regex]'blobs/[0-9a-f]{64}'
                        $packedBlobs = [Collections.Generic.HashSet[string]]::new()
                        foreach ($match in $blobPattern.Matches($desktopLatin)) { [void]$packedBlobs.Add($match.Value) }
                        $wantedBlobs = @($assetManifest.entries | ForEach-Object { "blobs/$($_.sha256)" } | Sort-Object -Unique)
                        $absentBlobs = @($wantedBlobs | Where-Object { -not $packedBlobs.Contains($_) })
                        $developmentBlobs = [Collections.Generic.HashSet[string]]::new()
                        if ($desktopDevelopment.Code -eq 0) {
                            $developmentLatin = [Text.Encoding]::GetEncoding(28591).GetString([IO.File]::ReadAllBytes($desktopDevelopmentExe))
                            foreach ($match in $blobPattern.Matches($developmentLatin)) { [void]$developmentBlobs.Add($match.Value) }
                        }
                        $developmentAbsent = @($wantedBlobs | Where-Object { -not $developmentBlobs.Contains($_) })
                        $imagesOk = $wantedBlobs.Count -gt 0 -and $absentBlobs.Count -eq 0 -and $packedBlobs.Count -eq $wantedBlobs.Count -and $developmentAbsent.Count -eq 0 -and $developmentBlobs.Count -eq $wantedBlobs.Count
                        Set-Result 'desktop embedded images' $(if ($imagesOk) { 'PASS' } else { 'FAIL' }) "Public $($packedBlobs.Count)/$($wantedBlobs.Count), Development $($developmentBlobs.Count)/$($wantedBlobs.Count) distinct images"

                        # Both executables carry the tracked Master byte for byte, and the same code reads it to the same answers.
                        $trackedMaster = Join-Path $repoRoot 'data/master_data.db'
                        $masterSize = (Get-Item -LiteralPath $trackedMaster).Length
                        $masterHash = (Get-FileHash -LiteralPath $trackedMaster -Algorithm SHA256).Hash
                        # The SQLite libraries in the executable carry the "SQLite format 3" magic string too, so the copy
                        # is located by the tracked file's own 100-byte database header and every candidate is hashed.
                        $masterHead = [Text.Encoding]::GetEncoding(28591).GetString([byte[]](Get-Content -LiteralPath $trackedMaster -AsByteStream -TotalCount 100))
                        $embeddedHash = {
                            param([string]$latin)
                            $found = ''
                            for ($start = $latin.IndexOf($masterHead, [StringComparison]::Ordinal); $start -ge 0 -and $start + $masterSize -le $latin.Length; $start = $latin.IndexOf($masterHead, $start + 1, [StringComparison]::Ordinal)) {
                                $bytes = [Text.Encoding]::GetEncoding(28591).GetBytes($latin.Substring($start, $masterSize))
                                $found = [BitConverter]::ToString([Security.Cryptography.SHA256]::HashData($bytes)).Replace('-', '')
                                if ($found -eq $masterHash) { break }
                            }
                            return $found
                        }
                        $publicMaster = & $embeddedHash $desktopLatin
                        $developmentMaster = if ($desktopDevelopment.Code -eq 0) { & $embeddedHash $developmentLatin } else { '' }
                        $digests = foreach ($profile in @(@{ Name = 'Public'; Tags = @() }, @{ Name = 'Development'; Tags = @('-tags', 'wwdev') })) {
                            $run = Invoke-Go (@('test', '-count=1', '-v', "-overlay=$overlay") + $profile.Tags + @('-run', '^TestMasterLookupDigest$', './internal/capture'))
                            if ($run.Output -match 'master lookup digest: ([0-9a-f]{64})') { $Matches[1] } else { "missing-$($profile.Name)" }
                        }
                        $masterProblems = @(
                            if ($publicMaster -ne $masterHash) { 'no Public copy' }
                            if ($developmentMaster -ne $masterHash) { 'no Development copy' }
                            if ($digests[0] -ne $digests[1] -or $digests[0] -like 'missing-*') { "lookup digests $($digests -join ' / ')" }
                        )
                        $digestText = if ($digests[0].Length -gt 16) { $digests[0].Substring(0, 16) } else { $digests[0] }
                        Set-Result 'master: embedded bytes + profile lookup parity' $(if ($masterProblems.Count -eq 0) { 'PASS' } else { 'FAIL' }) "SHA-256 $($masterHash.Substring(0, 16)); lookup digest $digestText$(if ($masterProblems.Count) { '; ' + ($masterProblems -join '; ') })"
                    } else {
                        Set-Unreached $desktopSteps 'BLOCKED' 'blocked: the Public desktop build failed'
                    }
                }

                # 4. Replay regression against private archived captures (Maintainer, with -PcapDirectory).
                if ($PcapDirectory -eq '') {
                    Set-Unreached $replaySteps 'NOT RUN' 'no -PcapDirectory'
                } elseif (-not (Test-Path -LiteralPath $PcapDirectory -PathType Container)) {
                    Set-Unreached $replaySteps 'BLOCKED' "-PcapDirectory $PcapDirectory is not a directory"
                } else {
                    $allPcaps = @(Get-ChildItem -LiteralPath $PcapDirectory -Filter *.pcap -Recurse)
                    $failures = @()
                    # WWlogs decodes the current game version only. Every capture still goes through the output audit, but
                    # only captures whose protocol session reported the current version form the corpus of steps 5 to 9;
                    # older versions and captures without a handshake are counted as excluded, never decoded.
                    $pcaps = @()
                    $excluded = @{}
                    foreach ($pcap in $allPcaps) {
                        $isolated = Join-Path $buildDirectory ("run-" + [Guid]::NewGuid().ToString('N'))
                        New-Item -ItemType Directory -Path $isolated | Out-Null
                        $before = @(Get-ChildItem -LiteralPath $isolated -Force -Recurse).Count
                        Push-Location $isolated
                        try {
                            $publicOut = & $publicExe -ip $ReplayIp -pcap-in $pcap.FullName 2>&1
                            $publicCode = $LASTEXITCODE
                            $developmentOut = & $developmentExe -ip $ReplayIp -pcap-in $pcap.FullName 2>&1
                            $developmentCode = $LASTEXITCODE
                        } finally { Pop-Location }
                        $created = @(Get-ChildItem -LiteralPath $isolated -Force -Recurse).Count - $before
                        $publicLine = ($publicOut | Where-Object { "$_" -like 'statistics_final *' }) -replace '^statistics_final ', ''
                        $developmentLine = ($developmentOut | Where-Object { "$_" -like 'statistics_final *' }) -replace '^statistics_final ', ''
                        if ($publicCode -ne 0 -or $developmentCode -ne 0) { $failures += "$($pcap.Name) (exit codes $publicCode/$developmentCode)" } elseif ($created -ne 0 -or -not $publicLine -or $publicLine -ne $developmentLine) { $failures += $pcap.Name }
                        $protocolLine = "$($developmentOut | Where-Object { "$_" -like 'protocol_session *' } | Select-Object -Last 1)"
                        $protocolStatus = if ($protocolLine -match 'status=(\S+) game_version=(\S+)') { "$($Matches[1]) $($Matches[2])" } else { 'missing' }
                        if ($protocolStatus -like 'supported *') {
                            $pcaps += $pcap
                        } else {
                            $excluded[$protocolStatus] = 1 + [int]$excluded[$protocolStatus]
                            if ($protocolStatus -eq 'missing') { $failures += "$($pcap.Name) (protocol $protocolStatus)" }
                        }
                    }
                    $excludedText = ($excluded.GetEnumerator() | Sort-Object Name | ForEach-Object { "$($_.Value) $($_.Name)" }) -join ', '
                    # A given capture directory without captures is a missing required input, not an optional one.
                    Set-Result 'replay: public output audit + statistics equality' $(if ($allPcaps.Count -gt 0 -and $failures.Count -eq 0) { 'PASS' } elseif ($allPcaps.Count -eq 0) { 'BLOCKED' } else { 'FAIL' }) "$($allPcaps.Count) captures; current game version corpus $($pcaps.Count)$(if ($excludedText) { "; not decoded: $excludedText" })$(if ($failures) { "; failed: $($failures -join ', ')" })"

                    # 5. Encounter replay: every capture twice through the development test binary; segmentation and
                    # statistics must repeat exactly and the encounter partition must add up to the interval totals.
                    if ($pcaps.Count -eq 0) {
                        Set-Unreached $replaySteps 'BLOCKED' 'no capture of the current game version under -PcapDirectory'
                    } else {
                        # The core collectors run from the core's test binary; the encounter collector reads the
                        # Development encounter output, so it runs from the Development module's.
                        $testBinary = Join-Path $buildDirectory 'capture-dev.test.exe'
                        $developmentTestBinary = Join-Path $buildDirectory 'devcapture.test.exe'
                        $testBuild = Invoke-Go @('run', './scripts/embed-key.go', '-profile', 'development', '-test-binary', './internal/capture', '-o', $testBinary)
                        if ($testBuild.Code -eq 0) {
                            $testBuild = Invoke-Go @('run', './scripts/embed-key.go', '-profile', 'development', '-test-binary', './internal/devcapture', '-o', $developmentTestBinary)
                        }
                        if ($testBuild.Code -ne 0) {
                            Set-Result 'replay: encounter determinism + partition' 'FAIL' $testBuild.Output.Trim()
                            Set-Unreached $replaySteps 'BLOCKED' 'blocked: the replay test binaries did not build'
                        } else {
                            $listPath = Join-Path $buildDirectory 'encounter-pcaps.txt'
                            $root = (Resolve-Path -LiteralPath $PcapDirectory).ProviderPath
                            $lines = foreach ($pcap in $pcaps) {
                                $name = $pcap.FullName.Substring($root.Length).TrimStart([char]'\', [char]'/') -replace '[\\/]', '__'
                                "$name|$($pcap.FullName)"
                            }
                            Set-Content -LiteralPath $listPath -Value $lines -Encoding utf8
                            $env:WWLOGS_ENCOUNTER_REPLAY_OUT = Join-Path $buildDirectory 'encounter-replay'
                            $env:WWLOGS_ENCOUNTER_REPLAY_PCAPS = $listPath
                            $env:WWLOGS_ENCOUNTER_REPLAY_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                            try {
                                $encounter = Invoke-Collector $developmentTestBinary 'TestEncounterReplayCollector' $env:WWLOGS_ENCOUNTER_REPLAY_OUT $pcaps.Count '*.encounters.json'
                            } finally {
                                Remove-Item Env:WWLOGS_ENCOUNTER_REPLAY_OUT, Env:WWLOGS_ENCOUNTER_REPLAY_PCAPS, Env:WWLOGS_ENCOUNTER_REPLAY_MASTER_DB -ErrorAction SilentlyContinue
                            }
                            Set-Result 'replay: encounter determinism + partition' $(if (-not $encounter.Problem) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($encounter.Problem) { "; $($encounter.Problem): $($encounter.Tail)" })"

                            # 6. Host session equivalence: the application host path (per-protocol-session intervals,
                            # capture.Session, view snapshots) must leave every total, encounter, and integrity unchanged.
                            $env:WWLOGS_SESSION_REPLAY_OUT = Join-Path $buildDirectory 'session-replay'
                            $env:WWLOGS_SESSION_REPLAY_PCAPS = $listPath
                            $env:WWLOGS_SESSION_REPLAY_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                            try {
                                $session = Invoke-Collector $testBinary 'TestSessionReplayCollector' $env:WWLOGS_SESSION_REPLAY_OUT $pcaps.Count '*.session.json'
                            } finally {
                                Remove-Item Env:WWLOGS_SESSION_REPLAY_OUT, Env:WWLOGS_SESSION_REPLAY_PCAPS, Env:WWLOGS_SESSION_REPLAY_MASTER_DB -ErrorAction SilentlyContinue
                            }
                            Set-Result 'replay: host session equivalence' $(if (-not $session.Problem) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($session.Problem) { "; $($session.Problem): $($session.Tail)" })"

                            # 7. Transport perturbation: duplicated, reordered, truncated and late-started deliveries of
                            # every capture, derived in memory; totals and splits must hold (see the test's header).
                            $env:WWLOGS_PERTURBATION_OUT = Join-Path $buildDirectory 'perturbation'
                            $env:WWLOGS_PERTURBATION_PCAPS = $listPath
                            $env:WWLOGS_PERTURBATION_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                            try {
                                $perturbation = Invoke-Collector $testBinary 'TestTransportPerturbationCollector' $env:WWLOGS_PERTURBATION_OUT $pcaps.Count '*.perturbation.json'
                            } finally {
                                Remove-Item Env:WWLOGS_PERTURBATION_OUT, Env:WWLOGS_PERTURBATION_PCAPS, Env:WWLOGS_PERTURBATION_MASTER_DB -ErrorAction SilentlyContinue
                            }
                            Set-Result 'replay: transport perturbation' $(if (-not $perturbation.Problem) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($perturbation.Problem) { "; $($perturbation.Problem): $($perturbation.Tail)" })"

                            # 8. Semantic regression: real captures must list the right statuses in the right rows and name the
                            # right skills. Totals alone cannot catch a status shown in the wrong row or a skill losing its name.
                            # The corpus must also hold every required Golden capture of the registry, run and passed: a
                            # corpus without one is not the Maintainer's corpus, even if the captures it has pass.
                            $coveragePath = if ($EvidenceOut -ne '') { Join-Path $EvidenceOut 'golden-coverage.json' } else { Join-Path $buildDirectory 'golden-coverage.json' }
                            Remove-Item -LiteralPath $coveragePath -ErrorAction SilentlyContinue
                            $env:WWLOGS_SEMANTIC_REPLAY_PCAPS = $listPath
                            $env:WWLOGS_SEMANTIC_REPLAY_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                            $env:WWLOGS_SEMANTIC_REPLAY_COVERAGE = $coveragePath
                            try {
                                $semantic = Invoke-Collector $testBinary 'TestSemanticReplayCollector'
                            } finally {
                                Remove-Item Env:WWLOGS_SEMANTIC_REPLAY_PCAPS, Env:WWLOGS_SEMANTIC_REPLAY_MASTER_DB, Env:WWLOGS_SEMANTIC_REPLAY_COVERAGE -ErrorAction SilentlyContinue
                            }
                            $goldenProblems = @()
                            if (-not $semantic.Problem) {
                                if (-not (Test-Path -LiteralPath $coveragePath -PathType Leaf)) {
                                    $goldenProblems += 'TestSemanticReplayCollector wrote no Golden coverage'
                                } else {
                                    $registry = Get-Content -LiteralPath (Join-Path $repoRoot 'tests/capture/golden_registry.json') -Raw | ConvertFrom-Json
                                    $goldenProblems += @(Test-VerifyGoldenCoverage $registry (Get-Content -LiteralPath $coveragePath -Raw | ConvertFrom-Json))
                                }
                            }
                            $semanticProblems = @($(if ($semantic.Problem) { "$($semantic.Problem): $($semantic.Tail)" })) + $goldenProblems
                            Set-Result 'replay: effect and skill semantics' $(if ($semanticProblems.Count -eq 0) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($semanticProblems.Count) { "; $($semanticProblems -join '; ')" } else { '; every required Golden capture ran and passed' })"

                            # 9. Combat History: every capture through the host session path into a fresh history file, in both
                            # profiles. History must not change the final view, must store every finished encounter and accepted
                            # result exactly once as the engine produced them, recompute from its results, read the same after
                            # reopening, and both profiles must store the same history (see the test's header).
                            $historySummaries = @{}
                            $historyFailures = @()
                            foreach ($historyProfile in @('public', 'development')) {
                                $historyBinary = Join-Path $buildDirectory "history-$historyProfile.test.exe"
                                $historyBuild = Invoke-Go @('run', './scripts/embed-key.go', '-profile', $historyProfile, '-test-binary', './internal/history', '-o', $historyBinary)
                                if ($historyBuild.Code -ne 0) { $historyFailures += "$historyProfile build: $($historyBuild.Output.Trim())"; continue }
                                $historyOut = Join-Path $buildDirectory "history-$historyProfile"
                                $env:WWLOGS_HISTORY_REPLAY_OUT = $historyOut
                                $env:WWLOGS_HISTORY_REPLAY_PCAPS = $listPath
                                $env:WWLOGS_HISTORY_REPLAY_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                                try {
                                    $historyRun = Invoke-Collector $historyBinary 'TestHistoryReplayCollector' $historyOut 1 'summary.json'
                                } finally {
                                    Remove-Item Env:WWLOGS_HISTORY_REPLAY_OUT, Env:WWLOGS_HISTORY_REPLAY_PCAPS, Env:WWLOGS_HISTORY_REPLAY_MASTER_DB -ErrorAction SilentlyContinue
                                }
                                if ($historyRun.Problem) { $historyFailures += "$historyProfile`: $($historyRun.Problem): $($historyRun.Tail)"; continue }
                                $historySummaries[$historyProfile] = Get-Content -LiteralPath (Join-Path $historyOut 'summary.json') -Raw
                                if ($EvidenceOut -ne '' -and $historyProfile -eq 'public') { Copy-Item -LiteralPath (Join-Path $historyOut 'summary.json') -Destination (Join-Path $EvidenceOut 'history-summary.json') -Force }
                            }
                            if ($historyFailures.Count -eq 0 -and $historySummaries['public'] -ne $historySummaries['development']) { $historyFailures += 'Public and Development stored different histories' }
                            $historyDetail = if ($historySummaries['public']) { $summary = $historySummaries['public'] | ConvertFrom-Json; "$($pcaps.Count) captures, $($summary.listedEncounters) encounters, $($summary.storedEvents) results, $($summary.effectChanges) effect changes of $($summary.effectInstances) instances, both profiles equal" } else { '' }
                            Set-Result 'replay: combat history fidelity' $(if ($historyFailures.Count -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($historyFailures.Count -eq 0) { $historyDetail } else { $historyFailures -join '; ' })
                        }
                    }
                }
            }
        }
    } finally {
        Remove-Item -LiteralPath $buildDirectory -Recurse -Force -ErrorAction SilentlyContinue
    }
} finally {
    Pop-Location
    foreach ($name in $clearedEnvironment.Keys) { Set-Item -LiteralPath "Env:$name" -Value $clearedEnvironment[$name] }
}

# A declared step that no branch above reported is a defect of this script, never a pass.
Set-Unreached $declaredSteps 'BLOCKED' 'not reached: verify.ps1 reported no result for this step'
$verdict = Get-VerifyVerdict $Mode @($declaredSteps | ForEach-Object { @($results[$_] -split ' - ', 2)[0] })

if ($EvidenceOut -ne '') {
    # The states exactly as reported above, in declared order, with the mode, commit and working tree they describe.
    $commit = "$(& git -C $repoRoot rev-parse HEAD 2>$null)".Trim()
    $dirty = @(& git -C $repoRoot status --porcelain 2>$null).Count -gt 0
    $steps = foreach ($name in $declaredSteps) {
        $parts = @($results[$name] -split ' - ', 2)
        [ordered]@{ name = $name; state = $parts[0]; private = $privateNeeds.Contains($name); detail = $(if ($parts.Count -gt 1) { $parts[1] } else { '' }) }
    }
    $testCounts = [ordered]@{}
    foreach ($profile in $goTests.Keys) {
        $tests = $goTests[$profile]
        $testCounts[$profile] = [ordered]@{ passed = $tests.Passed; failed = $tests.Failed; skipped = $tests.Skipped; subtests = $tests.Subtests; skippedTests = @($tests.SkippedTests) }
    }
    $evidence = [ordered]@{ commit = $commit; dirty = $dirty; mode = $Mode; pcapDirectory = ($PcapDirectory -ne ''); packages = $testPackages; overall = $verdict.Overall; counts = $verdict.Counts; goTests = $testCounts; results = @($steps) }
    $evidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $EvidenceOut 'verify-results.json') -Encoding utf8
}

Write-Host ''
Write-Host "Summary: $($declaredSteps.Count) steps: $(($verdict.Counts.GetEnumerator() | ForEach-Object { "$($_.Value) $($_.Key)" }) -join ', ')"
if ($verdict.Counts['SKIP'] -gt 0) { Write-Host "Skipped: $(@($privateNeeds.Keys | Where-Object { $results[$_] -like 'SKIP*' }).Count) private checks (Public mode reads no private material)" }
if ($verdict.Counts['NOT RUN'] -gt 0) { Write-Host "Not run: $((@($declaredSteps | Where-Object { $results[$_] -like 'NOT RUN*' })) -join ', ')" }
Write-Host $verdict.Line
exit $verdict.ExitCode
