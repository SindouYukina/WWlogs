package readiness_test

// Tests of the version readiness gate. The current repository must have no static gap, and every kind of gap the Global
// 3.8 adaptation actually had (or could have) must make the gate fail, on isolated defective copies: a manifest without
// the new role's art, a product classification that drops a HUD status, a new game mode the resolver cannot place, a
// History that keeps an old game version, a retired protocol layer, stale wire numbers, a required Golden capture that did
// not run, and a Master lookup that no longer resolves. Nothing here touches the tracked Master, manifest or any user data.

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

var reConstant = regexp.MustCompile(`(?m)^\s*(cmd|field)([A-Za-z0-9_]+)\s*=\s*(\d+)\s*//\s*(\S+)\s*$`)
var reHash = regexp.MustCompile(`\(SHA-256 ([0-9a-f]{64})\)`)

// schemaFromConstants writes a client schema (the protojs_schema.py layout) that agrees with the generated numbers, with
// one command optionally renumbered, as a stand-in for the private client schema.
func schemaFromConstants(t *testing.T, renumber string) string {
	t.Helper()
	root := repositoryRoot(t)
	var text string
	for _, file := range []string{"internal/capture/protocol_numbers.go", "tests/capture/protocol_numbers_test.go"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		text += strings.ReplaceAll(string(data), "\r\n", "\n") + "\n"
	}
	hashes := reHash.FindAllStringSubmatch(text, 2)
	if len(hashes) < 2 {
		t.Fatal("the generated numbers carry no Protocol.js and NetDefine.js hashes")
	}
	messages := map[string]map[string]map[string]string{}
	commands := map[string]string{}
	for _, m := range reConstant.FindAllStringSubmatch(text, -1) {
		number, key := m[3], m[4]
		if m[1] == "cmd" {
			if m[1]+m[2] == renumber {
				value, _ := strconv.Atoi(number)
				number = strconv.Itoa(value + 1)
			}
			commands[number] = key
			continue
		}
		dot := strings.LastIndex(key, ".")
		message, property := key[:dot], key[dot+1:]
		if messages[message] == nil {
			messages[message] = map[string]map[string]string{}
		}
		messages[message][number] = map[string]string{"name": property, "kind": "int32"}
	}
	schema := map[string]any{"protocolSha256": hashes[0][1], "netDefineSha256": hashes[1][1], "messages": messages, "commands": commands}
	return writeJSON(t, filepath.Join(t.TempDir(), "schema.json"), schema)
}

// clientInventory writes a client reference inventory of the given paths for the tracked manifest's resource.
func clientInventory(t *testing.T, paths []string) string {
	t.Helper()
	return writeJSON(t, filepath.Join(t.TempDir(), "client-references.json"), map[string]any{"client": "fixture", "resource": "3.8.9", "paths": paths, "exclusions": []any{}})
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
}

// A complete passing run is STATIC_READY, and only the owner's Live record makes it LIVE_VERIFIED: the two states never
// stand in for each other.
func TestStaticReadyAndLiveVerifiedAreSeparate(t *testing.T) {
	in := baseInputs(t)
	in.Locales = []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}
	in.ProtocolSchema = schemaFromConstants(t, "")
	in.ClientReferences = clientInventory(t, manifestPaths(t))
	in.ClientContent = contentInventory(t, nil)
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

