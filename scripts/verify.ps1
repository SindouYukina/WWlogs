<#
.SYNOPSIS
  One local verification entry point for both build profiles.

.DESCRIPTION
  Runs formatting, vet, and unit tests for the Public and Development profiles, builds both executables with
  embed-key, audits the public executable's output surface, and checks the build identity. These throw-away builds
  are Unofficial: verification never reads or advances the owner's official build counter.
  Every step reports PASS, FAIL, NOT RUN, or BLOCKED. Nothing is reported as PASS unless it executed.

  Steps that need local-only material (the owner-local key input, archived PCAP captures) are NOT RUN, not PASS, when
  that material is missing. Pass -PcapDirectory to add the replay regression, the public output audit, the
  encounter replay (determinism and partition), the host session equivalence and the transport perturbation replay
  through an embedded-key development test binary.
#>
param(
    # Directory that contains archived *.pcap files. Local and private; never read from the repository.
    [string]$PcapDirectory = '',
    # Local interface address used only to decide packet direction during offline replay.
    [string]$ReplayIp = '192.168.1.2'
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$overlay = Join-Path $repoRoot 'tests/go-overlay.json'
$results = [ordered]@{}

function Set-Result([string]$Name, [string]$State, [string]$Detail = '') {
    $results[$Name] = "$State$(if ($Detail) { " - $Detail" })"
    Write-Host ("{0,-9} {1}{2}" -f $State, $Name, $(if ($Detail) { "  ($Detail)" } else { '' }))
}

function Invoke-Go([string[]]$GoArguments) {
    $output = & go @GoArguments 2>&1
    return [pscustomobject]@{ Code = $LASTEXITCODE; Output = ($output | Out-String) }
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

    # 2. go vet and go test for each profile.
    $packages = @('./cmd/...', './internal/...', './tests/...')
    foreach ($profile in @(@{ Name = 'Public'; Tags = @() }, @{ Name = 'Development'; Tags = @('-tags', 'wwdev') })) {
        $vet = Invoke-Go (@('vet', "-overlay=$overlay") + $profile.Tags + $packages)
        Set-Result "go vet ($($profile.Name))" $(if ($vet.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($vet.Code -ne 0) { $vet.Output.Trim() })
        $test = Invoke-Go (@('test', '-count=1', "-overlay=$overlay") + $profile.Tags + @('./cmd/capture', './internal/buildinfo', './internal/capture', './internal/history', './internal/service', './internal/desktop', './internal/keyembed', './tests/assets', './tests/buildrun', './tests/versioninfo'))
        Set-Result "go test ($($profile.Name))" $(if ($test.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($test.Code -ne 0) { $test.Output.Trim() })
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
    foreach ($local in @('assets/game/local.tmp', 'assets/game/local.png', 'work/fonts/candidates/misans/local.ttf', 'work/fonts/src/misans/local.zip')) {
        & git check-ignore -q -- $local
        if ($LASTEXITCODE -ne 0) { $assetProblems += "$local is not ignored" }
    }
    if (@(& git ls-files -- work 'assets/**/*.ttf' 'assets/**/*.otf' 'assets/**/*.woff2').Count -gt 0) { $assetProblems += 'font candidates or work files are tracked' }
    Set-Result 'game assets: version control' $(if ($assetProblems.Count -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($assetProblems.Count -gt 0) { $assetProblems -join '; ' } else { "$($assetManifest.entries.Count) images" })

    # 3. Build both executables into a throw-away directory and audit them.
    # The restricted embed-key step alone probes and reads its owner-local key input.
    $keyStatus = Invoke-Go @('run', './scripts/embed-key.go', '-check-key')
    $missingKeyMessage = 'owner-local private key is not present'
    if ($keyStatus.Code -ne 0) {
        if ($keyStatus.Output.Contains($missingKeyMessage)) {
            Set-Result 'build + output audit' 'NOT RUN' $missingKeyMessage
        } else {
            Set-Result 'build + output audit' 'FAIL' $keyStatus.Output.Trim()
        }
    } else {
        $buildDirectory = Join-Path ([IO.Path]::GetTempPath()) ("wwlogs-verify-" + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $buildDirectory | Out-Null
        try {
            $publicExe = Join-Path $buildDirectory 'WWlogs.exe'
            $developmentExe = Join-Path $buildDirectory 'WWlogs-dev.exe'
            $publicBuild = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'public', '-o', $publicExe)
            $developmentBuild = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'development', '-o', $developmentExe)
            $publicKeyMissing = $publicBuild.Output.Contains($missingKeyMessage)
            $developmentKeyMissing = $developmentBuild.Output.Contains($missingKeyMessage)
            if ($publicKeyMissing -or $developmentKeyMissing) {
                Set-Result 'build + output audit' 'NOT RUN' $missingKeyMessage
            }
            Set-Result 'build (Public)' $(if ($publicKeyMissing) { 'NOT RUN' } elseif ($publicBuild.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($publicKeyMissing) { $missingKeyMessage } elseif ($publicBuild.Code -ne 0) { $publicBuild.Output.Trim() })
            Set-Result 'build (Development)' $(if ($developmentKeyMissing) { 'NOT RUN' } elseif ($developmentBuild.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($developmentKeyMissing) { $missingKeyMessage } elseif ($developmentBuild.Code -ne 0) { $developmentBuild.Output.Trim() })

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
                $help = (& $publicExe -h 2>&1) -join "`n"
                $flagLeak = @('pcap-out', 'event-jsonl-out', 'buff-event-out', 'status-file', 'research', 'live-diagnostic-out', 'frame-archive-out', 'protocol-schema', 'encounter-out') | Where-Object { $help -match "-$_\b" }
                Set-Result 'public flags' $(if (-not $flagLeak) { 'PASS' } else { 'FAIL' }) $(if ($flagLeak) { "exposes: $($flagLeak -join ', ')" })
                $bytes = [IO.File]::ReadAllBytes($publicExe)
                $latin = [Text.Encoding]::GetEncoding(28591).GetString($bytes)
                $stringLeak = @('combat-events.jsonl', 'dps-effects.jsonl', 'buff-events.jsonl', 'status.json', 'capture-spool', 'LIVE_DIAGNOSTIC_SNAPSHOT', 'event-jsonl-out', 'pcapgo', 'encounter-events.jsonl', 'BATTLE_STATE_SIGNAL', 'COMBAT_EVENT_RECORD', 'ENCOUNTER_TRANSITION') | Where-Object { $latin.Contains($_) }
                Set-Result 'public binary strings' $(if (-not $stringLeak) { 'PASS' } else { 'FAIL' }) $(if ($stringLeak) { "contains: $($stringLeak -join ', ')" })

                # Desktop host: the frontend is typechecked and built outside the repository with the pinned Node.js 24,
                # embedded into both profiles, and the Public desktop executable is audited like the console one.
                $frontendDist = Join-Path $buildDirectory 'frontend-dist'
                $frontendDevelopmentDist = Join-Path $buildDirectory 'frontend-dist-development'
                $frontendOutput = & (Join-Path $PSScriptRoot 'frontend.ps1') -Action Build -OutDir $frontendDist -Profile Public 2>&1 | Out-String
                if ($LASTEXITCODE -eq 0) {
                    $frontendOutput += & (Join-Path $PSScriptRoot 'frontend.ps1') -Action Build -OutDir $frontendDevelopmentDist -Profile Development 2>&1 | Out-String
                }
                if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath (Join-Path $frontendDist 'index.html')) -or -not (Test-Path -LiteralPath (Join-Path $frontendDevelopmentDist 'index.html'))) {
                    $state = if ($frontendOutput -match 'Node.js 24 was not found|is not 24\.x') { 'NOT RUN' } else { 'FAIL' }
                    Set-Result 'frontend typecheck + build' $state $frontendOutput.Trim()
                    Set-Result 'desktop build + audit' 'NOT RUN' 'frontend build did not complete'
                } else {
                    Set-Result 'frontend typecheck + build' 'PASS'
                    $desktopPublicExe = Join-Path $buildDirectory 'WWlogs-Desktop.exe'
                    $desktopDevelopmentExe = Join-Path $buildDirectory 'WWlogs-Desktop-dev.exe'
                    $desktopPublic = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'public', '-target', 'desktop', '-frontend-dist-public', $frontendDist, '-o', $desktopPublicExe)
                    $desktopDevelopment = Invoke-Go @('run', './scripts/embed-key.go', '-unofficial', '-profile', 'development', '-target', 'desktop', '-frontend-dist-development', $frontendDevelopmentDist, '-o', $desktopDevelopmentExe)
                    Set-Result 'desktop build (Public + Development)' $(if ($desktopPublic.Code -eq 0 -and $desktopDevelopment.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($desktopPublic.Code -ne 0) { $desktopPublic.Output.Trim() } elseif ($desktopDevelopment.Code -ne 0) { $desktopDevelopment.Output.Trim() })
                    if ($desktopPublic.Code -eq 0) {
                        $desktopHelp = (& $desktopPublicExe -h 2>&1) -join "`n"
                        $desktopFlagLeak = @('replay', 'replay-pacing', 'autostart', 'capture-diagnostics', 'webview-debug-port', 'overlay', 'showcase', 'showcase-overlays', 'asset-root', 'font-root', 'master-db', 'webview-args', 'simulate-capture-environment', 'simulate-npcap-offline', 'record-browser-url', 'simulate-ui-languages') | Where-Object { $desktopHelp -match "-$_\b" }
                        $desktopLatin = [Text.Encoding]::GetEncoding(28591).GetString([IO.File]::ReadAllBytes($desktopPublicExe))
                        $desktopStringLeak = @('combat-events.jsonl', 'dps-effects.jsonl', 'buff-events.jsonl', 'status.json', 'capture-spool', 'LIVE_DIAGNOSTIC_SNAPSHOT', 'event-jsonl-out', 'pcapgo', 'encounter-events.jsonl', 'BATTLE_STATE_SIGNAL', 'COMBAT_EVENT_RECORD', 'ENCOUNTER_TRANSITION', 'remote-debugging-port=%d') | Where-Object { $desktopLatin.Contains($_) }
                        $desktopProblems = @()
                        if ($desktopFlagLeak) { $desktopProblems += "flags: $($desktopFlagLeak -join ', ')" }
                        if ($desktopStringLeak) { $desktopProblems += "strings: $($desktopStringLeak -join ', ')" }
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
                        $embeddedHash = {
                            param([string]$latin)
                            $start = $latin.IndexOf("SQLite format 3$([char]0)", [StringComparison]::Ordinal)
                            if ($start -lt 0 -or $start + $masterSize -gt $latin.Length) { return '' }
                            $bytes = [Text.Encoding]::GetEncoding(28591).GetBytes($latin.Substring($start, $masterSize))
                            return [BitConverter]::ToString([Security.Cryptography.SHA256]::HashData($bytes)).Replace('-', '')
                        }
                        $publicMaster = & $embeddedHash $desktopLatin
                        $developmentMaster = if ($desktopDevelopment.Code -eq 0) { & $embeddedHash $developmentLatin } else { '' }
                        $digests = foreach ($profile in @(@{ Name = 'Public'; Tags = @() }, @{ Name = 'Development'; Tags = @('-tags', 'wwdev') })) {
                            $run = Invoke-Go (@('test', '-count=1', '-v', "-overlay=$overlay") + $profile.Tags + @('-run', '^TestMasterLookupDigest$', './internal/capture'))
                            if ($run.Output -match 'master lookup digest: ([0-9a-f]{64})') { $Matches[1] } else { "missing-$($profile.Name)" }
                        }
                        $masterOk = $publicMaster -eq $masterHash -and $developmentMaster -eq $masterHash -and $digests[0] -eq $digests[1] -and $digests[0] -notlike 'missing-*'
                        Set-Result 'master: embedded bytes + profile lookup parity' $(if ($masterOk) { 'PASS' } else { 'FAIL' }) "SHA-256 $($masterHash.Substring(0, 16)); lookup digest $($digests[0].Substring(0, 16))"
                    }
                }

                # 4. Optional replay regression against private archived captures.
                if ($PcapDirectory -eq '') {
                    Set-Result 'replay: public output audit + statistics equality' 'NOT RUN' 'no -PcapDirectory'
                } else {
                    $pcaps = Get-ChildItem -LiteralPath $PcapDirectory -Filter *.pcap -Recurse
                    $failures = @()
                    foreach ($pcap in $pcaps) {
                        $isolated = Join-Path $buildDirectory ("run-" + [Guid]::NewGuid().ToString('N'))
                        New-Item -ItemType Directory -Path $isolated | Out-Null
                        $before = @(Get-ChildItem -LiteralPath $isolated -Force -Recurse).Count
                        Push-Location $isolated
                        try {
                            $publicOut = & $publicExe -ip $ReplayIp -pcap-in $pcap.FullName 2>&1
                            $developmentOut = & $developmentExe -ip $ReplayIp -pcap-in $pcap.FullName 2>&1
                        } finally { Pop-Location }
                        $created = @(Get-ChildItem -LiteralPath $isolated -Force -Recurse).Count - $before
                        $publicLine = ($publicOut | Where-Object { "$_" -like 'statistics_final *' }) -replace '^statistics_final ', ''
                        $developmentLine = ($developmentOut | Where-Object { "$_" -like 'statistics_final *' }) -replace '^statistics_final ', ''
                        if ($created -ne 0 -or -not $publicLine -or $publicLine -ne $developmentLine) { $failures += $pcap.Name }
                    }
                    Set-Result 'replay: public output audit + statistics equality' $(if ($pcaps.Count -gt 0 -and $failures.Count -eq 0) { 'PASS' } elseif ($pcaps.Count -eq 0) { 'NOT RUN' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($failures) { "; failed: $($failures -join ', ')" })"

                    # 5. Encounter replay: every capture twice through the development test binary; segmentation and
                    # statistics must repeat exactly and the encounter partition must add up to the interval totals.
                    if ($pcaps.Count -eq 0) {
                        Set-Result 'replay: encounter determinism + partition' 'NOT RUN' 'no captures'
                    } else {
                        $testBinary = Join-Path $buildDirectory 'capture-dev.test.exe'
                        $testBuild = Invoke-Go @('run', './scripts/embed-key.go', '-profile', 'development', '-test-binary', './internal/capture', '-o', $testBinary)
                        if ($testBuild.Code -ne 0) {
                            Set-Result 'replay: encounter determinism + partition' 'FAIL' $testBuild.Output.Trim()
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
                                $encounterOut = & $testBinary '-test.run' '^TestEncounterReplayCollector$' '-test.count' '1' 2>&1
                                $encounterCode = $LASTEXITCODE
                            } finally {
                                Remove-Item Env:WWLOGS_ENCOUNTER_REPLAY_OUT, Env:WWLOGS_ENCOUNTER_REPLAY_PCAPS, Env:WWLOGS_ENCOUNTER_REPLAY_MASTER_DB -ErrorAction SilentlyContinue
                            }
                            Set-Result 'replay: encounter determinism + partition' $(if ($encounterCode -eq 0) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($encounterCode -ne 0) { '; ' + (($encounterOut | Select-Object -Last 20) -join ' ') })"

                            # 6. Host session equivalence: the application host path (per-protocol-session intervals,
                            # capture.Session, view snapshots) must leave every total, encounter, and integrity unchanged.
                            $env:WWLOGS_SESSION_REPLAY_OUT = Join-Path $buildDirectory 'session-replay'
                            $env:WWLOGS_SESSION_REPLAY_PCAPS = $listPath
                            $env:WWLOGS_SESSION_REPLAY_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                            try {
                                $sessionOut = & $testBinary '-test.run' '^TestSessionReplayCollector$' '-test.count' '1' 2>&1
                                $sessionCode = $LASTEXITCODE
                            } finally {
                                Remove-Item Env:WWLOGS_SESSION_REPLAY_OUT, Env:WWLOGS_SESSION_REPLAY_PCAPS, Env:WWLOGS_SESSION_REPLAY_MASTER_DB -ErrorAction SilentlyContinue
                            }
                            Set-Result 'replay: host session equivalence' $(if ($sessionCode -eq 0) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($sessionCode -ne 0) { '; ' + (($sessionOut | Select-Object -Last 20) -join ' ') })"

                            # 7. Transport perturbation: duplicated, reordered, truncated and late-started deliveries of
                            # every capture, derived in memory; totals and splits must hold (see the test's header).
                            $env:WWLOGS_PERTURBATION_OUT = Join-Path $buildDirectory 'perturbation'
                            $env:WWLOGS_PERTURBATION_PCAPS = $listPath
                            $env:WWLOGS_PERTURBATION_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                            try {
                                $perturbationOut = & $testBinary '-test.run' '^TestTransportPerturbationCollector$' '-test.count' '1' 2>&1
                                $perturbationCode = $LASTEXITCODE
                            } finally {
                                Remove-Item Env:WWLOGS_PERTURBATION_OUT, Env:WWLOGS_PERTURBATION_PCAPS, Env:WWLOGS_PERTURBATION_MASTER_DB -ErrorAction SilentlyContinue
                            }
                            Set-Result 'replay: transport perturbation' $(if ($perturbationCode -eq 0) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($perturbationCode -ne 0) { '; ' + (($perturbationOut | Select-Object -Last 20) -join ' ') })"

                            # 8. Semantic regression: real captures must list the right statuses in the right rows and name the
                            # right skills. Totals alone cannot catch a status shown in the wrong row or a skill losing its name.
                            $env:WWLOGS_SEMANTIC_REPLAY_PCAPS = $listPath
                            $env:WWLOGS_SEMANTIC_REPLAY_MASTER_DB = Join-Path $repoRoot 'data/master_data.db'
                            try {
                                $semanticOut = & $testBinary '-test.run' '^TestSemanticReplayCollector$' '-test.count' '1' 2>&1
                                $semanticCode = $LASTEXITCODE
                            } finally {
                                Remove-Item Env:WWLOGS_SEMANTIC_REPLAY_PCAPS, Env:WWLOGS_SEMANTIC_REPLAY_MASTER_DB -ErrorAction SilentlyContinue
                            }
                            Set-Result 'replay: effect and skill semantics' $(if ($semanticCode -eq 0) { 'PASS' } else { 'FAIL' }) "$($pcaps.Count) captures$(if ($semanticCode -ne 0) { '; ' + (($semanticOut | Select-Object -Last 12) -join ' ') })"

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
                                    $historyRun = & $historyBinary '-test.run' '^TestHistoryReplayCollector$' '-test.count' '1' 2>&1
                                    $historyCode = $LASTEXITCODE
                                } finally {
                                    Remove-Item Env:WWLOGS_HISTORY_REPLAY_OUT, Env:WWLOGS_HISTORY_REPLAY_PCAPS, Env:WWLOGS_HISTORY_REPLAY_MASTER_DB -ErrorAction SilentlyContinue
                                }
                                if ($historyCode -ne 0) { $historyFailures += "$historyProfile`: $(($historyRun | Select-Object -Last 12) -join ' ')"; continue }
                                $historySummaries[$historyProfile] = Get-Content -LiteralPath (Join-Path $historyOut 'summary.json') -Raw
                            }
                            if ($historyFailures.Count -eq 0 -and $historySummaries['public'] -ne $historySummaries['development']) { $historyFailures += 'Public and Development stored different histories' }
                            $historyDetail = if ($historySummaries['public']) { $summary = $historySummaries['public'] | ConvertFrom-Json; "$($pcaps.Count) captures, $($summary.listedEncounters) encounters, $($summary.storedEvents) results, both profiles equal" } else { '' }
                            Set-Result 'replay: combat history fidelity' $(if ($historyFailures.Count -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($historyFailures.Count -eq 0) { $historyDetail } else { $historyFailures -join '; ' })
                        }
                    }
                }
            }
        } finally {
            Remove-Item -LiteralPath $buildDirectory -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
} finally {
    Pop-Location
}

Write-Host ''
$failed = @($results.GetEnumerator() | Where-Object { $_.Value -like 'FAIL*' -or $_.Value -like 'BLOCKED*' })
Write-Host "Summary: $($results.Count) checks, $($failed.Count) failed"
if ($failed.Count -gt 0) { exit 1 }
exit 0
