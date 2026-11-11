package history

import (
	"context"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// TestBusyHistoryLosesOnlyThatBatchAndMarksItsEncounter holds the write lock on another connection past the busy timeout
// while the recorder stores the end of an encounter. That transaction fails as busy: its entries are counted lost and the
// encounter is never shown as finished or complete, while the next encounter is stored normally.
func TestBusyHistoryLosesOnlyThatBatchAndMarksItsEncounter(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	start := time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC).Local()
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		t.Fatal(err)
	}
	first := finishedEncounter(1, 1, start, 10, 100, capture.IntegrityIntact)
	recorder.Submit(first[:2], 0)
	waitQueued(t, recorder)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	recorder.Submit(first[2:], 0)
	waitQueued(t, recorder)
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if health := recorder.Health(); health.Lost != 1 || health.Stopped {
		t.Fatalf("health after the busy transaction = %+v", health)
	}
	recorder.Submit(finishedEncounter(1, 2, start.Add(time.Minute), 10, 200, capture.IntegrityIntact), 0)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	day, err := store.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 2 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	lost, kept := day.Encounters[0], day.Encounters[1]
	if lost.State != "abandoned" || lost.Complete || lost.IncompleteReason != incompleteEntriesLost || kept.State != "finished" || !kept.Complete {
		t.Fatalf("encounters = %+v", day.Encounters)
	}
	var failed int
	if err := store.db.QueryRowContext(ctx, "SELECT write_failed FROM capture_session WHERE id = ?", recorder.SessionID()).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("write_failed = %d (%v)", failed, err)
	}
	assertIntegrity(t, store)
}

// TestFailedTransactionStoresNothingOfItsBatch fails a transaction after it already inserted a combat result: the rollback
// leaves nothing of that batch, the encounter it named is marked incomplete, and later batches are stored.
func TestFailedTransactionStoresNothingOfItsBatch(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	start := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC).Local()
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		t.Fatal(err)
	}
	first := finishedEncounter(1, 1, start, 10, 100, capture.IntegrityIntact)
	recorder.Submit(first[:2], 0)
	waitQueued(t, recorder)
	record := capture.CombatEventRecord{Kind: capture.CombatEventDamage, Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: 1}, ObservedAt: start.Add(time.Second),
		Flow: capture.CombatFlowLocalOutgoing, Confidence: "confirmed", Evidence: "damage_rpc_response", Amount: 100}
	// The second entry cannot be stored, so the whole transaction rolls back, the result inserted before it included.
	recorder.Submit([]capture.HistoryEntry{
		{Kind: capture.HistoryEventAccepted, Sequence: 1, Event: &record},
		{Kind: capture.HistoryEventAccepted, Sequence: 2},
		first[2],
	}, 0)
	waitQueued(t, recorder)
	if got := countRows(t, store, "combat_event"); got != 0 {
		t.Fatalf("%d combat events stored from a failed transaction", got)
	}
	if health := recorder.Health(); health.Lost != 3 || health.Stopped {
		t.Fatalf("health = %+v", health)
	}
	recorder.Submit(finishedEncounter(1, 2, start.Add(time.Minute), 10, 200, capture.IntegrityIntact), 0)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	day, err := store.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 2 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	if lost := day.Encounters[0]; lost.State != "abandoned" || lost.Complete || lost.IncompleteReason != incompleteEntriesLost {
		t.Fatalf("encounter of the failed batch = %+v", lost)
	}
	if kept := day.Encounters[1]; kept.State != "finished" || !kept.Complete {
		t.Fatalf("next encounter = %+v", kept)
	}
	assertIntegrity(t, store)
}

// TestWriteTransactionsTakeTheWriteLockAtBegin pins the transaction mode the store sets on every connection
// (_txlock=immediate): a write transaction holds the write lock from its BEGIN, so a second writer waits for it instead of
// failing later when a deferred read transaction would try to become a write, while readers still read beside it.
func TestWriteTransactionsTakeTheWriteLockAtBegin(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	other, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExecContext(ctx, "BEGIN IMMEDIATE"); err == nil {
		_, _ = other.ExecContext(ctx, "ROLLBACK")
		t.Fatal("a second write transaction began while the first, which has not written yet, was open")
	}
	var tables int
	if err := other.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&tables); err != nil || tables == 0 {
		t.Fatalf("a reader beside the write transaction: %d tables (%v)", tables, err)
	}
}
