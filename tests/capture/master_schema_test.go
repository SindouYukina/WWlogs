package capture

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The runtime schema (internal/capture/master_schema.go) is what every Master must provide to the strict loader and the
// display resolver. These tests pin its declaration to its version, prove the tracked Master provides it, prove the check
// names every missing part, and prove a Master holding only the runtime schema loads the same as the tracked one, so the
// declaration is neither short of what the loader reads nor a list of every table of the file.

// masterRuntimeSchemaFingerprintV1 is the fingerprint of runtime schema version 1. A change of the declaration changes it:
// bump masterRuntimeSchemaVersion and set the new fingerprint here in the same change (see master_schema.go).
const masterRuntimeSchemaFingerprintV1 = "487d61099888761edfeca581e9a634fc9e2c63b18e570d9443724b7b80df2a69"

func TestMasterRuntimeSchemaVersionNamesItsDeclaration(t *testing.T) {
	fingerprints := map[int]string{1: masterRuntimeSchemaFingerprintV1}
	want, ok := fingerprints[masterRuntimeSchemaVersion]
	if !ok {
		t.Fatalf("runtime schema version %d has no pinned fingerprint", masterRuntimeSchemaVersion)
	}
	if got := masterRuntimeSchemaFingerprint(); got != want {
		t.Fatalf("the runtime schema declaration changed (fingerprint %s, version %d pins %s): bump masterRuntimeSchemaVersion and pin the new fingerprint", got, masterRuntimeSchemaVersion, want)
	}
}

func TestMasterRuntimeSchemaDeclarationIsWellFormed(t *testing.T) {
	known := map[string]bool{"TEXT": true, "INTEGER": true, "REAL": true}
	seen := map[string]bool{}
	views := 0
	for _, table := range MasterRuntimeSchema() {
		if seen[table.Name] {
			t.Errorf("%s is declared twice", table.Name)
		}
		seen[table.Name] = true
		if table.Scope != "product" && table.Scope != "development" {
			t.Errorf("%s has scope %q", table.Name, table.Scope)
		}
		if len(table.Columns) == 0 {
			t.Errorf("%s declares no column", table.Name)
		}
		if table.View {
			views++
		}
		columns := map[string]bool{}
		for _, column := range table.Columns {
			if columns[column.Name] {
				t.Errorf("%s.%s is declared twice", table.Name, column.Name)
			}
			columns[column.Name] = true
			if table.View && len(column.Types) > 0 {
				t.Errorf("view column %s.%s declares a type; a view's column type is computed", table.Name, column.Name)
			}
			for _, declared := range column.Types {
				if !known[declared] {
					t.Errorf("%s.%s accepts unknown type %q", table.Name, column.Name, declared)
				}
			}
		}
	}
	if views != 1 || len(seen) != 73 {
		t.Errorf("the runtime schema declares %d objects with %d views; this test pins 72 tables and 1 view, update it with the declaration", len(seen), views)
	}
	// MasterRuntimeSchema hands out a copy: changing it changes nothing the check reads.
	copied := MasterRuntimeSchema()
	copied[0].Columns[0].Name = "changed"
	if MasterRuntimeSchema()[0].Columns[0].Name == "changed" {
		t.Fatal("MasterRuntimeSchema returned the declaration itself")
	}
}

func TestTrackedMasterProvidesTheRuntimeSchema(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := checkMasterRuntimeSchema(db); err != nil {
		t.Fatal(err)
	}
}

// emptyRuntimeMaster writes a Master that holds exactly the runtime schema, every table empty.
func emptyRuntimeMaster(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.db")
	completeMasterFixture(t, path)
	return path
}

