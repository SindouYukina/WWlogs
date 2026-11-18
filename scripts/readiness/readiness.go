// Package readiness is the version readiness gate of AGENTS.md section 4.4: it checks that a game-version adaptation is
// complete across every required domain, from the client's definitions through the Master, the image pack and the shared
// Go decisions to what the Overlay and History show.
//
// It is build and verification tooling: no product package imports it, and it reads only what its Inputs name. Tracked
// inputs (the source tree, the Master, the image manifest) are always read. Private research inputs (the client protocol
// schema, the client's image references) and run evidence (scripts/verify.ps1 results, Golden coverage, the History
// replay summary, the owner's Live acceptance) are optional: a check whose input is missing is UNVERIFIED, never PASS, and
// an UNVERIFIED mandatory check keeps the version from STATIC_READY.
//
// The checks ask the product itself wherever the product decides: effect HUD eligibility comes from
// capture.MasterData.EffectDefinition (the definition the Overlay and History both use) and an instance's mode from
// capture.DisplayResolver.InstanceMode, so a defect in a shared decision fails the gate, not only a missing file.
package readiness

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wwlogs/internal/capture"
)

// Status is the result of one check (AGENTS.md 4.4 gate statuses).
type Status string

const (
	StatusPass          Status = "PASS"
	StatusFail          Status = "FAIL"
	StatusUnverified    Status = "UNVERIFIED"
	StatusNotApplicable Status = "NOT_APPLICABLE"
)

// Domain is one required readiness domain. Every report lists every domain.
type Domain string

const (
	DomainProtocol     Domain = "protocol"
	DomainMaster       Domain = "master"
	DomainCharacters   Domain = "characters"
	DomainEffects      Domain = "effects"
	DomainAssets       Domain = "assets"
	DomainContent      Domain = "content"
	DomainRuntime      Domain = "runtime"
	DomainVerification Domain = "verification"
)

// Domains lists the domains in report order with their titles.
var Domains = []struct {
	ID    Domain
	Title string
}{
	{DomainProtocol, "Protocol and transport"},
	{DomainMaster, "Runtime Master and localization"},
	{DomainCharacters, "Characters, skills, and combat semantics"},
	{DomainEffects, "HUD and effect presentation"},
	{DomainAssets, "Assets and bundled resources"},
	{DomainContent, "Game content identification"},
	{DomainRuntime, "Runtime, history, and profiles"},
	{DomainVerification, "Verification and corpus"},
}

// Input kinds say what a check reads.
const (
	KindStatic   = "static"   // tracked repository inputs only
	KindResearch = "research" // a private research input named by Inputs
	KindEvidence = "evidence" // results of a verification run named by Inputs
	KindLive     = "live"     // the owner's Live acceptance record
)

// maxFindings bounds the findings one check lists; the rest are counted.
const maxFindings = 40

// Coverage is a checked share with its denominator named.
type Coverage struct {
	Passed      int    `json:"passed"`
	Total       int    `json:"total"`
	Denominator string `json:"denominator"`
}

// Check is one gate check and its result.
type Check struct {
	ID        string    `json:"id"`
	Domain    Domain    `json:"domain"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Mandatory bool      `json:"mandatory"`
	Status    Status    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	Coverage  *Coverage `json:"coverage,omitempty"`
	Evidence  []string  `json:"evidence,omitempty"`
	Findings  []string  `json:"findings,omitempty"`
	// MoreFindings counts findings beyond maxFindings.
	MoreFindings int `json:"moreFindings,omitempty"`
	// Subjects lists every failing subject (for example "effect 1512310003"), without the bound of Findings.
	Subjects []string `json:"subjects,omitempty"`
}

// fail records a finding and marks the check failed.
func (c *Check) fail(format string, args ...any) {
	c.Status = StatusFail
	if len(c.Findings) >= maxFindings {
		c.MoreFindings++
		return
	}
	c.Findings = append(c.Findings, fmt.Sprintf(format, args...))
}

// failSubject records a finding about one subject; the subject is kept even when the findings are full.
func (c *Check) failSubject(subject, format string, args ...any) {
	c.Subjects = append(c.Subjects, subject)
	c.fail(format, args...)
}

// unverified marks a check that could not run, with the reason.
func (c *Check) unverified(reason string) {
	if c.Status != StatusFail {
		c.Status = StatusUnverified
	}
	if c.Reason == "" {
		c.Reason = reason
	}
}

// DomainResult summarizes one domain.
type DomainResult struct {
	ID     Domain `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	Checks int    `json:"checks"`
}

// Report is the gate's answer for one repository state.
type Report struct {
	Generated      time.Time `json:"generated"`
	GameVersion    string    `json:"gameVersion"`
	ProtocolClient string    `json:"protocolClient"`
	MasterSHA256   string    `json:"masterSha256,omitempty"`
	AssetClient    string    `json:"assetClient,omitempty"`
	AssetResource  string    `json:"assetResource,omitempty"`
	// StaticReady is true only when every mandatory static, research and evidence check passed or does not apply.
	StaticReady bool `json:"staticReady"`
	// LiveVerified is true only with a passing Live acceptance record for this game version.
	LiveVerified bool           `json:"liveVerified"`
	Domains      []DomainResult `json:"domains"`
	Checks       []*Check       `json:"checks"`
	// Blockers lists the mandatory checks that keep the version from STATIC_READY.
	Blockers []string `json:"blockers"`
}

// Classifier returns the effect definition the product attaches to an effect id. The gate uses the product's own
// (capture.MasterData.EffectDefinition); failure-injection tests substitute a defective one.
type Classifier func(masters *capture.MasterData, effectID uint64) capture.EffectDefinition

