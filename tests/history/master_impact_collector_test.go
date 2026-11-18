package history

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// TestHistoryMasterImpactCollector is a collector, not a regular test. Stored history keeps raw IDs and is named at view
// time with the Master the build embeds, so a Master update can change what an old encounter shows. With
// WWLOGS_HISTORY_MASTER_FIXTURE naming a history fixture directory (combat-history.db with its key record), and
// WWLOGS_HISTORY_MASTER_OLD and WWLOGS_HISTORY_MASTER_NEW naming two Master files, it reads every finished encounter on a
// fresh copy and renders every History view the desktop shows (the projected encounter, analysis, effect lanes, hit
// timeline, event pages, enemy and contributor names in every locale) once with each Master, and writes to
// WWLOGS_HISTORY_MASTER_OUT which views differ and how, with what the History names under each Master (identification:
// enemies, main enemies, stages and modes). The fixture is never changed. With WWLOGS_HISTORY_MASTER_REAL_INPUTS=1 it opens
// the copy with the real inputs of a build (this computer's SMBIOS table and the protocol key the build embeds), so a copy
// of the owner's history can be compared; that needs a test binary from scripts/embed-key.go -test-binary ./internal/history.
func TestHistoryMasterImpactCollector(t *testing.T) {
	fixture, oldPath, newPath, out := os.Getenv("WWLOGS_HISTORY_MASTER_FIXTURE"), os.Getenv("WWLOGS_HISTORY_MASTER_OLD"), os.Getenv("WWLOGS_HISTORY_MASTER_NEW"), os.Getenv("WWLOGS_HISTORY_MASTER_OUT")
	if fixture == "" || oldPath == "" || newPath == "" || out == "" {
		t.Skip("set WWLOGS_HISTORY_MASTER_FIXTURE, _OLD, _NEW and _OUT to compare stored history under two Masters")
	}
	var inputs KeyInputs
	if os.Getenv("WWLOGS_HISTORY_MASTER_REAL_INPUTS") == "1" {
		material, err := capture.ProtocolKeyMaterial()
		if err != nil {
			t.Skip("this test binary carries no embedded protocol key")
		}
		clear(material)
		inputs = KeyInputs{ProtocolKey: capture.ProtocolKeyMaterial}
	}
	store, err := Open(Options{Path: copyFixture(t, fixture), Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master", KeyInputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	var ids []int64
	rows, err := store.db.QueryContext(ctx, "SELECT id FROM encounter WHERE state IN "+listedStates+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	locales := []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}
	render := func(masterPath string) map[string]any {
		views := map[string]any{}
		for _, locale := range locales {
			masters, err := capture.LoadMasterData(masterPath, locale)
			if err != nil {
				t.Fatal(err)
			}
			names, err := capture.OpenDisplayResolver(masterPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				prefix := fmt.Sprintf("%s/encounter-%d/", locale, id)
				detail, err := store.Encounter(ctx, id, time.UTC)
				if err != nil {
					t.Fatal(err)
				}
				if detail.Snapshot != nil {
					view := masters.ProjectEncounter(*detail.Snapshot)
					views[prefix+"encounter"] = view
					var named []string
					for _, role := range view.Roles {
						named = append(named, displayName(t, ctx, names, "role", uint64(role.RoleID), locale))
						for _, skill := range role.Skills {
							named = append(named, fmt.Sprintf("%d=%s/%s", skill.SkillID, skill.Name, skill.NameStatus))
						}
					}
					views[prefix+"names"] = named
				}
				var enemies []string
				for _, enemy := range detail.Enemies {
					enemies = append(enemies, displayName(t, ctx, names, "monster", enemy.MonsterID, locale)+"|"+displayName(t, ctx, names, "template", enemy.TemplateID, locale))
				}
				views[prefix+"enemies"] = enemies
				query := AnalysisQuery{Enemy: -1, Effects: "all"}
				if analysis, err := store.Analysis(ctx, id, query, masters); err == nil {
					analysis.Snapshot = nil
					views[prefix+"analysis"] = analysis
				} else {
					views[prefix+"analysis"] = "error: " + err.Error()
				}
				if lanes, err := store.Effects(ctx, id, query, masters); err == nil {
					views[prefix+"effects"] = lanes
				} else {
					views[prefix+"effects"] = "error: " + err.Error()
				}
				if hits, err := store.Hits(ctx, id, query, masters); err == nil {
					views[prefix+"hits"] = hits
				} else {
					views[prefix+"hits"] = "error: " + err.Error()
				}
				var pages []any
				for offset := 0; offset < 1_000_000; offset += 500 {
					page, err := store.Events(ctx, id, EventQuery{AnalysisQuery: query, Offset: offset, Limit: 500}, masters)
					if err != nil {
						pages = append(pages, "error: "+err.Error())
						break
					}
					pages = append(pages, page)
					if len(page.Rows) < 500 {
						break
					}
				}
				views[prefix+"events"] = pages
			}
			names.Close()
			masters.Close()
		}
		return views
	}
	before, after := render(oldPath), render(newPath)
	type difference struct {
		View    string   `json:"view"`
		Paths   int      `json:"paths"`
		Samples []string `json:"samples"`
	}
	var differences []difference
	identical := 0
	for _, key := range sortedKeys(before) {
		a, b := normalizeJSON(t, before[key]), normalizeJSON(t, after[key])
		if reflect.DeepEqual(a, b) {
			identical++
			continue
		}
		var paths []string
		diffJSON(key, a, b, &paths)
		d := difference{View: key, Paths: len(paths)}
		if len(paths) > 8 {
			paths = paths[:8]
		}
		d.Samples = paths
		differences = append(differences, d)
	}
	// What the History names under each Master, with the desktop History view's rules (replay_test.go identify).
	identified := func(masterPath string) identification {
		names, err := capture.OpenDisplayResolver(masterPath)
		if err != nil {
			t.Fatal(err)
		}
		defer names.Close()
		var counted identification
		for _, id := range ids {
			detail, err := store.Encounter(ctx, id, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			identify(t, names, "history", fmt.Sprintf("encounter-%d", id), detail, &counted)
		}
		return counted
	}
	result := map[string]any{"encounters": len(ids), "locales": locales, "views": len(before), "identical": identical, "differences": differences,
		"identification": map[string]identification{"old": identified(oldPath), "new": identified(newPath)}}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d encounters, %d views, %d identical, %d differ (%s)", len(ids), len(before), identical, len(differences), filepath.Base(out))
}

func displayName(t *testing.T, ctx context.Context, names *capture.DisplayResolver, kind string, id uint64, locale string) string {
	if id == 0 {
		return ""
	}
	name, err := names.Resolve(ctx, kind, id, locale)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s:%d=%s/%s", kind, id, name.Name, name.Status)
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeJSON(t *testing.T, value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// diffJSON lists the paths where two decoded JSON values differ, with both values.
func diffJSON(path string, a, b any, out *[]string) {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: %v -> %v", path, short(a), short(b)))
			return
		}
		keys := map[string]bool{}
		for key := range av {
			keys[key] = true
		}
		for key := range bv {
			keys[key] = true
		}
		for _, key := range sortedKeys(map[string]any(toAny(keys))) {
			diffJSON(path+"."+key, av[key], bv[key], out)
		}
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			*out = append(*out, fmt.Sprintf("%s: %v -> %v", path, short(a), short(b)))
			return
		}
		for i := range av {
			diffJSON(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i], out)
		}
	default:
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, fmt.Sprintf("%s: %v -> %v", path, short(a), short(b)))
		}
	}
}

func toAny(keys map[string]bool) map[string]any {
	out := make(map[string]any, len(keys))
	for key := range keys {
		out[key] = true
	}
	return out
}

func short(value any) string {
	text := fmt.Sprint(value)
	if len(text) > 120 {
		text = text[:120] + "..."
	}
	return strings.ReplaceAll(text, "\n", " ")
}

// copyFixture copies every file of a history fixture except its lock file into a new directory, so the fixture itself is never
// opened; it returns the history path there.
func copyFixture(t *testing.T, from string) string {
	t.Helper()
	to := filepath.Join(t.TempDir(), "history")
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(from, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(to, "combat-history.db")
}
