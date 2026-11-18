package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// TestRecordingSwitchesLeaveTheStoredHistoryAsItWas: switching recording off during an encounter and on again changes no
// stored encounter, the database identity or the key record; the encounter cut short is kept unfinished; nothing is
// migrated, rebuilt or recreated, and after a restart every encounter is there.
func TestRecordingSwitchesLeaveTheStoredHistoryAsItWas(t *testing.T) {
	path := testPath(t)
	store, err := openWith(path, KeyInputs{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	storedRecord := readFile(t, keyRecordPath(path))
	start := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC).Local()
	ctx := context.Background()
	enabled := capture.HistoryEntry{Kind: capture.HistoryRecordingStarted, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStartEnabled}}
	stopped := capture.HistoryEntry{Kind: capture.HistoryRecordingStopped, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStopDisabled, Open: []string{"1-2"}}}
	recorder, err := store.BeginSession(SessionStart{Source: SourceLive, Paused: true})
	if err != nil {
		t.Fatal(err)
	}
	submit := func(entries ...capture.HistoryEntry) { recorder.Submit(entries, 0) }
	// states waits until the day lists the encounters in these history states.
	states := func(want ...string) []EncounterSummary {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			day, err := store.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, encounter := range day.Encounters {
				got = append(got, encounter.State)
			}
			if strings.Join(got, ",") == strings.Join(want, ",") {
				return day.Encounters
			}
			if time.Now().After(deadline) {
				t.Fatalf("encounters %v, want %v", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	submit(enabled, protocolOne)
	submit(finishedEncounter(1, 1, start, 20, 400, capture.IntegrityIntact)...)
	states("finished")
	first := firstEncounterViews(t, store)
	identity, err := readIdentity(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	second := finishedEncounter(1, 2, start.Add(time.Minute), 20, 500, capture.IntegrityIntact)
	submit(second[:2]...)
	// Off during the second encounter: the span ends at once and the second encounter stays unfinished.
	submit(stopped)
	states("finished", "abandoned")
	if got := firstEncounterViews(t, store); got != first {
		t.Fatalf("switching recording off changed a stored encounter:\nbefore %s\nafter  %s", first, got)
	}
	if cut := states("finished", "abandoned")[1]; cut.Complete || cut.IncompleteReason != "recording_disabled" {
		t.Fatalf("the encounter cut short while recording is off: %+v", cut)
	}
	var spanStatus, stopReason string
	if err := store.db.QueryRowContext(ctx, "SELECT status, stop_reason FROM capture_session ORDER BY id LIMIT 1").Scan(&spanStatus, &stopReason); err != nil ||
		spanStatus != "closed" || stopReason != capture.HistoryStopDisabled {
		t.Fatalf("the span recording was switched off in: %s %s (%v)", spanStatus, stopReason, err)
	}
	// The rest of the second encounter arrives while recording is off and is not stored; on again, the next encounter is.
	submit(second[2])
	submit(enabled, protocolOne)
	submit(finishedEncounter(1, 3, start.Add(2*time.Minute), 20, 600, capture.IntegrityIntact)...)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	if got := firstEncounterViews(t, store); got != first {
		t.Fatalf("switching recording on again changed a stored encounter:\nbefore %s\nafter  %s", first, got)
	}
	if again, err := readIdentity(ctx, store.db); err != nil || again != identity {
		t.Fatalf("identity %+v, was %+v (%v)", again, identity, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, keyRecordPath(path)), storedRecord) {
		t.Fatal("the key record changed")
	}
	reopened, err := openWith(path, KeyInputs{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	if status := reopened.Status(); status.State != StateReady || status.Migration != "" || status.RecoveredEncounters != 0 {
		t.Fatalf("after a restart: %+v", status)
	}
	if got := firstEncounterViews(t, reopened); got != first {
		t.Fatalf("after a restart the stored encounter differs:\nbefore %s\nafter  %s", first, got)
	}
	day, err := reopened.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 3 || day.Encounters[0].State != "finished" || day.Encounters[1].State != "abandoned" || day.Encounters[2].State != "finished" {
		t.Fatalf("after a restart: %+v (%v)", day.Encounters, err)
	}
	if !bytes.Equal(readFile(t, keyRecordPath(path)), storedRecord) {
		t.Fatal("the key record changed after a restart")
	}
	assertIntegrity(t, reopened)
}

// firstEncounterViews is the first stored encounter as the History reads it back: its summary, its effect window and the
// engine's stored result (snapshot, timeline, enemies), as JSON. The capture session it was recorded in is left out: a
// recording switch closes that span, which is the switch working.
func firstEncounterViews(t *testing.T, store *Store) string {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := store.db.QueryRowContext(ctx, "SELECT min(id) FROM encounter").Scan(&id); err != nil {
		t.Fatal(err)
	}
	detail, err := store.Encounter(ctx, id, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		Summary  EncounterSummary
		Effects  EffectWindow
		Snapshot *capture.EncounterSnapshot
		Timeline *capture.StoredTimeline
		Enemies  []capture.EncounterEnemySnapshot
	}{detail.Summary, detail.Effects, detail.Snapshot, detail.Timeline, detail.Enemies})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestClearAllKeepsTheHistoryFileAndItsKey: clearing every encounter, which the user asks for in Settings, deletes rows in
// the history, nothing else: the same database file with the same identity and key record stays, no rebuild or migration
// starts, and the history records again.
func TestClearAllKeepsTheHistoryFileAndItsKey(t *testing.T) {
	path := testPath(t)
	store, err := openWith(path, KeyInputs{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	twoEncounters(t, store)
	ctx := context.Background()
	storedRecord := readFile(t, keyRecordPath(path))
	identity, err := readIdentity(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.ClearHistory(ctx); err != nil || result.Encounters != 2 {
		t.Fatalf("clear = %+v (%v)", result, err)
	}
	if again, err := os.Stat(path); err != nil || !os.SameFile(file, again) {
		t.Fatalf("clearing replaced the database file (%v)", err)
	}
	if got, err := readIdentity(ctx, store.db); err != nil || got != identity {
		t.Fatalf("identity %+v, was %+v (%v)", got, identity, err)
	}
	if !bytes.Equal(readFile(t, keyRecordPath(path)), storedRecord) {
		t.Fatal("clearing changed the key record")
	}
	if status := store.Status(); status.State != StateReady || status.Rebuildable {
		t.Fatalf("after clearing: %+v", status)
	}
	if _, err := store.Rebuild(); !errors.Is(err, ErrNotRebuildable) {
		t.Fatalf("a cleared history was offered a rebuild: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, discardedSuffix) || strings.HasSuffix(name, rebuildSuffix) || strings.HasSuffix(name, migrationSuffix) {
			t.Fatalf("clearing left %s", name)
		}
	}
	recordOneEncounter(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if got := encountersWith(t, path, KeyInputs{}); got != 1 {
		t.Fatalf("after clearing and a restart the history holds %d encounters, want 1", got)
	}
}
