// Package readiness is the version readiness gate of AGENTS.md section 4.4: it checks that a game-version adaptation is
// complete across every required domain, from the client's definitions through the Master, the image pack and the shared
// Go decisions to what the Overlay and History show.
//
// It is build and verification tooling: no product package imports it, and it reads only what its Inputs name. Tracked
// inputs (the source tree, the Master, the image manifest) are always read. Run evidence (scripts/verify.ps1 results,
// Golden coverage, the History replay summary, the owner's Live acceptance) is optional: a check whose input is missing is
// UNVERIFIED, never PASS, and an UNVERIFIED mandatory check keeps the version from STATIC_READY.
//
// Research checks compare the Master and the image pack with the maintainer's private research inputs. The tracked gate
// declares each one, mandatory, at its place in the report; their implementation is private research tooling, a
// ResearchExtension compiled into this package by the maintainer's own overlay. Without one every research check is
// UNVERIFIED, so a gate built from the tracked tree alone never reports STATIC_READY.
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
	KindResearch = "research" // a private research input, read by a research extension
	KindEvidence = "evidence" // results of a verification run named by Inputs
	KindLive     = "live"     // the owner's Live acceptance record
)

// maxFindings bounds the findings one check lists; the rest are counted.
const maxFindings = 40

// Evidence completeness of an Assessment.
const (
	CompletenessVerified   = "VERIFIED"
	CompletenessPartial    = "PARTIAL"
	CompletenessBlocked    = "BLOCKED"
	CompletenessUnverified = "UNVERIFIED"
)

// Assessment is a check's neutral account of the evidence it decided on: which evidence (its version), over which source
// scope, how complete that evidence is, the coverage it measured (each share with its denominator), the categories of what
// did not pass and the limits that remain. The tracked gate only validates and reports it: a check of evidence that is not
// VERIFIED never passes.
type Assessment struct {
	EvidenceVersion string          `json:"evidenceVersion"`
	SourceScope     string          `json:"sourceScope"`
	Completeness    string          `json:"completeness"`
	Coverage        []NamedCoverage `json:"coverage,omitempty"`
	Reasons         []ReasonCount   `json:"reasons,omitempty"`
	Limitations     []string        `json:"limitations,omitempty"`
}

// NamedCoverage is one measured share of an Assessment with its denominator named.
type NamedCoverage struct {
	Name        string `json:"name"`
	Passed      int    `json:"passed"`
	Total       int    `json:"total"`
	Denominator string `json:"denominator"`
}

// ReasonCount counts the findings of one category.
type ReasonCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

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
	// Assessment is the evidence account of a check that decides on an assessed body of evidence (research checks).
	Assessment *Assessment `json:"assessment,omitempty"`
	// assessed marks a check that must report an Assessment to pass.
	assessed bool
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

	// ResearchInputs is the maintainer's private research inputs file (optional). The tracked gate never reads it; it
	// passes it to the research extensions.
	ResearchInputs string
	// Research are the research extensions that run the research checks; nil means the ones compiled into this package
	// (none in the tracked tree).
	Research []ResearchExtension

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
	in         Inputs
	report     *Report
	checks     map[string]*Check
	source     *protocolSource
	manifest   *manifestFile
	master     *masterView
	extensions []ResearchExtension
}

func (g *gate) check(id string, domain Domain, title, kind string, mandatory bool) *Check {
	c := &Check{ID: id, Domain: domain, Title: title, Kind: kind, Mandatory: mandatory, Status: StatusPass}
	g.report.Checks = append(g.report.Checks, c)
	g.checks[id] = c
	return c
}

// A ResearchExtension runs research checks (see the package comment). Titles and Checks are keyed by the IDs of the
// research checks the gate declares: Titles replaces a declared title, Checks runs the check once the data it needs is
// read. Extra declares and runs the extension's further checks after every tracked check.
type ResearchExtension struct {
	Name   string
	Titles map[string]string
	Checks map[string]func(run *ResearchRun, c *Check)
	Extra  func(run *ResearchRun)
}

