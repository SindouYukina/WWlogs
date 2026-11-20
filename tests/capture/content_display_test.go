package capture

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// The content model as the display resolver reads it (internal/capture/content_display.go, instance_display.go). The
// expected values of the embedded Master are the Master's own content rows, read independently of the resolver
// (content_context, content_entry, content_entry_locale; Global 3.8.12): what the tests check is how the resolver combines
// several contexts, keeps the name states apart, falls back to English and never gives one layer's name as another's.

func embeddedDisplayResolver(t *testing.T) *DisplayResolver {
	t.Helper()
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newDisplayResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resolver.Close() })
	return resolver
}

// layersOf indexes the layers of a content answer by dimension.
func layersOf(content InstanceContent) map[string]ContentLayer {
	layers := map[string]ContentLayer{}
	for _, layer := range content.Layers {
		layers[layer.Dimension] = layer
	}
	return layers
}

// TestInstanceContentOfTheEmbeddedMaster checks one instance of each kind of combination the Master holds:
//
//	107700001/2  matrix: 9 contexts; activity 100770001 (cycles 1-2) or 100770002 (cycles 3-9), cycles 1-2 without a
//	             name and 3-9 named, 9 unnamed stages, one mode (entry 0 "穩態協議" for 107700001, 1 "奇點擴張" for 107700002)
//	82110        role trial: one context, its stage name UNRESOLVED, trial character 1512 (莉莉)
//	9102         WeeklyRogue: 15 contexts through 15 cycles all named "隱秘廳堂", one through its unnamed stage
//	3925         Tactical Hologram: 18 contexts of one series "全像戰略·演武" over 3 bosses and 18 difficulties
//	20013        Tower of Adversity: 43 contexts (seasons without names), every area named "殘響之塔", one zone "深境區"
//	40286        ShipTower: one context, stage "無盡湍淵", mode "再生海域", a season without a name
//	8301         BossRush: one context, activity "仿聲戰略·諧度篇", an unnamed stage
//	8900         tower defense: activity 101900004, whose ja-JP text carries the client's <word> markup
func TestInstanceContentOfTheEmbeddedMaster(t *testing.T) {
	resolver := embeddedDisplayResolver(t)
	ctx := context.Background()
	official := func(name string) ContentLayer {
		return ContentLayer{State: ContentLayerOfficial, Name: name, Locale: "zh-TW"}
	}
	state := func(s ContentLayerState) ContentLayer { return ContentLayer{State: s} }
	open := func(n int) ContentLayer { return ContentLayer{State: ContentLayerRuntimeDependent, Candidates: n} }
	for _, c := range []struct {
		instance uint64
		system   string
		contexts int
		layers   map[string]ContentLayer
	}{
		{107700001, "wheel-tower", 9, map[string]ContentLayer{"activity": open(2), "cycle": open(9), "stage": state(ContentLayerNoOfficialName), "mode": official("穩態協議")}},
		{107700002, "wheel-tower", 9, map[string]ContentLayer{"activity": open(2), "cycle": open(9), "stage": state(ContentLayerNoOfficialName), "mode": official("奇點擴張")}},
		{82110, "role-trial", 1, map[string]ContentLayer{"stage": state(ContentLayerUnresolved), "role": {State: ContentLayerOfficial, Name: "莉莉", Locale: "zh-TW", RoleID: 1512}}},
		{9102, "weekly-rogue", 16, map[string]ContentLayer{"cycle": official("隱秘廳堂"), "stage": state(ContentLayerNoOfficialName)}},
		{3925, "tactical-hologram", 18, map[string]ContentLayer{"series": official("全像戰略·演武"), "stage": open(3), "difficulty": open(18)}},
		{20013, "tower-of-adversity", 43, map[string]ContentLayer{"season": state(ContentLayerNoOfficialName), "stage": official("殘響之塔"), "difficulty": official("深境區")}},
		{40286, "ship-tower", 1, map[string]ContentLayer{"season": state(ContentLayerNoOfficialName), "stage": official("無盡湍淵"), "mode": official("再生海域")}},
		{8301, "boss-rush", 1, map[string]ContentLayer{"activity": official("仿聲戰略·諧度篇"), "stage": state(ContentLayerNoOfficialName)}},
	} {
		got, err := resolver.InstanceContent(ctx, c.instance, "zh-TW")
		if err != nil {
			t.Fatal(err)
		}
		if got.System != c.system || got.Status != "VERIFIED" || got.Contexts != c.contexts {
			t.Errorf("instance %d: %s %s with %d contexts, want %s VERIFIED with %d", c.instance, got.System, got.Status, got.Contexts, c.system, c.contexts)
		}
		layers := layersOf(got)
		for dimension, want := range c.layers {
			want.Dimension = dimension
			if layers[dimension] != want {
				t.Errorf("instance %d %s: %+v, want %+v", c.instance, dimension, layers[dimension], want)
			}
		}
		if len(layers) != len(c.layers) {
			t.Errorf("instance %d has layers %v, want only %d", c.instance, got.Layers, len(c.layers))
		}
	}
	// Names follow the locale; markup the client renders is not part of the name.
	for _, c := range []struct {
		instance  uint64
		locale    string
		dimension string
		name      string
	}{
		{107700001, "en-US", "mode", "Stability Accords"},
		{107700002, "ja-JP", "mode", "奇点拡張"},
		{8900, "ja-JP", "activity", "夢がまだ、響き続けているなら"},
		{8900, "zh-TW", "activity", "若夢仍有回聲"},
		{82110, "en-US", "role", "Lily"},
	} {
		got, err := resolver.InstanceContent(ctx, c.instance, c.locale)
		if err != nil {
			t.Fatal(err)
		}
		if layer := layersOf(got)[c.dimension]; layer.State != ContentLayerOfficial || layer.Name != c.name || layer.Locale != c.locale {
			t.Errorf("instance %d %s %s: %+v, want %q", c.instance, c.locale, c.dimension, layer, c.name)
		}
	}
	for _, id := range []uint64{0, 8, 20016 * 1000, 999999999} {
		if got, err := resolver.InstanceContent(ctx, id, "zh-TW"); err != nil || got.Status != "UNKNOWN" || got.System != "" || len(got.Layers) != 0 {
			t.Errorf("instance %d outside the content model: %+v %v", id, got, err)
		}
	}
}

