package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// Flow control and losses: a replay waits for the writer instead of dropping, is kept only when all of it was stored, and an
// unfinished one never survives; a live capture never waits, and what it loses is counted in the span it belonged to.

func replayStart(name string) SessionStart {
	digest := sha256.Sum256([]byte(name))
	return SessionStart{Source: SourceReplay, SourceSHA256: hex.EncodeToString(digest[:])}
}

func acceptedResult(sequence uint64, at time.Time) capture.HistoryEntry {
	record := capture.CombatEventRecord{Kind: capture.CombatEventDamage, Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: sequence}, ObservedAt: at,
		Flow: capture.CombatFlowLocalOutgoing, Confidence: "confirmed", Evidence: "damage_rpc_response", Amount: 10}
	return capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &record}
}

func sessionCounts(t *testing.T, store *Store, id int64) (journal, queue, failed int64, stopReason string) {
	t.Helper()
	var reason *string
	if err := store.db.QueryRowContext(context.Background(), "SELECT journal_dropped, queue_dropped, write_failed, stop_reason FROM capture_session WHERE id = ?", id).Scan(&journal, &queue, &failed, &reason); err != nil {
		t.Fatal(err)
	}
	if reason != nil {
		stopReason = *reason
	}
	return journal, queue, failed, stopReason
}

// TestReplayWaitsForTheWriterInsteadOfDropping stalls the writer behind another connection's write lock while a replay hands
// over far more single-entry drains than the queue holds. The replay must wait (never queue beyond the bound, never drop),
// continue once the writer moves again, and store every entry.
func TestReplayWaitsForTheWriterInsteadOfDropping(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	recorder := begin(t, store, replayStart("paced"))
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	const total = 5 * maxQueuedBatches
	start := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC).Local()
	progress := make(chan int, total)
	go func() {
		for i := 1; i <= total; i++ {
			if err := recorder.SubmitWait(ctx, []capture.HistoryEntry{acceptedResult(uint64(i), start.Add(time.Duration(i)*time.Millisecond))}, 0); err != nil {
				t.Error(err)
				return
			}
			progress <- i
		}
		close(progress)
	}()
	time.Sleep(time.Second)
	handed := len(progress)
	if handed >= total {
		t.Fatalf("the replay handed over all %d drains while the writer was stalled", total)
	}
	if queued := recorder.queued.Load(); queued > maxPacedEntries {
		t.Fatalf("%d entries queued, beyond the replay bound %d", queued, maxPacedEntries)
	}
	if lost := recorder.Health().Lost; lost != 0 {
		t.Fatalf("a stalled writer cost the replay %d entries", lost)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	for range progress {
	}
	if err := recorder.Finish(ctx, capture.StopReasonReplayEnd); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, store, "combat_event"); got != total {
		t.Fatalf("stored %d of %d results", got, total)
	}
	if journal, queue, failed, reason := sessionCounts(t, store, recorder.SessionID()); journal != 0 || queue != 0 || failed != 0 || reason != capture.StopReasonReplayEnd {
		t.Fatalf("session counts journal %d queue %d failed %d, stop %q", journal, queue, failed, reason)
	}
	assertIntegrity(t, store)
}

// TestReplayStoppedBeforeItsEndIsRemoved stops a replay part way: nothing of it stays, and the same file can be replayed
// again.
func TestReplayStoppedBeforeItsEndIsRemoved(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	start := replayStart("stopped")
	recorder := begin(t, store, start)
	if err := recorder.SubmitWait(ctx, finishedEncounter(1, 1, time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC), 10, 100, capture.IntegrityIntact), 0); err != nil {
		t.Fatal(err)
	}
	waitQueued(t, recorder)
	if got := countRows(t, store, "encounter"); got != 1 {
		t.Fatalf("the replay stored %d encounters before it stopped", got)
	}
	if err := recorder.Finish(ctx, capture.StopReasonRequested); !errors.Is(err, ErrReplayNotStored) {
		t.Fatalf("finish of a stopped replay: %v", err)
	}
	if sessions, encounters := countRows(t, store, "capture_session"), countRows(t, store, "encounter"); sessions != 0 || encounters != 0 {
		t.Fatalf("a stopped replay left %d sessions and %d encounters", sessions, encounters)
	}
	again, err := store.BeginSession(start)
	if err != nil {
		t.Fatalf("the stopped replay blocks replaying the file again: %v", err)
	}
	again.Abort()
	assertIntegrity(t, store)
}

