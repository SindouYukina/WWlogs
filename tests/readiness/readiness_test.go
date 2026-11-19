package readiness_test

// Tests of the version readiness gate. The current repository must have no static gap, and every kind of gap the Global
// 3.8 adaptation actually had (or could have) must make the gate fail, on isolated defective copies: a manifest without
// the new role's art, a product classification that drops a HUD status, a new game mode the resolver cannot place, a
// History that keeps an old game version, a retired protocol layer, a required Golden capture that did not run, and a
// Master lookup that no longer resolves. Nothing here touches the tracked Master, manifest or any user data. The research
// checks are declared here and implemented by the maintainer's private research extension (research_boundary_test.go).

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"wwlogs/internal/capture"
	"wwlogs/scripts/readiness"
)

var (
	productOnce sync.Once
	product     *capture.MasterData
	productErr  error
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// sharedProduct loads the tracked Master through the product loader once for every test that does not change it.
func sharedProduct(t *testing.T) *capture.MasterData {
	t.Helper()
	productOnce.Do(func() {
		product, productErr = capture.LoadMasterData(filepath.Join(repositoryRoot(t), "data", "master_data.db"), "zh-TW")
	})
	if productErr != nil {
		t.Fatal(productErr)
	}
	return product
}

// baseInputs reads the tracked repository with the shared product Master.
func baseInputs(t *testing.T) readiness.Inputs {
	return readiness.Inputs{Root: repositoryRoot(t), Product: sharedProduct(t), Locales: []string{}}
}

func run(t *testing.T, in readiness.Inputs) *readiness.Report {
	t.Helper()
	report, err := readiness.Run(in)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func checkOf(t *testing.T, report *readiness.Report, id string) *readiness.Check {
	t.Helper()
	for _, c := range report.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("the report has no check %s", id)
	return nil
}

// wantFail asserts that a check failed with a finding containing each of the given texts.
func wantFail(t *testing.T, report *readiness.Report, id string, texts ...string) {
	t.Helper()
	c := checkOf(t, report, id)
	if c.Status != readiness.StatusFail {
		t.Fatalf("%s = %s (%s), want FAIL", id, c.Status, c.Reason)
	}
	// Findings are bounded for the report; Subjects keeps every failing subject.
	all := strings.Join(c.Findings, "\n") + "\n" + strings.Join(c.Subjects, "\n")
	for _, text := range texts {
		if !strings.Contains(all, text) {
			t.Errorf("%s findings do not mention %q:\n%s", id, text, all)
		}
	}
	if report.StaticReady && c.Mandatory {
		t.Errorf("a failed mandatory check %s left the version STATIC_READY", id)
	}
	blocked := false
	for _, blocker := range report.Blockers {
		blocked = blocked || strings.HasPrefix(blocker, id+" ")
	}
	if c.Mandatory && !blocked {
		t.Errorf("%s is not listed among the blockers %v", id, report.Blockers)
	}
}

func wantStatus(t *testing.T, report *readiness.Report, id string, status readiness.Status) {
	t.Helper()
	if c := checkOf(t, report, id); c.Status != status {
		t.Fatalf("%s = %s (%s; %v), want %s", id, c.Status, c.Reason, c.Findings, status)
	}
}

func writeJSON(t *testing.T, path string, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}

// manifestWithout writes a copy of the tracked manifest without the given image paths.
func manifestWithout(t *testing.T, paths ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), "assets", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	drop := map[string]bool{}
	for _, path := range paths {
		drop[path] = true
	}
	var kept []any
	for _, entry := range manifest["entries"].([]any) {
		if !drop[entry.(map[string]any)["path"].(string)] {
			kept = append(kept, entry)
		}
	}
	if len(kept) != len(manifest["entries"].([]any))-len(paths) {
		t.Fatalf("the manifest does not list every path to drop: %v", paths)
	}
	manifest["entries"] = kept
	return writeJSON(t, filepath.Join(t.TempDir(), "manifest.json"), manifest)
}

// manifestPaths lists the tracked manifest's image paths.
func manifestPaths(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), "assets", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

// passingEvidence writes verify results, Golden coverage and a History summary as a complete passing run would.
func passingEvidence(t *testing.T, in *readiness.Inputs) {
	t.Helper()
	dir := t.TempDir()
	steps := []map[string]string{}
	for _, name := range []string{"gofmt", "go vet (Public)", "go test (Public)", "go vet (Development)", "go test (Development)", "desktop embedded images",
		"master: embedded bytes + profile lookup parity", "replay: public output audit + statistics equality", "replay: effect and skill semantics", "replay: combat history fidelity"} {
		steps = append(steps, map[string]string{"name": name, "state": "PASS", "detail": ""})
	}
	in.VerifyResults = writeJSON(t, filepath.Join(dir, "verify-results.json"), map[string]any{"commit": strings.Repeat("0", 40), "dirty": false, "packages": []string{"./tests/readiness"}, "results": steps})
	in.GoldenCoverage = writeJSON(t, filepath.Join(dir, "golden-coverage.json"), map[string]any{"gameVersion": "3.8", "captures": []any{
		map[string]any{"name": "LIVE_20261113_224240", "sha256": "21f42752b3d03855febe0649389c1f9755e214a6ed51a5e6decc2d97a7036b21", "facts": map[string]int{"effects": 6, "completeness": 18}, "passed": true},
	}, "absent": []string{}})
	in.HistorySummary = writeJSON(t, filepath.Join(dir, "history-summary.json"), historySummary(nil, nil))
}

// historySummary writes the History replay summary of the corpus capture as a complete passing run reports it (18 encounters,
// 278 enemy instances, every one named), with the given unnamed enemies and scenes.
func historySummary(unidentified, unresolved []map[string]any) map[string]any {
	if unidentified == nil {
		unidentified = []map[string]any{}
	}
	if unresolved == nil {
		unresolved = []map[string]any{}
	}
	return map[string]any{"captures": 1, "listedEncounters": 18, "versionedEncounters": 18, "identification": map[string]any{
		"enemyInstances": 278, "enemiesUnidentified": len(unidentified), "mainEnemies": 18, "scenesKnown": 18, "unidentified": unidentified, "unresolvedScenes": unresolved}}
}

func TestCurrentRepositoryHasNoStaticGap(t *testing.T) {
	report := run(t, baseInputs(t))
	for _, c := range report.Checks {
		if c.Kind == readiness.KindStatic && c.Status == readiness.StatusFail {
			t.Errorf("%s FAIL: %v", c.ID, c.Findings)
		}
	}
	for _, id := range []string{"characters.art", "characters.skill-icons", "effects.hud-classification", "effects.hud-icons", "content.guide-categories", "master.relations"} {
		c := checkOf(t, report, id)
		if c.Status != readiness.StatusPass || c.Coverage == nil || c.Coverage.Total == 0 || c.Coverage.Passed != c.Coverage.Total {
			t.Errorf("%s = %s %+v, want a full PASS", id, c.Status, c.Coverage)
		}
	}
	if len(report.Domains) != len(readiness.Domains) {
		t.Errorf("the report lists %d domains, want %d", len(report.Domains), len(readiness.Domains))
	}
	// Without research inputs and verify evidence the version is never STATIC_READY, and those checks say why.
	if report.StaticReady {
		t.Error("a run without research inputs or evidence reported STATIC_READY")
	}
	wantStatus(t, report, "protocol.schema-keys", readiness.StatusUnverified)
	wantStatus(t, report, "verification.live-acceptance", readiness.StatusUnverified)
	wantStatus(t, report, "verification.client-impact", readiness.StatusUnverified)
}

// A complete passing run is STATIC_READY, and only the owner's Live record makes it LIVE_VERIFIED: the two states never
// stand in for each other. A stand-in research extension passes the research checks.
func TestStaticReadyAndLiveVerifiedAreSeparate(t *testing.T) {
	in := baseInputs(t)
	in.Locales = []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}
	in.Research = []readiness.ResearchExtension{passingResearch()}
	passingEvidence(t, &in)
	report := run(t, in)
	if !report.StaticReady || report.LiveVerified {
		t.Fatalf("STATIC_READY %v, LIVE_VERIFIED %v; blockers %v", report.StaticReady, report.LiveVerified, report.Blockers)
	}
	wantStatus(t, report, "verification.live-acceptance", readiness.StatusUnverified)

	in.LiveAcceptance = writeJSON(t, filepath.Join(t.TempDir(), "live.json"), map[string]any{"gameVersion": "3.8", "clientVersion": "Global 3.8.9", "buildNumber": 33,
		"executableSha256": strings.Repeat("a", 64), "date": "2026-11-15", "operator": "owner", "result": "PASS"})
	if report := run(t, in); !report.StaticReady || !report.LiveVerified {
		t.Fatalf("with a passing Live record: STATIC_READY %v, LIVE_VERIFIED %v", report.StaticReady, report.LiveVerified)
	}
	// A Live pass of another game version proves nothing for this one.
	in.LiveAcceptance = writeJSON(t, filepath.Join(t.TempDir(), "live.json"), map[string]any{"gameVersion": "3.7", "buildNumber": 20, "executableSha256": strings.Repeat("b", 64), "result": "PASS"})
	report = run(t, in)
	if report.LiveVerified || !report.StaticReady {
		t.Fatalf("with another version's Live record: STATIC_READY %v, LIVE_VERIFIED %v", report.StaticReady, report.LiveVerified)
	}
	wantStatus(t, report, "verification.live-acceptance", readiness.StatusFail)
}

