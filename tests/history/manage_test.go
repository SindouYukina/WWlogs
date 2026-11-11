package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func countRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// twoEncounters records two finished encounters in one span with combat events and effect changes in and between them.
func twoEncounters(t *testing.T, store *Store) (first, second int64) {
	t.Helper()
	start := time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC).Local()
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	entries := finishedEncounter(1, 1, start, 10, 100, capture.IntegrityIntact)
	entries = append(entries, finishedEncounter(1, 2, at(60), 10, 200, capture.IntegrityIntact)...)
	event := func(sequence uint64, seconds int, encounter string) []capture.HistoryEntry {
		record := capture.CombatEventRecord{Kind: capture.CombatEventDamage, Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: sequence}, ObservedAt: at(seconds),
			Flow: capture.CombatFlowLocalOutgoing, Confidence: "confirmed", Evidence: "rpc", Amount: 10}
		out := []capture.HistoryEntry{{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &record}}
		if encounter != "" {
			out = append(out, capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: sequence, Encounter: encounter})
		}
		return out
	}
	entries = append(entries, event(1, 2, "1-1")...)
	entries = append(entries, event(2, 30, "")...)
	entries = append(entries, event(3, 62, "1-2")...)
	// Instance 1 lives inside the first encounter; instance 2 starts in it and ends in the second.
	entries = append(entries,
		effectChange(1, 1, 1, capture.EffectChangeApply, capture.EffectObserved, at(1), nil),
		effectChange(2, 1, 1, capture.EffectChangeRemove, capture.EffectObserved, at(5), func(e *capture.HistoryEffect) { e.Reason = "confirmed_removed" }),
		effectChange(3, 2, 1, capture.EffectChangeApply, capture.EffectObserved, at(6), nil),
		effectChange(4, 2, 3, capture.EffectChangeRemove, capture.EffectObserved, at(65), func(e *capture.HistoryEffect) { e.Reason = "confirmed_removed" }),
	)
	record(t, store, liveStart, entries)
	day, err := store.Day(context.Background(), start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 2 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	return day.Encounters[0].ID, day.Encounters[1].ID
}

func TestDeletingAnEncounterRemovesItsResultsEventsAndEffects(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	first, second := twoEncounters(t, store)
	ctx := context.Background()
	result, err := store.DeleteEncounter(ctx, first)
	if err != nil || result.Encounters != 1 || result.Spans != 0 {
		t.Fatalf("delete = %+v (%v)", result, err)
	}
	if _, err := store.Encounter(ctx, first, time.UTC); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the deleted encounter is still listed: %v", err)
	}
	for table, want := range map[string]int{"encounter": 1, "encounter_contributor": 1, "encounter_enemy": 2, "encounter_timeline": 1, "combat_event": 2, "effect_instance": 1, "effect_event": 1} {
		if got := countRows(t, store, table); got != want {
			t.Fatalf("%s has %d rows, want %d", table, got, want)
		}
	}
	// Instance 2 keeps its change after the deleted encounter, and no longer claims its recorded start.
	var started int
	if err := store.db.QueryRow("SELECT start_recorded FROM effect_instance WHERE instance = 2").Scan(&started); err != nil || started != 0 {
		t.Fatalf("instance 2 start_recorded = %d (%v)", started, err)
	}
	if year, err := store.Year(ctx, 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("the heatmap still counts the deleted encounter: %+v (%v)", year, err)
	}
	assertIntegrity(t, store)
	// Deleting the last encounter removes the span with what it still held outside encounters.
	result, err = store.DeleteEncounter(ctx, second)
	if err != nil || result.Spans != 1 {
		t.Fatalf("second delete = %+v (%v)", result, err)
	}
	for _, table := range []string{"capture_session", "combat_event", "effect_instance", "effect_event", "encounter"} {
		if got := countRows(t, store, table); got != 0 {
			t.Fatalf("%s keeps %d rows", table, got)
		}
	}
	assertIntegrity(t, store)
}

