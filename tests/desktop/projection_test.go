package desktop

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

func TestDisplayProjectionRelocalizesExistingSnapshotWithoutChangingCore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, _ := sql.Open("sqlite", path)
	for _, table := range []string{"roleinfo_locale", "term_locale"} {
		if _, err := db.Exec("CREATE TABLE " + table + " (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))"); err != nil {
			t.Fatal(err)
		}
		for _, row := range [][3]string{{"42", "en-US", "Example"}, {"42", "ja-JP", "テスト"}} {
			if _, err := db.Exec("INSERT INTO "+table+" VALUES(?,?,?)", row[0], row[1], row[2]); err != nil {
				t.Fatal(err)
			}
		}
	}
	db.Close()
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	store := loadSettings(filepath.Join(t.TempDir(), "settings.json"), testLocaleCatalog())
	term := uint64(42)
	role := capture.ViewRole{RoleID: 42, Name: "Original", NameStatus: "VERIFIED", DamageDealt: 1234, DamageTaken: 9}
	effect := capture.ViewEffect{TermID: &term, HolderRoleID: &term, TermName: "Original", TermStatus: "CORRELATED", RemainingSeconds: new(float64)}
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{role}, Statistics: capture.ViewStatistics{DamageDealt: 1234, DamageTaken: 9}, Encounters: capture.ViewEncounters{Current: &capture.ViewEncounter{Roles: []capture.ViewRole{role}}}, Effects: capture.ViewEffects{Buffs: capture.ViewEffectRow{Effects: []capture.ViewEffect{effect}}, RecentEnded: []capture.ViewEffect{effect}}}
	before, _ := json.Marshal(view)
	source := &service.Snapshot{Sequence: 7, View: &view}
	projection := &displayProjection{resolver: resolver}
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.setLocale("ja")
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw []byte) struct {
		View           capture.ViewSnapshot
		Sequence       uint64
		Locale         string
		LocaleRevision uint64
	} {
		var result struct {
			View           capture.ViewSnapshot
			Sequence       uint64
			Locale         string
			LocaleRevision uint64
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	a, b := decode(first), decode(second)
	if a.View.Roles[0].Name != "Example" || b.View.Roles[0].Name != "テスト" || b.View.Effects.Buffs.Effects[0].TermName != "テスト" || b.View.Effects.Buffs.Effects[0].HolderName != "テスト" || b.View.Effects.RecentEnded[0].TermName != "テスト" {
		t.Fatal("existing names were not relocalized")
	}
	if b.View.Effects.Buffs.Effects[0].TermStatus != "CORRELATED" || !reflect.DeepEqual(a.View.Statistics, b.View.Statistics) || a.Sequence != b.Sequence || b.LocaleRevision <= a.LocaleRevision {
		t.Fatal("locale changed evidence, counters, or sequence")
	}
	after, _ := json.Marshal(view)
	if string(before) != string(after) {
		t.Fatal("projection mutated the core snapshot")
	}
	resolver.Close()
	if _, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{}); err != nil {
		t.Fatal("unchanged projection missed its serialized cache")
	}
}

