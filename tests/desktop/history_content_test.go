package desktop

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
)

// historyContentMaster is a Master with a guide category (5, entrance 89 offers instances 20016 and 30), a content system
// without a game name (wheel-tower: instance 107700001 in two contexts of two activities and one mode), one named by the
// same category (tower-of-adversity: 20016) and one named by another category (other: 30, which the guide relation
// places in category 5).
func historyContentMaster(t *testing.T) *capture.DisplayResolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.db")
	exec := func(statements ...string) {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	exec(
		"CREATE TABLE instancedungeon(id TEXT PRIMARY KEY, inst_type INTEGER)",
		"CREATE TABLE instancedungeon_locale(id TEXT NOT NULL, locale TEXT NOT NULL, name TEXT NOT NULL, PRIMARY KEY(id, locale))",
		"CREATE TABLE secondaryguidedata_locale(id TEXT NOT NULL, locale TEXT NOT NULL, text TEXT NOT NULL, PRIMARY KEY(id, locale))",
		"CREATE TABLE instancedungeonentrance_instance_ref(entrance_id TEXT NOT NULL, ordinal INTEGER NOT NULL, instance_dungeon_id TEXT NOT NULL, PRIMARY KEY(entrance_id, ordinal))",
		"CREATE TABLE dungeondetection(id TEXT PRIMARY KEY, dungeon_id TEXT, secondary TEXT, sub_dungeon_id TEXT, jump_with_sub_dungeon_id INTEGER NOT NULL DEFAULT 0, instance_dungeon_entrance_id TEXT)",
		"INSERT INTO instancedungeon VALUES ('20016', 2), ('30', 2), ('107700001', 2)",
		"INSERT INTO instancedungeon_locale VALUES ('20016', 'zh-TW', 'V1-1-4'), ('30', 'zh-TW', '另一個副本')",
		"INSERT INTO secondaryguidedata_locale VALUES ('5', 'zh-TW', '逆境深塔'), ('7', 'zh-TW', '另一種玩法')",
		"INSERT INTO instancedungeonentrance_instance_ref VALUES ('89', 0, '20016'), ('89', 1, '30')",
		"INSERT INTO dungeondetection VALUES ('20010008', '89', '5', NULL, 0, NULL)",
	)
	completeMasterFixture(t, path)
	exec(
		"INSERT INTO content_system(id, guide_category_id, name_status) VALUES ('wheel-tower', NULL, 'NO_OFFICIAL_NAME'), ('tower-of-adversity', '5', 'OFFICIAL'), ('other', '7', 'OFFICIAL')",
		`INSERT INTO content_entry(id, name_status) VALUES ('wheel-tower:activity:1', 'OFFICIAL'), ('wheel-tower:activity:2', 'OFFICIAL'),
			('wheel-tower:mode:0', 'OFFICIAL'), ('wheel-tower:stage:1', 'NO_OFFICIAL_NAME'), ('tower-of-adversity:stage:1', 'OFFICIAL'), ('other:stage:1', 'OFFICIAL')`,
		`INSERT INTO content_entry_locale(id, locale, name) VALUES ('wheel-tower:activity:1', 'zh-TW', '矩陣疊兵·先行版'), ('wheel-tower:activity:2', 'zh-TW', '終焉矩陣'),
			('wheel-tower:mode:0', 'zh-TW', '穩態協議'), ('tower-of-adversity:stage:1', 'zh-TW', '殘響之塔'), ('other:stage:1', 'zh-TW', '不應顯示')`,
		`INSERT INTO content_context(instance_id, system_id, ordinal, activity, stage, mode) VALUES
			('107700001', 'wheel-tower', 0, 'wheel-tower:activity:1', 'wheel-tower:stage:1', 'wheel-tower:mode:0'),
			('107700001', 'wheel-tower', 1, 'wheel-tower:activity:2', 'wheel-tower:stage:1', 'wheel-tower:mode:0'),
			('20016', 'tower-of-adversity', 0, NULL, 'tower-of-adversity:stage:1', NULL),
			('30', 'other', 0, NULL, 'other:stage:1', NULL)`,
	)
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resolver.Close() })
	return resolver
}

