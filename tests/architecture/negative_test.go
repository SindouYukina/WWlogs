package architecture

import (
	"encoding/json"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Negative tests: each case breaks one contract and requires that contract to report it, then requires the same
// contract to stay silent on the actual source. A violation inside one package is an in-memory override (a probe file
// added, or one line of a real file changed before parsing); one that spans packages goes through a temporary overlay,
// so that the go command and the export data see it. Nothing on disk changes. A contract that cannot fail on a known
// violation protects nothing.

// probe is an override: a slash path relative to the repository root and the file's source.
func probe(rel, buildConstraint, body string) [2]string {
	pkg := filepath.Base(filepath.Dir(filepath.FromSlash(rel)))
	if strings.HasPrefix(rel, "cmd/") {
		pkg = "main"
	}
	source := "package " + pkg + "\n\n" + body + "\n"
	if buildConstraint != "" {
		source = "//go:build " + buildConstraint + "\n\n" + source
	}
	return [2]string{rel, source}
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

// probeRepository is the repository seen through a temporary overlay: the committed entries plus the probe files.
func probeRepository(t *testing.T, files map[string]string) *repository {
	t.Helper()
	r := loadRepository(t)
	dir := t.TempDir()
	replace := map[string]string{}
	for from, to := range r.overlay {
		replace[from] = to
	}
	for rel, source := range files {
		path := filepath.Join(dir, strings.ReplaceAll(rel, "/", "__"))
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		replace[rel] = path
	}
	data, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return &repository{root: r.root, overlayFile: overlayFile, overlay: replace, mu: sync.Mutex{},
		listings: map[string]map[string]*listedPackage{}, packages: map[string][]*listedPackage{},
		dependencies: map[string]map[string]bool{}, fsets: map[string]*token.FileSet{}, imports: map[string]types.Importer{}}
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

// A file's name and build constraint disagree, and a third profile tag appears (contract 1).
func TestNegativeProfileFileConstraints(t *testing.T) {
	r := loadRepository(t)
	findings, _ := profileFileFindings(t, r, overrides(
		probe("cmd/capture/compose_probe.go", "wwdev", "const composeProbe = 1"),
		probe("cmd/capture/compose_probe_public.go", "", "const composeProbePublic = 1"),
		probe("cmd/capture/compose_probe_tag_development.go", "wwdev && research", "const composeProbeTag = 1"),
	))
	requireFinding(t, findings, "compose_probe.go", "not named *_development.go")
	requireFinding(t, findings, "compose_probe_public.go", "named for the Public profile")
	requireFinding(t, findings, "compose_probe_tag_development.go", `build tag "research"`)
	real, _ := profileFileFindings(t, r, nil)
	requireNoFinding(t, "profile files", real)
}

// A feature module gets a profile-specific implementation: a Development hook file in the core and its Public no-op
// (contract 2).
func TestNegativeProfileFilesInAFeatureModule(t *testing.T) {
	r := loadRepository(t)
	_, profilePackages := profileFileFindings(t, r, overrides(
		probe("internal/capture/architecture_probe_development.go", "wwdev", "func (p *Processor) architectureProbeHook() { p.Stats.ParseErrors++ }"),
		probe("internal/capture/architecture_probe_public.go", "!wwdev", "func (p *Processor) architectureProbeHook() {}"),
	))
	requireFinding(t, boundaryFindings(profilePackages), "internal/capture has profile-specific files but is not a build composition boundary")
	_, real := profileFileFindings(t, r, nil)
	requireNoFinding(t, "composition boundaries", boundaryFindings(real))
}

// A composition file computes and decides instead of naming what it links (contract 3).
func TestNegativeLogicInACompositionBoundary(t *testing.T) {
	r := loadRepository(t)
	changed := overrides(
		probe("cmd/capture/compose_probe_development.go", "wwdev", "import \"os\"\n\nvar composeProbe = len(os.Args) > 1\n\nfunc composeProbeDecide() bool { return composeProbe }"),
		probe("cmd/capture/compose_probe_public.go", "!wwdev", "var composeProbe = false"),
	)
	findings := wiringFindings(r.check(t, publicProfile, "cmd/capture", changed), r.check(t, developmentProfile, "cmd/capture", changed))
	requireFinding(t, findings, "compose_probe_development.go", "declares the function composeProbeDecide")
	requireFinding(t, findings, "compose_probe_development.go", "computes a value")
	requireNoFinding(t, "wiring", wiringFindings(productCheck(t, publicProfile, "cmd/capture"), productCheck(t, developmentProfile, "cmd/capture")))
}

// A shared product package imports a Development module, so the Public executables link it; and a new package only the
// Development composition links is not declared (contract 4).
func TestNegativePublicLinksADevelopmentModule(t *testing.T) {
	probes := overrides(
		probe("internal/desktop/architecture_probe.go", "", "import _ \"wwlogs/internal/devcapture\""),
		probe("internal/devprobe/probe.go", "", "// Package devprobe is an architecture probe.\n\nconst Probe = 1"),
		realFileWith(t, "cmd/capture/compose_development.go", "import \"wwlogs/internal/devcapture\"", "import (\n\t\"wwlogs/internal/devcapture\"\n\t_ \"wwlogs/internal/devprobe\"\n)"),
	)
	p := probeRepository(t, probes)
	links := developmentLinkFindings(p.executableDependencies(t, publicProfile, ""), p.executableDependencies(t, developmentProfile, ""))
	requireFinding(t, links, "the Public executables", "wwlogs/internal/devcapture")
	requireFinding(t, links, "only the Development executables link wwlogs/internal/devprobe")
	requireFinding(t, developmentImportFindings(t, p, nil), "internal/desktop/architecture_probe.go imports the Development module wwlogs/internal/devcapture")
	r := loadRepository(t)
	requireNoFinding(t, "Development links", developmentLinkFindings(r.executableDependencies(t, publicProfile, ""), r.executableDependencies(t, developmentProfile, "")))
	requireNoFinding(t, "Development imports", developmentImportFindings(t, r, nil))
}

// A research helper moves into the core and only the Development module calls it (contract 5).
func TestNegativeDevelopmentImplementationInTheCore(t *testing.T) {
	p := probeRepository(t, overrides(
		probe("internal/capture/architecture_probe.go", "", "// ArchitectureProbeResearchLine formats a research line.\nfunc ArchitectureProbeResearchLine(p *Processor) int { return p.Stats.ParseErrors }"),
		probe("internal/devcapture/architecture_probe.go", "", "import \"wwlogs/internal/capture\"\n\nfunc architectureProbeUse(p *processor) int { return capture.ArchitectureProbeResearchLine(p.Processor) }"),
	))
	findings, _ := coreServiceFindings(p.check(t, publicProfile, corePackage, nil), coreUsers(t, p))
	requireFinding(t, findings, "func ArchitectureProbeResearchLine of the core is used only by Development modules")
	real, _ := coreServiceFindings(productCheck(t, publicProfile, corePackage), coreUsers(t, loadRepository(t)))
	requireNoFinding(t, "core declarations", real)
}

// An observation point starts returning a value the core could decide with, or receiving a pointer to a core value
// (contract 6).
func TestNegativeObservationPointReturnsAValue(t *testing.T) {
	core := loadRepository(t).check(t, publicProfile, corePackage, overrides(
		realFileWith(t, "internal/capture/observer.go", "\tBeginProtocolSession()", "\tBeginProtocolSession()\n\tArchitectureProbeDecision() bool\n\tArchitectureProbeRow(row *BuffLifecycleAuditRow)"),
	))
	findings, _, _ := observationFindings(core)
	requireFinding(t, findings, "Observer.ArchitectureProbeDecision returns a value")
	requireFinding(t, findings, "Observer.ArchitectureProbeRow takes the pointer row")
	real, _, _ := observationFindings(productCheck(t, publicProfile, corePackage))
	requireNoFinding(t, "observation points", real)
}

// Development code changes core state: a core counter through the embedded core processor, a whole core event
// through its pointer, and the effect row a hook received (contract 7).
func TestNegativeDevelopmentWritesCoreState(t *testing.T) {
	development := loadRepository(t).check(t, developmentProfile, "internal/devcapture", overrides(
		probe("internal/devcapture/architecture_probe.go", "", "import \"wwlogs/internal/capture\"\n\n"+
			"func (p *processor) architectureProbeCountsParseError() { p.Stats.ParseErrors++ }\n\n"+
			"func architectureProbeReplacesEvent(event *capture.CombatEvent) { *event = capture.CombatEvent{} }\n\n"+
			"func architectureProbeChangesRow(row *capture.BuffLifecycleAuditRow) { row.SessionNumber++ }\n\n"+
			"func (p *processor) architectureProbeOwnState() { p.dev.research = true }"),
	))
	findings, _ := developmentWriteFindings(development, modulePath+"/"+corePackage)
	requireFinding(t, findings, "architectureProbeCountsParseError", "Processor.Stats")
	requireFinding(t, findings, "architectureProbeReplacesEvent", "CombatEvent")
	requireFinding(t, findings, "architectureProbeChangesRow", "BuffLifecycleAuditRow.SessionNumber")
	for _, finding := range findings {
		if strings.Contains(finding, "architectureProbeOwnState") {
			t.Fatalf("a write of the Development state is reported: %s", finding)
		}
	}
	real, _ := developmentWriteFindings(productCheck(t, developmentProfile, "internal/devcapture"), modulePath+"/"+corePackage)
	requireNoFinding(t, "Development writes", real)
}

// A research record is added to the core processor and only Development would read it (contract 8).
func TestNegativeDevelopmentStateInTheCoreProcessor(t *testing.T) {
	core := loadRepository(t).check(t, publicProfile, corePackage, overrides(
		realFileWith(t, "internal/capture/protocol.go", "type Processor struct {", "type Processor struct {\n\tArchitectureProbeResearch map[uint64]uint64"),
		probe("internal/capture/architecture_probe.go", "", "func (p *Processor) architectureProbeRecord(key uint64) { p.ArchitectureProbeResearch[key] = key }"),
	))
	findings, _ := coreFieldFindings(core)
	requireFinding(t, findings, "Processor.ArchitectureProbeResearch is never read by the core")
	real, _ := coreFieldFindings(productCheck(t, publicProfile, corePackage))
	requireNoFinding(t, "core fields", real)
}

// The research schema is declared in the core again (contract 9).
func TestNegativeResearchSchemaEntersTheCore(t *testing.T) {
	core := loadRepository(t).check(t, publicProfile, corePackage, overrides(
		probe("internal/capture/architecture_probe.go", "", "type protocolSchema struct{ GameVersion string }"),
	))
	requireFinding(t, researchSchemaFindings(core, productCheck(t, developmentProfile, researchModule)), "the core declares the research schema type protocolSchema")
	requireNoFinding(t, "research schema", researchSchemaFindings(productCheck(t, publicProfile, corePackage), productCheck(t, developmentProfile, researchModule)))
}

// The Public command line offers a Development diagnostic flag, and a Development flag silently disappears
// (contract 10).
func TestNegativeDevelopmentFlagsLeakOrDisappear(t *testing.T) {
	r := loadRepository(t)
	module := productCheck(t, developmentProfile, "internal/devcapture")
	where := flagDefinitions(module)["frame-archive-out"]
	if where == "" {
		t.Fatal("the actual source defines no -frame-archive-out; choose another required flag for the probe")
	}
	changedModule := r.check(t, developmentProfile, "internal/devcapture", overrides(realFileWith(t, where, `"frame-archive-out"`, `"frame-archive-renamed-out"`)))
	publicLine := r.check(t, publicProfile, corePackage, overrides(
		probe("internal/capture/architecture_probe.go", "", "import \"flag\"\n\nfunc architectureProbePublicFlag() { flag.String(\"event-jsonl-out\", \"\", \"\") }"),
	))
	findings := developmentFlagFindings(changedModule, map[*checkedPackage]map[string]bool{publicLine: publicCommandLines[corePackage]})
	requireFinding(t, findings, "-frame-archive-out is no longer defined")
	requireFinding(t, findings, "architecture_probe.go", "a Public command line defines the Development diagnostic flag -event-jsonl-out")
	lines := map[*checkedPackage]map[string]bool{}
	for rel, shared := range publicCommandLines {
		lines[productCheck(t, publicProfile, rel)] = shared
	}
	requireNoFinding(t, "Development flags", developmentFlagFindings(module, lines))
}
