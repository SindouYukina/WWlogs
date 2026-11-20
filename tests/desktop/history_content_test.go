package desktop

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
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