// TestInstanceModeOfContentSystems: an instance a content system lists takes the system's mode, named by its guide
// category, or kind content_system with the system's name state when the game names no system; a guide relation that names
// the same category is not overridden, only confirmed.
func TestInstanceModeOfContentSystems(t *testing.T) {
	resolver := embeddedDisplayResolver(t)
	ctx := context.Background()
	for _, c := range []struct {
		instance uint64
		want     InstanceMode
	}{
		{107700001, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "wheel-tower", NameStatus: "NO_OFFICIAL_NAME"}},
		{107700002, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "wheel-tower", NameStatus: "NO_OFFICIAL_NAME"}},
		{82110, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "role-trial", NameStatus: "NO_OFFICIAL_NAME"}},
		{8301, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "boss-rush", NameStatus: "NO_OFFICIAL_NAME"}},
		{8900, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "tower-defense", NameStatus: "NO_OFFICIAL_NAME"}},
		{9102, InstanceMode{Kind: InstanceModeCategory, Name: "千道門扉的異想", Status: "VERIFIED", Locale: "zh-TW", Basis: "content", System: "weekly-rogue"}},
		{3925, InstanceMode{Kind: InstanceModeCategory, Name: "全像戰略", Status: "VERIFIED", Locale: "zh-TW", Basis: "content", System: "tactical-hologram"}},
		{3924, InstanceMode{Kind: InstanceModeCategory, Name: "全像戰略", Status: "VERIFIED", Locale: "zh-TW", Basis: "content", System: "tactical-hologram"}},
		{20016, InstanceMode{Kind: InstanceModeCategory, Name: "逆境深塔", Status: "VERIFIED", Locale: "zh-TW", Basis: "content", System: "tower-of-adversity"}},
		{40286, InstanceMode{Kind: InstanceModeCategory, Name: "冥歌海墟", Status: "VERIFIED", Locale: "zh-TW", Basis: "content", System: "ship-tower"}},
	} {
		got, err := resolver.InstanceMode(ctx, c.instance, "zh-TW")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("instance %d: %+v, want %+v", c.instance, got, c.want)
		}
	}
	if got, err := resolver.InstanceMode(ctx, 40286, "en-US"); err != nil || got.Name != "Whimpering Wastes" || got.Locale != "en-US" {
		t.Fatalf("en-US ShipTower mode = %+v (%v)", got, err)
	}
}

