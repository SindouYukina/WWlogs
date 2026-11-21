<#
  The rules of scripts/verify.ps1, kept apart so that tests/verify/rules.tests.ps1 can check them without running the
  suite: where a Maintainer run finds the private Master contract, how a go test -json stream reads, what a required test
  and the required Golden captures need, and which verdict and exit code a run earns in each mode. The functions only
  read the arguments they are given.

  Modes. Maintainer, the default, is the maintainer's complete verification: it also runs the checks that need private
  material (the Master contract, the owner-local key, the capture corpus), and a missing private input fails the run; it
  never turns into Public. Public verifies a checkout with tracked files and public tools only: it reads no private
  material, and every check that needs some is SKIP, which is never a pass.
#>

# The Master contract is internal work kept out of the repository, at this place of the Git-ignored work tree.
# WWLOGS_MASTER_CONTRACT names another file. Only Maintainer mode reads it.
$VerifyMasterContractRelativePath = 'work/acceptance/master/master_contract.json'

# Resolve-VerifyMasterContract says which Master contract a Maintainer run checks and whether the file can be read. A
# relative override is taken from BaseDirectory, where the caller started. A missing or unreadable file is the Problem.
function Resolve-VerifyMasterContract([string]$RepoRoot, [string]$Override, [string]$BaseDirectory) {
    if ($Override) {
        $shown = $Override
        $path = if ([IO.Path]::IsPathRooted($Override)) { $Override } else { Join-Path $BaseDirectory $Override }
        $source = 'WWLOGS_MASTER_CONTRACT'
    } else {
        $shown = $VerifyMasterContractRelativePath
        $path = Join-Path $RepoRoot $VerifyMasterContractRelativePath
        $source = 'default location'
    }
    $path = [IO.Path]::GetFullPath($path)
    $problem = ''
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        $problem = "the Master contract is missing: $shown ($source)"
    } else {
        try {
            [void][IO.File]::ReadAllBytes($path)
        } catch {
            $problem = "the Master contract cannot be read: $shown ($source): $($_.Exception.Message)"
        }
    }
    return [pscustomobject]@{ Path = $path; Shown = $shown; Source = $source; Problem = $problem }
}

# go test -json events: test2json writes Time, Action, Package, Test, Elapsed and Output in this order; a build error is
# an ImportPath event.
$VerifyGoTestEvent = [regex]'^\{"Time":"[^"]*","Action":"(?<a>[a-z-]+)","Package":"(?<p>[^"]*)"(?:,"Test":"(?<t>(?:[^"\\]|\\.)*)")?(?:,"Elapsed":[-0-9.e+]+)?(?:,"Output":"(?<o>(?:[^"\\]|\\.)*)")?'
$VerifyGoBuildEvent = [regex]'^\{"ImportPath":"(?<p>[^"]*)","Action":"(?<a>[a-z-]+)"(?:,"Output":"(?<o>(?:[^"\\]|\\.)*)")?'

function ConvertFrom-VerifyJsonString([string]$Escaped) {
    if ($Escaped -notmatch '\\') { return $Escaped }
    return ('"' + $Escaped + '"' | ConvertFrom-Json)
}

# Read-VerifyGoTestJson reads the lines of a go test -json run: the last state (pass, fail, skip) of every test, keyed
# "<package> <test>"; the top-level tests that passed, failed and were skipped; the subtests; and FailureText, the output
# that explains a failure (failed tests and packages, build errors and any line that is not an event). A test that never
# reported a state has none: it did not run, and it never counts as passed.
function Read-VerifyGoTestJson([string[]]$Lines) {
    $states = @{}
    $topLevel = [Collections.Generic.HashSet[string]]::new()
    $packages = @{}
    $outputs = @{}
    $loose = [Collections.Generic.List[string]]::new()
    foreach ($line in $Lines) {
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $match = $VerifyGoTestEvent.Match($line)
        if (-not $match.Success) { $match = $VerifyGoBuildEvent.Match($line) }
        if (-not $match.Success) {
            $loose.Add($line.TrimEnd())
            continue
        }
        $action = $match.Groups['a'].Value
        $package = $match.Groups['p'].Value
        $test = $match.Groups['t'].Value
        $key = if ($test) { "$package $test" } else { $package }
        switch ($action) {
            { $_ -in 'pass', 'fail', 'skip' } {
                if (-not $test) {
                    $packages[$package] = $action
                } else {
                    $states[$key] = $action
                    if (-not $test.Contains('/')) { [void]$topLevel.Add($key) }
                }
            }
            'build-fail' { $packages[$package] = 'fail' }
            { $_ -in 'output', 'build-output' } {
                if (-not $outputs.ContainsKey($key)) { $outputs[$key] = [Collections.Generic.List[string]]::new() }
                $outputs[$key].Add($match.Groups['o'].Value)
            }
        }
    }
    $ofState = { param($state) @($topLevel | Where-Object { $states[$_] -eq $state } | Sort-Object) }
    $failedTests = & $ofState 'fail'
    $skippedTests = & $ofState 'skip'
    $failedPackages = @($packages.Keys | Where-Object { $packages[$_] -eq 'fail' } | Sort-Object)
    # The output of every failed test and subtest, then of every failed package (a panic, a build error, the FAIL line).
    $explained = @($states.Keys | Where-Object { $states[$_] -eq 'fail' } | Sort-Object) + $failedPackages
    $text = [Text.StringBuilder]::new()
    foreach ($line in $loose) { [void]$text.AppendLine($line) }
    foreach ($key in $explained) {
        if ($outputs.ContainsKey($key)) {
            foreach ($piece in $outputs[$key]) { [void]$text.Append((ConvertFrom-VerifyJsonString $piece)) }
        }
    }
    return [pscustomobject]@{
        States         = $states
        Passed         = (& $ofState 'pass').Count
        Failed         = $failedTests.Count
        Skipped        = $skippedTests.Count
        Subtests       = $states.Count - $topLevel.Count
        SkippedTests   = $skippedTests
        FailedTests    = $failedTests
        FailedPackages = $failedPackages
        FailureText    = $text.ToString().Trim()
    }
}