// TestReplayThatLostAnEntryIsRemoved fails one transaction of a replay: the replay reached its end, but not all of it was
// stored, so it is removed and Finish says so.
func TestReplayThatLostAnEntryIsRemoved(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	start := replayStart("failed")
	recorder := begin(t, store, start)
	day := time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC)
	first := finishedEncounter(1, 1, day, 10, 100, capture.IntegrityIntact)
	// The accepted entry without its event cannot be stored, so its whole transaction fails.
	if err := recorder.SubmitWait(ctx, append(first[:2:2], capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: 1}), 0); err != nil {
		t.Fatal(err)
	}
	waitQueued(t, recorder)
	if err := recorder.SubmitWait(ctx, finishedEncounter(1, 2, day.Add(time.Minute), 10, 200, capture.IntegrityIntact), 0); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Finish(ctx, capture.StopReasonReplayEnd); !errors.Is(err, ErrReplayNotStored) {
		t.Fatalf("finish of a replay that lost entries: %v", err)
	}
	if sessions := countRows(t, store, "capture_session"); sessions != 0 {
		t.Fatalf("a replay that lost entries left %d sessions", sessions)
	}
	again, err := store.BeginSession(start)
	if err != nil {
		t.Fatalf("the failed replay blocks replaying the file again: %v", err)
	}
	again.Abort()
}

// TestUnfinishedReplayIsRemovedWhenTheFileOpens ends the application the hard way while a replay and a live capture are
// stored in the same file: the next open removes the replay and abandons the live session's unfinished encounter.
func TestUnfinishedReplayIsRemovedWhenTheFileOpens(t *testing.T) {
	path := testPath(t)
	store := openTestStore(t, path, nil)
	ctx := context.Background()
	start := replayStart("crashed")
	day := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	replay := begin(t, store, start)
	if err := replay.SubmitWait(ctx, finishedEncounter(1, 1, day, 10, 100, capture.IntegrityIntact), 0); err != nil {
		t.Fatal(err)
	}
	waitQueued(t, replay)
	live := begin(t, store, liveStart)
	live.Submit(finishedEncounter(1, 1, day.Add(time.Hour), 10, 100, capture.IntegrityIntact)[:2], 0)
	waitQueued(t, live)
	if err := store.closeHandles(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path, nil)
	status := reopened.Status()
	if status.State != StateReady || status.RemovedReplays != 1 || status.RecoveredSessions != 1 || status.RecoveredEncounters != 1 {
		t.Fatalf("status after the abnormal exit = %+v", status)
	}
	var source, state string
	if err := reopened.db.QueryRowContext(ctx, "SELECT s.source, e.state FROM encounter e JOIN capture_session s ON s.id = e.capture_session_id").Scan(&source, &state); err != nil || source != SourceLive || state != "abandoned" {
		t.Fatalf("remaining encounter: %s %s (%v)", source, state, err)
	}
	again, err := reopened.BeginSession(start)
	if err != nil {
		t.Fatalf("the removed replay blocks replaying the file again: %v", err)
	}
	again.Abort()
}

// TestRefusedLiveDrainKeepsItsSpanMarker floods a live recorder with a drain the queue cannot hold, a recording stop at its
// end. The drain is counted on the span it belonged to and marks its encounter, while its stop marker still ends that span,
// so the encounter of the next span is stored complete in its own span.
func TestRefusedLiveDrainKeepsItsSpanMarker(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	day := time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
	recorder := begin(t, store, liveStart)
	recorder.Submit(finishedEncounter(1, 1, day, 10, 100, capture.IntegrityIntact)[:2], 0)
	waitQueued(t, recorder)
	flood := make([]capture.HistoryEntry, maxQueuedEntries+1, maxQueuedEntries+2)
	for i := range flood {
		flood[i] = capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: uint64(i + 1), Encounter: "1-1"}
	}
	flood = append(flood, capture.HistoryEntry{Kind: capture.HistoryRecordingStopped, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStopDisabled, Open: []string{"1-1"}}})
	recorder.Submit(flood, 0)
	next := append([]capture.HistoryEntry{{Kind: capture.HistoryRecordingStarted, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStartEnabled}}, protocolOne},
		finishedEncounter(1, 2, day.Add(time.Hour), 10, 200, capture.IntegrityIntact)...)
	recorder.Submit(next, 0)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT s.id, s.queue_dropped, s.stop_reason, e.sequence, e.state, e.complete, coalesce(e.incomplete_reason, '')
		FROM capture_session s JOIN encounter e ON e.capture_session_id = s.id ORDER BY s.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		span, dropped    int64
		stop             string
		sequence         int64
		state            string
		complete         bool
		incompleteReason string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.span, &r.dropped, &r.stop, &r.sequence, &r.state, &r.complete, &r.incompleteReason); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 2 || got[0].span == got[1].span {
		t.Fatalf("spans and encounters = %+v", got)
	}
	first, second := got[0], got[1]
	if first.dropped != int64(len(flood)-1) || first.stop != capture.HistoryStopDisabled || first.sequence != 1 || first.state != "abandoned" || first.complete || first.incompleteReason != incompleteEntriesLost {
		t.Fatalf("first span = %+v", first)
	}
	if second.dropped != 0 || second.sequence != 2 || second.state != "finished" || !second.complete {
		t.Fatalf("second span = %+v", second)
	}
	if lost := recorder.Health().Lost; lost != uint64(len(flood)-1) {
		t.Fatalf("health counts %d lost entries", lost)
	}
	assertIntegrity(t, store)
}

