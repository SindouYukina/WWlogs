param(
    # Public runs the default (untagged) build, Development adds the wwdev tag, Both runs the two in turn.
    [ValidateSet('Public', 'Development', 'Both')]
    [string]$Profile = 'Both',
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$GoTestArgs = @()
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$overlayPath = Join-Path $repoRoot 'tests/go-overlay.json'
if (-not (Test-Path -LiteralPath $overlayPath -PathType Leaf)) {
    throw "Test overlay is missing: $overlayPath"
}

$packages = @('./cmd/capture', './internal/buildinfo', './internal/capture', './internal/history', './internal/service', './internal/desktop', './internal/keyembed', './tests/assets', './tests/buildrun', './tests/versioninfo')
$profiles = if ($Profile -eq 'Both') { @('Public', 'Development') } else { @($Profile) }

Push-Location $repoRoot
try {
    foreach ($current in $profiles) {
        $tagArguments = if ($current -eq 'Development') { @('-tags', 'wwdev') } else { @() }
        Write-Host "== go test ($current profile)"
        $arguments = @("-overlay=$overlayPath") + $tagArguments + $GoTestArgs + $packages
        & go test @arguments
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
    }
    exit 0
} finally {
    Pop-Location
}
