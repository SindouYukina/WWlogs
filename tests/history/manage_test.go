package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	recorder := begin(t, store, liveStart)
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

func TestClearingTheHistoryRemovesEverything(t *testing.T) {
	path := testPath(t)
	store := openTestStore(t, path, nil)
	twoEncounters(t, store)
	record(t, store, liveStart, finishedEncounter(1, 3, time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC), 10, 300, capture.IntegrityIntact))
	// Another file of the user beside the history is never touched.
	other := filepath.Join(filepath.Dir(path), "notes.txt")
	if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.ClearHistory(context.Background())
	if err != nil || result.Encounters != 3 || result.Spans != 2 || !result.Compacted {
		t.Fatalf("clear = %+v (%v)", result, err)
	}
	for _, table := range []string{"capture_session", "encounter", "combat_event", "effect_event", "effect_instance", "capture_gap", "encounter_enemy"} {
		if got := countRows(t, store, table); got != 0 {
			t.Fatalf("%s keeps %d rows", table, got)
		}
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

// aliveBefore lists, for every instance of span with a change before at, the sequence of its last change before at, read
// two ways: by scanning every change of the span before at (what the analysis read before effect_instance_by_life), and
// through the instances' stored life as the analysis reads it now. Both must name the same changes.
func aliveBefore(t *testing.T, store *Store, span int64, at int64) (scanned, byLife map[int64]int64) {
	t.Helper()
	read := func(query string, args ...any) map[int64]int64 {
		rows, err := store.db.Query(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[int64]int64{}
		for rows.Next() {
			var instance, sequence int64
			var change string
			if err := rows.Scan(&instance, &sequence, &change); err != nil {
				t.Fatal(err)
			}
			if !terminalChanges[change] {
				out[instance] = sequence
			}
		}
		return out
	}
	scanned = read("SELECT ev.instance, max(ev.sequence), ev.change_kind FROM effect_event ev WHERE ev.capture_session_id = ? AND ev.observed_at_ns < ? GROUP BY ev.instance", span, at)
	byLife = read(`SELECT ev.instance, ev.sequence, ev.change_kind FROM effect_instance i JOIN effect_event ev ON ev.capture_session_id = i.capture_session_id AND ev.instance = i.instance
		AND ev.sequence = (SELECT e.sequence FROM effect_event e WHERE e.capture_session_id = i.capture_session_id AND e.instance = i.instance AND +e.observed_at_ns < ? ORDER BY e.sequence DESC LIMIT 1)
		WHERE i.capture_session_id = ? AND i.first_change_ns < ? AND (i.ended_at_ns IS NULL OR i.ended_at_ns >= ?)`, at, span, at, at)
	return scanned, byLife
}

// TestInstanceLifeFindsTheInstancesAliveAtAnyMoment checks, at every second of a span and again after a deletion removed the
// changes of an encounter, that the stored life of each effect instance finds exactly the instances a scan of all earlier
// changes finds alive.
func TestInstanceLifeFindsTheInstancesAliveAtAnyMoment(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	first, _ := twoEncounters(t, store)
	var span, from int64
	if err := store.db.QueryRow("SELECT capture_session_id, started_at_ns FROM encounter WHERE id = ?", first).Scan(&span, &from); err != nil {
		t.Fatal(err)
	}
	check := func(stage string) {
		for second := int64(-1); second <= 80; second++ {
			at := from + second*int64(time.Second)
			scanned, byLife := aliveBefore(t, store, span, at)
			if !reflect.DeepEqual(scanned, byLife) {
				t.Fatalf("%s, %d s: scanning finds %v alive, the stored life %v", stage, second, scanned, byLife)
			}
		}
	}
	check("before the deletion")
	if _, err := store.DeleteEncounter(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	check("after the deletion")
}

func TestDeletingSeveralEncountersRemovesThemInOneDeletion(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	first, second := twoEncounters(t, store)
	// A second span holding one encounter of its own.
	start := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC).Local()
	record(t, store, liveStart, finishedEncounter(2, 1, start, 10, 300, capture.IntegrityIntact))
	ctx := context.Background()
	day, err := store.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	third := day.Encounters[0].ID
	// A repeated id counts once.
	result, err := store.DeleteEncounters(ctx, []int64{first, third, first})
	if err != nil || result.Encounters != 2 || result.Spans != 1 {
		t.Fatalf("delete = %+v (%v)", result, err)
	}
	for _, id := range []int64{first, third} {
		if _, err := store.Encounter(ctx, id, time.UTC); !errors.Is(err, ErrNotFound) {
			t.Fatalf("encounter %d is still listed: %v", id, err)
		}
	}
	if _, err := store.Encounter(ctx, second, time.UTC); err != nil {
		t.Fatalf("the encounter that was not chosen is gone: %v", err)
	}
	// The first span keeps the second encounter and what lies outside the deleted one, as a single deletion leaves it; the span
	// left without encounters is gone with everything it held.
	for table, want := range map[string]int{"capture_session": 1, "encounter": 1, "combat_event": 2, "effect_instance": 1, "effect_event": 1} {
		if got := countRows(t, store, table); got != want {
			t.Fatalf("%s has %d rows, want %d", table, got, want)
		}
	}
	if year, err := store.Year(ctx, 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("the heatmap still counts deleted encounters: %+v (%v)", year, err)
	}
	assertIntegrity(t, store)
	if result, err := store.DeleteEncounters(ctx, nil); err != nil || result != (DeleteResult{}) {
		t.Fatalf("an empty deletion = %+v (%v)", result, err)
	}
}

func TestDeletingSeveralEncountersIsAllOrNothing(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	first, second := twoEncounters(t, store)
	ctx := context.Background()
	tables := []string{"capture_session", "encounter", "combat_event", "effect_instance", "effect_event"}
	before := map[string]int{}
	for _, table := range tables {
		before[table] = countRows(t, store, table)
	}
	// An unknown id refuses the deletion of the known one chosen with it, even after that one was removed in the transaction.
	if _, err := store.DeleteEncounters(ctx, []int64{first, second + 1000}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a deletion with an unknown encounter = %v", err)
	}
	for _, table := range tables {
		if got := countRows(t, store, table); got != before[table] {
			t.Fatalf("after the refused deletion %s has %d rows, want %d", table, got, before[table])
		}
	}
	// So does an encounter still being recorded.
	recorder := begin(t, store, liveStart)
	start := time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC).Local()
	recorder.Submit(finishedEncounter(2, 1, start, 10, 100, capture.IntegrityIntact)[:2], 0)
	waitQueued(t, recorder)
	day, err := store.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var open int64
	for _, encounter := range day.Encounters {
		if encounter.State == "open" {
			open = encounter.ID
		}
	}
	if open == 0 {
		t.Fatalf("no encounter in progress: %+v", day.Encounters)
	}
	if _, err := store.DeleteEncounters(ctx, []int64{second, open}); !errors.Is(err, ErrEncounterRecording) {
		t.Fatalf("a deletion with an encounter in progress = %v", err)
	}
	for _, id := range []int64{first, second} {
		if _, err := store.Encounter(ctx, id, time.UTC); err != nil {
			t.Fatalf("encounter %d was deleted by a refused deletion: %v", id, err)
		}
	}
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	assertIntegrity(t, store)
}

func TestDeletionReadsAnInstanceLifeByItsKey(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	// The temporary table lives on one connection, so the plan is read on the same one.
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS deleted_effect_instance (instance INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.QueryContext(ctx, "EXPLAIN QUERY PLAN "+instanceLifeUpdate, 1)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	rows.Close()
	plan := strings.Join(steps, " | ")
	// Answering min() through effect_event_by_time walks the span's changes in time order until it meets the instance, about
	// 5 s per deleted encounter of a real capture; both correlated reads must use the instance's own key range.
	if strings.Contains(plan, "effect_event_by_time") || strings.Count(plan, "SEARCH e USING PRIMARY KEY (capture_session_id=? AND instance=?)") != 2 {
		t.Fatalf("the instance life update reads effect_event by %s", plan)
	}
}