// TestLostEffectChangesMarkTheWholeSpan refuses a drain of effect changes: they belong to no single encounter, so no encounter
// of the span may be shown complete, those stored after the loss included.
func TestLostEffectChangesMarkTheWholeSpan(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	day := time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)
	recorder := begin(t, store, liveStart)
	first := finishedEncounter(1, 1, day, 10, 100, capture.IntegrityIntact)
	recorder.Submit(first[:2], 0)
	waitQueued(t, recorder)
	effects := make([]capture.HistoryEntry, maxQueuedEntries+1)
	for i := range effects {
		effects[i] = capture.HistoryEntry{Kind: capture.HistoryEffectChanged, Sequence: uint64(i + 1)}
	}
	recorder.Submit(effects, 0)
	recorder.Submit(first[2:], 0)
	recorder.Submit(finishedEncounter(1, 2, day.Add(time.Hour), 10, 200, capture.IntegrityIntact), 0)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	var incomplete, total int
	if err := store.db.QueryRowContext(ctx, "SELECT coalesce(sum(complete = 0 AND incomplete_reason = ?), 0), count(*) FROM encounter", incompleteEntriesLost).Scan(&incomplete, &total); err != nil || total != 2 || incomplete != 2 {
		t.Fatalf("%d of %d encounters marked incomplete (%v)", incomplete, total, err)
	}
	if _, queue, _, _ := sessionCounts(t, store, recorder.SessionID()); queue != int64(len(effects)) {
		t.Fatalf("queue_dropped = %d", queue)
	}
}

// TestFailedTransactionCarriesItsSpanMarker fails the transaction that holds a recording stop: its entries are counted on the
// span they belonged to, the stop still ends that span, and the next span is stored normally.
func TestFailedTransactionCarriesItsSpanMarker(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	day := time.Date(2026, 11, 13, 7, 0, 0, 0, time.UTC)
	recorder := begin(t, store, liveStart)
	first := finishedEncounter(1, 1, day, 10, 100, capture.IntegrityIntact)
	failing := append(first[:2:2],
		capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: 1},
		capture.HistoryEntry{Kind: capture.HistoryRecordingStopped, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStopDisabled, Open: []string{"1-1"}}})
	recorder.Submit(failing, 0)
	waitQueued(t, recorder)
	if health := recorder.Health(); health.Lost != 3 || health.Stopped {
		t.Fatalf("health after the failed transaction = %+v", health)
	}
	next := append([]capture.HistoryEntry{{Kind: capture.HistoryRecordingStarted, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStartEnabled}}, protocolOne},
		finishedEncounter(1, 2, day.Add(time.Hour), 10, 200, capture.IntegrityIntact)...)
	recorder.Submit(next, 0)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	if spans := countRows(t, store, "capture_session"); spans != 2 {
		t.Fatalf("%d spans", spans)
	}
	if _, _, failed, reason := sessionCounts(t, store, recorder.SessionID()); failed != 3 || reason != capture.HistoryStopDisabled {
		t.Fatalf("first span: write_failed %d, stop %q", failed, reason)
	}
	var sequence int64
	var state string
	var complete bool
	if err := store.db.QueryRowContext(ctx, "SELECT sequence, state, complete FROM encounter").Scan(&sequence, &state, &complete); err != nil || sequence != 2 || state != "finished" || !complete {
		t.Fatalf("stored encounter %d %s complete %t (%v)", sequence, state, complete, err)
	}
	assertIntegrity(t, store)
}