// Failure injection 1: the new role's art and skill icons are missing from the pack (Global 3.8, role 1512, Build 28).
func TestGateFailsMissingNewRoleImages(t *testing.T) {
	in := baseInputs(t)
	in.ManifestPath = manifestWithout(t, "game/characters/1512/avatar.webp", "game/characters/1512/portrait.webp", "game/skills/by-config/1006301/icon.webp")
	report := run(t, in)
	wantFail(t, report, "characters.art", "role 1512 has no avatar", "role 1512 has no portrait")
	wantFail(t, report, "characters.skill-icons", "role 1512 UI skill 1006301 has no icon")
	// The trial versions the owner checks a new role with resolve to the role and show its missing art too.
	wantFail(t, report, "characters.trial-parent", "its parent 1512 has no avatar")
	// The image files are still on disk without a manifest entry.
	wantFail(t, report, "assets.manifest-integrity", "game/characters/1512/avatar.webp is in the pack directory but not in the manifest")
}

// Failure injection 2: the client HUD draws a status the product gives no displayable classification.
func TestGateFailsHUDStatusWithoutDisplayableClass(t *testing.T) {
	in := baseInputs(t)
	in.Classify = func(masters *capture.MasterData, id uint64) capture.EffectDefinition {
		definition := masters.EffectDefinition(id)
		if id == 10041000 {
			definition.Polarity = capture.EffectPolarityUnclassified
		}
		if id == 10061000 {
			definition.HUDIcon = false
		}
		return definition
	}
	report := run(t, in)
	wantFail(t, report, "effects.hud-classification", "effect 10041000", "effect 10061000")
}

