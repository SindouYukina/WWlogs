package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Negative tests: each case breaks one contract in memory (an added probe file, or one line of a real file changed
// before parsing; nothing on disk changes) and requires that contract to report it, then requires the same contract to
// stay silent on the actual source. A contract that cannot fail on a known violation protects nothing.

const probePackage = "internal/capture"

// file is an override that adds a probe file (path, source) to the capture package.
func file(name, buildConstraint, body string) [2]string {
	source := "package capture\n\n" + body + "\n"
	if buildConstraint != "" {
		source = "//go:build " + buildConstraint + "\n\n" + source
	}
	return [2]string{probePackage + "/" + name, source}
}

// overrides collects override files by path.
func overrides(files ...[2]string) map[string]string {
	result := map[string]string{}
	for _, file := range files {
		result[file[0]] = file[1]
	}
	return result
}

// realFileWith returns an override of an actual file, named by a file:line position, with one replacement applied.
func realFileWith(t *testing.T, where, old, new string) [2]string {
	t.Helper()
	rel := where
	if colon := strings.LastIndex(rel, ":"); colon >= 0 {
		rel = rel[:colon]
	}
	data, err := loadRepository(t).source(rel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q; the probe needs a new anchor", rel, old)
	}
	return [2]string{rel, strings.Replace(string(data), old, new, 1)}
}

// probeChecks type-checks the capture package with the overrides in both profiles.
func probeChecks(t *testing.T, changed map[string]string) (public, development *checkedPackage) {
	t.Helper()
	r := loadRepository(t)
	return r.check(t, publicProfile, probePackage, changed), r.check(t, developmentProfile, probePackage, changed)
}

// requireFinding fails unless a finding contains every fragment.
func requireFinding(t *testing.T, findings []string, fragments ...string) {
	t.Helper()
	for _, finding := range findings {
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(finding, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("no finding contains %q; findings:\n%s", fragments, strings.Join(findings, "\n"))
}

func requireNoFinding(t *testing.T, contract string, findings []string) {
	t.Helper()
	if len(findings) > 0 {
		t.Fatalf("%s reports the actual source:\n%s", contract, strings.Join(findings, "\n"))
	}
}

// The shared core reads Development state directly instead of through a hook (contract 2).
func TestNegativeSharedCodeReadsDevelopmentState(t *testing.T) {
	public, development := probeChecks(t, overrides(file("architecture_probe.go", "",
		"func (p *processor) architectureProbeReadsResearch() bool { return p.dev.research }")))
	findings := sharedReferenceFindings(public, development)
	requireFinding(t, findings, "architecture_probe.go", "field processorDiagnostics.research", "only the Development profile declares")
	requireFinding(t, findings, "Public profile does not type-check", "research")
	requireNoFinding(t, "shared references", sharedReferenceFindings(productCheck(t, publicProfile, probePackage), productCheck(t, developmentProfile, probePackage)))
}

// A Public file offers a Development diagnostic flag, and a Development flag silently disappears (contract 7).
func TestNegativeDevelopmentFlagsLeakOrDisappear(t *testing.T) {
	realDevelopment := productCheck(t, developmentProfile, probePackage)
	where := flagDefinitions(realDevelopment)[developmentFile]["frame-archive-out"]
	if where == "" {
		t.Fatal("the actual source defines no -frame-archive-out; choose another required flag for the probe")
	}
	public, development := probeChecks(t, overrides(
		file("architecture_probe_public.go", "!wwdev", "import \"flag\"\n\nfunc architectureProbePublicFlag() { flag.String(\"event-jsonl-out\", \"\", \"\") }"),
		realFileWith(t, where, `"frame-archive-out"`, `"frame-archive-renamed-out"`),
	))
	findings := developmentFlagFindings(public, development)
	requireFinding(t, findings, "architecture_probe_public.go", "Public-only file defines the Development diagnostic flag -event-jsonl-out")
	requireFinding(t, findings, "-frame-archive-out is no longer defined")
	requireNoFinding(t, "Development flags", developmentFlagFindings(productCheck(t, publicProfile, probePackage), realDevelopment))
}

// A shared file imports the Development PCAP writer, so the Public executables link it (contract 7). The go command
// reads the probe through a temporary overlay: the committed entries plus the probe.
func TestNegativePublicLinksTheDevelopmentPCAPWriter(t *testing.T) {
	r := loadRepository(t)
	dir := t.TempDir()
	probeSource := filepath.Join(dir, "architecture_probe_pcap.go")
	if err := os.WriteFile(probeSource, []byte("package capture\n\nimport _ \"github.com/google/gopacket/pcapgo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replace := map[string]string{probePackage + "/architecture_probe_pcap.go": probeSource}
	for from, to := range r.overlay {
		replace[from] = to
	}
	data, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	findings := developmentDependencyFindings(r.executableDependencies(t, publicProfile, overlayFile), r.executableDependencies(t, developmentProfile, ""))
	requireFinding(t, findings, "Public executables", "github.com/google/gopacket/pcapgo")
	requireNoFinding(t, "Development-only dependencies", developmentDependencyFindings(r.executableDependencies(t, publicProfile, ""), r.executableDependencies(t, developmentProfile, "")))
}

// A file's name and build constraint disagree, and a third profile tag appears (contract 1).
func TestNegativeProfileFileConstraints(t *testing.T) {
	r := loadRepository(t)
	findings, _ := profileFileFindings(t, r, overrides(
		file("architecture_probe_development.go", "", "func architectureProbeUnconstrained() {}"),
		file("architecture_probe_research.go", "wwresearch", "func architectureProbeThirdProfile() {}"),
	))
	requireFinding(t, findings, "architecture_probe_development.go", "named for the Development profile", "shared")
	requireFinding(t, findings, "architecture_probe_research.go", `build tag "wwresearch"`)
	real, _ := profileFileFindings(t, r, nil)
	requireNoFinding(t, "profile files", real)
}

// A hook returns a Development decision to the core, its two forms differ, and a split type carries Public fields
// (contract 3).
func TestNegativeSplitDeclarationsCarryValues(t *testing.T) {
	public, development := probeChecks(t, overrides(
		file("architecture_probe_public.go", "!wwdev", "func (p *processor) architectureProbeDecision() bool { return false }\n\n"+
			"func (p *processor) architectureProbeHook(int) {}\n\n"+
			"type architectureProbeState struct{ count int }"),
		file("architecture_probe_development.go", "wwdev", "func (p *processor) architectureProbeDecision() bool { return p.dev.research }\n\n"+
			"func (p *processor) architectureProbeHook(value int64) {}\n\n"+
			"type architectureProbeState struct{ count int }"),
		file("architecture_probe.go", "", "func (p *processor) architectureProbeUse() int {\n\tif p.architectureProbeDecision() {\n\t\treturn 1\n\t}\n\treturn 0\n}"),
	))
	findings, _ := splitDeclarationFindings(public, development)
	requireFinding(t, findings, "method processor.architectureProbeDecision", "returns a value")
	requireFinding(t, findings, "method processor.architectureProbeHook", "differ")
	requireFinding(t, findings, "type architectureProbeState", "has 1 fields")
	real, _ := splitDeclarationFindings(productCheck(t, publicProfile, probePackage), productCheck(t, developmentProfile, probePackage))
	requireNoFinding(t, "split declarations", real)
}

// The core takes the research schema as an input (contract 6).
func TestNegativeResearchSchemaEntersTheCore(t *testing.T) {
	public, _ := probeChecks(t, overrides(file("architecture_probe.go", "",
		"func (p *processor) architectureProbeDecodesWithSchema(schema *protocolSchema) bool { return schema != nil }")))
	requireFinding(t, researchSchemaFindings(public), "architecture_probe.go", "research schema declaration protocolSchema")
	requireNoFinding(t, "research schema", researchSchemaFindings(productCheck(t, publicProfile, probePackage)))
}

// Development code changes core state: a core counter, the effect row a hook received, a whole core event through its
// pointer, and a shared package variable (contract 4).
func TestNegativeDevelopmentWritesCoreState(t *testing.T) {
	_, development := probeChecks(t, overrides(
		file("architecture_probe.go", "", "var architectureProbeShared int"),
		file("architecture_probe_development.go", "wwdev", "func (p *processor) architectureProbeCountsParseError() { p.stats.parseErrors++ }\n\n"+
			"func architectureProbeChangesRow(row *buffLifecycleAuditRow) { row.SessionNumber++ }\n\n"+
			"func architectureProbeReplacesEvent(event *combatEvent) { *event = combatEvent{} }\n\n"+
			"func architectureProbeSetsShared() { architectureProbeShared = 1 }\n\n"+
			"func (p *processor) architectureProbeOwnState() { p.dev.research = true }"),
	))
	findings, _ := developmentWriteFindings(development)
	requireFinding(t, findings, "architectureProbeCountsParseError", "core state processor.stats")
	requireFinding(t, findings, "architectureProbeChangesRow", "buffLifecycleAuditRow.SessionNumber", "core type")
	requireFinding(t, findings, "architectureProbeReplacesEvent", "combatEvent")
	requireFinding(t, findings, "architectureProbeSetsShared", "shared package variable architectureProbeShared")
	for _, finding := range findings {
		if strings.Contains(finding, "architectureProbeOwnState") {
			t.Fatalf("a write of the Development state is reported: %s", finding)
		}
	}
	real, _ := developmentWriteFindings(productCheck(t, developmentProfile, probePackage))
	requireNoFinding(t, "Development writes", real)
}

// A research record is added to the core processor and only Development reads it (contract 5).
func TestNegativeDevelopmentStateInTheCoreProcessor(t *testing.T) {
	realPublic := productCheck(t, publicProfile, probePackage)
	declaring := realPublic.where(realPublic.pkg.Scope().Lookup(coreStateType).Pos())
	public, _ := probeChecks(t, overrides(
		realFileWith(t, declaring, "type processor struct {", "type processor struct {\n\tarchitectureProbeResearch map[uint64]uint64"),
		file("architecture_probe.go", "", "func (p *processor) architectureProbeRecord(key uint64) { p.architectureProbeResearch[key] = key }"),
		file("architecture_probe_development.go", "wwdev", "func (p *processor) architectureProbeReport() int { return len(p.architectureProbeResearch) }"),
	))
	findings, _ := coreFieldFindings(public)
	requireFinding(t, findings, "processor.architectureProbeResearch is never read by shared code")
	real, _ := coreFieldFindings(realPublic)
	requireNoFinding(t, "core fields", real)
}
