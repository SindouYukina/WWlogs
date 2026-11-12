<#
.SYNOPSIS
  The one WWlogs build entry point: builds one build run of the Development and/or Public executable.

.DESCRIPTION
  Builds the frontend of each requested profile outside the repository, then runs scripts/embed-key.go, the build
  orchestrator. It allocates one official Build Number for the whole run from the owner's Git-ignored
  .local/build-state.json, builds and verifies every requested executable in a staging location, installs them
  together, and only then records the number. A failed or partial run installs nothing and leaves the counter as it
  was. Without the owner's counter the executables are built and labelled Unofficial.

  Default outputs: bin\development\WWlogs.exe and bin\public\WWlogs.exe (the console analyzer target writes
  WWlogs-Capture.exe in the same directories).
#>
param(
    # Public is the default so that forgetting the parameter can never produce a diagnostic build. Both builds the two
    # profiles in one build run that shares one Build Number.
    [ValidateSet('Public', 'Development', 'Both')]
    [string]$Profile = 'Public',
    # Desktop builds WWlogs.exe, the Wails desktop host with the embedded frontend; Capture builds the console analyzer.
    [ValidateSet('Desktop', 'Capture')]
    [string]$Target = 'Desktop',
    # Optional output file for a single-profile run.
    [string]$Output = '',
    # Optional root of the per-profile output directories; defaults to bin.
    [string]$OutputRoot = '',
    # Fail when tracked files have uncommitted changes. Use this for any executable that is distributed.
    [switch]$RequireClean,
    # Build without reading or advancing the official counter; the executables say Unofficial.
    [switch]$Unofficial,
    # Owner-directed first initialization of the official counter. Refused when a counter exists or existed.
    [switch]$InitializeBuildNumber,
    # Isolated counter directory for build-system tests; defaults to .local.
    [string]$BuildState = ''
)

$ErrorActionPreference = 'Stop'
# Build with the system Go only: never switch to, or download, another toolchain for a newer go.mod requirement.
$env:GOTOOLCHAIN = 'local'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$goProfile = $Profile.ToLowerInvariant()
$arguments = @('run', './scripts/embed-key.go', '-profile', $goProfile, '-target', $Target.ToLowerInvariant())
if ($Output -ne '') { $arguments += @('-o', $Output) }
if ($OutputRoot -ne '') { $arguments += @('-output-root', $OutputRoot) }
if ($RequireClean) { $arguments += '-require-clean' }
if ($Unofficial) { $arguments += '-unofficial' }
if ($InitializeBuildNumber) { $arguments += '-init-build-number' }
if ($BuildState -ne '') { $arguments += @('-build-state', $BuildState) }

$frontendDirectories = @()
Push-Location $repoRoot
try {
    if ($Target -eq 'Desktop') {
        # Each profile's frontend is built outside the repository and embedded through the build overlay. Building it
        # is an intermediate step and uses no Build Number.
        $profiles = if ($Profile -eq 'Both') { @('Development', 'Public') } else { @($Profile) }
        foreach ($current in $profiles) {
            $directory = Join-Path ([IO.Path]::GetTempPath()) ('wwlogs-frontend-dist-' + [Guid]::NewGuid().ToString('N'))
            $frontendDirectories += $directory
            & (Join-Path $PSScriptRoot 'frontend.ps1') -Action Build -OutDir $directory -Profile $current
            $arguments += @("-frontend-dist-$($current.ToLowerInvariant())", $directory)
        }
    }
    & go @arguments
    exit $LASTEXITCODE
} finally {
    foreach ($directory in $frontendDirectories) {
        if (Test-Path -LiteralPath $directory) { Remove-Item -LiteralPath $directory -Recurse -Force }
    }
    Pop-Location
}