// Failure injection 3: statuses the HUD draws without an arrow are dropped as unresolved (the Build 31 defect with Lily's
// 1512310003, 1512800104, 1512999015 and 2002205502).
func TestGateFailsDroppedNeutralHUDStatuses(t *testing.T) {
	in := baseInputs(t)
	in.Classify = func(masters *capture.MasterData, id uint64) capture.EffectDefinition {
		definition := masters.EffectDefinition(id)
		if definition.Polarity == capture.EffectPolarityNeutral {
			definition.Polarity = capture.EffectPolarityUnclassified
		}
		return definition
	}
	report := run(t, in)
	wantFail(t, report, "effects.hud-classification", "effect 1512310003", "effect 1512800104", "effect 1512999015", "effect 2002205502")
}

// Failure injection 4 and 8: a new game mode whose category the Master does not name and the resolver cannot place, and a
// trial role whose parent the Master no longer has, in an isolated copy of the Master.
func TestGateFailsNewGameModeAndBrokenMasterRelation(t *testing.T) {
	root := repositoryRoot(t)
	fixture := filepath.Join(t.TempDir(), "master_data.db")
	copyFile(t, filepath.Join(root, "data", "master_data.db"), fixture)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(fixture))
	if err != nil {
		t.Fatal(err)
	}
	var free string
	if err := db.QueryRow(`SELECT id FROM instancedungeon WHERE id NOT IN (SELECT sub_dungeon_id FROM dungeondetection WHERE sub_dungeon_id IS NOT NULL)
		AND id NOT IN (SELECT dungeon_id FROM silentareadetection WHERE dungeon_id IS NOT NULL) AND id NOT IN (SELECT instance_dungeon_id FROM instancedungeonentrance_instance_ref)
		AND id NOT IN (SELECT inst_id FROM slashtowerstageinfo) AND id NOT IN (SELECT instance_dungeon_id FROM slashandtowercfg_instance_ref) ORDER BY CAST(id AS INTEGER) LIMIT 1`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	exec := func(statement string) {
		t.Helper()
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	// A new Adventure Guide category whose only row names an instance through a relation the resolver does not read, and
	// whose name the Master lacks: the content of a new mode reached nowhere.
	exec(`INSERT INTO dungeondetection (id, dungeon_id, secondary, sub_dungeon_id, jump_with_sub_dungeon_id, instance_dungeon_entrance_id) VALUES ('99990001', NULL, '99', '` + free + `', 0, NULL)`)
	report := run(t, readiness.Inputs{Root: root, MasterPath: fixture, Locales: []string{"zh-TW"}})
	if loads := checkOf(t, report, "master.loads"); loads.Status == readiness.StatusFail {
		t.Fatalf("the product loader refused the fixture: %v", loads.Findings)
	}
	wantFail(t, report, "master.relations", "dungeondetection.secondary -> secondaryguidedata: 1 rows")
	wantFail(t, report, "content.guide-categories", "guide category 99 has no zh-TW name")
	wantFail(t, report, "content.instance-modes", "guide category 99 reaches instances only it names (instance "+free+")")

	// A trial role whose parent the Master no longer has: the relation fails, and the product loader refuses the Master.
	exec(`INSERT INTO trialroleinfo (id, group_id, type, parent_role_id) VALUES ('99990002', '0', 1, '9999')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report = run(t, readiness.Inputs{Root: root, MasterPath: fixture, Locales: []string{"zh-TW"}})
	wantFail(t, report, "master.relations", "trialroleinfo.parent_role_id -> roleinfo: 1 rows")
	wantFail(t, report, "master.loads", "TrialRoleInfo Id 99990002 references missing RoleInfo Id 9999")
}

// Failure injection 5: History would keep recording the previous game version: a version constant of its own while the
// protocol moved to 3.9, an image pack still of 3.8, and a replay whose stored encounters do not carry the decoded version.
func TestGateFailsStaleHistoryGameVersion(t *testing.T) {
	root := repositoryRoot(t)
	tree := t.TempDir()
	numbers, err := os.ReadFile(filepath.Join(root, "internal", "capture", "protocol_numbers.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := regexp.MustCompile(`const currentGameVersion = "[^"]*"`).ReplaceAllString(string(numbers), `const currentGameVersion = "3.9"`)
	text = regexp.MustCompile(`const currentProtocolClient = "[^"]*"`).ReplaceAllString(text, `const currentProtocolClient = "Global 3.9.0"`)
	if err := os.MkdirAll(filepath.Join(tree, "internal", "capture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "internal", "capture", "protocol_numbers.go"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(root, "tests", "capture", "protocol_numbers_test.go"), filepath.Join(tree, "tests", "capture", "protocol_numbers_test.go"))
	copyFile(t, filepath.Join(root, "tests", "capture", "golden_registry.json"), filepath.Join(tree, "tests", "capture", "golden_registry.json"))
	stale := "package history\n\n// recordedGameVersion is what new encounters are stored with.\nconst recordedGameVersion = \"3.8\"\n"
	if err := os.MkdirAll(filepath.Join(tree, "internal", "history"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "internal", "history", "version.go"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	in := readiness.Inputs{Root: tree, MasterPath: filepath.Join(root, "data", "master_data.db"), ManifestPath: filepath.Join(root, "assets", "manifest.json"),
		AssetRoot: filepath.Join(root, "assets"), Product: sharedProduct(t), Locales: []string{}}
	in.HistorySummary = writeJSON(t, filepath.Join(t.TempDir(), "history-summary.json"), map[string]any{"captures": 1, "listedEncounters": 18, "versionedEncounters": 0})
	report := run(t, in)
	wantFail(t, report, "runtime.history-version-source", "internal/history/version.go:4 recordedGameVersion holds the game version literal \"3.8\"")
	wantFail(t, report, "assets.manifest-version", "the decoder reads game version 3.9")
	wantFail(t, report, "runtime.history-version-replay", "0 of 18 stored encounters carry game version 3.9")
	// The Golden registry still describes the previous version.
	wantFail(t, report, "verification.golden-coverage", "the Golden registry is for game version \"3.8\", the decoder reads \"3.9\"")
}

// Failure injection 6: the protocol was renewed but a retired translation layer and a stray wire number stayed in the
// product.
func TestGateFailsRetiredProtocolLayer(t *testing.T) {
	root := repositoryRoot(t)
	tree := t.TempDir()
	copyFile(t, filepath.Join(root, "internal", "capture", "protocol_numbers.go"), filepath.Join(tree, "internal", "capture", "protocol_numbers.go"))
	copyFile(t, filepath.Join(root, "tests", "capture", "protocol_numbers_test.go"), filepath.Join(tree, "tests", "capture", "protocol_numbers_test.go"))
	for name, body := range map[string]string{
		"protocol_translation.go":       "package capture\n\n// translateFrame re-encodes a current frame into the previous version's numbers.\nfunc translateFrame() {}\n",
		"protocol_translation_3.8.json": "{}\n",
		"damage_numbers.go":             "package capture\n\nconst (\n\tcmdDamageExecuteRequestOld = 15361\n)\n",
	} {
		if err := os.WriteFile(filepath.Join(tree, "internal", "capture", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := readiness.Inputs{Root: tree, MasterPath: filepath.Join(root, "data", "master_data.db"), ManifestPath: filepath.Join(root, "assets", "manifest.json"),
		AssetRoot: filepath.Join(root, "assets"), Product: sharedProduct(t), Locales: []string{}}
	report := run(t, in)
	wantFail(t, report, "protocol.single-native-decoder", "internal/capture/protocol_translation.go: a retired protocol layer", "internal/capture/protocol_translation_3.8.json: a retired protocol layer", "internal/capture/damage_numbers.go:4 assigns a wire number outside the generated file")

	// The tracked tree passes the check: the injection, not the fixture, made it fail.
	wantStatus(t, run(t, baseInputs(t)), "protocol.single-native-decoder", readiness.StatusPass)
}

// Failure injection 7: a required Golden capture did not run, ran without facts, failed, or ran from another file.
func TestGateFailsUnexecutedRequiredGolden(t *testing.T) {
	const capture = "LIVE_20261113_224240"
	const pinned = "21f42752b3d03855febe0649389c1f9755e214a6ed51a5e6decc2d97a7036b21"
	cases := []struct {
		name     string
		captures []any
		want     string
	}{
		{"absent", []any{map[string]any{"name": "LIVE_20261112_110155", "sha256": "", "facts": map[string]int{"effects": 2}, "passed": true}}, "required Golden capture " + capture + " was not executed"},
		{"no facts", []any{map[string]any{"name": capture, "sha256": pinned, "facts": map[string]int{}, "passed": true}}, "ran without checking any Golden fact"},
		{"failed", []any{map[string]any{"name": capture, "sha256": pinned, "facts": map[string]int{"completeness": 18}, "passed": false}}, "failed its Golden facts"},
		{"other file", []any{map[string]any{"name": capture, "sha256": strings.Repeat("c", 64), "facts": map[string]int{"completeness": 18}, "passed": true}}, "the registry pins 21f42752b3d0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInputs(t)
			in.GoldenCoverage = writeJSON(t, filepath.Join(t.TempDir(), "golden-coverage.json"), map[string]any{"gameVersion": "3.8", "captures": tc.captures, "absent": []string{}})
			wantFail(t, run(t, in), "verification.golden-coverage", tc.want)
		})
	}
	// A verify run that skipped the semantic regression is no Golden coverage either.
	in := baseInputs(t)
	passingEvidence(t, &in)
	var results map[string]any
	data, _ := os.ReadFile(in.VerifyResults)
	_ = json.Unmarshal(data, &results)
	for _, step := range results["results"].([]any) {
		if step.(map[string]any)["name"] == "replay: effect and skill semantics" {
			step.(map[string]any)["state"] = "NOT RUN"
		}
	}
	in.VerifyResults = writeJSON(t, in.VerifyResults, results)
	report := run(t, in)
	wantFail(t, report, "verification.golden-coverage", "replay: effect and skill semantics")
	wantStatus(t, report, "verification.regression-suite", readiness.StatusUnverified)
}

// A Public verify run (scripts/verify.ps1 -Mode Public) reports the steps that need private material SKIP. A skipped step
// is no pass: the gate's evidence checks stay open and the version is not STATIC_READY on such a run.
func TestGateTakesNoPublicVerifyRunAsPassing(t *testing.T) {
	in := baseInputs(t)
	passingEvidence(t, &in)
	var results map[string]any
	data, err := os.ReadFile(in.VerifyResults)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	results["mode"] = "Public"
	for _, step := range results["results"].([]any) {
		switch step.(map[string]any)["name"] {
		case "desktop embedded images", "master: embedded bytes + profile lookup parity", "replay: public output audit + statistics equality",
			"replay: effect and skill semantics", "replay: combat history fidelity":
			step.(map[string]any)["state"] = "SKIP"
			step.(map[string]any)["detail"] = "Public mode: needs the owner-local key"
		}
	}
	in.VerifyResults = writeJSON(t, in.VerifyResults, results)
	report := run(t, in)
	for _, id := range []string{"verification.regression-suite", "runtime.profile-parity", "assets.embedded-parity"} {
		wantStatus(t, report, id, readiness.StatusUnverified)
	}
	wantFail(t, report, "verification.golden-coverage", "replay: effect and skill semantics")
	if report.StaticReady {
		t.Fatal("a Public verify run made the version STATIC_READY")
	}
}

// Failure injection: a HUD status's icon is missing from the pack.
func TestGateFailsMissingHUDIcon(t *testing.T) {
	var icon string
	for _, path := range manifestPaths(t) {
		if strings.HasPrefix(path, "game/buffs/") {
			icon = path
			break
		}
	}
	if icon == "" {
		t.Fatal("the manifest lists no buff icon")
	}
	in := baseInputs(t)
	in.ManifestPath = manifestWithout(t, icon)
	report := run(t, in)
	c := checkOf(t, report, "effects.hud-icons")
	if c.Status != readiness.StatusFail {
		t.Fatalf("effects.hud-icons = %s with %s removed from the manifest, want FAIL", c.Status, icon)
	}
}

// Failure injection: the negative-status role credit lacks its static inputs, in an isolated copy of the Master: a new
// negative-status DamageId no status Buff executes, a status Buff whose period the Master lost, and an InvokePeriod naming a
// status without a period, which the credit has no rule for.
func TestGateFailsNegativeStatusSettlementGaps(t *testing.T) {
	root := repositoryRoot(t)
	clean := run(t, readiness.Inputs{Root: root, MasterPath: filepath.Join(root, "data", "master_data.db"), Locales: []string{"zh-TW"}})
	if c := checkOf(t, clean, "combat.negative-status-settlement"); c.Status != readiness.StatusPass {
		t.Fatalf("combat.negative-status-settlement on the tracked Master = %s: %v", c.Status, c.Findings)
	}
	fixture := filepath.Join(t.TempDir(), "master_data.db")
	copyFile(t, filepath.Join(root, "data", "master_data.db"), fixture)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(fixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO damage (id, calculate_type, formula_type, type) VALUES ('1008', 0, 9, 10)`,
		`UPDATE buff SET period = NULL WHERE id = '10041000'`,
		`INSERT INTO buff (id, extra_effect_id, period) VALUES ('99990067', '67', 0)`,
		`INSERT INTO buff_extra_effect_parameters (owner_id, ordinal, value) VALUES ('99990067', 0, '10061000')`,
		// Fusion Burst spreads on overflow; without a stack limit its overflow cannot be told.
		`UPDATE buff SET stack_limit_count = 0 WHERE id = '10021000'`,
		// A status Buff without its duration policy cannot be read as instant or not.
		`UPDATE buff SET duration_policy = NULL WHERE id = '150721001'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report := run(t, readiness.Inputs{Root: root, MasterPath: fixture, Locales: []string{"zh-TW"}})
	if loads := checkOf(t, report, "master.loads"); loads.Status == readiness.StatusFail {
		t.Fatalf("the product loader refused the fixture: %v", loads.Findings)
	}
	wantFail(t, report, "combat.negative-status-settlement",
		"negative-status DamageId 1008 has no executing status Buff",
		"status Buff 10041000 of DamageId 1001 lacks a settlement column",
		"InvokePeriod 99990067 names status Buff 10061000, which has no period",
		"status Buff 10021000 of DamageId 1004 spreads on overflow but has no positive stack limit",
		"status Buff 150721001 of DamageId 1005 lacks a settlement column")
}

// Failure injection: the History replay of the corpus stored an enemy without identity, an enemy without a name in one
// locale and an encounter whose scene has no kind of play; a recorded limit with its reason excuses only what it names, and a
// summary from a collector without identification counts proves nothing.
func TestGateFailsUnnamedHistoryContent(t *testing.T) {
	root := repositoryRoot(t)
	in := baseInputs(t)
	in.HistorySummary = writeJSON(t, filepath.Join(t.TempDir(), "history-summary.json"), historySummary(
		[]map[string]any{
			{"capture": "LIVE_20261113_224240", "encounter": "1-2", "ordinal": 0, "missingLocales": []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}},
			{"capture": "LIVE_20261113_224240", "encounter": "1-3", "ordinal": 1, "templateId": 24000013, "missingLocales": []string{"ko-KR"}},
		},
		[]map[string]any{{"capture": "LIVE_20261113_224240", "encounter": "1-14", "instanceId": 9102, "modeUnresolved": []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}}},
	))
	report := run(t, in)
	wantFail(t, report, "content.history-identification",
		"LIVE_20261113_224240 encounter 1-2 enemy 0 has no MonsterInfo or TemplateConfig identity",
		"LIVE_20261113_224240 encounter 1-3 enemy 1 (monster 0, template 24000013) has no name in ko-KR",
		"LIVE_20261113_224240 encounter 1-14: scene 9102 has no kind of play in zh-TW")

	// The same summary with the scene's limit recorded: the enemies still fail, the scene does not.
	registry := filepath.Join(t.TempDir(), "golden_registry.json")
	data, err := os.ReadFile(filepath.Join(root, "tests", "capture", "golden_registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, entry := range document["captures"].([]any) {
		capture := entry.(map[string]any)
		if capture["name"] == "LIVE_20261113_224240" {
			capture["identificationLimits"] = []any{map[string]any{"kind": "mode", "scene": 9102, "reason": "not an Adventure Guide category (fixture)"}}
		}
	}
	in.GoldenRegistry = writeJSON(t, registry, document)
	report = run(t, in)
	c := checkOf(t, report, "content.history-identification")
	findings := strings.Join(c.Findings, "\n")
	if c.Status != readiness.StatusFail || strings.Contains(findings, "scene 9102") || !strings.Contains(findings, "encounter 1-2 enemy 0") {
		t.Fatalf("a recorded mode limit must excuse only its scene: %s %v", c.Status, c.Findings)
	}

	in = baseInputs(t)
	in.HistorySummary = writeJSON(t, filepath.Join(t.TempDir(), "history-summary.json"), map[string]any{"captures": 1, "listedEncounters": 18, "versionedEncounters": 18})
	wantStatus(t, run(t, in), "content.history-identification", readiness.StatusUnverified)
}