// The projection relocalizes names and nothing else, so the engine's ranking contract reaches every window unchanged and
// is the same in both build profiles.
func TestDisplayProjectionKeepsTheEnginesRankingContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, _ := sql.Open("sqlite", path)
	for _, table := range []string{"roleinfo_locale", "term_locale"} {
		if _, err := db.Exec("CREATE TABLE " + table + " (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))"); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	store := loadSettings(filepath.Join(t.TempDir(), "settings.json"), testLocaleCatalog())
	share, roleDPS, vehicleDPS := 0.4, 40.0, 60.0
	vehicle := &capture.ViewVehicle{VehicleDamageSnapshot: capture.VehicleDamageSnapshot{Kind: "motorcycle", DamageDealt: 600}, DamageShare: &share, Rank: 0, DPS: &vehicleDPS}
	role := capture.ViewRole{RoleID: 42, DamageDealt: 400, DamageShare: &share, Rank: 1, DPS: &roleDPS}
	encounter := capture.ViewEncounter{EncounterID: "1-1-1", Roles: []capture.ViewRole{role}, Vehicle: vehicle}
	interval := capture.ViewRole{RoleID: 42, DamageDealt: 400, DamageShare: &share, Rank: 0}
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{interval}, Encounters: capture.ViewEncounters{Current: &encounter, Recent: []capture.ViewEncounter{encounter}}}
	projection := &displayProjection{resolver: resolver}
	raw, err := projection.encode(&service.Snapshot{Sequence: 1, View: &view}, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct{ View capture.ViewSnapshot }
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []capture.ViewEncounter{*decoded.View.Encounters.Current, decoded.View.Encounters.Recent[0]} {
		if got := scope.Roles[0]; got.Rank != 1 || got.DPS == nil || *got.DPS != roleDPS || got.DamageShare == nil || *got.DamageShare != share {
			t.Fatalf("role row = %+v", got)
		}
		if got := scope.Vehicle; got.Rank != 0 || got.DPS == nil || *got.DPS != vehicleDPS {
			t.Fatalf("vehicle row = %+v", got)
		}
	}
	if got := decoded.View.Roles[0]; got.Rank != 0 || got.DPS != nil {
		t.Fatalf("interval row = %+v", got)
	}
}

// A trial character is shown like the role it plays: its row carries the parent's RoleId for images, in the live view and in
// a stored encounter (History reads it through the same encounter projection), while the row's own RoleId and its numbers
// stay as recorded.
func TestDisplayProjectionGivesATrialRoleItsParentsArt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, _ := sql.Open("sqlite", path)
	for _, statement := range []string{
		"CREATE TABLE roleinfo_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE roleinfo (id TEXT PRIMARY KEY,element_id TEXT)",
		"CREATE TABLE trialroleinfo (id TEXT PRIMARY KEY,group_id TEXT,type INTEGER,parent_role_id TEXT)",
		"INSERT INTO roleinfo VALUES('1312','6')",
		"INSERT INTO roleinfo_locale VALUES('1312','zh-TW','鎖暝'),('1312','en-US','Fixture role')",
		"INSERT INTO trialroleinfo VALUES('13127004','13127004',1,'1312')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	store := loadSettings(filepath.Join(t.TempDir(), "settings.json"), testLocaleCatalog())
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{{RoleID: 13127004, DamageDealt: 900}, {RoleID: 1312, DamageDealt: 400}, {RoleID: 77}}}
	raw, err := (&displayProjection{resolver: resolver}).encode(&service.Snapshot{Sequence: 3, View: &view}, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	var shown struct{ View capture.ViewSnapshot }
	if err := json.Unmarshal(raw, &shown); err != nil {
		t.Fatal(err)
	}
	trial, owned, unknown := shown.View.Roles[0], shown.View.Roles[1], shown.View.Roles[2]
	if trial.RoleID != 13127004 || trial.StaticRoleID != 1312 || trial.Name != "Fixture role" || trial.ElementID != 6 || trial.DamageDealt != 900 {
		t.Fatalf("trial role row = %+v, want its own RoleId and numbers with the parent's art, name and element", trial)
	}
	if owned.StaticRoleID != 1312 || unknown.StaticRoleID != 77 {
		t.Fatalf("a role that is not a trial keeps its own art: %+v %+v", owned, unknown)
	}
	if view.Roles[0].StaticRoleID != 0 {
		t.Fatal("projection mutated the core snapshot")
	}
	encounter := capture.ViewEncounter{Roles: []capture.ViewRole{{RoleID: 13127004}}}
	(&nameProjection{ctx: t.Context(), resolver: resolver, locale: "zh-TW"}).encounter(&encounter)
	if encounter.Roles[0].StaticRoleID != 1312 {
		t.Fatalf("stored encounter role = %+v, want the parent's art", encounter.Roles[0])
	}
}

