<#
.SYNOPSIS
  Typecheck or build the WWlogs frontend with the system Node.js and npm.

.DESCRIPTION
  Copies the frontend sources to a temporary directory outside the repository, installs the exact dependencies from
  frontend/package-lock.json there with `npm ci`, and runs the typecheck or the production build. Nothing is installed
  or built inside the repository. The build output goes to -OutDir, which must be outside the repository.

  Node.js is the system installation on PATH (or the directory named by $env:WWLOGS_NODE_HOME), and npm is the npm
  command on PATH, which a global npm upgrade places ahead of the copy bundled with Node.js. Their versions must fall in
  the ranges of frontend/package.json "engines" (">=X.Y.Z <M"): a patch or minor upgrade of the system tools needs no
  repository change, and a new major version stops the build until it is verified and the range is widened.
#>
param(
    [ValidateSet('Typecheck', 'Build')]
    [string]$Action = 'Build',
    # Output directory for Build; must be outside the repository.
    [string]$OutDir = '',
    # Public is the default so that forgetting the parameter can never bundle Development-only surfaces.
    [ValidateSet('Public', 'Development')]
    [string]$Profile = 'Public'
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$frontend = Join-Path $repoRoot 'frontend'
$engines = (Get-Content -LiteralPath (Join-Path $frontend 'package.json') -Raw | ConvertFrom-Json).engines

# A tool version must be at least the verified minimum and below the next major version: ">=X.Y.Z <M".
function Assert-EngineRange([string]$Key, [string]$Name, [string]$Version, [string]$Where) {
    $range = [string]$engines.$Key
    if ($range -notmatch '^>=(\d+)\.(\d+)\.(\d+) <(\d+)$') { throw "frontend/package.json engines.$Key must have the form '>=X.Y.Z <M', found '$range'" }
    $minimum = [version]::new([int]$Matches[1], [int]$Matches[2], [int]$Matches[3])
    $nextMajor = [int]$Matches[4]
    if ($Version -notmatch '^v?(\d+)\.(\d+)\.(\d+)') { throw "Cannot read the $Name version '$Version' at $Where" }
    $found = [version]::new([int]$Matches[1], [int]$Matches[2], [int]$Matches[3])
    if ($found -lt $minimum -or $found.Major -ge $nextMajor) {
        throw "$Name $Version at $Where is outside '$range' (frontend/package.json engines.$Key). Upgrade the system $Name, or verify the new version with scripts/verify.ps1 and then change the range."
    }
}

# Node.js is the system installation on PATH, unless WWLOGS_NODE_HOME names another Node.js directory.
function Resolve-NodeHome {
    if ($env:WWLOGS_NODE_HOME) {
        if (Test-Path -LiteralPath (Join-Path $env:WWLOGS_NODE_HOME 'node.exe') -PathType Leaf) { return $env:WWLOGS_NODE_HOME }
        throw "WWLOGS_NODE_HOME does not contain node.exe: $env:WWLOGS_NODE_HOME"
    }
    $command = Get-Command node -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { return (Split-Path -Parent $command.Source) }
    throw "Node.js was not found on PATH. Install Node.js $($engines.node) or set WWLOGS_NODE_HOME"
}

# npm is the npm command on PATH (with WWLOGS_NODE_HOME, the copy in that directory). It runs through its npm-cli.js, as
# npm.cmd does, because npm.cmd prints path warnings on UNC checkouts.
function Resolve-NpmCli([string]$NodeHome) {
    $directories = @()
    if (-not $env:WWLOGS_NODE_HOME) { $directories += @(Get-Command npm -All -ErrorAction SilentlyContinue | ForEach-Object { Split-Path -Parent $_.Source }) }
    $directories += $NodeHome
    foreach ($directory in $directories) {
        $cli = Join-Path $directory 'node_modules/npm/bin/npm-cli.js'
        if (Test-Path -LiteralPath $cli -PathType Leaf) { return $cli }
    }
    throw "npm was not found on PATH. Install npm $($engines.npm)"
}

$nodeHome = Resolve-NodeHome
$node = Join-Path $nodeHome 'node.exe'
$nodeVersion = (& $node --version).Trim()
Assert-EngineRange 'node' 'Node.js' $nodeVersion $nodeHome
$npm = Resolve-NpmCli $nodeHome
# Child tools (npm scripts, vite) must find this Node.js first. The caller's PATH is restored on exit: verify.ps1 and
# build.ps1 run this script once per profile in one process, and a leaked entry put the npm bundled with Node.js ahead
# of a global npm upgrade on the second run.
$callerPath = $env:PATH
$env:PATH = "$nodeHome;$env:PATH"
try {
    $npmVersion = (& $node $npm --version).Trim()
    Assert-EngineRange 'npm' 'npm' $npmVersion $npm
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
                Write-Host "Frontend typecheck: PASS (Node.js $nodeVersion, npm $npmVersion)"
            } else {
                & $node $npm run --silent typecheck
                if ($LASTEXITCODE -ne 0) { throw "typecheck failed ($LASTEXITCODE)" }
                $env:WWLOGS_FRONTEND_PROFILE = $Profile.ToLowerInvariant()
                try {
                    & ./node_modules/.bin/vite.cmd build --outDir $OutDir --emptyOutDir --logLevel warn
                    if ($LASTEXITCODE -ne 0) { throw "vite build failed ($LASTEXITCODE)" }
                } finally {
                    Remove-Item Env:WWLOGS_FRONTEND_PROFILE -ErrorAction SilentlyContinue
                }
                if (-not (Test-Path -LiteralPath (Join-Path $OutDir 'index.html'))) { throw 'vite build produced no index.html' }
                # Public output must not contain the Showcase, its mock data or its font tooling, whatever chunk they could land in.
                $developmentMarkers = @('__wwShowcase', '/preview-fonts/', 'Mock adapter', 'mock-', 'font-recheck')
                if ($Profile -eq 'Public' -and (Get-ChildItem -LiteralPath $OutDir -Recurse -File | Where-Object { $_.Name -match '^ShowcaseApp' -or ($_.Extension -in '.js', '.css', '.html' -and (Select-String -LiteralPath $_.FullName -SimpleMatch -Quiet -Pattern $developmentMarkers)) })) {
                    throw 'The Public frontend output contains Development-only Showcase material'
                }
                Write-Host "Frontend build ($Profile): PASS (Node.js $nodeVersion, npm $npmVersion) -> $OutDir"
            }
        } finally {
            Pop-Location
        }
    } finally {
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }
} finally {
    $env:PATH = $callerPath
}
