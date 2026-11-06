param(
    # Public is the default so that forgetting the parameter can never produce a diagnostic build.
    [ValidateSet('Public', 'Development')]
    [string]$Profile = 'Public',
    # Capture builds the console analyzer; Desktop builds the Wails desktop host with the embedded frontend.
    [ValidateSet('Capture', 'Desktop')]
    [string]$Target = 'Capture',
    # Optional output path; defaults to dist\WWlogs[-Desktop].exe (Public) or dist\WWlogs[-Desktop]-dev.exe (Development).
    [string]$Output = '',
    # Fail when tracked files have uncommitted changes. Use this for any executable that is distributed.
    [switch]$RequireClean
)

$ErrorActionPreference = 'Stop'
# embed-key generates and verifies the shared build/windows/icon.ico as an embedded Windows resource.
# Keep source assets outside bin; only temporary generated resources are cleaned after a build.
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$arguments = @('run', './scripts/embed-key.go', '-profile', $Profile.ToLowerInvariant(), '-target', $Target.ToLowerInvariant())
if ($Output -ne '') {
    $arguments += @('-o', $Output)
}
if ($RequireClean) {
    $arguments += '-require-clean'
}

$frontendDist = ''
Push-Location $repoRoot
try {
    if ($Target -eq 'Desktop') {
        # The frontend is built outside the repository and embedded through the build overlay.
        $frontendDist = Join-Path ([IO.Path]::GetTempPath()) ('wwlogs-frontend-dist-' + [Guid]::NewGuid().ToString('N'))
        & (Join-Path $PSScriptRoot 'frontend.ps1') -Action Build -OutDir $frontendDist
        $arguments += @('-frontend-dist', $frontendDist)
    }
    & go @arguments
    exit $LASTEXITCODE
} finally {
    if ($frontendDist -ne '' -and (Test-Path -LiteralPath $frontendDist)) {
        Remove-Item -LiteralPath $frontendDist -Recurse -Force
    }
    Pop-Location
}