func TestShellChangeReencodesWithoutTouchingCoreSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, _ := sql.Open("sqlite", path)
	if _, err := db.Exec("CREATE TABLE roleinfo_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE roleinfo (id TEXT PRIMARY KEY,element_id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO roleinfo VALUES('42','3')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	store := loadSettings(filepath.Join(t.TempDir(), "settings.json"), testLocaleCatalog())
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{{RoleID: 42}, {RoleID: 77}}}
	source := &service.Snapshot{Sequence: 7, View: &view}
	projection := &displayProjection{resolver: resolver}
	decode := func(raw []byte) (result struct {
		Sequence uint64
		View     capture.ViewSnapshot
		Overlays OverlayStates
	}) {
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{Effects: OverlayState{Visible: true, Editing: true}}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	a, b := decode(first), decode(second)
	if a.Overlays.Effects.Visible || !b.Overlays.Effects.Editing || b.Overlays.Combat.Visible || a.Sequence != b.Sequence {
		t.Fatalf("overlay shell: %+v %+v", a.Overlays, b.Overlays)
	}
	if b.View.Roles[0].ElementID != 3 || b.View.Roles[1].ElementID != 0 {
		t.Fatalf("element lookup must be Master data or unknown: %+v", b.View.Roles)
	}
	if view.Roles[0].ElementID != 0 {
		t.Fatal("projection mutated the core snapshot")
	}
}

func TestProjectedShellCarriesUpdateInterval(t *testing.T) {
	store := loadSettings(filepath.Join(t.TempDir(), "settings.json"), testLocaleCatalog())
	projection := &displayProjection{}
	source := &service.Snapshot{Sequence: 3}
	decode := func(raw []byte) (shell struct {
		Sequence         uint64
		UpdateIntervalMs int
		AnimationFps     *int
	}) {
		if err := json.Unmarshal(raw, &shell); err != nil {
			t.Fatal(err)
		}
		return shell
	}
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(first); got.UpdateIntervalMs != 250 || got.AnimationFps != nil {
		t.Fatalf("defaults (the retired frame-rate field must not be published): %+v", got)
	}
	if _, err := store.setUpdateInterval(750); err != nil {
		t.Fatal(err)
	}
	// The same core snapshot is re-encoded with the new shell, so every window sees the change without new data.
	second, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(second); got.UpdateIntervalMs != 750 || got.Sequence != 3 {
		t.Fatalf("after change: %+v", got)
	}
	// A heartbeat never comes before the next scheduled snapshot of a running capture.
	if wait := snapshotWait(store.current().Settings); wait != 1750*time.Millisecond {
		t.Fatalf("heartbeat limit at 750 ms = %v", wait)
	}
	if wait := snapshotWait(Settings{}); wait != 1250*time.Millisecond {
		t.Fatalf("default heartbeat limit = %v", wait)
	}
}

