package readiness

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// impactReport is the research input clientImpact: the client update impact report of
// work/version-update/tools/hotfix_impact.py (PROCEDURE.md S1a). It compares the client the product was adapted to
// (base) with an installed or new client (target) of the same platform and region, and classifies what changed for the
// runtime: NO_CHANGE, NO_RUNTIME_IMPACT, RUNTIME_RELEVANT, INVESTIGATION_REQUIRED or BLOCKED.
type impactReport struct {
	Schema   int    `json:"schema"`
	Platform string `json:"platform"`
	Region   string `json:"region"`
	Base     struct {
		Label        string `json:"label"`
		GameVersion  string `json:"gameVersion"`
		ClientRegion string `json:"clientRegion"`
	} `json:"base"`
	Target struct {
		Label        string `json:"label"`
		GameVersion  string `json:"gameVersion"`
		ClientRegion string `json:"clientRegion"`
	} `json:"target"`
	Overall string `json:"overall"`
	Domains map[string]struct {
		Classification string `json:"classification"`
		Reasons        []struct {
			Level  string `json:"level"`
			Reason string `json:"reason"`
		} `json:"reasons"`
	} `json:"domains"`
	LiveChecklist []string `json:"liveChecklist"`
}

// clientImpactCheck connects the client the product decodes with the client an update brought: a static gate over the
// product's own sources says nothing about a newer installed client until its impact is known.
func (g *gate) clientImpactCheck() {
	c := g.check("verification.client-impact", DomainVerification, "The client update impact report covers the client the product decodes and the client it compares with", KindResearch, true)
	if g.in.ClientImpact == "" {
		c.unverified("no client update impact report was supplied (research input clientImpact; work/version-update/PROCEDURE.md S1a)")
		return
	}
	var report impactReport
	if err := readJSON(g.in.ClientImpact, &report); err != nil {
		c.unverified(fmt.Sprintf("the client update impact report cannot be read: %v", err))
		return
	}
	if report.Schema != 1 {
		c.fail("the client update impact report has schema %d, the gate reads schema 1", report.Schema)
		return
	}
	if g.source == nil || g.source.protocolClient == "" {
		c.unverified("the decoded protocol client is unknown, so the report's clients cannot be compared")
		return
	}
	base := strings.TrimSpace(report.Base.ClientRegion + " " + report.Base.GameVersion)
	target := strings.TrimSpace(report.Target.ClientRegion + " " + report.Target.GameVersion)
	product := g.source.protocolClient
	var classes []string
	for name, domain := range report.Domains {
		classes = append(classes, name+" "+domain.Classification)
	}
	sort.Strings(classes)
	c.Evidence = append(c.Evidence, fmt.Sprintf("%s (%s %s): %s -> %s %s; %s", filepath.Base(filepath.Dir(g.in.ClientImpact)), report.Platform, report.Region, base, target, report.Overall, strings.Join(classes, ", ")))
	if report.Base.ClientRegion != report.Region || report.Target.ClientRegion != report.Region {
		c.fail("the report compares %s with %s as region %s: platforms and regions are compared only with themselves", base, target, report.Region)
		return
	}
	reasons := func(level string) string {
		var out []string
		for _, name := range sortedKeys(report.Domains) {
			for _, r := range report.Domains[name].Reasons {
				if r.Level == level {
					out = append(out, name+": "+r.Reason)
					break
				}
			}
		}
		return strings.Join(out, "; ")
	}
	switch report.Overall {
	case "NO_CHANGE", "NO_RUNTIME_IMPACT":
		if product != base && product != target {
			c.fail("the report compares %s with %s, the decoder reads %s: compare the client the product was adapted to", base, target, product)
		}
	case "RUNTIME_RELEVANT":
		switch product {
		case target:
			// The product was adapted to the target; the other checks gate it against the target's inputs.
		case base:
			c.fail("the update %s -> %s is RUNTIME_RELEVANT and the product still decodes %s: adapt it (PROCEDURE.md S2-S11) and gate the target client's inputs (%s)", base, target, base, reasons("RUNTIME_RELEVANT"))
		default:
			c.fail("the report compares %s with %s, the decoder reads %s", base, target, product)
		}
	case "INVESTIGATION_REQUIRED":
		c.unverified(fmt.Sprintf("the impact of %s -> %s needs evidence: %s", base, target, reasons("INVESTIGATION_REQUIRED")))
	case "BLOCKED":
		c.fail("the impact check of %s -> %s is BLOCKED: %s", base, target, reasons("BLOCKED"))
	default:
		c.fail("the report's overall classification %q is not one of NO_CHANGE, NO_RUNTIME_IMPACT, RUNTIME_RELEVANT, INVESTIGATION_REQUIRED, BLOCKED", report.Overall)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
