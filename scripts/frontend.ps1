<#
.SYNOPSIS
  Typecheck or build the WWlogs frontend with the pinned Node.js and npm toolchain.

.DESCRIPTION
  Copies the frontend sources to a temporary directory outside the repository, installs the exact dependencies from
  frontend/package-lock.json there with `npm ci`, and runs the typecheck or the production build. Nothing is installed
  or built inside the repository. The build output goes to -OutDir, which must be outside the repository.

  Node.js is taken from $env:WWLOGS_NODE_HOME, the versioned local WWlogs toolchain, or PATH.
  Node.js and npm must match frontend/package.json.
#>
param(
    [ValidateSet('Typecheck', 'Build')]
    [string]$Action = 'Build',
    # Output directory for Build; must be outside the repository.
    [string]$OutDir = ''
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$frontend = Join-Path $repoRoot 'frontend'
$engines = (Get-Content -LiteralPath (Join-Path $frontend 'package.json') -Raw | ConvertFrom-Json).engines
$pinnedNode = "node-v$($engines.node)-win-x64"

function Resolve-NodeHome {
    $candidates = @()
    if ($env:WWLOGS_NODE_HOME) { $candidates += $env:WWLOGS_NODE_HOME }
    if ($env:LOCALAPPDATA) { $candidates += (Join-Path $env:LOCALAPPDATA "WWlogs\toolchain\$pinnedNode") }
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath (Join-Path $candidate 'node.exe') -PathType Leaf) { return $candidate }
    }
    $command = Get-Command node -ErrorAction SilentlyContinue
    if ($command) { return (Split-Path -Parent $command.Source) }
    throw "Node.js was not found. Install $pinnedNode under %LOCALAPPDATA%\WWlogs\toolchain or set WWLOGS_NODE_HOME"
}

$nodeHome = Resolve-NodeHome
$node = Join-Path $nodeHome 'node.exe'
$npm = Join-Path $nodeHome 'node_modules/npm/bin/npm-cli.js'
$nodeVersion = (& $node --version).Trim()
if ($nodeVersion -ne "v$($engines.node)") {
    throw "Expected Node.js v$($engines.node), found $nodeVersion at $nodeHome"
}
if (-not (Test-Path -LiteralPath $npm -PathType Leaf)) {
    throw "Install npm $($engines.npm) in the selected Node.js directory"
}
$env:PATH = "$nodeHome;$env:PATH"
$npmVersion = (& $node $npm --version).Trim()
if ($npmVersion -ne $engines.npm) {
    throw "Expected npm $($engines.npm), found $npmVersion at $nodeHome"
}
& $node (Join-Path $repoRoot 'frontend/locales/validate.mjs')
if ($LASTEXITCODE -ne 0) { throw 'Bundled localization validation failed' }

# Pure formatting and trust-state checks use Node's built-in runner and tracked sources only.
Push-Location $repoRoot
try {
    $testFiles = @(Get-ChildItem -LiteralPath 'tests/frontend' -Filter '*.test.mjs' | ForEach-Object { "tests/frontend/$($_.Name)" })
    & $node --test @testFiles
    if ($LASTEXITCODE -ne 0) { throw "frontend behavior tests failed ($LASTEXITCODE)" }
} finally {
    Pop-Location
}

if ($Action -eq 'Build') {
    if ($OutDir -eq '') { throw '-OutDir is required for Build' }
    $OutDir = [IO.Path]::GetFullPath($OutDir)
    if ($OutDir.StartsWith($repoRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "-OutDir must be outside the repository: $OutDir"
    }
}

$work = Join-Path ([IO.Path]::GetTempPath()) ('wwlogs-frontend-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
    Get-ChildItem -LiteralPath $frontend -Force | Where-Object { $_.Name -notin @('node_modules', 'dist') } |
        Copy-Item -Destination $work -Recurse
    Push-Location $work
    try {
        & $node $npm ci --strict-peer-deps --no-audit --no-fund --loglevel=error
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed ($LASTEXITCODE)" }
        if ($Action -eq 'Typecheck') {
            & $node $npm run --silent typecheck
            if ($LASTEXITCODE -ne 0) { throw "typecheck failed ($LASTEXITCODE)" }
            Write-Host "Frontend typecheck: PASS (Node.js $nodeVersion)"
        } else {
            & $node $npm run --silent typecheck
            if ($LASTEXITCODE -ne 0) { throw "typecheck failed ($LASTEXITCODE)" }
            & ./node_modules/.bin/vite.cmd build --outDir $OutDir --emptyOutDir --logLevel warn
            if ($LASTEXITCODE -ne 0) { throw "vite build failed ($LASTEXITCODE)" }
            if (-not (Test-Path -LiteralPath (Join-Path $OutDir 'index.html'))) { throw 'vite build produced no index.html' }
            Write-Host "Frontend build: PASS (Node.js $nodeVersion) -> $OutDir"
        }
    } finally {
        Pop-Location
    }
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