func TestDisplayProjectionRelocalizesSkillNamesWithoutChangingSkillStatistics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, _ := sql.Open("sqlite", path)
	for _, statement := range []string{
		"CREATE TABLE roleinfo_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE skilldescription_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		// The UI skill-tree table holds a different name under the same number; it must not be used.
		"CREATE TABLE skill_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"INSERT INTO skilldescription_locale VALUES('1409011','en-US','Strike'),('1409011','ja-JP','打撃')",
		"INSERT INTO skill_locale VALUES('1409011','en-US','Wrong namespace')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	store := loadSettings(filepath.Join(t.TempDir(), "settings.json"), testLocaleCatalog())
	share := 0.75
	skills := []capture.ViewSkill{{SkillID: 1409011, Name: "Core", NameStatus: "VERIFIED", DamageDealt: 300, DamageShare: &share, OutgoingEvents: 4}, {SkillID: 1409099, DamageDealt: 100, OutgoingEvents: 1}}
	role := capture.ViewRole{RoleID: 42, DamageDealt: 400, Skills: skills}
	vehicle := &capture.ViewVehicle{Skills: []capture.ViewSkill{{SkillID: 1409011, DamageDealt: 9, OutgoingEvents: 1}}}
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{role}, Vehicle: vehicle, Encounters: capture.ViewEncounters{Current: &capture.ViewEncounter{Roles: []capture.ViewRole{role}, Vehicle: vehicle}, Recent: []capture.ViewEncounter{{Roles: []capture.ViewRole{role}}}}}
	before, _ := json.Marshal(view)
	source := &service.Snapshot{Sequence: 3, View: &view}
	projection := &displayProjection{resolver: resolver}
	decode := func(raw []byte) (result struct{ View capture.ViewSnapshot }) {
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.setLocale("ja")
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	a, b := decode(first), decode(second)
	for _, scoped := range []struct {
		name string
		a, b []capture.ViewSkill
	}{
		{"interval", a.View.Roles[0].Skills, b.View.Roles[0].Skills},
		{"current", a.View.Encounters.Current.Roles[0].Skills, b.View.Encounters.Current.Roles[0].Skills},
		{"recent", a.View.Encounters.Recent[0].Roles[0].Skills, b.View.Encounters.Recent[0].Roles[0].Skills},
		{"vehicle", a.View.Encounters.Current.Vehicle.Skills, b.View.Encounters.Current.Vehicle.Skills},
	} {
		if scoped.a[0].Name != "Strike" || scoped.b[0].Name != "打撃" {
			t.Fatalf("%s skill names = %q / %q", scoped.name, scoped.a[0].Name, scoped.b[0].Name)
		}
		for i := range scoped.a {
			left, right := scoped.a[i], scoped.b[i]
			left.Name, right.Name = "", ""
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("%s locale changed skill statistics: %+v / %+v", scoped.name, scoped.a[i], scoped.b[i])
			}
		}
	}
	if unnamed := b.View.Roles[0].Skills[1]; unnamed.Name != "" || unnamed.NameStatus != "UNKNOWN" || unnamed.SkillID != 1409099 {
		t.Fatalf("a skill without an official name must stay UNKNOWN with its ID: %+v", unnamed)
	}
	after, _ := json.Marshal(view)
	if string(before) != string(after) {
		t.Fatal("projection mutated the core snapshot")
	}
}