# Get-VerifyRequiredTestProblems lists every required test that did not pass in every run: Runs maps a run name (a
# profile) to the result of Read-VerifyGoTestJson. A skip, a failure and a test that did not report are all problems.
function Get-VerifyRequiredTestProblems([Collections.IDictionary]$Runs, [string]$Package, [string[]]$Names) {
    $problems = @()
    foreach ($run in $Runs.Keys) {
        foreach ($name in $Names) {
            switch ($Runs[$run].States["$Package $name"]) {
                'pass' { }
                'fail' { $problems += "$name failed ($run)" }
                'skip' { $problems += "$name was skipped ($run)" }
                default { $problems += "$name did not run ($run)" }
            }
        }
    }
    return $problems
}

# Test-VerifyGoldenCoverage lists why a corpus run did not execute every required capture of the Golden registry
# (tests/capture/golden_registry.json) with its facts passing: the rule the version readiness gate applies to the same
# coverage (verification.golden-coverage). A capture of the run counts for a registry entry when its name contains the
# entry's name; it must be the pinned file, check at least one fact, and pass.
function Test-VerifyGoldenCoverage($Registry, $Coverage) {
    $problems = @()
    $required = @($Registry.captures | Where-Object { $_.required })
    if ($required.Count -eq 0) { $problems += 'the Golden registry requires no capture' }
    foreach ($entry in $required) {
        $runs = @($Coverage.captures | Where-Object { "$($_.name)".Contains($entry.name) })
        if ($runs.Count -eq 0) {
            $problems += "required Golden capture $($entry.name) was not executed"
            continue
        }
        foreach ($run in $runs) {
            $facts = 0
            if ($run.facts) { foreach ($property in $run.facts.PSObject.Properties) { $facts += [int]$property.Value } }
            if ($entry.sha256 -and $run.sha256 -ne $entry.sha256) {
                $problems += "required Golden capture $($entry.name) ran from another file (SHA-256 $($run.sha256), the registry pins $($entry.sha256))"
            } elseif ($facts -eq 0) {
                $problems += "required Golden capture $($entry.name) ran without checking any Golden fact"
            } elseif (-not $run.passed) {
                $problems += "required Golden capture $($entry.name) failed its Golden facts"
            }
        }
    }
    return $problems
}

# Get-VerifyVerdict turns the states of the declared steps into the run's verdict and exit code (0 pass, 1 fail,
# 2 incomplete). Maintainer passes only when every step passed: FAIL, BLOCKED and SKIP (Maintainer mode skips nothing)
# fail it, NOT RUN leaves it INCOMPLETE. Public skips the private checks by design, so a run whose other steps all passed
# is PASS WITH PRIVATE CHECKS SKIPPED; FAIL and BLOCKED fail it and NOT RUN leaves it INCOMPLETE. Only PASS counts as passed.
function Get-VerifyVerdict([ValidateSet('Maintainer', 'Public')][string]$Mode, [string[]]$States) {
    $counts = [ordered]@{ 'PASS' = 0; 'FAIL' = 0; 'BLOCKED' = 0; 'NOT RUN' = 0; 'SKIP' = 0 }
    foreach ($state in $States) {
        if (-not $counts.Contains($state)) { throw "unknown step state '$state'" }
        $counts[$state]++
    }
    $failing = $counts['FAIL'] + $counts['BLOCKED'] + $(if ($Mode -eq 'Maintainer') { $counts['SKIP'] } else { 0 })
    if ($States.Count -eq 0 -or $failing -gt 0) {
        $overall, $exitCode = 'FAIL', 1
    } elseif ($counts['NOT RUN'] -gt 0) {
        $overall, $exitCode = 'INCOMPLETE', 2
    } elseif ($counts['SKIP'] -gt 0) {
        $overall, $exitCode = 'PASS WITH PRIVATE CHECKS SKIPPED', 0
    } else {
        $overall, $exitCode = 'PASS', 0
    }
    return [pscustomobject]@{ Mode = $Mode; Overall = $overall; ExitCode = $exitCode; Counts = $counts; Line = "$Mode Verification: $overall" }
}