// Failure injection 6: the protocol was renewed but a retired translation layer stayed in the product, and the committed
// numbers do not match the current client schema.
func TestGateFailsRetiredProtocolLayerAndStaleNumbers(t *testing.T) {
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
	in.ProtocolSchema = schemaFromConstants(t, "cmdDamageExecuteRequest")
	report := run(t, in)
	wantFail(t, report, "protocol.single-native-decoder", "internal/capture/protocol_translation.go: a retired protocol layer", "internal/capture/protocol_translation_3.8.json: a retired protocol layer", "internal/capture/damage_numbers.go:4 assigns a wire number outside the generated file")
	wantFail(t, report, "protocol.schema-keys", "cmdDamageExecuteRequest")

	// The same tree without the defects passes both checks: the injection, not the fixture, made them fail.
	clean := baseInputs(t)
	clean.ProtocolSchema = schemaFromConstants(t, "")
	report = run(t, clean)
	wantStatus(t, report, "protocol.single-native-decoder", readiness.StatusPass)
	wantStatus(t, report, "protocol.schema-keys", readiness.StatusPass)
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

// The client reference inventory catches an image the client references but the pack lacks, and one the pack keeps after
// the client stopped referencing it.
func TestGateFailsClientReferenceDrift(t *testing.T) {
	paths := manifestPaths(t)
	in := baseInputs(t)
	in.ClientReferences = clientInventory(t, append(paths[1:], "game/buffs/999999999/icon.webp"))
	report := run(t, in)
	wantFail(t, report, "assets.client-references", "game/buffs/999999999/icon.webp is referenced by the client but missing from the pack", paths[0]+" is in the pack but no longer referenced")
}

// contentInventory writes a client content inventory (research input clientContent) whose client side is a copy of the
// tracked Master, changed by mutate: the clean control of master.client-content, and every defect injected into it.
func contentInventory(t *testing.T, mutate func(inventory map[string]any)) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(repositoryRoot(t), "data", "master_data.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	column := func(query string) []string {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			out = append(out, value)
		}
		return out
	}
	tables := map[string]any{}
	for _, table := range []string{"roleinfo", "trialroleinfo", "buff", "gameplaycue", "skill", "secondaryguidedata", "dungeondetection", "silentareadetection", "instancedungeon"} {
		ids := column("SELECT CAST(id AS TEXT) FROM " + table)
		tables[table] = map[string]any{"sourceRows": len(ids), "decodedRows": len(ids), "ids": ids}
	}
	inventory := map[string]any{
		"client": "Global 3.8.9", "resource": "3.8.9", "tables": tables,
		"playableRoles": column("SELECT CAST(id AS TEXT) FROM roleinfo WHERE role_type = 1"),
		"hudBuffs": column(`SELECT DISTINCT b.owner_id FROM buff_gameplay_cue_ids b JOIN gameplaycue g ON g.id = CAST(CAST(b.value AS INTEGER) AS TEXT)
			WHERE g.path_asset_id IS NOT NULL AND g.path_asset_id <> '' AND g.cue_type IN (2, 14, 24, 37)`),
		"prefabEntities": prefabEntitiesOf(t, db),
		"exclusions":     []any{},
	}
	if mutate != nil {
		mutate(inventory)
	}
	return writeJSON(t, filepath.Join(t.TempDir(), "client-content.json"), inventory)
}

// prefabEntitiesOf lists the prefab entities of the Master's prefab relations as the inventory lists the client's: every
// template in ordinal order and every MonsterInfo of the entity.
func prefabEntitiesOf(t *testing.T, db *sql.DB) []map[string]any {
	t.Helper()
	entities := map[string]map[string]any{}
	var order []string
	rows, err := db.Query("SELECT prefab_id, entity_id, template_id FROM prefab_entity_template_ref ORDER BY CAST(prefab_id AS INTEGER), CAST(entity_id AS INTEGER), ordinal")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var prefab, entity, template string
		if err := rows.Scan(&prefab, &entity, &template); err != nil {
			t.Fatal(err)
		}
		key := prefab + "/" + entity
		if entities[key] == nil {
			entities[key] = map[string]any{"prefab": prefab, "entity": entity, "blueprint": "", "templates": []string{}, "monsters": []string{}}
			order = append(order, key)
		}
		entities[key]["templates"] = append(entities[key]["templates"].([]string), template)
	}
	rows.Close()
	rows, err = db.Query("SELECT prefab_id, entity_id, monster_id FROM prefab_entity_monster ORDER BY CAST(prefab_id AS INTEGER), CAST(entity_id AS INTEGER), CAST(monster_id AS INTEGER)")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var prefab, entity, monster string
		if err := rows.Scan(&prefab, &entity, &monster); err != nil {
			t.Fatal(err)
		}
		key := prefab + "/" + entity
		if entities[key] == nil {
			entities[key] = map[string]any{"prefab": prefab, "entity": entity, "blueprint": "", "templates": []string{}, "monsters": []string{}}
			order = append(order, key)
		}
		entities[key]["monsters"] = append(entities[key]["monsters"].([]string), monster)
	}
	rows.Close()
	out := make([]map[string]any, 0, len(order))
	for _, key := range order {
		out = append(out, entities[key])
	}
	return out
}

