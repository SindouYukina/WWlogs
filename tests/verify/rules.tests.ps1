<#
  Tests of the rules scripts/verify.ps1 decides with (scripts/verify/rules.ps1): where Maintainer mode finds the private
  Master contract and what happens when it cannot, how a go test -json stream reads, when the required private tests and
  the required Golden captures count as run, and which verdict and exit code each mode earns. Every input is built here or
  in a temporary directory; nothing private is read. scripts/verify.ps1 runs this file as its step "verify: mode and
  result rules" and requires the last line "verify rules: N of N checks passed".
#>
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '../../scripts/verify/rules.ps1')

$script:checks = 0
$script:failures = [Collections.Generic.List[string]]::new()
function Assert-That([bool]$Condition, [string]$What) {
    $script:checks++
    if (-not $Condition) { $script:failures.Add($What) }
}

# Go test -json lines as test2json writes them.
function New-GoEvent([string]$Action, [string]$Test = '', [string]$Output = '', [string]$Package = 'wwlogs/internal/capture') {
    $event = [ordered]@{ Time = '2026-11-19T00:00:00Z'; Action = $Action; Package = $Package }
    if ($Test) { $event['Test'] = $Test }
    if ($Action -in 'pass', 'fail', 'skip') { $event['Elapsed'] = 0.01 }
    if ($Output) { $event['Output'] = $Output }
    return ($event | ConvertTo-Json -Compress)
}
function New-GoRun([hashtable]$States) {
    $lines = @()
    foreach ($test in $States.Keys) {
        $lines += New-GoEvent 'run' $test
        $lines += New-GoEvent 'output' $test "--- $($States[$test].ToUpperInvariant()): $test (0.01s)`n"
        $lines += New-GoEvent $States[$test] $test
    }
    $lines += New-GoEvent 'pass'
    return (Read-VerifyGoTestJson $lines)
}

