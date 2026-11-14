package history

import (
	"context"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// The History Library lists stored encounters across dates, and gives each record the class its stored fields support.

func libraryIDs(view LibraryView) []int64 {
	var ids []int64
	for _, entry := range view.Encounters {
		ids = append(ids, entry.ID)
	}
	return ids
}

func libraryClass(t *testing.T, view LibraryView, id int64) string {
	t.Helper()
	for _, entry := range view.Encounters {
		if entry.ID == id {
			return entry.Class
		}
	}
	t.Fatalf("encounter %d is not listed in %v", id, libraryIDs(view))
	return ""
}

// listedID returns the id of the encounter that started at start.
func listedID(t *testing.T, store *Store, start time.Time) int64 {
	t.Helper()
	var id int64
	if err := store.db.QueryRowContext(context.Background(), "SELECT id FROM encounter WHERE started_at_ns = ?", start.UnixNano()).Scan(&id); err != nil {
		t.Fatalf("no encounter started at %s: %v", start, err)
	}
	return id
}

func TestLibraryClassesFollowStoredFacts(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	intact := finishedEncounter(1, 1, base, 10, 300, capture.IntegrityIntact)
	degraded := finishedEncounter(1, 2, base.Add(time.Hour), 10, 400, capture.IntegrityDegraded)
	unreliable := finishedEncounter(1, 3, base.Add(2*time.Hour), 10, 500, capture.IntegrityUnreliable)
	lost := finishedEncounter(1, 4, base.Add(3*time.Hour), 10, 600, capture.IntegrityIntact)
	assigned := capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: 99, Encounter: "1-4"}
	record(t, store, liveStart, intact, degraded, unreliable, []capture.HistoryEntry{lost[0], assigned, lost[1], lost[2]})
	view, err := store.Library(context.Background(), LibraryQuery{}, time.UTC)
	if err != nil || view.Total != 4 {
		t.Fatalf("library = %+v (%v)", view, err)
	}
	for start, want := range map[time.Time]string{base: ClassComplete, base.Add(time.Hour): ClassDegraded, base.Add(2 * time.Hour): ClassUnreliable, base.Add(3 * time.Hour): ClassIncomplete} {
		if got := libraryClass(t, view, listedID(t, store, start)); got != want {
			t.Errorf("encounter at %s: class %s, want %s", start.Format(time.Kitchen), got, want)
		}
	}
	// Newest first by default.
	if ids := libraryIDs(view); ids[0] != listedID(t, store, base.Add(3*time.Hour)) {
		t.Fatalf("order = %v", ids)
	}
	only, err := store.Library(context.Background(), LibraryQuery{Classes: []string{ClassComplete}}, time.UTC)
	if err != nil || only.Total != 1 || only.Encounters[0].Class != ClassComplete {
		t.Fatalf("complete only = %+v (%v)", only, err)
	}
}

// resultEntries are accepted damage results of protocol session 1 with the given stored keys, assigned to an encounter.
func resultEntries(first uint64, encounter string, at time.Time, keys ...uint64) []capture.HistoryEntry {
	var entries []capture.HistoryEntry
	for i, key := range keys {
		sequence := first + uint64(i)
		entries = append(entries,
			capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &capture.CombatEventRecord{Kind: capture.CombatEventDamage,
				Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: key}, ObservedAt: at.Add(time.Duration(i) * time.Millisecond), Evidence: "damage_rpc_response",
				Flow: capture.CombatFlowLocalOutgoing, Confidence: capture.AttributionConfirmed, Amount: 10,
				Source: capture.CombatEndpoint{EntityID: 1, Relation: "LOCAL"}, Target: capture.CombatEndpoint{EntityID: 2, Relation: "UNRELATED"}}},
			capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: sequence, Encounter: encounter})
	}
	return entries
}