// ResearchRun is one gate run as a research extension sees it.
type ResearchRun struct {
	// Inputs is the private research inputs file the caller named, or "".
	Inputs string
	g      *gate
}

// Check declares a further mandatory check of the extension; it passes unless the extension records otherwise.
func (r *ResearchRun) Check(id string, domain Domain, title, kind string) *Check {
	return r.g.check(id, domain, title, kind, true)
}

// registeredResearch are the research extensions compiled into this package. The tracked tree registers none.
var registeredResearch []ResearchExtension

// registerResearch adds a research extension compiled into this package; the extension calls it from its init.
func registerResearch(extension ResearchExtension) {
	registeredResearch = append(registeredResearch, extension)
}

// researchCheck declares a mandatory research check at its place in the report.
func (g *gate) researchCheck(id string, domain Domain, title string) *Check {
	for _, extension := range g.extensions {
		if own := extension.Titles[id]; own != "" {
			title = own
			break
		}
	}
	return g.check(id, domain, title, KindResearch, true)
}

// assessedResearchCheck declares a mandatory research check that must report an evidence Assessment to pass.
func (g *gate) assessedResearchCheck(id string, domain Domain, title string) *Check {
	c := g.researchCheck(id, domain, title)
	c.assessed = true
	return c
}

// enforceAssessment keeps a check of incomplete or unstated evidence from passing (fail-closed): an assessed check without
// an Assessment, an unknown completeness, or a completeness other than VERIFIED leaves the check UNVERIFIED instead of
// PASS. A FAIL stays a FAIL.
func enforceAssessment(c *Check) {
	a := c.Assessment
	switch {
	case a == nil:
		if c.assessed {
			c.unverified("the research check reported no evidence assessment (evidence version, source scope, completeness)")
		}
	case a.Completeness != CompletenessVerified && a.Completeness != CompletenessPartial && a.Completeness != CompletenessBlocked && a.Completeness != CompletenessUnverified:
		c.unverified(fmt.Sprintf("the research check reported an unknown evidence completeness %q", a.Completeness))
	case a.Completeness != CompletenessVerified:
		c.unverified(fmt.Sprintf("the evidence is %s: a check of incomplete evidence never passes", a.Completeness))
	}
}

// research runs a declared research check with the extension that implements it. Without one the check stays
// UNVERIFIED: the tracked gate cannot decide it.
func (g *gate) research(c *Check) {
	for _, extension := range g.extensions {
		if run := extension.Checks[c.ID]; run != nil {
			run(&ResearchRun{Inputs: g.in.ResearchInputs, g: g}, c)
			enforceAssessment(c)
			return
		}
	}
	reason := "a check of the maintainer's private research inputs: no research extension that implements it is compiled into this gate"
	if g.in.ResearchInputs != "" {
		reason += " (the research inputs file was not read)"
	}
	c.unverified(reason)
}

// Run executes every check and returns the report. Only an unusable Root is an error; everything else is a check result.
func Run(in Inputs) (*Report, error) {
	if in.Root == "" {
		return nil, fmt.Errorf("readiness: repository root is required")
	}
	in.defaults()
	g := &gate{in: in, report: &Report{Generated: in.Now().UTC()}, checks: map[string]*Check{}, extensions: in.Research}
	if g.extensions == nil {
		g.extensions = registeredResearch
	}
	g.protocolChecks()
	g.research(g.researchCheck("verification.client-impact", DomainVerification, "The client update was compared with the client the product decodes (private research check)"))
	g.assetChecks()
	g.masterChecks()
	g.contentCoverageChecks()
	g.runtimeChecks()
	g.evidenceChecks()
	for _, extension := range g.extensions {
		if extension.Extra != nil {
			extension.Extra(&ResearchRun{Inputs: g.in.ResearchInputs, g: g})
		}
	}
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
