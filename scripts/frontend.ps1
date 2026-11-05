<#
.SYNOPSIS
  Typecheck or build the WWlogs frontend with the pinned Node.js 24 toolchain.

.DESCRIPTION
  Copies the frontend sources to a temporary directory outside the repository, installs the exact dependencies from
  frontend/package-lock.json there with `npm ci`, and runs the typecheck or the production build. Nothing is installed
  or built inside the repository. The build output goes to -OutDir, which must be outside the repository.

  Node.js is taken from $env:WWLOGS_NODE_HOME, else from %LOCALAPPDATA%\WWlogs\toolchain\node-v24.22.2-win-x64, else
  from PATH; the version must be 24.x or the script fails.
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
$pinnedNode = 'node-v24.22.2-win-x64'

function Resolve-NodeHome {
    $candidates = @()
    if ($env:WWLOGS_NODE_HOME) { $candidates += $env:WWLOGS_NODE_HOME }
    if ($env:LOCALAPPDATA) { $candidates += (Join-Path $env:LOCALAPPDATA "WWlogs\toolchain\$pinnedNode") }
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath (Join-Path $candidate 'node.exe') -PathType Leaf) { return $candidate }
    }
    $command = Get-Command node -ErrorAction SilentlyContinue
    if ($command) { return (Split-Path -Parent $command.Source) }
    throw "Node.js 24 was not found; install $pinnedNode under %LOCALAPPDATA%\WWlogs\toolchain or set WWLOGS_NODE_HOME"
}

$nodeHome = Resolve-NodeHome
$nodeVersion = (& (Join-Path $nodeHome 'node.exe') --version).Trim()
if ($nodeVersion -notmatch '^v24\.') {
    throw "Node.js $nodeVersion at $nodeHome is not 24.x; the frontend toolchain is pinned to Node.js 24 LTS"
}
$env:PATH = "$nodeHome;$env:PATH"

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
        & npm ci --no-audit --no-fund --loglevel=error
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed ($LASTEXITCODE)" }
        if ($Action -eq 'Typecheck') {
            & npm run --silent typecheck
            if ($LASTEXITCODE -ne 0) { throw "typecheck failed ($LASTEXITCODE)" }
            Write-Host "Frontend typecheck: PASS (Node.js $nodeVersion)"
        } else {
            & npm run --silent typecheck
            if ($LASTEXITCODE -ne 0) { throw "typecheck failed ($LASTEXITCODE)" }
            & npx --no-install vite build --outDir $OutDir --emptyOutDir --logLevel warn
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