// An analyser that keyed results by the raw RequestId left keys that restart after the counter's top; the encounters from
// there on are known_affected. One that continued the RequestId past the wrap left rising keys and nothing is flagged.
func TestLibraryFlagsRecordsAfterAnUnhandledRequestIdWrap(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	build := func(t *testing.T, beforeKeys, afterKeys []uint64) (*Store, int64, int64) {
		store := openTestStore(t, testPath(t), nil)
		first := finishedEncounter(1, 1, base, 10, 300, capture.IntegrityIntact)
		second := finishedEncounter(1, 2, base.Add(time.Minute), 10, 400, capture.IntegrityIntact)
		batch := append([]capture.HistoryEntry{first[0], first[1]}, resultEntries(1, "1-1", base.Add(time.Second), beforeKeys...)...)
		batch = append(batch, first[2], second[0], second[1])
		batch = append(batch, resultEntries(100, "1-2", base.Add(time.Minute+time.Second), afterKeys...)...)
		record(t, store, liveStart, append(batch, second[2]))
		return store, listedID(t, store, base), listedID(t, store, base.Add(time.Minute))
	}
	t.Run("raw keys restart", func(t *testing.T) {
		store, before, after := build(t, []uint64{32760, 32765, 32767}, []uint64{0, 1, 2})
		view, err := store.Library(ctx, LibraryQuery{}, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if got := libraryClass(t, view, before); got != ClassComplete {
			t.Errorf("before the wrap: %s", got)
		}
		if got := libraryClass(t, view, after); got != ClassKnownAffected {
			t.Errorf("after the wrap: %s, want %s", got, ClassKnownAffected)
		}
	})
	t.Run("continued keys", func(t *testing.T) {
		store, before, after := build(t, []uint64{32760, 32765, 32767}, []uint64{32768, 32769, 32770})
		view, err := store.Library(ctx, LibraryQuery{}, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if libraryClass(t, view, before) != ClassComplete || libraryClass(t, view, after) != ClassComplete {
			t.Errorf("continued keys flagged: %+v", view.Encounters)
		}
	})
	t.Run("short session", func(t *testing.T) {
		store, _, after := build(t, []uint64{100, 200}, []uint64{1, 2})
		view, err := store.Library(ctx, LibraryQuery{}, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		// Keys that never reached the counter's top do not show a wrap, whatever their order.
		if got := libraryClass(t, view, after); got != ClassComplete {
			t.Errorf("short session flagged: %s", got)
		}
	})
}

func TestLibraryFiltersSortsAndPagesAcrossDates(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	var batches [][]capture.HistoryEntry
	for day := 0; day < 5; day++ {
		start := time.Date(2026, 9, 10+day, 12, 0, 0, 0, time.UTC)
		batches = append(batches, finishedEncounter(1, uint64(day+1), start, 10*(day+1), int64(1000*(day+1)), capture.IntegrityIntact))
	}
	record(t, store, liveStart, batches...)
	dated, err := store.Library(ctx, LibraryQuery{From: "2026-09-11", To: "2026-09-13"}, time.UTC)
	if err != nil || dated.Total != 3 {
		t.Fatalf("dated = %d (%v)", dated.Total, err)
	}
	if dated.Facets.FirstDate != "2026-09-10" || dated.Facets.LastDate != "2026-09-14" {
		t.Fatalf("facet dates = %s..%s", dated.Facets.FirstDate, dated.Facets.LastDate)
	}
	if len(dated.Facets.GameVersions) != 1 || dated.Facets.GameVersions[0].Value != capture.CurrentGameVersion() || dated.Facets.GameVersions[0].Count != 3 {
		t.Fatalf("version facet = %+v", dated.Facets.GameVersions)
	}
	if dated.Facets.Roles[1505] != 3 {
		t.Fatalf("role facet = %+v", dated.Facets.Roles)
	}
	long, err := store.Library(ctx, LibraryQuery{MinSeconds: 25, Sort: SortDurationDesc}, time.UTC)
	if err != nil || long.Total != 3 || long.Encounters[0].DurationSeconds == nil || *long.Encounters[0].DurationSeconds != 50 {
		t.Fatalf("long = %+v (%v)", long, err)
	}
	page, err := store.Library(ctx, LibraryQuery{Sort: SortStartAsc, Offset: 1, Limit: 2}, time.UTC)
	if err != nil || page.Total != 5 || len(page.Encounters) != 2 || page.Encounters[0].LocalDate != "2026-09-11" {
		t.Fatalf("page = %+v (%v)", page, err)
	}
	none, err := store.Library(ctx, LibraryQuery{GameVersions: []string{"9.9"}}, time.UTC)
	if err != nil || none.Total != 0 {
		t.Fatalf("other version = %d (%v)", none.Total, err)
	}
	byRole, err := store.Library(ctx, LibraryQuery{RoleIDs: []uint64{1505}}, time.UTC)
	if err != nil || byRole.Total != 5 {
		t.Fatalf("by role = %d (%v)", byRole.Total, err)
	}
	searched, err := store.Library(ctx, LibraryQuery{MatchInstances: []uint64{}, MatchRoleIDs: []uint64{}}, time.UTC)
	if err != nil || searched.Total != 0 {
		t.Fatalf("a search that matched nothing listed %d (%v)", searched.Total, err)
	}
}

// Deleting a record changes what the library lists at once, though the library cached its index.
func TestLibraryFollowsADeletion(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	base := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	record(t, store, liveStart, finishedEncounter(1, 1, base, 10, 300, capture.IntegrityIntact), finishedEncounter(1, 2, base.Add(time.Hour), 10, 300, capture.IntegrityIntact))
	if view, err := store.Library(ctx, LibraryQuery{}, time.UTC); err != nil || view.Total != 2 {
		t.Fatalf("before = %d (%v)", view.Total, err)
	}
	if _, err := store.DeleteEncounter(ctx, listedID(t, store, base)); err != nil {
		t.Fatal(err)
	}
	if view, err := store.Library(ctx, LibraryQuery{}, time.UTC); err != nil || view.Total != 1 {
		t.Fatalf("after = %d (%v)", view.Total, err)
	}
}

// A comparison reads the class of the encounters it names; an ID that is not listed has none.
func TestRecordClassesOfNamedEncounters(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	base := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	record(t, store, liveStart, finishedEncounter(1, 1, base, 10, 300, capture.IntegrityIntact), finishedEncounter(1, 2, base.Add(time.Hour), 10, 400, capture.IntegrityDegraded))
	intact, degraded := listedID(t, store, base), listedID(t, store, base.Add(time.Hour))
	ctx := context.Background()
	before, err := store.fingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	classes, err := store.RecordClasses(ctx, []int64{intact, degraded, 999999})
	if err != nil || len(classes) != 2 || classes[intact] != ClassComplete || classes[degraded] != ClassDegraded {
		t.Fatalf("classes = %v (%v)", classes, err)
	}
	// What a comparison reads leaves the history as it was.
	for _, id := range []int64{intact, degraded} {
		if _, err := store.Encounter(ctx, id, time.UTC); err != nil {
			t.Fatal(err)
		}
	}
	if after, err := store.fingerprint(ctx); err != nil || after != before {
		t.Fatalf("the history changed while it was read: %v -> %v (%v)", before, after, err)
	}
}
