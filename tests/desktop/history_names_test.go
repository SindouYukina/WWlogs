package desktop

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
)

// historyNamesMaster is a Master with one Tower of Adversity stage (20016, entrance 89, guide category 5) and the open
// world (8), as the Global 3.8.9 Master relates them.
func historyNamesMaster(t *testing.T) *capture.DisplayResolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE instancedungeon(id TEXT PRIMARY KEY, inst_type INTEGER)",
		"CREATE TABLE instancedungeon_locale(id TEXT NOT NULL, locale TEXT NOT NULL, name TEXT NOT NULL, text_key TEXT, PRIMARY KEY(id, locale))",
		"CREATE TABLE secondaryguidedata_locale(id TEXT NOT NULL, locale TEXT NOT NULL, text TEXT NOT NULL, text_key TEXT, PRIMARY KEY(id, locale))",
		"CREATE TABLE instancedungeonentrance_instance_ref(entrance_id TEXT NOT NULL, ordinal INTEGER NOT NULL, instance_dungeon_id TEXT NOT NULL, PRIMARY KEY(entrance_id, ordinal))",
		"CREATE TABLE dungeondetection(id TEXT PRIMARY KEY, dungeon_id TEXT, secondary TEXT, sub_dungeon_id TEXT, jump_with_sub_dungeon_id INTEGER NOT NULL DEFAULT 0, instance_dungeon_entrance_id TEXT)",
		"CREATE TABLE silentareadetection(id TEXT PRIMARY KEY, dungeon_id TEXT, secondary TEXT, instance_dungeon_entrance_id TEXT)",
		"CREATE TABLE slashtowerstageinfo(id TEXT PRIMARY KEY, inst_id TEXT, season_version INTEGER)",
		"CREATE TABLE slashandtowercfg_instance_ref(cfg_id TEXT NOT NULL, ordinal INTEGER NOT NULL, instance_dungeon_id TEXT NOT NULL, PRIMARY KEY(cfg_id, ordinal))",
		"INSERT INTO instancedungeon VALUES('20016', 2), ('8', 1)",
		"INSERT INTO instancedungeon_locale VALUES('20016', 'zh-TW', 'V1-1-4', 'InstanceDungeon_20016_MapName'), ('8', 'zh-TW', '瑝瓏', 'InstanceDungeon_8_MapName')",
		"INSERT INTO secondaryguidedata_locale VALUES('5', 'zh-TW', '逆境深塔', 'DetectionText_5_Text')",
		"INSERT INTO instancedungeonentrance_instance_ref VALUES('89', 0, '20016')",
		"INSERT INTO dungeondetection VALUES('20010008', '89', '5', '20001', 0, NULL)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	db.Close()
	completeMasterFixture(t, path)
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resolver.Close() })
	return resolver
}

// A scene or mode name is VERIFIED only for an encounter of the game version the Master describes; an encounter of another
// version, or without a stored version, reads it as CORRELATED. Monster names are never raised by this rule.
func TestHistoryNamesAreVerifiedOnlyInTheCurrentGameVersion(t *testing.T) {
	names := &nameProjection{ctx: context.Background(), resolver: historyNamesMaster(t), locale: "zh-TW"}
	stage := history.EncounterSummary{Scene: history.SceneRef{Known: true, InstanceID: 20016}}
	for _, c := range []struct {
		version string
		status  string
	}{{capture.CurrentGameVersion(), "VERIFIED"}, {"3.7", "CORRELATED"}, {"", "CORRELATED"}} {
		stage.GameVersion = c.version
		got := names.summary(stage)
		if got.SceneName != (historyName{Name: "V1-1-4", Status: c.status}) || got.Mode != (historyMode{Kind: capture.InstanceModeCategory, Name: "逆境深塔", Status: c.status}) {
			t.Fatalf("version %q: scene %+v mode %+v, want %s", c.version, got.SceneName, got.Mode, c.status)
		}
	}
	world := history.EncounterSummary{Scene: history.SceneRef{Known: true, InstanceID: 8}, GameVersion: capture.CurrentGameVersion()}
	if got := names.summary(world); got.SceneName.Name != "瑝瓏" || got.Mode != (historyMode{Kind: capture.InstanceModeOpenWorld, Status: "VERIFIED"}) {
		t.Fatalf("open world: scene %+v mode %+v", got.SceneName, got.Mode)
	}
	unknown := history.EncounterSummary{Scene: history.SceneRef{Known: false}, GameVersion: capture.CurrentGameVersion()}
	if got := names.summary(unknown); got.SceneName.Status != "UNKNOWN" || got.Mode.Status != "UNKNOWN" || got.Mode.Kind != "" {
		t.Fatalf("no scene: scene %+v mode %+v", got.SceneName, got.Mode)
	}
	if names.err != nil {
		t.Fatal(names.err)
	}
}