// Inputs names everything the gate reads. Root is required; every other path is optional and defaults as documented.
type Inputs struct {
	// Root is the repository root (source tree).
	Root string
	// MasterPath defaults to Root/data/master_data.db.
	MasterPath string
	// ManifestPath defaults to Root/assets/manifest.json; AssetRoot to Root/assets.
	ManifestPath string
	AssetRoot    string
	// GoldenRegistry defaults to Root/tests/capture/golden_registry.json.
	GoldenRegistry string

	// Research inputs (private, optional).
	ProtocolSchema   string
	ClientReferences string
	// ClientContent is the client content inventory (client_content_inventory.py); master.client-content compares it.
	ClientContent string
	// ClientImpact is the client update impact report (hotfix_impact.py); verification.client-impact reads it.
	ClientImpact string

	// Evidence of a verification run (optional).
	VerifyResults  string
	GoldenCoverage string
	HistorySummary string
	LiveAcceptance string

	// Classify defaults to the product's EffectDefinition.
	Classify Classifier
	// Locales are the Master locales the product loader is checked in; nil means all five bundled ones.
	Locales []string
	// Product, when set, is the product's MasterData already loaded from MasterPath; the gate uses it for the product
	// decisions and leaves it open. Tests share one load this way.
	Product *capture.MasterData
	// Now defaults to time.Now.
	Now func() time.Time
}

func (in *Inputs) defaults() {
	if in.MasterPath == "" {
		in.MasterPath = filepath.Join(in.Root, "data", "master_data.db")
	}
	if in.ManifestPath == "" {
		in.ManifestPath = filepath.Join(in.Root, "assets", "manifest.json")
	}
	if in.AssetRoot == "" {
		in.AssetRoot = filepath.Join(in.Root, "assets")
	}
	if in.GoldenRegistry == "" {
		in.GoldenRegistry = filepath.Join(in.Root, "tests", "capture", "golden_registry.json")
	}
	if in.Classify == nil {
		in.Classify = func(masters *capture.MasterData, id uint64) capture.EffectDefinition {
			return masters.EffectDefinition(id)
		}
	}
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.Locales == nil {
		in.Locales = masterLocales
	}
}

// gate carries one run's shared state.
type gate struct {
	in       Inputs
	report   *Report
	checks   map[string]*Check
	source   *protocolSource
	manifest *manifestFile
	master   *masterView
}

func (g *gate) check(id string, domain Domain, title, kind string, mandatory bool) *Check {
	c := &Check{ID: id, Domain: domain, Title: title, Kind: kind, Mandatory: mandatory, Status: StatusPass}
	g.report.Checks = append(g.report.Checks, c)
	g.checks[id] = c
	return c
}

// Run executes every check and returns the report. Only an unusable Root is an error; everything else is a check result.
func Run(in Inputs) (*Report, error) {
	if in.Root == "" {
		return nil, fmt.Errorf("readiness: repository root is required")
	}
	in.defaults()
	g := &gate{in: in, report: &Report{Generated: in.Now().UTC()}, checks: map[string]*Check{}}
	g.protocolChecks()
	g.clientImpactCheck()
	g.assetChecks()
	g.masterChecks()
	g.runtimeChecks()
	g.evidenceChecks()
	if g.master != nil {
		g.master.close()
	}
	g.summarize()
	return g.report, nil
}

func (g *gate) summarize() {
	r := g.report
	byDomain := map[Domain][]*Check{}
	for _, c := range r.Checks {
		byDomain[c.Domain] = append(byDomain[c.Domain], c)
	}
	r.StaticReady = true
	r.Blockers = []string{}
	for _, d := range Domains {
		result := DomainResult{ID: d.ID, Title: d.Title, Status: StatusPass, Checks: len(byDomain[d.ID])}
		applicable := 0
		for _, c := range byDomain[d.ID] {
			if c.Status != StatusNotApplicable {
				applicable++
			}
			switch {
			case c.Status == StatusFail:
				result.Status = StatusFail
			case c.Status == StatusUnverified && result.Status != StatusFail:
				result.Status = StatusUnverified
			}
			if c.Mandatory && c.Kind != KindLive && (c.Status == StatusFail || c.Status == StatusUnverified) {
				r.StaticReady = false
				r.Blockers = append(r.Blockers, fmt.Sprintf("%s %s: %s", c.ID, c.Status, firstNonEmpty(c.Reason, strings.Join(c.Findings, "; "))))
			}
		}
		if len(byDomain[d.ID]) == 0 {
			// A domain without any check is itself a gap: the matrix never drops a domain.
			result.Status = StatusUnverified
			r.StaticReady = false
			r.Blockers = append(r.Blockers, fmt.Sprintf("domain %s has no check", d.ID))
		} else if applicable == 0 {
			result.Status = StatusNotApplicable
		}
		r.Domains = append(r.Domains, result)
	}
	if live := g.checks["verification.live-acceptance"]; live != nil {
		r.LiveVerified = live.Status == StatusPass
	}
	sort.SliceStable(r.Checks, func(i, j int) bool { return domainOrder(r.Checks[i].Domain) < domainOrder(r.Checks[j].Domain) })
}

func domainOrder(d Domain) int {
	for i, entry := range Domains {
		if entry.ID == d {
			return i
		}
	}
	return len(Domains)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// majorMinor returns the major.minor prefix of a version string ("3.8.9" -> "3.8"), or "" when it has none.
func majorMinor(version string) string {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 2 || !allDigits(parts[0]) || !allDigits(parts[1]) {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
