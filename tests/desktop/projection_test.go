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
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{role}, Statistics: capture.ViewStatistics{DamageDealt: 1234, DamageTaken: 9}, Encounters: capture.ViewEncounters{Current: &capture.ViewEncounter{Roles: []capture.ViewRole{role}}}, Effects: capture.ViewEffects{Active: []capture.ViewEffect{effect}, RecentEnded: []capture.ViewEffect{effect}}}
	before, _ := json.Marshal(view)
	source := &service.Snapshot{Sequence: 7, View: &view}
	projection := &displayProjection{resolver: resolver}
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.setLocale("ja")
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
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
	if a.View.Roles[0].Name != "Example" || b.View.Roles[0].Name != "テスト" || b.View.Effects.Active[0].TermName != "テスト" || b.View.Effects.Active[0].HolderName != "テスト" || b.View.Effects.RecentEnded[0].TermName != "テスト" {
		t.Fatal("existing names were not relocalized")
	}
	if b.View.Effects.Active[0].TermStatus != "CORRELATED" || !reflect.DeepEqual(a.View.Statistics, b.View.Statistics) || a.Sequence != b.Sequence || b.LocaleRevision <= a.LocaleRevision {
		t.Fatal("locale changed evidence, counters, or sequence")
	}
	after, _ := json.Marshal(view)
	if string(before) != string(after) {
		t.Fatal("projection mutated the core snapshot")
	}
	resolver.Close()
	if _, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}); err != nil {
		t.Fatal("unchanged projection missed its serialized cache")
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
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{Effects: OverlayState{Visible: true, Editing: true}})
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
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
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
	second, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
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
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.setLocale("ja")
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
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
		"INSERT INTO skill_locale VALUES('1006203','en-US','Liberation'),('1006203','ja-JP','解放')",
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
	}
	view := capture.ViewSnapshot{Roles: []capture.ViewRole{{RoleID: 1312, DamageDealt: 406, Skills: skills}}}
	source := &service.Snapshot{Sequence: 4, View: &view}
	projection := &displayProjection{resolver: resolver}
	decode := func(raw []byte) (result struct{ View capture.ViewSnapshot }) {
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, err := projection.encode(source, store.current(), testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.setLocale("ja")
	if err != nil {
		t.Fatal(err)
	}
	second, err := projection.encode(source, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{})
	if err != nil {
		t.Fatal(err)
	}
	en, ja := decode(first).View.Roles[0].Skills, decode(second).View.Roles[0].Skills
	want := []struct{ en, ja, status string }{{"Liberation", "解放", "DERIVED"}, {"Reminiscence: Fleurdelys", "Reminiscence: Fleurdelys", "DERIVED"}, {"Wrong runtime row", "Wrong runtime row", "VERIFIED"}, {"", "", "UNKNOWN"}}
	for i, w := range want {
		if en[i].Name != w.en || ja[i].Name != w.ja || en[i].NameStatus != w.status || ja[i].NameStatus != w.status {
			t.Fatalf("row %d: en=%q/%s ja=%q/%s, want %q/%q/%s", i, en[i].Name, en[i].NameStatus, ja[i].Name, ja[i].NameStatus, w.en, w.ja, w.status)
		}
		if en[i].NameSource != skills[i].NameSource || en[i].SourceID != skills[i].SourceID || en[i].DamageDealt != skills[i].DamageDealt || en[i].OutgoingEvents != skills[i].OutgoingEvents {
			t.Fatalf("row %d: projection changed the row: %+v", i, en[i])
		}
	}
}
