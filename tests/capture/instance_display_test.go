package capture

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// The kind of play and the stage of the instances the Build 31 LIVE capture fought in, against the embedded Global 3.8.9
// Master. Each mode follows a relation the client resolves itself (instance_display.go); the expected names are the
// game's own SecondaryGuideData texts and InstanceDungeon MapNames:
//
//	8          InstType 1 (BigWorldInstance)                                  open world
//	20016..24  DungeonDetection 20010008/9 -> entrance 89 -> InstanceDungeonList  逆境深塔 (Secondary 5)
//	5067       DungeonDetection 20010560 SubDungeonId, Secondary 6               角色教學 (Lily's tutorial)
//	3925       SilentAreaDetection 30020022..24 -> entrance 3925 -> list          全像戰略 (Secondary 61)
//	210404021  SilentAreaDetection 30010030 DungeonId (namespace not shown)      無音清剿 (Secondary 22), CORRELATED
//	40286/7    SlashTowerStageInfo, the ShipTower system of category 28          冥歌海墟, CORRELATED
func TestInstanceModeOfTheBuild31Instances(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newDisplayResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	ctx := context.Background()
	for _, c := range []struct {
		instance uint64
		kind     InstanceModeKind
		name     string
		status   string
		basis    string
	}{
		{8, InstanceModeOpenWorld, "", "VERIFIED", "inst_type"},
		{20016, InstanceModeCategory, "逆境深塔", "VERIFIED", "entrance"},
		{20019, InstanceModeCategory, "逆境深塔", "VERIFIED", "entrance"},
		{20024, InstanceModeCategory, "逆境深塔", "VERIFIED", "entrance"},
		{5067, InstanceModeCategory, "角色教學", "VERIFIED", "sub_dungeon"},
		{3925, InstanceModeCategory, "全像戰略", "VERIFIED", "entrance"},
		{210404021, InstanceModeCategory, "無音清剿", "CORRELATED", "silent_area_dungeon"},
		{40286, InstanceModeCategory, "冥歌海墟", "CORRELATED", "ship_tower"},
		{40287, InstanceModeCategory, "冥歌海墟", "CORRELATED", "ship_tower"},
		{0, "", "", "UNKNOWN", ""},
		{999999999, "", "", "UNKNOWN", ""},
	} {
		got, err := resolver.InstanceMode(ctx, c.instance, "zh-TW")
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != c.kind || got.Name != c.name || got.Status != c.status || got.Basis != c.basis {
			t.Errorf("instance %d: mode %+v, want %s %q %s %s", c.instance, got, c.kind, c.name, c.status, c.basis)
		}
	}
	// A locale changes only the name; English is the fallback of a locale without the text.
	if got, err := resolver.InstanceMode(ctx, 20016, "en-US"); err != nil || got.Name != "Tower of Adversity" || got.Status != "VERIFIED" {
		t.Fatalf("en-US mode = %+v (%v)", got, err)
	}
	if got, err := resolver.InstanceMode(ctx, 40286, "en-US"); err != nil || got.Name != "Whimpering Wastes" {
		t.Fatalf("en-US ShipTower mode = %+v (%v)", got, err)
	}
}

// The stage is the instance's own MapName; an instance without one is named by its Tactical Hologram entrance set, which
// several holograms share, so that name is CORRELATED.
func TestInstanceStageNamesMapsAndHologramSets(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newDisplayResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	ctx := context.Background()
	for _, c := range []struct {
		instance uint64
		name     string
		status   string
	}{
		{8, "瑝瓏", "VERIFIED"},
		{20019, "V1-2-3", "VERIFIED"},
		{40286, "無盡湍淵-上半", "VERIFIED"},
		{5067, "訓練儀-淵武", "VERIFIED"},
		{210404021, "影造圓庭·凝率", "VERIFIED"},
		{3925, "全像戰略·演武", "CORRELATED"},
		{999999999, "", "UNKNOWN"},
	} {
		got, err := resolver.InstanceStage(ctx, c.instance, "zh-TW")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != c.name || got.Status != c.status {
			t.Errorf("instance %d: stage %+v, want %q %s", c.instance, got, c.name, c.status)
		}
	}
}

// A Master without the guide tables names no mode and fails nothing.
func TestInstanceModeWithoutGuideTables(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "older.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE instancedungeon(id TEXT PRIMARY KEY, inst_type INTEGER)",
		"INSERT INTO instancedungeon VALUES('8', 1), ('20016', 2)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := newDisplayResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	for _, id := range []uint64{8, 20016} {
		if got, err := resolver.InstanceMode(context.Background(), id, "zh-TW"); err != nil || got.Status != "UNKNOWN" || got.Kind != "" {
			t.Fatalf("instance %d without guide tables: %+v (%v)", id, got, err)
		}
	}
}