// TestHistoryNamesOfContentSystems: an encounter in an instance a content system lists shows the system's mode (a system
// the game names nothing keeps its id, which the UI labels) and the layers that system gave, each with its state and, when
// named, a status that follows the encounter's game version. The stage stays the MapName: an instance without one stays
// UNKNOWN there, whatever its content names. A content system that contradicts a guide relation gives no content.
func TestHistoryNamesOfContentSystems(t *testing.T) {
	names := &nameProjection{ctx: context.Background(), resolver: historyContentMaster(t), locale: "zh-TW"}
	matrix := history.EncounterSummary{Scene: history.SceneRef{Known: true, InstanceID: 107700001}, GameVersion: capture.CurrentGameVersion()}
	got := names.summary(matrix)
	if want := (historyMode{Kind: capture.InstanceModeContentSystem, Status: "VERIFIED", System: "wheel-tower", NameStatus: "NO_OFFICIAL_NAME"}); got.Mode != want {
		t.Fatalf("matrix mode %+v, want %+v", got.Mode, want)
	}
	if got.SceneName != (historyName{Status: "UNKNOWN"}) {
		t.Fatalf("matrix stage %+v: an instance without a MapName has no stage name", got.SceneName)
	}
	want := &historyContent{System: "wheel-tower", Layers: []historyContentLayer{
		{Dimension: "activity", State: "RUNTIME_DEPENDENT", Candidates: 2},
		{Dimension: "stage", State: "NO_OFFICIAL_NAME"},
		{Dimension: "mode", State: "OFFICIAL", Name: "穩態協議", Status: "VERIFIED"},
	}}
	if !reflect.DeepEqual(got.Content, want) {
		t.Fatalf("matrix content %+v, want %+v", got.Content, want)
	}
	if key := modeKey(got.Mode); key != "content:wheel-tower" {
		t.Fatalf("matrix mode key %q", key)
	}
	older := matrix
	older.GameVersion = "3.7"
	if layer := names.summary(older).Content.Layers[2]; layer.Status != "CORRELATED" || layer.Name != "穩態協議" {
		t.Fatalf("a content name of another game version: %+v, want CORRELATED", layer)
	}
	tower := names.summary(history.EncounterSummary{Scene: history.SceneRef{Known: true, InstanceID: 20016}, GameVersion: capture.CurrentGameVersion()})
	if tower.Mode != (historyMode{Kind: capture.InstanceModeCategory, Name: "逆境深塔", Status: "VERIFIED", System: "tower-of-adversity"}) || tower.SceneName.Name != "V1-1-4" ||
		tower.Content == nil || len(tower.Content.Layers) != 1 || tower.Content.Layers[0].Name != "殘響之塔" {
		t.Fatalf("tower: mode %+v stage %+v content %+v", tower.Mode, tower.SceneName, tower.Content)
	}
	conflict := names.summary(history.EncounterSummary{Scene: history.SceneRef{Known: true, InstanceID: 30}, GameVersion: capture.CurrentGameVersion()})
	if conflict.Mode != (historyMode{Kind: capture.InstanceModeCategory, Name: "逆境深塔", Status: "VERIFIED"}) || conflict.Content != nil {
		t.Fatalf("contradicted content: mode %+v content %+v, want the guide relation's mode and no content", conflict.Mode, conflict.Content)
	}
	if names.err != nil {
		t.Fatal(names.err)
	}
	// The library selects a content system by its key and finds an instance by a content name.
	instances := names.instances(map[uint64]int{107700001: 1, 20016: 1, 30: 1})
	byMode := resolveLibraryFilters(HistoryLibraryQuery{Modes: []string{"content:wheel-tower"}}, instances, nil, func(uint64) string { return "" })
	if !reflect.DeepEqual(byMode.Instances, []uint64{107700001}) {
		t.Fatalf("mode filter selected %v", byMode.Instances)
	}
	byText := resolveLibraryFilters(HistoryLibraryQuery{Text: "穩態"}, instances, nil, func(uint64) string { return "" })
	if !reflect.DeepEqual(byText.MatchInstances, []uint64{107700001}) {
		t.Fatalf("text search matched %v", byText.MatchInstances)
	}
	if hidden := resolveLibraryFilters(HistoryLibraryQuery{Text: "不應顯示"}, instances, nil, func(uint64) string { return "" }); len(hidden.MatchInstances) != 0 {
		t.Fatalf("a contradicted content name was searchable: %v", hidden.MatchInstances)
	}
}