// addTableID adds an id to one table of a content inventory, as a row the client defines.
func addTableID(inventory map[string]any, table, id string) {
	entry := inventory["tables"].(map[string]any)[table].(map[string]any)
	entry["ids"] = append(entry["ids"].([]string), id)
	entry["sourceRows"] = entry["sourceRows"].(int) + 1
	entry["decodedRows"] = entry["decodedRows"].(int) + 1
}

// The clean control: an inventory of exactly the Master's client rows passes, with every row counted.
func TestGateClientContentMatchesTheMaster(t *testing.T) {
	in := baseInputs(t)
	in.ClientContent = contentInventory(t, nil)
	report := run(t, in)
	wantStatus(t, report, "master.client-content", readiness.StatusPass)
	if c := checkOf(t, report, "master.client-content"); c.Coverage == nil || c.Coverage.Total == 0 || c.Coverage.Passed != c.Coverage.Total {
		t.Fatalf("coverage %+v, want every client row", c.Coverage)
	}
}

// Failure injection: the client defines a new role, a new HUD status and new rows that the Master build dropped. The
// other Master checks walk the Master's own rows and cannot see them.
func TestGateFailsClientContentTheMasterLacks(t *testing.T) {
	in := baseInputs(t)
	in.ClientContent = contentInventory(t, func(inventory map[string]any) {
		addTableID(inventory, "roleinfo", "9999")
		inventory["playableRoles"] = append(inventory["playableRoles"].([]string), "9999")
		addTableID(inventory, "buff", "999999999")
		addTableID(inventory, "gameplaycue", "999999998")
		inventory["hudBuffs"] = append(inventory["hudBuffs"].([]string), "999999999")
		addTableID(inventory, "instancedungeon", "999997")
	})
	report := run(t, in)
	wantFail(t, report, "master.client-content", "roleinfo 9999 is in the client but not in the Master", "playable role 9999 of the client is not playable in the Master",
		"buff 999999999 is in the client but not in the Master", "the client HUD draws buff 999999999 but the Master gives it no HUD cue",
		"gameplaycue 999999998 is in the client but not in the Master", "instancedungeon 999997 is in the client but not in the Master")
	// The static checks that read the Master alone still pass: only the cross-layer check sees the dropped rows.
	wantStatus(t, report, "characters.art", readiness.StatusPass)
	wantStatus(t, report, "effects.hud-classification", readiness.StatusPass)
}

// Failure injection: the Master gives a buff a HUD cue the client no longer draws (a stale Master after an update).
func TestGateFailsClientContentStaleHUDRelation(t *testing.T) {
	in := baseInputs(t)
	in.ClientContent = contentInventory(t, func(inventory map[string]any) {
		hud := inventory["hudBuffs"].([]string)
		inventory["hudBuffs"] = hud[1:]
	})
	report := run(t, in)
	wantFail(t, report, "master.client-content", "a HUD cue the client does not draw")
}