// TestInstanceStageIsTheMapNameOnly: the stage is the instance's official MapName, never the series, activity or mode of
// its content; a placeholder MapName falls back to English.
func TestInstanceStageIsTheMapNameOnly(t *testing.T) {
	resolver := embeddedDisplayResolver(t)
	ctx := context.Background()
	for _, c := range []struct {
		instance       uint64
		locale         string
		name, nameLang string
	}{
		{3925, "zh-TW", "", ""},
		{107700001, "zh-TW", "", ""},
		{40286, "zh-TW", "無盡湍淵-上半", "zh-TW"},
		{82110, "zh-TW", "桃祈角色試用副本", "zh-TW"},
		{82110, "zh-CN", "TaoqiCharacter trial copy", "en-US"},
		{20016, "zh-CN", "V1-1-4", "en-US"},
	} {
		got, err := resolver.InstanceStage(ctx, c.instance, c.locale)
		if err != nil {
			t.Fatal(err)
		}
		status := "VERIFIED"
		if c.name == "" {
			status = "UNKNOWN"
		}
		if got.Name != c.name || got.Locale != c.nameLang || got.Status != status {
			t.Errorf("instance %d %s: stage %+v, want %q (%s)", c.instance, c.locale, got, c.name, c.nameLang)
		}
	}
}

// contentFixture is a Master with one guide category and the content model rows of a test, completed to the runtime
// schema.
func contentFixture(t *testing.T, statements ...string) *DisplayResolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "content.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	base := []string{
		"CREATE TABLE instancedungeon(id TEXT PRIMARY KEY, inst_type INTEGER)",
		"CREATE TABLE secondaryguidedata_locale(id TEXT NOT NULL, locale TEXT NOT NULL, text TEXT NOT NULL, PRIMARY KEY(id, locale))",
		"CREATE TABLE roleinfo_locale(id TEXT NOT NULL, locale TEXT NOT NULL, name TEXT NOT NULL, PRIMARY KEY(id, locale))",
		"CREATE TABLE instancedungeonentrance_instance_ref(entrance_id TEXT NOT NULL, ordinal INTEGER NOT NULL, instance_dungeon_id TEXT NOT NULL, PRIMARY KEY(entrance_id, ordinal))",
		"CREATE TABLE dungeondetection(id TEXT PRIMARY KEY, dungeon_id TEXT, secondary TEXT, sub_dungeon_id TEXT, jump_with_sub_dungeon_id INTEGER NOT NULL DEFAULT 0, instance_dungeon_entrance_id TEXT)",
		"INSERT INTO secondaryguidedata_locale VALUES ('5', 'zh-TW', '逆境深塔'), ('5', 'en-US', 'Tower of Adversity'), ('7', 'zh-TW', '另一種玩法')",
	}
	for i := 1; i <= 40; i++ {
		base = append(base, "INSERT INTO instancedungeon VALUES ('"+strconv.Itoa(i)+"', 2)")
	}
	exec := func(statements []string) {
		t.Helper()
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	db.SetMaxOpenConns(1)
	exec(base)
	// The content tables come from the runtime schema, with their keys, before the test's rows go in.
	if err := completeMasterSchema(db); err != nil {
		t.Fatal(err)
	}
	exec(statements)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	resolver, err := OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resolver.Close() })
	return resolver
}

