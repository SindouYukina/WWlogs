param(
    [ValidateSet('Public', 'Development', 'Both')]
    [string]$Profile = 'Both',
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$GoTestArgs = @()
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$testGroups = @(
    @{ TestDirectory = 'tests/capture'; PackageDirectory = 'internal/capture' }
)
$replace = [ordered]@{}

foreach ($group in $testGroups) {
    $testDirectory = Join-Path $repoRoot $group.TestDirectory
    $packageDirectory = Join-Path $repoRoot $group.PackageDirectory
    if (-not (Test-Path -LiteralPath $testDirectory -PathType Container)) {
        throw "Test directory does not exist: $testDirectory"
    }
    if (-not (Test-Path -LiteralPath $packageDirectory -PathType Container)) {
        throw "Go package directory does not exist: $packageDirectory"
    }

    foreach ($testFile in Get-ChildItem -LiteralPath $testDirectory -Filter '*_test.go' -File | Sort-Object Name) {
        $virtualFile = Join-Path $packageDirectory ('central_' + $testFile.Name)
        if (Test-Path -LiteralPath $virtualFile) {
            throw "Overlay target already exists: $virtualFile"
        }
        $replace[$virtualFile] = $testFile.FullName
    }
}

if ($replace.Count -eq 0) {
    throw 'No centralized Go test files were found.'
}

$overlayPath = Join-Path ([IO.Path]::GetTempPath()) ('wwlogs-test-overlay-' + [guid]::NewGuid().ToString('N') + '.json')
$overlay = @{ Replace = $replace } | ConvertTo-Json -Depth 4
[IO.File]::WriteAllText($overlayPath, $overlay, [Text.UTF8Encoding]::new($false))

try {
    Push-Location $repoRoot
    try {
        $profiles = if ($Profile -eq 'Both') { @('Public', 'Development') } else { @($Profile) }
        foreach ($current in $profiles) {
            $tagArguments = if ($current -eq 'Development') { @('-tags', 'wwdev') } else { @() }
            Write-Host "== go test ($current profile)"
            $arguments = @("-overlay=$overlayPath") + $tagArguments + $GoTestArgs + @('./cmd/capture', './internal/buildinfo', './internal/capture', './tests/versioninfo')
            & go test @arguments
            if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        }
        exit 0
    } finally {
        Pop-Location
    }
} finally {
    Remove-Item -LiteralPath $overlayPath -Force -ErrorAction SilentlyContinue
}