func TestAnEncounterBeingRecordedCannotBeDeleted(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC).Local()
	recorder.Submit(finishedEncounter(1, 1, start, 10, 100, capture.IntegrityIntact)[:2], 0)
	waitQueued(t, recorder)
	day, err := store.Day(context.Background(), start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 || day.Encounters[0].State != "open" {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	if _, err := store.DeleteEncounter(context.Background(), day.Encounters[0].ID); !errors.Is(err, ErrEncounterRecording) {
		t.Fatalf("an encounter in progress was deleted: %v", err)
	}
	if _, err := store.ClearHistory(context.Background()); !errors.Is(err, ErrRecordingActive) {
		t.Fatalf("the history was cleared while recording: %v", err)
	}
	if err := recorder.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
}

func TestClearingTheHistoryRemovesEverythingAndItsCopies(t *testing.T) {
	path := testPath(t)
	createSchemaTwo(t, path)
	store := openTestStore(t, path, nil)
	twoEncounters(t, store)
	backups, _ := filepath.Glob(path + ".schema-*.bak")
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	// Another file of the user beside the history is never touched.
	other := filepath.Join(filepath.Dir(path), "notes.txt")
	if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.ClearHistory(context.Background())
	if err != nil || result.Encounters != 3 || result.Spans != 3 || result.Backups != 1 || !result.Compacted {
		t.Fatalf("clear = %+v (%v)", result, err)
	}
	for _, table := range []string{"capture_session", "encounter", "combat_event", "effect_event", "effect_instance", "capture_gap", "encounter_enemy"} {
		if got := countRows(t, store, table); got != 0 {
			t.Fatalf("%s keeps %d rows", table, got)
		}
	}
	if left, _ := filepath.Glob(path + ".schema-*.bak"); len(left) != 0 {
		t.Fatalf("copies remain: %v", left)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("another file was removed")
	}
	// Compaction leaves no free page behind.
	var free int
	if err := store.db.QueryRow("PRAGMA freelist_count").Scan(&free); err != nil || free != 0 {
		t.Fatalf("free pages after compaction = %d (%v)", free, err)
	}
	assertIntegrity(t, store)
	// The cleared history records again, encrypted with the same key.
	record(t, store, liveStart, finishedEncounter(1, 1, time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC), 10, 100, capture.IntegrityIntact))
	store.Close()
	requireNoPlaintext(t, filepath.Dir(path))
	if year, err := openTestStore(t, path, nil).Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("after clearing: %+v (%v)", year, err)
	}
}

func TestReadOnlyHistoryRefusesDeletion(t *testing.T) {
	path := testPath(t)
	first := openTestStore(t, path, nil)
	twoEncounters(t, first)
	second, _ := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	defer second.Close()
	if _, err := second.ClearHistory(context.Background()); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("a read-only history was cleared: %v", err)
	}
	if _, err := second.DeleteEncounter(context.Background(), 1); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("a read-only history deleted an encounter: %v", err)
	}
}

// TestEveryForeignKeyIsSearchedByAKey keeps deletions fast: SQLite finds the child rows of a deleted parent with a query
// on the child columns, and without statistics its planner must still reach them through a key on all of those columns,
// or it scans a wider range once per deleted parent row.
func TestEveryForeignKeyIsSearchedByAKey(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	tables, err := tableNames(context.Background(), store.db)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, table := range tables {
		children := map[int][]string{}
		rows, err := store.db.Query("SELECT id, \"from\" FROM pragma_foreign_key_list(?) ORDER BY id, seq", table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int
			var column string
			if err := rows.Scan(&id, &column); err != nil {
				t.Fatal(err)
			}
			children[id] = append(children[id], column)
		}
		rows.Close()
		for _, columns := range children {
			// A timeline contributor's buckets are found among one encounter's few buckets through the primary key prefix.
			if table == "encounter_timeline_bucket" {
				continue
			}
			plan := childSearchPlan(t, store, table, columns)
			for _, column := range columns {
				// A single INTEGER PRIMARY KEY column is the rowid itself.
				if !strings.Contains(plan, column+"=?") && !(len(columns) == 1 && strings.Contains(plan, "INTEGER PRIMARY KEY (rowid=?)")) {
					t.Errorf("%s(%v) is searched by %q, not by all of its foreign key columns", table, columns, plan)
				}
			}
			checked++
		}
	}
	if checked < 10 {
		t.Fatalf("only %d foreign keys checked", checked)
	}
}

// childSearchPlan is SQLite's plan for finding the child rows of one parent.
func childSearchPlan(t *testing.T, store *Store, table string, columns []string) string {
	t.Helper()
	conditions := make([]string, 0, len(columns))
	for _, column := range columns {
		conditions = append(conditions, column+" = 1")
	}
	rows, err := store.db.Query("EXPLAIN QUERY PLAN SELECT 1 FROM " + table + " WHERE " + strings.Join(conditions, " AND "))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	return strings.Join(details, "; ")
}