// TestHistoryNamesOfContentSystemsWithTheEmbeddedMaster checks what the History views send with the Master the build
// embeds, in every bundled Master locale: a row (nameProjection.summary, as HistoryDay, HistoryLibrary and HistoryCompare
// send it) and the mode filter option of the library and the career page (modeFacet) must name the same mode, and a
// content system the game names no system for must reach both with its system, which the UI labels. The instances are the
// two matrix instances of the owner's 2026-11-22 LIVE encounters (no MapName: no stage name, and only the content layers
// the Master names stand in for one) and one instance of every other kind the History names (the Master's content rows
// themselves are checked in tests/capture/content_display_test.go).
func TestHistoryNamesOfContentSystemsWithTheEmbeddedMaster(t *testing.T) {
	masters, err := capture.LoadMasterData("", "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { masters.Close() })
	resolver := masters.DisplayResolver()
	cases := []struct {
		instance uint64
		kind     capture.InstanceModeKind
		system   string
		stage    bool // the instance has a stage name of its own
	}{
		{107700001, capture.InstanceModeContentSystem, "wheel-tower", false},
		{107700002, capture.InstanceModeContentSystem, "wheel-tower", false},
		{82110, capture.InstanceModeContentSystem, "role-trial", true},
		{8301, capture.InstanceModeContentSystem, "boss-rush", true},
		{8900, capture.InstanceModeContentSystem, "tower-defense", true},
		{9102, capture.InstanceModeCategory, "weekly-rogue", true},
		{3925, capture.InstanceModeCategory, "tactical-hologram", false},
		{40286, capture.InstanceModeCategory, "ship-tower", true},
		{20016, capture.InstanceModeCategory, "tower-of-adversity", true},
		{210404021, capture.InstanceModeCategory, "", true},
		{8, capture.InstanceModeOpenWorld, "", true},
	}
	matrixModes := map[string][2]string{"zh-TW": {"穩態協議", "奇點擴張"}, "en-US": {"Stability Accords", "Singularity Expansion"}}
	for _, locale := range []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"} {
		names := &nameProjection{ctx: t.Context(), resolver: resolver, locale: locale}
		counts := map[uint64]int{}
		for _, c := range cases {
			counts[c.instance] = 1
			row := names.summary(history.EncounterSummary{Scene: history.SceneRef{Known: true, InstanceID: c.instance}, GameVersion: capture.CurrentGameVersion()})
			if row.Mode.Kind != c.kind || row.Mode.System != c.system || row.Mode.Status == "UNKNOWN" {
				t.Fatalf("%s %d: row mode %+v, want kind %s of system %q", locale, c.instance, row.Mode, c.kind, c.system)
			}
			if c.kind == capture.InstanceModeCategory && row.Mode.Name == "" || c.kind != capture.InstanceModeCategory && row.Mode.Name != "" {
				t.Fatalf("%s %d: mode name %q: only a category is named by the Master; the UI labels the others", locale, c.instance, row.Mode.Name)
			}
			if (row.SceneName.Name != "") != c.stage {
				t.Fatalf("%s %d: stage %+v, want a stage name of its own: %v", locale, c.instance, row.SceneName, c.stage)
			}
			option := modeFacet(names.instanceNames(c.instance))
			if option.Key != modeKey(row.Mode) || option.Kind != row.Mode.Kind || option.Name != row.Mode.Name || option.System != row.Mode.System {
				t.Fatalf("%s %d: filter option %+v names another mode than the row %+v", locale, c.instance, option, row.Mode)
			}
		}
		for i, instance := range []uint64{107700001, 107700002} {
			row := names.summary(history.EncounterSummary{Scene: history.SceneRef{Known: true, InstanceID: instance}, GameVersion: capture.CurrentGameVersion()})
			if row.SceneName != (historyName{Status: "UNKNOWN"}) || row.Content == nil || row.Content.System != "wheel-tower" {
				t.Fatalf("%s %d: stage %+v content %+v", locale, instance, row.SceneName, row.Content)
			}
			var shown []string
			for _, layer := range row.Content.Layers {
				shown = append(shown, layer.Dimension+"="+layer.State)
				switch {
				case layer.Dimension == "mode" && (layer.State != "OFFICIAL" || layer.Name == "" || layer.Status != "VERIFIED"):
					t.Fatalf("%s %d: the matrix mode is one named entry: %+v", locale, instance, layer)
				case layer.Dimension == "mode" && matrixModes[locale][i] != "" && layer.Name != matrixModes[locale][i]:
					t.Fatalf("%s %d: mode %q, want %q", locale, instance, layer.Name, matrixModes[locale][i])
				case layer.Dimension != "mode" && layer.Name != "":
					// Activity and cycle are left open by the Master: no current activity such as 終焉矩陣 is chosen.
					t.Fatalf("%s %d: layer %+v names a value the static data cannot choose", locale, instance, layer)
				}
			}
			if strings.Join(shown, " ") != "activity=RUNTIME_DEPENDENT cycle=RUNTIME_DEPENDENT stage=NO_OFFICIAL_NAME mode=OFFICIAL" {
				t.Fatalf("%s %d: content layers %v", locale, instance, shown)
			}
		}
		// The library's filter options as Go sends them: every content system without a game name carries its system.
		raw, err := json.Marshal(libraryFacets(history.LibraryFacets{Instances: counts}, names.instances(counts), map[uint64]uint64{}, names))
		if err != nil {
			t.Fatal(err)
		}
		var facets struct {
			Modes []map[string]any `json:"modes"`
		}
		if err := json.Unmarshal(raw, &facets); err != nil {
			t.Fatal(err)
		}
		systems := map[string]bool{}
		for _, option := range facets.Modes {
			if option["kind"] == string(capture.InstanceModeContentSystem) {
				system, _ := option["system"].(string)
				if option["key"] != "content:"+system || system == "" {
					t.Fatalf("%s: content system option %v must carry its system", locale, option)
				}
				systems[system] = true
			}
		}
		if want := map[string]bool{"wheel-tower": true, "role-trial": true, "boss-rush": true, "tower-defense": true}; !reflect.DeepEqual(systems, want) {
			t.Fatalf("%s: content system options %v, want %v", locale, systems, want)
		}
		if names.err != nil {
			t.Fatal(names.err)
		}
	}
}