// Failure injection: the inventory was extracted from another client than the decoder's, or its extraction is partial.
func TestGateFailsClientContentFromAnotherClientOrPartialExtraction(t *testing.T) {
	in := baseInputs(t)
	in.ClientContent = contentInventory(t, func(inventory map[string]any) { inventory["client"] = "Global 3.9.0" })
	wantFail(t, run(t, in), "master.client-content", `the inventory describes client "Global 3.9.0", the decoder reads "Global 3.8.9"`)

	in.ClientContent = contentInventory(t, func(inventory map[string]any) {
		entry := inventory["tables"].(map[string]any)["buff"].(map[string]any)
		entry["decodedRows"] = entry["decodedRows"].(int) - 3
	})
	wantFail(t, run(t, in), "master.client-content", "buff: 28009 of the client's 28012 rows decoded in the extraction snapshot")

	in.ClientContent = contentInventory(t, func(inventory map[string]any) {
		inventory["exclusions"] = []any{map[string]any{"table": "buff", "id": "1"}}
	})
	wantFail(t, run(t, in), "master.client-content", "the exclusion of buff 1 states no reason")
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

// The clean control of content.prefab-entities: a client whose prefabs are exactly the Master's relations passes, every
// entity counted.
func TestGatePrefabEntitiesMatchTheMaster(t *testing.T) {
	in := baseInputs(t)
	in.ClientContent = contentInventory(t, nil)
	report := run(t, in)
	wantStatus(t, report, "content.prefab-entities", readiness.StatusPass)
	if c := checkOf(t, report, "content.prefab-entities"); c.Coverage == nil || c.Coverage.Total < 5000 || c.Coverage.Passed != c.Coverage.Total {
		t.Fatalf("coverage %+v, want every prefab entity of the Global 3.8 client", c.Coverage)
	}
}

// Failure injection: the client spawns an enemy from a prefab entity the Master has no relation for (the Global 3.8 defect:
// Tower of Adversity enemies showed no main enemy), a prefab entity whose template the Master names differently, and a Master
// relation for an entity the client no longer defines.
func TestGateFailsPrefabEntityGaps(t *testing.T) {
	in := baseInputs(t)
	in.ClientContent = contentInventory(t, func(inventory map[string]any) {
		entities := inventory["prefabEntities"].([]map[string]any)
		for _, entity := range entities {
			if entity["prefab"] == "222850001" && entity["entity"] == "1850000" {
				entity["templates"] = []string{"24000014"}
			}
		}
		var kept []map[string]any
		for _, entity := range entities {
			if !(entity["prefab"] == "222700104" && entity["entity"] == "1700001") {
				kept = append(kept, entity)
			}
		}
		kept = append(kept, map[string]any{"prefab": "222850001", "entity": "1859999", "blueprint": "Monster_New", "templates": []string{"24000013"}, "monsters": []string{}})
		inventory["prefabEntities"] = kept
	})
	report := run(t, in)
	wantFail(t, report, "content.prefab-entities",
		"prefab 222850001 entity 1859999 (Monster_New): the Master has no relation, so the product leaves this enemy unidentified",
		"prefab 222850001 entity 1850000 (): the product names TemplateConfig [24000013], the client [24000014]",
		"the Master relates prefab entity 222700104/1700001, which the client does not define")
}

// Failure injection: a Master without the prefab relations, loaded by the product, leaves every prefab entity of the client
// unidentified; an inventory from before the prefab inventory keeps the check UNVERIFIED.
func TestGateFailsMasterWithoutPrefabRelations(t *testing.T) {
	root := repositoryRoot(t)
	inventory := contentInventory(t, nil)
	fixture := filepath.Join(t.TempDir(), "master_data.db")
	copyFile(t, filepath.Join(root, "data", "master_data.db"), fixture)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(fixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DROP TABLE prefab_entity_template_ref", "DROP TABLE prefab_entity_monster"} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report := run(t, readiness.Inputs{Root: root, MasterPath: fixture, Locales: []string{"zh-TW"}, ClientContent: inventory})
	// Findings are bounded; every failing entity is still a subject.
	wantFail(t, report, "content.prefab-entities", "the Master has no relation, so the product leaves this enemy unidentified", "prefab entity 222850001/1850000")
	if c := checkOf(t, report, "content.prefab-entities"); c.Coverage == nil || c.Coverage.Passed != 0 {
		t.Fatalf("coverage %+v, want no prefab entity identified", c.Coverage)
	}

	in := baseInputs(t)
	in.ClientContent = contentInventory(t, func(inventory map[string]any) { delete(inventory, "prefabEntities") })
	wantStatus(t, run(t, in), "content.prefab-entities", readiness.StatusUnverified)
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