// TestInstanceContentRules: how several contexts combine per dimension, the name states, the English fallback, the
// placeholder and markup rules, and that a system or entry known only from data needs no code.
func TestInstanceContentRules(t *testing.T) {
	resolver := contentFixture(t,
		"INSERT INTO content_system(id, guide_category_id, name_status) VALUES ('sys-a', NULL, 'NO_OFFICIAL_NAME'), ('sys-b', NULL, 'UNRESOLVED'), ('sys-new', '5', 'OFFICIAL')",
		`INSERT INTO content_entry(id, name_status) VALUES
			('sys-a:activity:1', 'OFFICIAL'), ('sys-a:activity:2', 'OFFICIAL'), ('sys-a:activity:3', 'OFFICIAL'),
			('sys-a:cycle:1', 'OFFICIAL'), ('sys-a:cycle:2', 'NO_OFFICIAL_NAME'),
			('sys-a:stage:1', 'NO_OFFICIAL_NAME'), ('sys-a:stage:2', 'NO_OFFICIAL_NAME'),
			('sys-a:mode:1', 'OFFICIAL'), ('sys-a:mode:2', 'OFFICIAL'), ('sys-a:series:1', 'OFFICIAL'),
			('sys-b:stage:1', 'UNRESOLVED'), ('sys-b:stage:2', 'UNRESOLVED'),
			('sys-new:stage:1', 'OFFICIAL'), ('sys-new:difficulty:1', 'OFFICIAL')`,
		`INSERT INTO content_entry_locale(id, locale, name) VALUES
			('sys-a:activity:1', 'zh-TW', '活動甲'), ('sys-a:activity:2', 'zh-TW', '活動甲'), ('sys-a:activity:3', 'zh-TW', '活動乙'),
			('sys-a:cycle:1', 'zh-TW', '週期一'),
			('sys-a:mode:1', 'en-US', 'English only'), ('sys-a:mode:1', 'zh-CN', 'dnt/占位'),
			('sys-a:mode:2', 'ja-JP', '日本語だけ'),
			('sys-a:series:1', 'zh-TW', '系列<word>名</word>'),
			('sys-new:stage:1', 'zh-TW', '同一個鍵'), ('sys-new:difficulty:1', 'zh-TW', '難度一')`,
		"INSERT INTO roleinfo_locale VALUES ('1001', 'zh-TW', '角色一'), ('1002', 'zh-TW', '角色二'), ('1003', 'zh-CN', 'dnt/占位角色')",
		`INSERT INTO content_context(instance_id, system_id, ordinal, activity, cycle, stage, mode, series, role_id) VALUES
			('1', 'sys-a', 0, 'sys-a:activity:1', NULL, 'sys-a:stage:1', 'sys-a:mode:1', NULL, '1001'),
			('1', 'sys-a', 1, 'sys-a:activity:2', 'sys-a:cycle:1', 'sys-a:stage:2', 'sys-a:mode:1', NULL, '1001'),
			('2', 'sys-a', 0, 'sys-a:activity:1', 'sys-a:cycle:1', NULL, 'sys-a:mode:2', 'sys-a:series:1', '1001'),
			('2', 'sys-a', 1, 'sys-a:activity:3', 'sys-a:cycle:2', NULL, 'sys-a:mode:2', 'sys-a:series:1', '1002'),
			('3', 'sys-b', 0, NULL, NULL, 'sys-b:stage:1', NULL, NULL, '1003'),
			('3', 'sys-b', 1, NULL, NULL, 'sys-b:stage:2', NULL, NULL, NULL),
			('4', 'sys-new', 0, NULL, NULL, 'sys-new:stage:1', NULL, NULL, NULL)`,
		"INSERT INTO content_context(instance_id, system_id, ordinal, difficulty) VALUES ('4', 'sys-new', 1, 'sys-new:difficulty:1')",
	)
	ctx := context.Background()
	check := func(instance uint64, locale string, want map[string]ContentLayer) {
		t.Helper()
		got, err := resolver.InstanceContent(ctx, instance, locale)
		if err != nil {
			t.Fatal(err)
		}
		layers := layersOf(got)
		for dimension, layer := range want {
			layer.Dimension = dimension
			if layers[dimension] != layer {
				t.Errorf("instance %d %s %s: %+v, want %+v", instance, locale, dimension, layers[dimension], layer)
			}
		}
		if len(layers) != len(want) {
			t.Errorf("instance %d %s: layers %+v, want %d", instance, locale, got.Layers, len(want))
		}
	}
	// Two entries with the same name are one name; a dimension a context leaves NULL counts only where it is modelled;
	// the English fallback is marked by its locale; one character in every context is named with its RoleId.
	check(1, "zh-TW", map[string]ContentLayer{
		"activity": {State: ContentLayerOfficial, Name: "活動甲", Locale: "zh-TW"},
		"cycle":    {State: ContentLayerOfficial, Name: "週期一", Locale: "zh-TW"},
		"stage":    {State: ContentLayerNoOfficialName},
		"mode":     {State: ContentLayerOfficial, Name: "English only", Locale: "en-US"},
		"role":     {State: ContentLayerOfficial, Name: "角色一", Locale: "zh-TW", RoleID: 1001},
	})
	// A placeholder is never the name: zh-CN falls back to English too. Entries without a text in the locale or English
	// are named nowhere, which is not a choice between them.
	check(1, "zh-CN", map[string]ContentLayer{
		"activity": {State: ContentLayerNameMissing},
		"cycle":    {State: ContentLayerNameMissing},
		"stage":    {State: ContentLayerNoOfficialName},
		"mode":     {State: ContentLayerOfficial, Name: "English only", Locale: "en-US"},
		"role":     {State: ContentLayerNameMissing, RoleID: 1001},
	})
	// Different names, a named and an unnamed cycle, two characters: the runtime chooses none. A name only in another
	// locale is missing here; markup is not part of a name.
	check(2, "zh-TW", map[string]ContentLayer{
		"activity": {State: ContentLayerRuntimeDependent, Candidates: 2},
		"cycle":    {State: ContentLayerRuntimeDependent, Candidates: 2},
		"mode":     {State: ContentLayerNameMissing},
		"series":   {State: ContentLayerOfficial, Name: "系列名", Locale: "zh-TW"},
		"role":     {State: ContentLayerRuntimeDependent, Candidates: 2},
	})
	// UNRESOLVED stays UNRESOLVED, never "no official name"; a placeholder role name is no name.
	check(3, "zh-CN", map[string]ContentLayer{
		"stage": {State: ContentLayerUnresolved},
		"role":  {State: ContentLayerNameMissing, RoleID: 1003},
	})
	// A system that exists only as data (sys-new, named by guide category 5) needs no code: the same key in another
	// system's namespace is another entry.
	check(4, "zh-TW", map[string]ContentLayer{
		"stage":      {State: ContentLayerOfficial, Name: "同一個鍵", Locale: "zh-TW"},
		"difficulty": {State: ContentLayerOfficial, Name: "難度一", Locale: "zh-TW"},
	})
	for _, c := range []struct {
		instance uint64
		want     InstanceMode
	}{
		{1, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "sys-a", NameStatus: "NO_OFFICIAL_NAME"}},
		{3, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "sys-b", NameStatus: "UNRESOLVED"}},
		{4, InstanceMode{Kind: InstanceModeCategory, Name: "逆境深塔", Status: "VERIFIED", Locale: "zh-TW", Basis: "content", System: "sys-new"}},
		{5, InstanceMode{Status: "UNKNOWN"}},
	} {
		got, err := resolver.InstanceMode(ctx, c.instance, "zh-TW")
		if err != nil || got != c.want {
			t.Errorf("instance %d: mode %+v (%v), want %+v", c.instance, got, err, c.want)
		}
	}
}

