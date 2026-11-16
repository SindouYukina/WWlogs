package readiness

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Run evidence: what a verification run actually executed. The gate never reruns the suite; it reads the
// machine-readable results scripts/verify.ps1 -EvidenceOut writes, and a check whose evidence is missing is UNVERIFIED.

// verifyResults is verify-results.json.
type verifyResults struct {
	Commit   string   `json:"commit"`
	Dirty    bool     `json:"dirty"`
	Packages []string `json:"packages"`
	Results  []struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	} `json:"results"`
}

func (v *verifyResults) state(name string) (string, string, bool) {
	for _, r := range v.Results {
		if r.Name == name {
			return r.State, r.Detail, true
		}
	}
	return "", "", false
}

// goldenRegistry is tests/capture/golden_registry.json: the captures whose Golden facts the corpus requires.
type goldenRegistry struct {
	GameVersion string `json:"gameVersion"`
	Captures    []struct {
		Name     string `json:"name"`
		SHA256   string `json:"sha256"`
		Required bool   `json:"required"`
		Note     string `json:"note"`
		// IdentificationLimits record why a stored enemy, stage or kind of play of the capture cannot be named.
		IdentificationLimits []identificationLimit `json:"identificationLimits"`
	} `json:"captures"`
}

// goldenCoverage is what TestSemanticReplayCollector executed (WWLOGS_SEMANTIC_REPLAY_COVERAGE).
type goldenCoverage struct {
	GameVersion string `json:"gameVersion"`
	Captures    []struct {
		Name   string         `json:"name"`
		SHA256 string         `json:"sha256"`
		Facts  map[string]int `json:"facts"`
		Passed bool           `json:"passed"`
	} `json:"captures"`
	Absent []string `json:"absent"`
}

// historySummary is the History replay collector's summary.json.
type historySummary struct {
	Captures            int                    `json:"captures"`
	ListedEncounters    int                    `json:"listedEncounters"`
	VersionedEncounters int                    `json:"versionedEncounters"`
	Identification      *historyIdentification `json:"identification"`
}