$root = Join-Path ([IO.Path]::GetTempPath()) ('wwlogs-verify-rules-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($root)
try {
    # Maintainer finds the contract at its default place, or where WWLOGS_MASTER_CONTRACT says; missing is a problem.
    $repo = Join-Path $root 'repo'
    $default = Join-Path $repo $VerifyMasterContractRelativePath
    [void][IO.Directory]::CreateDirectory((Split-Path -Parent $default))
    Set-Content -LiteralPath $default -Value '{"version": 1}'
    $found = Resolve-VerifyMasterContract $repo '' $root
    Assert-That ($found.Problem -eq '' -and $found.Source -eq 'default location' -and $found.Path -eq [IO.Path]::GetFullPath($default)) 'Maintainer mode with the contract at its default place resolves it'
    $override = Join-Path $root 'elsewhere.json'
    Set-Content -LiteralPath $override -Value '{"version": 1}'
    $found = Resolve-VerifyMasterContract $repo $override $root
    Assert-That ($found.Problem -eq '' -and $found.Source -eq 'WWLOGS_MASTER_CONTRACT' -and $found.Path -eq $override) 'WWLOGS_MASTER_CONTRACT overrides the default place'
    $found = Resolve-VerifyMasterContract $repo 'elsewhere.json' $root
    Assert-That ($found.Problem -eq '' -and $found.Path -eq $override) 'a relative WWLOGS_MASTER_CONTRACT is taken from where the caller started'
    $found = Resolve-VerifyMasterContract $repo (Join-Path $root 'wrong-path.json') $root
    Assert-That ($found.Problem -like 'the Master contract is missing:*WWLOGS_MASTER_CONTRACT*') 'a wrong WWLOGS_MASTER_CONTRACT path is a problem, not a fallback to the default'
    Remove-Item -LiteralPath $default
    $found = Resolve-VerifyMasterContract $repo '' $root
    Assert-That ($found.Problem -like 'the Master contract is missing:*default location*') 'a missing default contract is a problem'
    [void][IO.Directory]::CreateDirectory($default)
    $found = Resolve-VerifyMasterContract $repo '' $root
    Assert-That ($found.Problem -ne '') 'a directory in place of the contract is a problem'

    # go test -json: states, top-level counts, subtests, failure text; a test that never reported has no state.
    $lines = @(
        (New-GoEvent 'start'),
        (New-GoEvent 'run' 'TestA'), (New-GoEvent 'output' 'TestA' "=== RUN   TestA`n"), (New-GoEvent 'pass' 'TestA'),
        (New-GoEvent 'run' 'TestB'), (New-GoEvent 'output' 'TestB' "    b_test.go:9: set WWLOGS_X`n"), (New-GoEvent 'skip' 'TestB'),
        (New-GoEvent 'run' 'TestC'), (New-GoEvent 'run' 'TestC/one'), (New-GoEvent 'output' 'TestC/one' "    c_test.go:3: want 1, got 2`n"),
        (New-GoEvent 'fail' 'TestC/one'), (New-GoEvent 'output' 'TestC' "--- FAIL: TestC (0.00s)`n"), (New-GoEvent 'fail' 'TestC'),
        (New-GoEvent 'run' 'TestD'), (New-GoEvent 'run' 'TestD/two'), (New-GoEvent 'pass' 'TestD/two'), (New-GoEvent 'pass' 'TestD'),
        (New-GoEvent 'output' '' "FAIL`n"), (New-GoEvent 'fail'),
        'ok  	something printed outside the event stream'
    )
    $read = Read-VerifyGoTestJson $lines
    Assert-That ($read.Passed -eq 2 -and $read.Skipped -eq 1 -and $read.Failed -eq 1 -and $read.Subtests -eq 2) "go test -json counts top-level tests and subtests apart (got $($read.Passed)/$($read.Skipped)/$($read.Failed)/$($read.Subtests))"
    Assert-That ($read.States['wwlogs/internal/capture TestB'] -eq 'skip' -and $read.SkippedTests -contains 'wwlogs/internal/capture TestB') 'a test that skipped itself is skipped, never passed'
    Assert-That ($read.FailedPackages -contains 'wwlogs/internal/capture' -and $read.FailureText -match 'want 1, got 2' -and $read.FailureText -match 'outside the event stream') 'the failure text holds the failed subtest output and lines outside the stream'
    Assert-That ($read.FailureText -notmatch 'set WWLOGS_X') 'the failure text leaves out tests that did not fail'
    Assert-That ($null -eq $read.States['wwlogs/internal/capture TestE']) 'a test that never reported has no state'
    $build = Read-VerifyGoTestJson @('{"ImportPath":"wwlogs/internal/capture [wwlogs/internal/capture.test]","Action":"build-output","Output":"x.go:1: syntax error\n"}', '{"ImportPath":"wwlogs/internal/capture [wwlogs/internal/capture.test]","Action":"build-fail"}')
    Assert-That ($build.FailedPackages.Count -eq 1 -and $build.FailureText -match 'syntax error' -and $build.Passed -eq 0) 'a build failure is a failed package with its compiler output'
    $escaped = Read-VerifyGoTestJson @((New-GoEvent 'output' 'TestQ' "quote `" tab `t backslash \ end`n"), (New-GoEvent 'fail' 'TestQ'))
    Assert-That ($escaped.FailureText -eq "quote `" tab `t backslash \ end") 'escaped output is decoded'

    # The required private tests: each must pass in every profile.
    $required = @('TestTrackedMasterSatisfiesItsContract', 'TestMasterContractIsConsistent')
    $allPass = @{ TestTrackedMasterSatisfiesItsContract = 'pass'; TestMasterContractIsConsistent = 'pass' }
    $runs = [ordered]@{ Public = (New-GoRun $allPass); Development = (New-GoRun $allPass) }
    Assert-That (@(Get-VerifyRequiredTestProblems $runs 'wwlogs/internal/capture' $required).Count -eq 0) 'the private Master checks pass when both pass in both profiles'
    foreach ($case in @(
            @{ Name = 'a failed check'; State = 'fail'; Word = 'failed' },
            @{ Name = 'a skipped check'; State = 'skip'; Word = 'was skipped' }
        )) {
        $runs = [ordered]@{ Public = (New-GoRun $allPass); Development = (New-GoRun @{ TestTrackedMasterSatisfiesItsContract = $case.State; TestMasterContractIsConsistent = 'pass' }) }
        $problems = @(Get-VerifyRequiredTestProblems $runs 'wwlogs/internal/capture' $required)
        Assert-That ($problems.Count -eq 1 -and $problems[0] -eq "TestTrackedMasterSatisfiesItsContract $($case.Word) (Development)") "$($case.Name) fails the private Master checks"
    }
    $runs = [ordered]@{ Public = (New-GoRun @{ TestMasterContractIsConsistent = 'pass' }); Development = (New-GoRun $allPass) }
    $problems = @(Get-VerifyRequiredTestProblems $runs 'wwlogs/internal/capture' $required)
    Assert-That ($problems.Count -eq 1 -and $problems[0] -eq 'TestTrackedMasterSatisfiesItsContract did not run (Public)') 'a check that did not run fails the private Master checks'

    # Golden coverage: every required capture of the registry ran from its pinned file, checked facts and passed.
    $registry = '{"captures":[{"name":"LIVE_A","sha256":"aa","required":true},{"name":"LIVE_B","required":false}]}' | ConvertFrom-Json
    $coverage = { param($captures) ([ordered]@{ captures = $captures } | ConvertTo-Json -Depth 4 | ConvertFrom-Json) }
    $ok = & $coverage @(@{ name = 'LIVE_A__capture.pcap'; sha256 = 'aa'; facts = @{ effect = 3 }; passed = $true })
    Assert-That (@(Test-VerifyGoldenCoverage $registry $ok).Count -eq 0) 'a corpus that ran the required Golden capture passes'
    foreach ($case in @(
            @{ Name = 'absent'; Captures = @(@{ name = 'LIVE_B__capture.pcap'; sha256 = 'bb'; facts = @{ effect = 1 }; passed = $true }); Want = 'was not executed' },
            @{ Name = 'another file'; Captures = @(@{ name = 'LIVE_A__capture.pcap'; sha256 = 'cc'; facts = @{ effect = 3 }; passed = $true }); Want = 'ran from another file' },
            @{ Name = 'no fact'; Captures = @(@{ name = 'LIVE_A__capture.pcap'; sha256 = 'aa'; facts = @{}; passed = $true }); Want = 'without checking any Golden fact' },
            @{ Name = 'failed'; Captures = @(@{ name = 'LIVE_A__capture.pcap'; sha256 = 'aa'; facts = @{ effect = 3 }; passed = $false }); Want = 'failed its Golden facts' }
        )) {
        $problems = @(Test-VerifyGoldenCoverage $registry (& $coverage $case.Captures))
        Assert-That ($problems.Count -eq 1 -and $problems[0] -like "*$($case.Want)*") "a required Golden capture that is $($case.Name) fails the corpus"
    }
    $none = '{"captures":[{"name":"LIVE_B","required":false}]}' | ConvertFrom-Json
    Assert-That (@(Test-VerifyGoldenCoverage $none $ok).Count -eq 1) 'a registry that requires no capture fails'

    # Verdicts and exit codes. Only PASS counts as passed; Maintainer never passes with a skip and never becomes Public.
    $verdict = Get-VerifyVerdict 'Maintainer' @('PASS', 'PASS', 'PASS')
    Assert-That ($verdict.Line -eq 'Maintainer Verification: PASS' -and $verdict.ExitCode -eq 0) 'Maintainer with every step passed is PASS, exit 0'
    $verdict = Get-VerifyVerdict 'Maintainer' @('PASS', 'BLOCKED', 'PASS')
    Assert-That ($verdict.Line -eq 'Maintainer Verification: FAIL' -and $verdict.ExitCode -eq 1 -and $verdict.Mode -eq 'Maintainer') 'Maintainer with a blocked private input is FAIL, exit 1, and stays Maintainer'
    $verdict = Get-VerifyVerdict 'Maintainer' @('PASS', 'FAIL')
    Assert-That ($verdict.Overall -eq 'FAIL' -and $verdict.ExitCode -eq 1) 'Maintainer with a failed step is FAIL'
    $verdict = Get-VerifyVerdict 'Maintainer' @('PASS', 'SKIP')
    Assert-That ($verdict.Overall -eq 'FAIL' -and $verdict.ExitCode -eq 1) 'Maintainer with a skipped step is FAIL: Maintainer mode skips nothing'
    $verdict = Get-VerifyVerdict 'Maintainer' @('PASS', 'NOT RUN')
    Assert-That ($verdict.Overall -eq 'INCOMPLETE' -and $verdict.ExitCode -eq 2 -and $verdict.Counts['PASS'] -eq 1) 'Maintainer with a step not run is INCOMPLETE, exit 2, and the step is not counted as passed'
    $verdict = Get-VerifyVerdict 'Public' @('PASS', 'SKIP', 'SKIP')
    Assert-That ($verdict.Line -eq 'Public Verification: PASS WITH PRIVATE CHECKS SKIPPED' -and $verdict.ExitCode -eq 0 -and $verdict.Counts['PASS'] -eq 1 -and $verdict.Counts['SKIP'] -eq 2) 'Public with its private checks skipped passes with the skips stated and counted apart'
    $verdict = Get-VerifyVerdict 'Public' @('FAIL', 'SKIP')
    Assert-That ($verdict.Line -eq 'Public Verification: FAIL' -and $verdict.ExitCode -eq 1) 'Public with a failed public step is FAIL'
    $verdict = Get-VerifyVerdict 'Public' @('BLOCKED', 'SKIP')
    Assert-That ($verdict.Overall -eq 'FAIL' -and $verdict.ExitCode -eq 1) 'Public with a blocked public step is FAIL'
    $verdict = Get-VerifyVerdict 'Public' @('PASS', 'NOT RUN', 'SKIP')
    Assert-That ($verdict.Overall -eq 'INCOMPLETE' -and $verdict.ExitCode -eq 2) 'Public with a step not run is INCOMPLETE'
    $verdict = Get-VerifyVerdict 'Maintainer' @()
    Assert-That ($verdict.Overall -eq 'FAIL') 'a run without steps is FAIL'
    $refused = $false
    try { [void](Get-VerifyVerdict 'Maintainer' @('PASSED')) } catch { $refused = $true }
    Assert-That $refused 'an unknown state is refused, never counted'
} finally {
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}

foreach ($failure in $script:failures) { Write-Host "FAIL: $failure" }
Write-Host "verify rules: $($script:checks - $script:failures.Count) of $($script:checks) checks passed"
if ($script:failures.Count -gt 0) { exit 1 }
exit 0
