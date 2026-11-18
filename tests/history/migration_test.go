package history

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Moving v0.7.0 histories to a derived key (migrate.go). Every fixture is a v0.7.0 history (legacyStore) with 100 encounters
// in its file and 40 only in its write-ahead log, plus effect changes; the derived key comes from the synthetic inputs of the
// first vector.

const (
	migrationCheckpointed = 100
	migrationInLog        = 40
)

// migrationSteps are the steps migrateLegacy and cleanUpMigration name, in order.
var migrationSteps = []string{"copied", "verified", "closed", "marked", "moved-old", "moved-copy", "recorded", "cleanup-marked", "opened", "removed-old"}

// legacyMigrationFixture is a v0.7.0 history with committed encounters in its file and in its write-ahead log.
func legacyMigrationFixture(t *testing.T) string {
	t.Helper()
	path := testPath(t)
	store := legacyStore(t, path)
	recordOneEncounter(t, store)
	return logFixtureOf(t, store, migrationCheckpointed, migrationInLog)
}

// legacyTables reads what a v0.7.0 history holds, from a copy of it opened with its own key.
func legacyTables(t *testing.T, path string) historyTables {
	t.Helper()
	copied := filepath.Join(t.TempDir(), "read", "combat-history.db")
	copyHistoryFiles(t, path, copied)
	db, err := openEncrypted(copied, mustLoadKey(t, copied), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tables, err := readHistoryTables(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return tables
}

// leftoverMigrationFiles lists the files a finished or undone migration must not leave beside path.
func leftoverMigrationFiles(path string) []string {
	var left []string
	for _, file := range append(databaseFiles(historyFile(path, migratingSuffix)), append(databaseFiles(historyFile(path, legacySuffix)), historyFile(path, migrationSuffix))...) {
		if _, err := os.Lstat(file); err == nil {
			left = append(left, filepath.Base(file))
		}
	}
	return left
}

// requireMigrated checks that the store opened the history at path with the derived key, holds want, and that nothing of the
// v0.7.0 history or of the migration is left.
func requireMigrated(t *testing.T, store *Store, path string, want historyTables) {
	t.Helper()
	status := store.Status()
	if status.State != StateReady || status.KeyProtection != keyProtectionDerived {
		t.Fatalf("status = %+v", status)
	}
	if hex.EncodeToString(store.key) != kdfVectors[0].key {
		t.Fatal("the moved history is not keyed with the derived key")
	}
	got, err := readHistoryTables(context.Background(), store.db)
	if err != nil {
		t.Fatal(err)
	}
	if !got.equal(want) {
		t.Fatal("the moved history does not hold the rows of the v0.7.0 history")
	}
	if left := leftoverMigrationFiles(path); len(left) > 0 {
		t.Fatalf("the migration left %v", left)
	}
	if _, err := os.Stat(keyPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the v0.7.0 key file is still there (%v)", err)
	}
	if readRecord(t, path).KeyCheck != kdfVectors[0].check {
		t.Fatal("the record does not describe the derived key")
	}
}

// requireLegacy checks that the store opened the history at path with its v0.7.0 key, as it was, and that the migration left
// nothing.
func requireLegacy(t *testing.T, store *Store, path string, want historyTables) {
	t.Helper()
	status := store.Status()
	if status.State != StateReady || status.KeyProtection != "dpapi-user" || status.Migration != MigrationFailed {
		t.Fatalf("status = %+v", status)
	}
	got, err := readHistoryTables(context.Background(), store.db)
	if err != nil {
		t.Fatal(err)
	}
	if !got.equal(want) {
		t.Fatal("the v0.7.0 history changed")
	}
	if left := leftoverMigrationFiles(path); len(left) > 0 {
		t.Fatalf("the migration left %v", left)
	}
	if _, err := os.Stat(keyPath(path)); err != nil {
		t.Fatalf("the v0.7.0 key file is gone (%v)", err)
	}
}

// TestLegacyHistoryMovesToADerivedKey opens a v0.7.0 history for recording: it is moved to the derived key with every row,
// its key file and the migration files are gone, its views read the same, and it then opens as any derived history.
func TestLegacyHistoryMovesToADerivedKey(t *testing.T) {
	path := legacyMigrationFixture(t)
	want := legacyTables(t, path)
	legacyKey := mustLoadKey(t, path)
	store := openTestStore(t, path, nil)
	if store.Status().Migration != MigrationDone {
		t.Fatalf("status = %+v", store.Status())
	}
	requireMigrated(t, store, path, want)
	if year, err := store.Year(context.Background(), 2026, time.UTC); err != nil || year.Total != migrationCheckpointed+migrationInLog+1 {
		t.Fatalf("year = %+v (%v)", year, err)
	}
	requireNoPlaintext(t, filepath.Dir(path))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// The moved file no longer opens with the v0.7.0 key, and the next open finds nothing to move.
	if outcome, err := preflight(path, legacyKey, true); err != nil || outcome != preflightKeyMismatch {
		t.Fatalf("the v0.7.0 key still decrypts the moved history: %s (%v)", outcome, err)
	}
	reopened := openTestStore(t, path, nil)
	if status := reopened.Status(); status.Migration != "" || status.KeyProtection != keyProtectionDerived {
		t.Fatalf("reopened status = %+v", status)
	}
	assertIntegrity(t, reopened)
}

// TestMigrationThatCannotRunChangesNothing: without a derived key, or when any step up to the switch fails, the v0.7.0
// history stays as it was, opens with its own key, and moves at a later open.
func TestMigrationThatCannotRunChangesNothing(t *testing.T) {
	failing := errors.New("synthetic failure")
	cases := map[string]struct {
		inputs KeyInputs
		step   string
		hook   func(t *testing.T, path string)
	}{
		"no motherboard identity": {inputs: KeyInputs{Machine: func() (Machine, error) { return Machine{}, failing }, ProtocolKey: platformKeyInputs.ProtocolKey}},
		"no protocol key":         {inputs: KeyInputs{Machine: platformKeyInputs.Machine, ProtocolKey: func() ([]byte, error) { return nil, failing }}},
		"a copy that differs": {step: "copied", hook: func(t *testing.T, path string) {
			copied, err := openEncrypted(historyFile(path, migratingSuffix), mustHex(kdfVectors[0].key), false)
			if err != nil {
				t.Fatal(err)
			}
			defer copied.Close()
			if _, err := copied.Exec("DELETE FROM encounter_risk; UPDATE encounter SET damage_dealt = damage_dealt + 1 WHERE id = (SELECT max(id) FROM encounter)"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, step := range migrationSteps[:len(migrationSteps)-1] {
		cases["failure after "+step] = struct {
			inputs KeyInputs
			step   string
			hook   func(t *testing.T, path string)
		}{step: step}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := legacyMigrationFixture(t)
			want := legacyTables(t, path)
			if c.step != "" {
				saved := migrationStep
				t.Cleanup(func() { migrationStep = saved })
				migrationStep = func(step string) error {
					if step != c.step {
						return nil
					}
					if c.hook != nil {
						c.hook(t, path)
						return nil
					}
					return failing
				}
			}
			inputs := c.inputs
			store, err := openWith(path, inputs)
			if err != nil {
				t.Fatal(err)
			}
			requireLegacy(t, store, path, want)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			migrationStep = func(string) error { return nil }
			moved := openTestStore(t, path, nil)
			requireMigrated(t, moved, path, want)
		})
	}
}

// TestInterruptedMigrationIsFinishedOrUndone ends a migrating process at every step, as a crash or power loss would leave the
// files: the next open finishes the migration or undoes it and moves the history again, and in every case it holds every row.
func TestInterruptedMigrationIsFinishedOrUndone(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range migrationSteps {
		t.Run(step, func(t *testing.T) {
			path := legacyMigrationFixture(t)
			want := legacyTables(t, path)
			helper := exec.Command(executable, "-test.run=^TestMigrationHelperProcess$", "-test.count=1")
			helper.Env = append(os.Environ(), "WWLOGS_MIGRATION_HELPER=1", "WWLOGS_MIGRATION_DB="+path, "WWLOGS_MIGRATION_STOP="+step)
			output, err := helper.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("the migrating process did not stop at %s: %v\n%s", step, err, output)
			}
			store := openTestStore(t, path, nil)
			requireMigrated(t, store, path, want)
		})
	}
}

// TestMigrationHelperProcess is not a test of its own: TestInterruptedMigrationIsFinishedOrUndone starts the test binary
// again with WWLOGS_MIGRATION_HELPER set; it then opens the history named by WWLOGS_MIGRATION_DB and ends the process at
// once, without closing anything, when the migration reaches the step WWLOGS_MIGRATION_STOP names.
func TestMigrationHelperProcess(t *testing.T) {
	if os.Getenv("WWLOGS_MIGRATION_HELPER") != "1" {
		t.Skip("runs only as the helper process of another test")
	}
	stop := os.Getenv("WWLOGS_MIGRATION_STOP")
	migrationStep = func(step string) error {
		if step == stop {
			os.Exit(7)
		}
		return nil
	}
	store, err := Open(Options{Path: os.Getenv("WWLOGS_MIGRATION_DB"), Location: time.UTC, Build: &testBuild})
	fmt.Println("helper: the migration never reached", stop, store.Status(), err)
	os.Exit(5)
}

// TestDamagedMigrationRecordPutsTheOldHistoryBack: a migration record that cannot be read, beside an old file moved aside, is
// treated as an unfinished switch: the old history comes back as it was, and moves again.
func TestDamagedMigrationRecordPutsTheOldHistoryBack(t *testing.T) {
	path := legacyMigrationFixture(t)
	want := legacyTables(t, path)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(executable, "-test.run=^TestMigrationHelperProcess$", "-test.count=1")
	helper.Env = append(os.Environ(), "WWLOGS_MIGRATION_HELPER=1", "WWLOGS_MIGRATION_DB="+path, "WWLOGS_MIGRATION_STOP=recorded")
	if output, err := helper.CombinedOutput(); err == nil {
		t.Fatalf("the helper did not stop: %s", output)
	}
	writeFile(t, historyFile(path, migrationSuffix), []byte("{\"stage\": \"swi"))
	if err := os.Remove(keyRecordPath(path)); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, path, nil)
	requireMigrated(t, store, path, want)
}

// TestReaderBesideAMigrationWaits: a second WWlogs that finds a migration in progress beside the recording one does not read
// the files until it is done.
func TestReaderBesideAMigrationWaits(t *testing.T) {
	path := legacyMigrationFixture(t)
	lock, err := acquireWriterLock(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	writeMigrationMarker(historyFile(path, migrationSuffix), migrationMarker{Stage: stageSwitch, Record: newKeyRecord(sourceSystemUUID, mustHex(kdfVectors[0].check), kdfVectors[0].keyID)})
	before := historyDigests(t, filepath.Dir(path))
	reader, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if err == nil || reader.Status().State != StateUnavailable || reader.Status().Reason != ReasonLocked {
		t.Fatalf("reader beside a migration: %+v (%v)", reader.Status(), err)
	}
	reader.Close()
	requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
}

// TestLegacyMigrationCollector moves a real v0.7.0 history, the fixture directory WWLOGS_MIGRATION_FIXTURE names
// (combat-history.db with its key file and logs), in a copy: the fixture is never opened itself. It compares every table
// and the History views before and after, and writes the row counts and the time the move took to WWLOGS_MIGRATION_OUT.
// It needs the current Windows user's DPAPI to unseal the fixture's key; the derived key is the synthetic one of this
// package, so nothing derived from this computer is written.
func TestLegacyMigrationCollector(t *testing.T) {
	fixture, out := os.Getenv("WWLOGS_MIGRATION_FIXTURE"), os.Getenv("WWLOGS_MIGRATION_OUT")
	if fixture == "" || out == "" {
		t.Skip("WWLOGS_MIGRATION_FIXTURE and WWLOGS_MIGRATION_OUT are not set")
	}
	path := copyFixture(t, fixture)
	want := legacyTables(t, path)
	// The views of the v0.7.0 history, read without moving it.
	legacyView, err := openWith(path, KeyInputs{Machine: func() (Machine, error) { return Machine{}, errors.New("not now") }, ProtocolKey: platformKeyInputs.ProtocolKey})
	if err != nil {
		t.Fatal(err)
	}
	before := migrationViews(t, legacyView)
	if err := legacyView.Close(); err != nil {
		t.Fatal(err)
	}
	fileSize := func(name string) int64 {
		info, err := os.Stat(name)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	sizes := map[string]int64{"db": fileSize(path), "wal": fileSize(path + "-wal")}
	// Each step's share of the move, from the steps migrateLegacy and cleanUpMigration name.
	steps := map[string]string{}
	saved := migrationStep
	stepStarted := time.Now()
	migrationStep = func(step string) error {
		steps[step] = time.Since(stepStarted).Round(time.Millisecond).String()
		stepStarted = time.Now()
		return nil
	}
	started := time.Now()
	store, err := openWith(path, KeyInputs{})
	elapsed := time.Since(started)
	migrationStep = saved
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireMigrated(t, store, path, want)
	after := migrationViews(t, store)
	if after != before {
		t.Fatalf("the views changed:\nbefore %s\nafter  %s", before, after)
	}
	rows := map[string]int64{}
	for name, table := range want.tables {
		rows[name] = table.rows
	}
	summary := map[string]any{"tables": len(want.tables), "rows": rows, "legacyBytes": sizes, "migratedBytes": fileSize(path), "openWithMigration": elapsed.String(), "steps": steps, "viewsEqual": true}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(summary, "", "  ")
	writeFile(t, filepath.Join(out, "migration-summary.json"), data)
	t.Logf("moved %d tables in %s: %s", len(want.tables), elapsed, strings.ReplaceAll(string(data), "\n", " "))
}

// migrationViews is what the History views answer for a store: every year's totals, the library index and each listed
// encounter's stored result.
func migrationViews(t *testing.T, store *Store) string {
	t.Helper()
	ctx := context.Background()
	var text strings.Builder
	for year := 2025; year <= 2027; year++ {
		view, err := store.Year(ctx, year, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&text, "%d:%d:%v;", year, view.Total, view.Days)
	}
	library, err := store.Library(ctx, LibraryQuery{Limit: MaxLibraryPage}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&text, "library %d;", library.Total)
	for _, entry := range library.Encounters {
		detail, err := store.Encounter(ctx, entry.ID, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(detail.Snapshot)
		fmt.Fprintf(&text, "%d:%s:%x;", entry.ID, entry.State, encoded)
	}
	return text.String()
}
