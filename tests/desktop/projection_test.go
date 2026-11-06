package desktop

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
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
