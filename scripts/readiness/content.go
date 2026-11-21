package readiness

// The content coverage checks: whether the content the gated client defines is found in sources that were obtained and
// parsed, placed in its content systems by verified evidence, held by the Master and identified by the product. Each states
// its own denominators. They are research checks: the tracked gate declares them, mandatory and assessed, and only the
// maintainer's private research extension decides them; a gate built from the tracked tree alone leaves them UNVERIFIED.
// A check of evidence whose assessment is not VERIFIED never passes (enforceAssessment).

// contentCoverageChecks declares and runs the content coverage checks once the Master is loaded, so that a research
// extension can ask the product's own resolver about each instance.
func (g *gate) contentCoverageChecks() {
	for _, c := range []struct{ id, title string }{
		{"content.source-coverage", "The client sources that define content were obtained and parsed for the gated client (private research check)"},
		{"content.discovery", "Static discovery of the gated client ran on verified sources and every content system it found is registered or queued for research (private research check)"},
		{"content.semantic-mapping", "Every in-scope content system's identity, sources and instance rules are verified on the gated client (private research check)"},
		{"content.master-mapping", "The Master holds the relations and names that place and name every in-scope content instance (private research check)"},
		{"content.runtime-mapping", "The product resolves the kind of play of every in-scope content instance and shows no placeholder name (private research check)"},
	} {
		g.research(g.assessedResearchCheck(c.id, DomainContent, c.title))
	}
}