// TestMasterRuntimeSchemaCheckNamesEveryMissingPart removes, one at a time, every table, view and column of the runtime
// schema from a Master that holds exactly that schema, and gives every typed column another type: the check must refuse
// each one by name. Tables and columns the runtime does not read are accepted.
func TestMasterRuntimeSchemaCheckNamesEveryMissingPart(t *testing.T) {
	path := emptyRuntimeMaster(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Hundreds of schema changes of a throw-away file: no need to wait for the disk after each.
	if _, err := db.Exec("PRAGMA synchronous=OFF; PRAGMA journal_mode=MEMORY"); err != nil {
		t.Fatal(err)
	}
	exec := func(statements ...string) {
		t.Helper()
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	refused := func(what, want string) {
		t.Helper()
		err := checkMasterRuntimeSchema(db)
		switch {
		case err == nil:
			t.Errorf("%s: the check accepted the Master", what)
		case !errors.Is(err, errMasterRuntimeSchema):
			t.Errorf("%s: %v is not a runtime schema error", what, err)
		case !strings.Contains(err.Error(), want):
			t.Errorf("%s: %q does not name %q", what, err, want)
		}
	}
	if err := checkMasterRuntimeSchema(db); err != nil {
		t.Fatalf("a Master holding exactly the runtime schema is refused: %v", err)
	}
	exec("CREATE TABLE research_only(id TEXT)", "ALTER TABLE roleinfo ADD COLUMN research_note TEXT")
	if err := checkMasterRuntimeSchema(db); err != nil {
		t.Fatalf("a table and a column the runtime does not read are refused: %v", err)
	}
	cases := 0
	for _, table := range MasterRuntimeSchema() {
		if table.View {
			continue
		}
		exec("ALTER TABLE " + masterQuote(table.Name) + " RENAME TO masterschema_aside")
		refused("table "+table.Name+" removed", "missing table "+table.Name)
		exec("ALTER TABLE masterschema_aside RENAME TO " + masterQuote(table.Name))
		cases++
		for _, column := range table.Columns {
			exec("ALTER TABLE " + masterQuote(table.Name) + " RENAME COLUMN " + masterQuote(column.Name) + " TO masterschema_aside")
			refused("column "+table.Name+"."+column.Name+" removed", "missing column "+table.Name+"."+column.Name)
			exec("ALTER TABLE " + masterQuote(table.Name) + " RENAME COLUMN masterschema_aside TO " + masterQuote(column.Name))
			cases++
			if len(column.Types) == 0 {
				continue
			}
			other := "BLOB"
			exec("ALTER TABLE "+masterQuote(table.Name)+" RENAME COLUMN "+masterQuote(column.Name)+" TO masterschema_aside",
				"ALTER TABLE "+masterQuote(table.Name)+" ADD COLUMN "+masterQuote(column.Name)+" "+other)
			refused("column "+table.Name+"."+column.Name+" declared "+other, "column "+table.Name+"."+column.Name+" is declared \""+other+"\"")
			exec("ALTER TABLE "+masterQuote(table.Name)+" DROP COLUMN "+masterQuote(column.Name),
				"ALTER TABLE "+masterQuote(table.Name)+" RENAME COLUMN masterschema_aside TO "+masterQuote(column.Name))
			cases++
		}
	}
	exec("DROP VIEW summon_skill_vision_ref")
	refused("view removed", "missing view summon_skill_vision_ref")
	exec("CREATE TABLE summon_skill_vision_ref(summon_type INTEGER, summon_skill_id TEXT, vision_id TEXT, phantom_skill_id TEXT)")
	refused("view replaced by a table", "summon_skill_vision_ref is a table, not a view")
	exec("DROP TABLE summon_skill_vision_ref", masterFixtureSummonView)
	if err := checkMasterRuntimeSchema(db); err != nil {
		t.Fatalf("the restored Master is refused: %v", err)
	}
	// A rename would carry the view along (SQLite rewrites the view), so the base table is dropped and created again.
	exec("DROP TABLE vision")
	refused("a base table of the view removed", "view summon_skill_vision_ref cannot be read")
	for _, table := range MasterRuntimeSchema() {
		if table.Name == "vision" {
			exec(masterFixtureObjectSQL(table))
		}
	}
	if err := checkMasterRuntimeSchema(db); err != nil {
		t.Fatalf("the restored Master is refused: %v", err)
	}
	if cases < 72+211 {
		t.Fatalf("only %d cases ran", cases)
	}
}

// TestEveryMasterEntryPointRefusesAMasterWithoutTheRuntimeSchema: the loader, the desktop data set and the display
// resolver all refuse a Master missing part of the runtime schema with the same error, which names the part and nothing
// of the file's location.
func TestEveryMasterEntryPointRefusesAMasterWithoutTheRuntimeSchema(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	completeMasterFixture(t, path)
	execMasterSQL(t, path, []string{"DROP TABLE lordgymentranceset_locale", "ALTER TABLE buff DROP COLUMN period"})
	_, loadErr := LoadGameMasterDatabase(path, "zh-TW")
	_, dataErr := LoadMasterData(path, "zh-TW")
	_, displayErr := OpenDisplayResolver(path)
	for name, err := range map[string]error{"LoadGameMasterDatabase": loadErr, "LoadMasterData": dataErr, "OpenDisplayResolver": displayErr} {
		if !errors.Is(err, errMasterRuntimeSchema) {
			t.Errorf("%s: %v, want the runtime schema error", name, err)
			continue
		}
		message := err.Error()
		if !strings.Contains(message, "missing table lordgymentranceset_locale") || !strings.Contains(message, "missing column buff.period") {
			t.Errorf("%s does not name both missing parts: %s", name, message)
		}
		if strings.Contains(message, filepath.Dir(path)) || strings.Contains(message, filepath.ToSlash(filepath.Dir(path))) {
			t.Errorf("%s names the file's location: %s", name, message)
		}
	}
}

// TestTrackedMasterProjectedToTheRuntimeSchemaLoadsTheSame drops from a copy of the tracked Master every table and view
// the runtime schema does not list. The copy must load, and to the same Master as the tracked file: so the runtime reads
// nothing outside its declaration. (The Master Phase M2 record compares every layer and locale with the equivalence
// harness; this test keeps the guarantee in every run.)
func TestTrackedMasterProjectedToTheRuntimeSchemaLoadsTheSame(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the tracked Master")
	}
	source := filepath.Join("..", "..", "data", "master_data.db")
	projected := filepath.Join(t.TempDir(), "projected.db")
	vacuumMasterInto(t, source, projected)
	db, err := sql.Open("sqlite", projected)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, table := range MasterRuntimeSchema() {
		declared[table.Name] = true
	}
	rows, err := db.Query("SELECT name, type FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY type DESC, name")
	if err != nil {
		t.Fatal(err)
	}
	var drops []string
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			t.Fatal(err)
		}
		if !declared[name] {
			drops = append(drops, "DROP "+strings.ToUpper(kind)+" "+masterQuote(name))
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if len(drops) < 200 {
		t.Fatalf("only %d objects lie outside the runtime schema", len(drops))
	}
	execMasterSQL(t, projected, drops)
	full, err := LoadGameMasterDatabase(source, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	minimal, err := LoadGameMasterDatabase(projected, "zh-TW")
	if err != nil {
		t.Fatalf("a Master holding only the runtime schema does not load: %v", err)
	}
	fullEntries, minimalEntries := masterLoadedEntries(full), masterLoadedEntries(minimal)
	if !reflect.DeepEqual(fullEntries, minimalEntries) {
		differences := 0
		for field, entries := range fullEntries {
			if !reflect.DeepEqual(entries, minimalEntries[field]) {
				differences++
				t.Errorf("%s differs without the %d objects outside the runtime schema", field, len(drops))
			}
		}
		t.Fatalf("%d loaded fields differ", differences)
	}
}

func BenchmarkMasterRuntimeSchemaCheck(b *testing.B) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	b.ReportAllocs()
	for range b.N {
		if err := checkMasterRuntimeSchema(db); err != nil {
			b.Fatal(err)
		}
	}
}

// completeMasterFixture adds to the fixture Master at path every table, view and column of the runtime schema that it
// lacks: empty, and declared with the first type the runtime accepts. A fixture that creates only what its test needs
// then has the whole structure the strict loader requires; nothing the fixture created changes, apart from columns
// added to its tables. tests/devcapture and tests/desktop keep copies; change them with this one.
func completeMasterFixture(t testing.TB, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := completeMasterSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func completeMasterSchema(db *sql.DB) error {
	for _, table := range MasterRuntimeSchema() {
		var kind string
		switch err := db.QueryRow("SELECT type FROM sqlite_master WHERE name = ?", table.Name).Scan(&kind); {
		case err == sql.ErrNoRows:
			if _, err := db.Exec(masterFixtureObjectSQL(table)); err != nil {
				return err
			}
			continue
		case err != nil:
			return err
		}
		if table.View {
			continue
		}
		present := map[string]bool{}
		rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table.Name)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			present[name] = true
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, column := range table.Columns {
			if !present[column.Name] {
				if _, err := db.Exec("ALTER TABLE " + masterQuote(table.Name) + " ADD COLUMN " + masterFixtureColumnSQL(column)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func masterFixtureColumnSQL(column MasterSchemaColumn) string {
	if len(column.Types) == 0 {
		return masterQuote(column.Name)
	}
	return masterQuote(column.Name) + " " + column.Types[0]
}

// masterFixtureObjectSQL creates a declared table with its columns, or a declared view as an empty one.
func masterFixtureObjectSQL(table MasterSchemaTable) string {
	columns := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		if table.View {
			columns[i] = "NULL AS " + masterQuote(column.Name)
		} else {
			columns[i] = masterFixtureColumnSQL(column)
		}
	}
	if table.View {
		return "CREATE VIEW " + masterQuote(table.Name) + " AS SELECT " + strings.Join(columns, ", ") + " WHERE 0"
	}
	return "CREATE TABLE " + masterQuote(table.Name) + " (" + strings.Join(columns, ", ") + ")"
}