// TestInstanceModeKeepsAGuideRelationTheContentModelContradicts: the content model never overrides a guide relation that
// names another category, a system the game names nothing, or several systems; the answer then is the guide relation's,
// with the disagreement named. An instance only several systems list is AMBIGUOUS, and its content is not chosen either. A
// system whose category has no name keeps its mode (content_system, NAME_MISSING) instead of becoming UNKNOWN.
func TestInstanceModeKeepsAGuideRelationTheContentModelContradicts(t *testing.T) {
	resolver := contentFixture(t,
		"INSERT INTO instancedungeonentrance_instance_ref VALUES ('90', 0, '10'), ('90', 1, '11'), ('90', 2, '12'), ('90', 3, '13')",
		"INSERT INTO dungeondetection(id, dungeon_id, secondary) VALUES ('1', '90', '5')",
		"INSERT INTO content_system(id, guide_category_id, name_status) VALUES ('other-category', '7', 'OFFICIAL'), ('same-category', '5', 'OFFICIAL'), ('nameless', NULL, 'NO_OFFICIAL_NAME'), ('textless', '8', 'OFFICIAL')",
		`INSERT INTO content_context(instance_id, system_id, ordinal) VALUES ('10', 'other-category', 0), ('11', 'same-category', 0), ('12', 'nameless', 0),
			('13', 'same-category', 0), ('13', 'nameless', 0), ('20', 'same-category', 0), ('20', 'nameless', 0), ('21', 'textless', 0)`,
	)
	ctx := context.Background()
	guide := InstanceMode{Kind: InstanceModeCategory, Name: "逆境深塔", Status: "VERIFIED", Locale: "zh-TW", Basis: "entrance"}
	with := func(mode InstanceMode, conflict string) InstanceMode { mode.Conflict = conflict; return mode }
	for _, c := range []struct {
		instance uint64
		want     InstanceMode
	}{
		{10, with(guide, "content:other-category")},
		{11, InstanceMode{Kind: InstanceModeCategory, Name: "逆境深塔", Status: "VERIFIED", Locale: "zh-TW", Basis: "content", System: "same-category"}},
		{12, with(guide, "content:nameless")},
		{13, with(guide, "systems:nameless,same-category")},
		{20, InstanceMode{Status: "AMBIGUOUS", Conflict: "systems:nameless,same-category"}},
		{21, InstanceMode{Kind: InstanceModeContentSystem, Status: "VERIFIED", Basis: "content", System: "textless", NameStatus: "NAME_MISSING"}},
	} {
		got, err := resolver.InstanceMode(ctx, c.instance, "zh-TW")
		if err != nil || got != c.want {
			t.Errorf("instance %d: mode %+v (%v), want %+v", c.instance, got, err, c.want)
		}
	}
	if got, err := resolver.InstanceContent(ctx, 20, "zh-TW"); err != nil || got.Status != "AMBIGUOUS" || got.System != "" || !reflect.DeepEqual(got.Systems, []string{"nameless", "same-category"}) || len(got.Layers) != 0 {
		t.Fatalf("instance 20: content %+v (%v), want AMBIGUOUS without layers", got, err)
	}
}

// TestInstanceContentRefusesABrokenModel: a context that names a system or an entry the Master does not hold is a broken
// Master, reported as an error, never shown as UNKNOWN content.
func TestInstanceContentRefusesABrokenModel(t *testing.T) {
	resolver := contentFixture(t,
		"INSERT INTO content_system(id, name_status) VALUES ('sys-a', 'NO_OFFICIAL_NAME')",
		"INSERT INTO content_context(instance_id, system_id, ordinal, stage) VALUES ('1', 'sys-a', 0, 'sys-a:stage:missing'), ('2', 'sys-missing', 0, NULL)",
	)
	ctx := context.Background()
	if got, err := resolver.InstanceContent(ctx, 1, "zh-TW"); err == nil {
		t.Errorf("a context naming a missing entry gave %+v", got)
	}
	if got, err := resolver.InstanceMode(ctx, 2, "zh-TW"); err == nil {
		t.Errorf("a context naming a missing system gave mode %+v", got)
	}
}