// liveAcceptance is the owner's Live acceptance record (PROCEDURE.md 5.3).
type liveAcceptance struct {
	GameVersion      string   `json:"gameVersion"`
	ClientVersion    string   `json:"clientVersion"`
	BuildNumber      int      `json:"buildNumber"`
	ExecutableSHA256 string   `json:"executableSha256"`
	Date             string   `json:"date"`
	Operator         string   `json:"operator"`
	Result           string   `json:"result"`
	Checks           []string `json:"checks"`
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

func (g *gate) runtimeChecks() {
	g.versionSourceCheck()
}

func (g *gate) evidenceChecks() {
	parity := g.check("runtime.profile-parity", DomainRuntime, "Public and Development produce the same statistics, History and Master answers", KindEvidence, true)
	replayVersion := g.check("runtime.history-version-replay", DomainRuntime, "Every encounter the History stored from the corpus carries the decoded game version", KindEvidence, true)
	identification := g.check("content.history-identification", DomainContent, "Every enemy, main enemy, stage and kind of play the History stored from the corpus is named in every bundled Master locale, or its limit is recorded", KindEvidence, true)
	embedded := g.check("assets.embedded-parity", DomainAssets, "Both desktop profiles embed every distinct manifest image", KindEvidence, true)
	suite := g.check("verification.regression-suite", DomainVerification, "Every step of scripts/verify.ps1 ran and passed", KindEvidence, true)
	golden := g.check("verification.golden-coverage", DomainVerification, "Every required Golden capture was executed with its facts and passed", KindEvidence, true)
	injection := g.check("verification.failure-injection", DomainVerification, "The gate's failure-injection tests ran in both profiles", KindEvidence, true)
	live := g.check("verification.live-acceptance", DomainVerification, "Owner-operated Live/Npcap acceptance of this game version", KindLive, true)

	var verify verifyResults
	verifyLoaded := false
	if g.in.VerifyResults == "" {
		for _, c := range []*Check{parity, embedded, suite, injection} {
			c.unverified("no verify evidence was supplied (scripts/verify.ps1 -EvidenceOut; PROCEDURE.md S12)")
		}
	} else if err := readJSON(g.in.VerifyResults, &verify); err != nil {
		for _, c := range []*Check{parity, embedded, suite, injection} {
			c.unverified(fmt.Sprintf("the verify evidence cannot be read: %v", err))
		}
	} else {
		verifyLoaded = true
		source := fmt.Sprintf("verify-results.json (commit %s, modified %v)", short(verify.Commit), verify.Dirty)
		for _, c := range []*Check{parity, embedded, suite, injection} {
			c.Evidence = append(c.Evidence, source)
		}
		need := func(c *Check, names ...string) {
			for _, name := range names {
				state, detail, ok := verify.state(name)
				switch {
				case !ok:
					c.unverified(fmt.Sprintf("verify step %q is absent", name))
				case state == "PASS":
					c.Evidence = append(c.Evidence, fmt.Sprintf("%s: PASS %s", name, detail))
				case state == "FAIL" || state == "BLOCKED":
					c.fail("verify step %q: %s %s", name, state, detail)
				default:
					c.unverified(fmt.Sprintf("verify step %q: %s %s", name, state, detail))
				}
			}
		}
		need(parity, "go test (Public)", "go test (Development)", "replay: public output audit + statistics equality", "master: embedded bytes + profile lookup parity", "replay: combat history fidelity")
		need(embedded, "desktop embedded images")
		passed := 0
		for _, r := range verify.Results {
			switch r.State {
			case "PASS":
				passed++
			case "FAIL", "BLOCKED":
				suite.fail("%s: %s %s", r.Name, r.State, r.Detail)
			default:
				suite.unverified(fmt.Sprintf("%s: %s %s", r.Name, r.State, r.Detail))
			}
		}
		if len(verify.Results) == 0 {
			suite.unverified("the verify evidence lists no step")
		}
		suite.Coverage = &Coverage{Passed: passed, Total: len(verify.Results), Denominator: "verify.ps1 steps"}
		if !slices.Contains(verify.Packages, "./tests/readiness") {
			injection.unverified("the verify run did not test ./tests/readiness")
		} else {
			need(injection, "go test (Public)", "go test (Development)")
		}
	}

	// History replay: the collector's summary.
	if g.in.HistorySummary == "" {
		replayVersion.unverified("no History replay summary was supplied (scripts/verify.ps1 -EvidenceOut with -PcapDirectory)")
		identification.unverified("no History replay summary was supplied (scripts/verify.ps1 -EvidenceOut with -PcapDirectory)")
	} else {
		var summary historySummary
		switch err := readJSON(g.in.HistorySummary, &summary); {
		case err != nil:
			replayVersion.unverified(fmt.Sprintf("the History replay summary cannot be read: %v", err))
			identification.unverified(fmt.Sprintf("the History replay summary cannot be read: %v", err))
		case summary.ListedEncounters == 0:
			replayVersion.unverified("the History replay stored no encounter")
			identification.unverified("the History replay stored no encounter")
		default:
			if summary.VersionedEncounters != summary.ListedEncounters {
				replayVersion.fail("%d of %d stored encounters carry game version %s", summary.VersionedEncounters, summary.ListedEncounters, g.report.GameVersion)
			} else {
				replayVersion.Coverage = &Coverage{Passed: summary.VersionedEncounters, Total: summary.ListedEncounters, Denominator: "encounters stored by the History replay collector (Public profile)"}
			}
			g.historyIdentificationCheck(identification, summary)
		}
	}

	g.goldenCheck(golden, verify, verifyLoaded)
	g.liveCheck(live)
}

func (g *gate) goldenCheck(c *Check, verify verifyResults, verifyLoaded bool) {
	var registry goldenRegistry
	if err := readJSON(g.in.GoldenRegistry, &registry); err != nil {
		c.fail("the Golden registry cannot be read: %v", err)
		return
	}
	var required []string
	for _, entry := range registry.Captures {
		if entry.Required {
			required = append(required, entry.Name)
		}
	}
	if len(required) == 0 {
		c.fail("the Golden registry requires no capture")
	}
	if registry.GameVersion != g.report.GameVersion {
		c.fail("the Golden registry is for game version %q, the decoder reads %q", registry.GameVersion, g.report.GameVersion)
	}
	if g.in.GoldenCoverage == "" {
		c.unverified("no Golden coverage was supplied (scripts/verify.ps1 -EvidenceOut with -PcapDirectory)")
		return
	}
	var coverage goldenCoverage
	if err := readJSON(g.in.GoldenCoverage, &coverage); err != nil {
		c.unverified(fmt.Sprintf("the Golden coverage cannot be read: %v", err))
		return
	}
	executed := 0
	for _, entry := range registry.Captures {
		if !entry.Required {
			continue
		}
		found := false
		for _, ran := range coverage.Captures {
			if !strings.Contains(ran.Name, entry.Name) {
				continue
			}
			found = true
			facts := 0
			for _, n := range ran.Facts {
				facts += n
			}
			switch {
			case entry.SHA256 != "" && ran.SHA256 != entry.SHA256:
				c.fail("required capture %s ran from a file with SHA-256 %s, the registry pins %s", entry.Name, short(ran.SHA256), short(entry.SHA256))
			case facts == 0:
				c.fail("required capture %s ran without checking any Golden fact", entry.Name)
			case !ran.Passed:
				c.fail("required capture %s failed its Golden facts", entry.Name)
			default:
				executed++
				c.Evidence = append(c.Evidence, fmt.Sprintf("%s: %d facts %v", entry.Name, facts, ran.Facts))
			}
		}
		if !found {
			c.fail("required Golden capture %s was not executed", entry.Name)
		}
	}
	if len(coverage.Absent) > 0 {
		c.Evidence = append(c.Evidence, "supplementary captures absent: "+strings.Join(coverage.Absent, ", "))
	}
	c.Coverage = &Coverage{Passed: executed, Total: len(required), Denominator: "required Golden captures (tests/capture/golden_registry.json)"}
	if verifyLoaded {
		if state, detail, ok := verify.state("replay: effect and skill semantics"); !ok || state != "PASS" {
			c.fail("verify step \"replay: effect and skill semantics\": %s %s", state, detail)
		}
	}
}

func (g *gate) liveCheck(c *Check) {
	if g.in.LiveAcceptance == "" {
		c.unverified("LIVE / Npcap: NOT RUN (no owner acceptance record for this game version)")
		return
	}
	var record liveAcceptance
	if err := readJSON(g.in.LiveAcceptance, &record); err != nil {
		c.unverified(fmt.Sprintf("the Live acceptance record cannot be read: %v", err))
		return
	}
	c.Evidence = append(c.Evidence, fmt.Sprintf("%s %s, build %d, executable %s, %s by %s: %s", record.GameVersion, record.ClientVersion, record.BuildNumber, short(record.ExecutableSHA256), record.Date, record.Operator, record.Result))
	switch {
	case record.GameVersion != g.report.GameVersion:
		c.fail("the Live acceptance is for game version %q, the decoder reads %q", record.GameVersion, g.report.GameVersion)
	case record.BuildNumber <= 0 || len(record.ExecutableSHA256) != 64:
		c.fail("the Live acceptance names no official build and executable")
	case record.Result != "PASS":
		c.fail("the Live acceptance result is %q", record.Result)
	}
}
