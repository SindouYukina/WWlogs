package readiness_test

// The research boundary of the readiness gate: the checks of the maintainer's private research inputs are declared by the
// tracked gate, mandatory and in their place, and implemented only by a research extension compiled into the package from
// outside the repository. A gate built from the tracked tree alone leaves them UNVERIFIED and never reports STATIC_READY,
// and the tracked package holds no research check implementation, registration or reader of the research inputs.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"wwlogs/scripts/readiness"
)

// declaredResearchChecks are the research checks the tracked gate declares, in report order.
var declaredResearchChecks = []string{"protocol.schema-keys", "master.client-content", "assets.client-references", "content.prefab-entities", "verification.client-impact"}

// passingResearch is a stand-in research extension whose checks all pass.
func passingResearch() readiness.ResearchExtension {
	checks := map[string]func(*readiness.ResearchRun, *readiness.Check){}
	for _, id := range declaredResearchChecks {
		checks[id] = func(*readiness.ResearchRun, *readiness.Check) {}
	}
	return readiness.ResearchExtension{Name: "stand-in", Checks: checks}
}

// Without a research extension every research check is UNVERIFIED, so even a run whose other checks all pass, with a
// research inputs file and a passing Live record, is LIVE_VERIFIED but never STATIC_READY.
func TestResearchChecksNeedAResearchExtension(t *testing.T) {
	in := baseInputs(t)
	in.Locales = []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}
	in.Research = []readiness.ResearchExtension{}
	in.ResearchInputs = writeJSON(t, filepath.Join(t.TempDir(), "research-inputs.json"), map[string]any{"gameVersion": "3.8"})
	passingEvidence(t, &in)
	in.LiveAcceptance = writeJSON(t, filepath.Join(t.TempDir(), "live.json"), map[string]any{"gameVersion": "3.8", "clientVersion": "Global 3.8.9", "buildNumber": 33,
		"executableSha256": strings.Repeat("a", 64), "date": "2026-11-15", "operator": "owner", "result": "PASS"})
	report := run(t, in)
	var research []string
	for _, c := range report.Checks {
		if c.Kind != readiness.KindResearch {
			continue
		}
		research = append(research, c.ID)
		if !c.Mandatory || c.Status != readiness.StatusUnverified || !strings.Contains(c.Reason, "no research extension") || !strings.Contains(c.Reason, "not read") {
			t.Errorf("%s = %s, mandatory %v (%s), want a mandatory UNVERIFIED research check", c.ID, c.Status, c.Mandatory, c.Reason)
		}
	}
	if !slices.Equal(research, declaredResearchChecks) {
		t.Errorf("research checks %v, want %v", research, declaredResearchChecks)
	}
	if report.StaticReady || !report.LiveVerified {
		t.Fatalf("STATIC_READY %v, LIVE_VERIFIED %v; blockers %v", report.StaticReady, report.LiveVerified, report.Blockers)
	}
	for _, id := range declaredResearchChecks {
		found := false
		for _, blocker := range report.Blockers {
			found = found || strings.HasPrefix(blocker, id+" UNVERIFIED")
		}
		if !found {
			t.Errorf("%s is not among the blockers %v", id, report.Blockers)
		}
	}
	// A stand-in extension that passes them is the only thing missing: with it the same run is STATIC_READY.
	in.Research = []readiness.ResearchExtension{passingResearch()}
	if report := run(t, in); !report.StaticReady {
		t.Fatalf("with passing research checks: blockers %v", report.Blockers)
	}
}

// researchBoundaryViolations checks the readiness package source: a research check is declared only through the
// research slot helper, no tracked file registers a research extension or reads the research inputs file, and no research
// extension file is part of the tracked package.
func researchBoundaryViolations(files map[string]string) []string {
	var violations []string
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, "zz_research_") {
			violations = append(violations, name+": a research extension file is in the tracked package")
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, files[name], 0)
		if err != nil {
			violations = append(violations, name+": "+err.Error())
			continue
		}
		for _, decl := range file.Decls {
			function, _ := decl.(*ast.FuncDecl)
			ast.Inspect(decl, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.CallExpr:
					if ident, ok := n.Fun.(*ast.Ident); ok && ident.Name == "registerResearch" {
						violations = append(violations, fset.Position(n.Pos()).String()+": registers a research extension")
					}
					if selector, ok := n.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "check" && (function == nil || function.Name.Name != "researchCheck") {
						for _, argument := range n.Args {
							if ident, ok := argument.(*ast.Ident); ok && ident.Name == "KindResearch" {
								violations = append(violations, fset.Position(n.Pos()).String()+": declares a research check outside researchCheck")
							}
						}
					}
				case *ast.SelectorExpr:
					if n.Sel.Name == "ResearchInputs" && name != "readiness.go" {
						violations = append(violations, fset.Position(n.Pos()).String()+": reads the research inputs outside readiness.go")
					}
				}
				return true
			})
		}
	}
	return violations
}

// The tracked readiness package keeps to the research boundary.
func TestResearchChecksStayOutOfTheTrackedGate(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "scripts", "readiness")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(data)
	}
	if len(files) == 0 {
		t.Fatal("no source file in scripts/readiness")
	}
	for _, violation := range researchBoundaryViolations(files) {
		t.Error(violation)
	}
}

// The boundary check finds every kind of violation, in memory: a research check declared directly, a registration, a
// reader of the research inputs and an extension file in the package.
func TestResearchBoundaryCheckFindsViolations(t *testing.T) {
	files := map[string]string{
		"readiness.go":             "package readiness\n\nfunc (g *gate) researchCheck(id string) *Check { return g.check(id, DomainMaster, \"t\", KindResearch, true) }\n",
		"master.go":                "package readiness\n\nfunc (g *gate) inline() { g.check(\"master.x\", DomainMaster, \"t\", KindResearch, true) }\n\nfunc read(g *gate) { _ = g.in.ResearchInputs }\n",
		"zz_research_extension.go": "package readiness\n\nfunc init() { registerResearch(ResearchExtension{}) }\n",
	}
	got := strings.Join(researchBoundaryViolations(files), "\n")
	for _, want := range []string{
		"master.go:3:", "declares a research check outside researchCheck",
		"master.go:5:", "reads the research inputs outside readiness.go",
		"zz_research_extension.go: a research extension file is in the tracked package",
		"zz_research_extension.go:3:", "registers a research extension",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the boundary check missed %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "readiness.go:") {
		t.Errorf("the research slot helper itself was reported:\n%s", got)
	}
}