func TestDisplayProjectionRelocalizesDerivedSkillNamesThroughTheirSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, _ := sql.Open("sqlite", path)
	for _, statement := range []string{
		"CREATE TABLE skilldescription_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE skill_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE roleinfo_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE roleinfo (id TEXT PRIMARY KEY,element_id TEXT)",
		"CREATE TABLE phantomitem_locale (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		// The runtime SkillId shares its number with an unrelated SkillDescription row; the source must decide.
		"INSERT INTO skilldescription_locale VALUES('1006203','en-US','Wrong runtime row')",
		"INSERT INTO skill_locale VALUES('1006203','en-US','Liberation'),('1006203','ja-JP','解放'),('1004406','en-US','Intro'),('1006109','en-US','Herself a Thousand Lanterns'),('1006109','ja-JP','身を千灯と為す'),('1006106','en-US','Glance on a Whim')",
		"INSERT INTO phantomitem_locale VALUES('60001062','en-US','Reminiscence: Fleurdelys')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	resolver, err := capture.OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	store := loadSettings(filepath.Join(t.TempDir(), "settings.json"), testLocaleCatalog())
	skills := []capture.ViewSkill{
		{SkillID: 1312050, Name: "x", NameStatus: "DERIVED", NameSource: "damage_list", SourceID: 1006203, DamageDealt: 300, OutgoingEvents: 4},
		{SkillID: 280026010, Name: "x", NameStatus: "DERIVED", NameSource: "echo", SourceID: 60001062, DamageDealt: 100, OutgoingEvents: 2},
		{SkillID: 1006203, NameStatus: "UNKNOWN", DamageDealt: 5, OutgoingEvents: 1},
		{SkillID: 1409099, Name: "x", NameStatus: "DERIVED", NameSource: "damage_list", SourceID: 1009999, DamageDealt: 1, OutgoingEvents: 1},
		{SkillID: 1209400, Name: "x", NameStatus: "DERIVED", NameSource: "skill_type", SourceID: 1004406, DamageDealt: 2, OutgoingEvents: 1},
	}
	outro := capture.ViewUnassignedSkill{UnassignedSkillTotals: capture.UnassignedSkillTotals{DamageDealt: 19012, OutgoingEvents: 8, MissingSkillEvents: 8}, Name: "x", NameStatus: "DERIVED", NameSource: "damage_type", SourceID: 1006109, IconKind: "skill-config", IconID: 1006109}
	unnamed := capture.ViewUnassignedSkill{UnassignedSkillTotals: capture.UnassignedSkillTotals{DamageDealt: 3, OutgoingEvents: 1, MissingSkillEvents: 1}, UnnamedReason: "no_relation"}
	stale := capture.ViewUnassignedSkill{UnassignedSkillTotals: capture.UnassignedSkillTotals{DamageDealt: 4, OutgoingEvents: 1, MissingSkillEvents: 1}, Name: "x", NameStatus: "DERIVED", NameSource: "damage_type", SourceID: 1009999, IconKind: "skill-config", IconID: 1009999}
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{{RoleID: 1312, DamageDealt: 406, Skills: skills, UnassignedSkill: outro}, {RoleID: 1311, DamageDealt: 3, UnassignedSkill: unnamed}, {RoleID: 1209, DamageDealt: 4, UnassignedSkill: stale}}}
	source := &service.Snapshot{Sequence: 4, View: &view}
	projection := &displayProjection{resolver: resolver}
	decode := func(raw []byte) (result struct{ View capture.ViewSnapshot }) {
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.setLocale("ja")
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	en, ja := decode(first).View.Roles[0].Skills, decode(second).View.Roles[0].Skills
	want := []struct{ en, ja, status string }{{"Liberation", "解放", "DERIVED"}, {"Reminiscence: Fleurdelys", "Reminiscence: Fleurdelys", "DERIVED"}, {"Wrong runtime row", "Wrong runtime row", "VERIFIED"}, {"", "", "UNKNOWN"}, {"Intro", "Intro", "DERIVED"}}
	for i, w := range want {
		if en[i].Name != w.en || ja[i].Name != w.ja || en[i].NameStatus != w.status || ja[i].NameStatus != w.status {
			t.Fatalf("row %d: en=%q/%s ja=%q/%s, want %q/%q/%s", i, en[i].Name, en[i].NameStatus, ja[i].Name, ja[i].NameStatus, w.en, w.ja, w.status)
		}
		if en[i].NameSource != skills[i].NameSource || en[i].SourceID != skills[i].SourceID || en[i].DamageDealt != skills[i].DamageDealt || en[i].OutgoingEvents != skills[i].OutgoingEvents {
			t.Fatalf("row %d: projection changed the row: %+v", i, en[i])
		}
	}
	enOutro, jaOutro := decode(first).View.Roles[0].UnassignedSkill, decode(second).View.Roles[0].UnassignedSkill
	if enOutro.Name != "Herself a Thousand Lanterns" || jaOutro.Name != "身を千灯と為す" || enOutro.NameStatus != "DERIVED" || enOutro.NameSource != "damage_type" || enOutro.DamageDealt != 19012 || enOutro.MissingSkillEvents != 8 {
		t.Fatalf("Outro row: en=%+v ja=%+v", enOutro, jaOutro)
	}
	if plain := decode(first).View.Roles[1].UnassignedSkill; plain.Name != "" || plain.UnnamedReason != "no_relation" || plain.DamageDealt != 3 {
		t.Fatalf("an unnamed row was changed: %+v", plain)
	}
	// A source whose record has no name in any locale falls back to plainly unassigned.
	if gone := decode(first).View.Roles[2].UnassignedSkill; gone.Name != "" || gone.NameSource != "" || gone.SourceID != 0 || gone.IconKind != "" || gone.DamageDealt != 4 {
		t.Fatalf("a source without a name kept its reference: %+v", gone)
	}
}
