param(
    # Public is the default so that forgetting the parameter can never produce a diagnostic build.
    [ValidateSet('Public', 'Development')]
    [string]$Profile = 'Public',
    # Optional output path; defaults to bin\WWlogs.exe (Public) or bin\WWlogs-dev.exe (Development).
    [string]$Output = '',
    # Fail when tracked files have uncommitted changes. Use this for any executable that is distributed.
    [switch]$RequireClean
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$arguments = @('run', './scripts/embed-key.go', '-profile', $Profile.ToLowerInvariant())
if ($Output -ne '') {
    $arguments += @('-o', $Output)
}
if ($RequireClean) {
    $arguments += '-require-clean'
}

Push-Location $repoRoot
try {
    & go @arguments
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
