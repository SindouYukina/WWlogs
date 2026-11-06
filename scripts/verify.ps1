<#
.SYNOPSIS
  One local verification entry point for both build profiles.

.DESCRIPTION
  Runs formatting, vet, and unit tests for the Public and Development profiles, builds both executables with
  embed-key, audits the public executable's output surface, and checks the build identity.
  Every step reports PASS, FAIL, NOT RUN, or BLOCKED. Nothing is reported as PASS unless it executed.

  Steps that need local-only material (privateKey.pem, archived PCAP captures) are NOT RUN, not PASS, when
  that material is missing. Pass -PcapDirectory to add the replay regression, the public output audit, and the
  encounter replay (determinism and partition) through an embedded-key development test binary.
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
    # 1. gofmt. Compare after stripping CR so a CRLF checkout does not read as a formatting difference.
    $unformatted = @()
    foreach ($file in (& git ls-files '*.go')) {
        $text = (Get-Content -LiteralPath $file -Raw) -replace "`r", ''
        $formatted = $text | & gofmt 2>$null
        if (($formatted -join "`n").TrimEnd() -ne $text.TrimEnd()) { $unformatted += $file }
    }
    if ($unformatted.Count -eq 0) { Set-Result 'gofmt' 'PASS' } else { Set-Result 'gofmt' 'FAIL' ($unformatted -join ', ') }

    # 2. go vet and go test for each profile.
    $packages = @('./cmd/...', './internal/...', './tests/...')
    foreach ($profile in @(@{ Name = 'Public'; Tags = @() }, @{ Name = 'Development'; Tags = @('-tags', 'wwdev') })) {
        $vet = Invoke-Go (@('vet', "-overlay=$overlay") + $profile.Tags + $packages)
        Set-Result "go vet ($($profile.Name))" $(if ($vet.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($vet.Code -ne 0) { $vet.Output.Trim() })
        $test = Invoke-Go (@('test', '-count=1', "-overlay=$overlay") + $profile.Tags + @('./cmd/capture', './internal/buildinfo', './internal/capture', './internal/service', './internal/desktop', './internal/keyembed', './tests/versioninfo'))
        Set-Result "go test ($($profile.Name))" $(if ($test.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($test.Code -ne 0) { $test.Output.Trim() })
    }

    # 3. Build both executables into a throw-away directory and audit them.
    $keyPath = Join-Path $repoRoot 'privateKey.pem'
    if (-not (Test-Path -LiteralPath $keyPath -PathType Leaf)) {
        Set-Result 'build + output audit' 'NOT RUN' 'privateKey.pem is not present'
    } else {
        $buildDirectory = Join-Path ([IO.Path]::GetTempPath()) ("wwlogs-verify-" + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $buildDirectory | Out-Null
        try {
            $publicExe = Join-Path $buildDirectory 'WWlogs.exe'
            $developmentExe = Join-Path $buildDirectory 'WWlogs-dev.exe'
            $publicBuild = Invoke-Go @('run', './scripts/embed-key.go', '-profile', 'public', '-o', $publicExe)
            $developmentBuild = Invoke-Go @('run', './scripts/embed-key.go', '-profile', 'development', '-o', $developmentExe)
            Set-Result 'build (Public)' $(if ($publicBuild.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($publicBuild.Code -ne 0) { $publicBuild.Output.Trim() })
            Set-Result 'build (Development)' $(if ($developmentBuild.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($developmentBuild.Code -ne 0) { $developmentBuild.Output.Trim() })

            if ($publicBuild.Code -eq 0 -and $developmentBuild.Code -eq 0) {
                # Build identity: profile from compiled constants, SHA-256 against an independent tool.
                $publicVersion = (& $publicExe -version) -join "`n"
                $developmentVersion = (& $developmentExe -version) -join "`n"
                $developmentHash = (Get-FileHash -LiteralPath $developmentExe -Algorithm SHA256).Hash.ToLowerInvariant()
                $identityOk = ($publicVersion -match 'Build profile: public') -and ($developmentVersion -match 'Build profile: development') -and
                    ($developmentVersion -match "Executable SHA-256: $developmentHash") -and ($publicVersion -notmatch 'Executable SHA-256') -and
                    ($publicVersion -match 'Commit: [0-9a-f]{40}')
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
                    $desktopPublic = Invoke-Go @('run', './scripts/embed-key.go', '-profile', 'public', '-target', 'desktop', '-frontend-dist', $frontendDist, '-o', $desktopPublicExe)
                    $desktopDevelopment = Invoke-Go @('run', './scripts/embed-key.go', '-profile', 'development', '-target', 'desktop', '-frontend-dist', $frontendDevelopmentDist, '-o', $desktopDevelopmentExe)
                    Set-Result 'desktop build (Public + Development)' $(if ($desktopPublic.Code -eq 0 -and $desktopDevelopment.Code -eq 0) { 'PASS' } else { 'FAIL' }) $(if ($desktopPublic.Code -ne 0) { $desktopPublic.Output.Trim() } elseif ($desktopDevelopment.Code -ne 0) { $desktopDevelopment.Output.Trim() })
                    if ($desktopPublic.Code -eq 0) {
                        $desktopHelp = (& $desktopPublicExe -h 2>&1) -join "`n"
                        $desktopFlagLeak = @('replay', 'replay-pacing', 'autostart', 'capture-diagnostics', 'webview-debug-port', 'publish-interval', 'overlay', 'showcase', 'showcase-overlays', 'asset-root') | Where-Object { $desktopHelp -match "-$_\b" }
                        $desktopLatin = [Text.Encoding]::GetEncoding(28591).GetString([IO.File]::ReadAllBytes($desktopPublicExe))
                        $desktopStringLeak = @('combat-events.jsonl', 'dps-effects.jsonl', 'buff-events.jsonl', 'status.json', 'capture-spool', 'LIVE_DIAGNOSTIC_SNAPSHOT', 'event-jsonl-out', 'pcapgo', 'encounter-events.jsonl', 'BATTLE_STATE_SIGNAL', 'COMBAT_EVENT_RECORD', 'ENCOUNTER_TRANSITION', 'remote-debugging-port=%d') | Where-Object { $desktopLatin.Contains($_) }
                        $desktopProblems = @()
                        if ($desktopFlagLeak) { $desktopProblems += "flags: $($desktopFlagLeak -join ', ')" }
                        if ($desktopStringLeak) { $desktopProblems += "strings: $($desktopStringLeak -join ', ')" }
                        Set-Result 'desktop public flags + strings' $(if ($desktopProblems.Count -eq 0) { 'PASS' } else { 'FAIL' }) ($desktopProblems -join '; ')
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
                            $publicOut = & $publicExe -ip $ReplayIp -master-db (Join-Path $repoRoot 'data/master_data.db') -pcap-in $pcap.FullName 2>&1
                            $developmentOut = & $developmentExe -ip $ReplayIp -master-db (Join-Path $repoRoot 'data/master_data.db') -pcap-in $pcap.FullName 2>&1
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
