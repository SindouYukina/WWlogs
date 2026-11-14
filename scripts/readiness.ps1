<#
.SYNOPSIS
  The version readiness gate: checks that a game-version adaptation is complete in every required domain.

.DESCRIPTION
  Runs scripts/readiness.go over the tracked source tree, the Master and the image manifest, together with the optional
  private research inputs and the evidence of a scripts/verify.ps1 run, and writes readiness.json and readiness.md (the
  Core Acceptance Matrix) to -Out. Usage, check list and troubleshooting: scripts/readiness/README.md.

  Exit code 0: STATIC_READY. 1: not STATIC_READY (the report lists the blockers). 2: the gate could not run.
  LIVE_VERIFIED is reported separately and needs the owner's -LiveAcceptance record.
#>
param(
    # Output directory for readiness.json and readiness.md.
    [Parameter(Mandatory = $true)][string]$Out,
    # Directory written by scripts/verify.ps1 -EvidenceOut (verify-results.json, golden-coverage.json, history-summary.json).
    [string]$Evidence = '',
    # Private research inputs file (protocolSchema, clientReferences); see scripts/readiness/README.md.
    [string]$ResearchInputs = '',
    # The owner's Live acceptance record for this game version.
    [string]$LiveAcceptance = ''
)

$ErrorActionPreference = 'Stop'
# Build with the system Go only: never switch to, or download, another toolchain for a newer go.mod requirement.
$env:GOTOOLCHAIN = 'local'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath
$overlay = Join-Path $repoRoot 'tests/go-overlay.json'

$arguments = @('run', "-overlay=$overlay", (Join-Path $repoRoot 'scripts/readiness.go'), '-root', $repoRoot, '-out', [IO.Path]::GetFullPath($Out))
if ($Evidence -ne '') { $arguments += @('-evidence', [IO.Path]::GetFullPath($Evidence)) }
if ($ResearchInputs -ne '') { $arguments += @('-research-inputs', [IO.Path]::GetFullPath($ResearchInputs)) }
if ($LiveAcceptance -ne '') { $arguments += @('-live-acceptance', [IO.Path]::GetFullPath($LiveAcceptance)) }

Push-Location $repoRoot
try {
    & go @arguments
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
