package history

import (
	"context"
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// Concurrency of the encrypted write-ahead log.
//
// The history file uses the "xts" VFS of github.com/ncruces/go-sqlite3: it encrypts 512-byte sectors and writes a sector
// that a write only partly covers by reading, updating and rewriting it. Write-ahead log frames (24 bytes plus a page) do
// not line up with sectors, so every commit rewrites the sector that holds the end of the previous frame. Before v0.37.0
// (PR #446) a reader on another connection, or in another process, reading that frame at the same moment could decrypt a
// mix of old and new ciphertext: a malformed page or silently wrong values, and a checkpoint copying such a frame would put
// it into the file. These tests run the store's own pool as the application does (one writer, readers on other
// connections, checkpoints in between) and check every value read against what was written.

// TestEncryptedWALReadersSeeOnlyCommittedRows commits transactions of varied size while readers on other connections
// check the rows they read, most of them the newest, and checkpoints run between them. Then it checks every row again, also
// after the file was checkpointed, closed and reopened.
func TestEncryptedWALReadersSeeOnlyCommittedRows(t *testing.T) {
	duration := stressDuration(4 * time.Second)
	readers := stressReaders(6)
	path := testPath(t)
	store := openTestStore(t, path, nil)
	recorder := begin(t, store, liveStart)
	span := recorder.SessionID()
	ctx := context.Background()
	ledger := &stressLedger{}
	var committed, commits atomic.Int64
	commitLatency, checkpointLatency := &latencies{}, &latencies{}
	readLatency := map[string]*latencies{"recent": {}, "range": {}, "totals": {}}
	var reads atomic.Int64
	failures := &failureLog{}
	stop := make(chan struct{})
	stopped := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	measure := startSampler(path)
	var wg sync.WaitGroup
	var writerErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		writerErr = stressWrite(ctx, store.db, span, stressTxSize, stopped, func(_, last int64, latency time.Duration) {
			commits.Add(1)
			committed.Store(last)
			commitLatency.add(latency)
		})
	}()
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			random := rand.New(rand.NewPCG(seed, 0x57574c48))
			for !stopped() {
				last := committed.Load()
				var kind string
				var err error
				started := time.Now()
				switch pick := random.IntN(10); {
				case pick < 6:
					// The newest rows: their frames share sectors with the frames being written.
					kind = "recent"
					_, err = stressCheckRange(ctx, store.db, span, max(0, last-48), 96)
				case pick < 9:
					kind = "range"
					_, err = stressCheckRange(ctx, store.db, span, random.Int64N(last+1), 400)
				default:
					kind = "totals"
					_, err = stressCheckTotals(ctx, store.db, span, ledger)
				}
				readLatency[kind].add(time.Since(started))
				reads.Add(1)
				if err != nil {
					failures.add(kind, err)
				}
			}
		}(uint64(i + 1))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopped() {
			started := time.Now()
			var busy, frames, copied int
			if err := store.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil {
				failures.add("checkpoint", err)
			}
			checkpointLatency.add(time.Since(started))
			time.Sleep(100 * time.Millisecond)
		}
	}()
	time.Sleep(duration)
	close(stop)
	wg.Wait()
	process := measure.finish()
	if writerErr != nil {
		t.Fatalf("writer: %v", writerErr)
	}
	counts, messages, failed := failures.snapshot()
	last := committed.Load()
	stressVerifyAll(t, store.db, span, last, ledger)
	if _, err := store.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	assertIntegrity(t, store)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Every row again from the reopened file, where the checkpoints put them.
	reopened := openTestStore(t, path, nil)
	stressVerifyAll(t, reopened.db, span, last, ledger)
	assertIntegrity(t, reopened)
	summaries := map[string]latencySummary{}
	for kind, l := range readLatency {
		summaries[kind] = l.summary()
	}
	writeStressReport(t, "storage", map[string]any{
		"driver": driverVersion(), "readers": readers, "rows": last, "commits": commits.Load(), "reads": reads.Load(),
		"rowsPerSecond": float64(last) / process.Seconds, "commitsPerSecond": float64(commits.Load()) / process.Seconds,
		"commitLatency": commitLatency.summary(), "readLatency": summaries, "checkpointLatency": checkpointLatency.summary(),
		"process": process, "failures": counts, "failureMessages": messages,
	})
	t.Logf("%s: %d rows in %d commits, %d reads by %d readers, %d failed reads %v", driverVersion(), last, commits.Load(), reads.Load(), readers, failed, counts)
	if failed > 0 {
		t.Fatalf("%d reads failed: %v", failed, messages)
	}
}

// stressEncounter is what one synthetic encounter stored: its combat events, effect changes and totals.
type stressEncounter struct {
	sequence  uint64
	start     time.Time
	events    int
	damage    int64
	changes   int
	instances int
}

// stressEncounterEntries builds the journal of encounter n as the engine submits it: opening and activity, combat results
// assigned to it with effect lifecycle changes in between, then the finished encounter whose totals are the results' sum.
func stressEncounterEntries(n uint64, base time.Time, nextEvent, nextEffect, nextInstance *uint64) ([]capture.HistoryEntry, stressEncounter) {
	start := base.Add(time.Duration(n) * 40 * time.Minute)
	seconds := 20 + int(n*37%120)
	key := "1-" + strconv.FormatUint(n, 10)
	events := 6 + int(n*13%35)
	instances := 1 + int(n%4)
	var body []capture.HistoryEntry
	var damage int64
	var changes int
	at := func(i, of int) time.Time {
		return start.Add(time.Duration(1+i*(seconds-2)/max(1, of)) * time.Second)
	}
	lastEvent := uint64(0)
	effectAt := 0
	for i := 0; i < events; i++ {
		*nextEvent++
		sequence := *nextEvent
		amount := stressAmount(int64(sequence))
		damage += amount
		record := capture.CombatEventRecord{Kind: capture.CombatEventDamage, Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: sequence}, ObservedAt: at(i, events),
			Flow: capture.CombatFlowLocalOutgoing, Confidence: "confirmed", Evidence: "damage_rpc_response", Amount: amount}
		body = append(body, capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &record},
			capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: sequence, Encounter: key})
		lastEvent = sequence
		// Effect lifecycles of this encounter interleave with its results.
		if i%max(1, events/instances) == 0 && effectAt < instances {
			*nextInstance++
			instance := *nextInstance
			stacks := int(n % 5)
			changeAt := at(i, events)
			*nextEffect++
			body = append(body, effectChange(*nextEffect, instance, lastEvent, capture.EffectChangeApply, capture.EffectObserved, changeAt, nil))
			for s := 0; s < stacks; s++ {
				*nextEffect++
				stack := uint64(s + 2)
				body = append(body, effectChange(*nextEffect, instance, lastEvent, capture.EffectChangeStack, capture.EffectObserved, changeAt, func(e *capture.HistoryEffect) { e.Stack = stack }))
			}
			*nextEffect++
			body = append(body, effectChange(*nextEffect, instance, lastEvent, capture.EffectChangeRemove, capture.EffectObserved, changeAt.Add(time.Second), func(e *capture.HistoryEffect) {
				e.Reason = string(capture.EffectLifecycleConfirmedRemoved)
			}))
			changes += stacks + 2
			effectAt++
		}
	}
	entries := finishedEncounter(1, n, start, seconds, damage, capture.IntegrityIntact)
	entries[2].Finished.Snapshot.Totals.OutgoingEvents = uint64(events)
	out := append(entries[:2:2], body...)
	out = append(out, entries[2])
	return out, stressEncounter{sequence: n, start: entries[0].Opened.StartedAt, events: events, damage: damage, changes: changes, instances: effectAt}
}

// TestRecorderStoresEverythingWhileTheHistoryIsQueried feeds the recorder encounters with combat results and effect
// lifecycles as fast as its queue allows, while the History view's own queries (year, day, encounter detail) run on other
// connections and checkpoints run in between. Every stored encounter must match what was submitted: results, totals,
// effect changes and their order; nothing may be lost.
func TestRecorderStoresEverythingWhileTheHistoryIsQueried(t *testing.T) {
	duration := stressDuration(4 * time.Second)
	readers := stressReaders(4)
	path := testPath(t)
	store := openTestStore(t, path, nil)
	recorder := begin(t, store, liveStart)
	ctx := context.Background()
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Local()
	var expectedMu sync.Mutex
	expected := map[int64]stressEncounter{}
	var finished []int64
	var produced atomic.Int64
	stop := make(chan struct{})
	stopped := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	failures := &failureLog{}
	readLatency := map[string]*latencies{"year": {}, "day": {}, "encounter": {}}
	checkpointLatency := &latencies{}
	var throttled, maxQueued atomic.Int64
	var nextEvent, nextEffect, nextInstance uint64
	var entriesSubmitted int64
	measure := startSampler(path)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := uint64(1); !stopped(); n++ {
			entries, encounter := stressEncounterEntries(n, base, &nextEvent, &nextEffect, &nextInstance)
			for recorder.queued.Load() > 60000 {
				throttled.Add(1)
				time.Sleep(time.Millisecond)
			}
			expectedMu.Lock()
			expected[encounter.start.UnixNano()] = encounter
			expectedMu.Unlock()
			// The opening first, then the rest as the engine drains it.
			recorder.Submit(entries[:2], 0)
			recorder.Submit(entries[2:], 0)
			entriesSubmitted += int64(len(entries))
			produced.Store(int64(n))
			if q := recorder.queued.Load(); q > maxQueued.Load() {
				maxQueued.Store(q)
			}
		}
	}()
	lookup := func(start time.Time) (stressEncounter, bool) {
		expectedMu.Lock()
		defer expectedMu.Unlock()
		e, ok := expected[start.UnixNano()]
		return e, ok
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			random := rand.New(rand.NewPCG(seed, 0x57574c48))
			seen := 0
			for !stopped() {
				started := time.Now()
				switch pick := random.IntN(10); {
				case pick < 2:
					year, err := store.Year(ctx, 2026, time.UTC)
					readLatency["year"].add(time.Since(started))
					if err != nil {
						failures.add("year", err)
					} else if year.Total < seen || int64(year.Total) > produced.Load() {
						failures.add("year", errStressMismatch)
					} else {
						seen = year.Total
					}
				case pick < 6:
					n := 1 + random.Int64N(max(1, produced.Load()))
					date := base.Add(time.Duration(n) * 40 * time.Minute).In(time.UTC).Format("2006-01-02")
					day, err := store.Day(ctx, date, 0, MaxDayPage, time.UTC)
					readLatency["day"].add(time.Since(started))
					if err != nil {
						failures.add("day", err)
						continue
					}
					for _, summary := range day.Encounters {
						if summary.State != "finished" {
							continue
						}
						want, ok := lookup(summary.StartedAt)
						if !ok || summary.DamageDealt == nil || *summary.DamageDealt != want.damage || !summary.Complete {
							failures.add("day", errStressMismatch)
							continue
						}
						expectedMu.Lock()
						finished = append(finished, summary.ID)
						expectedMu.Unlock()
					}
				default:
					expectedMu.Lock()
					var id int64
					if len(finished) > 0 {
						id = finished[random.IntN(len(finished))]
					}
					expectedMu.Unlock()
					if id == 0 {
						continue
					}
					detail, err := store.Encounter(ctx, id, time.UTC)
					readLatency["encounter"].add(time.Since(started))
					if err != nil {
						failures.add("encounter", err)
						continue
					}
					want, ok := lookup(detail.Summary.StartedAt)
					var events, damage int64
					if err := store.db.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(amount), 0) FROM combat_event WHERE encounter_id = ?", id).Scan(&events, &damage); err != nil {
						failures.add("encounter", err)
						continue
					}
					if !ok || detail.Snapshot == nil || detail.Snapshot.Totals.DamageDealt != want.damage || detail.Effects.Changes != want.changes ||
						detail.Effects.Instances != want.instances || events != int64(want.events) || damage != want.damage {
						failures.add("encounter", errStressMismatch)
					}
				}
			}
		}(uint64(i + 1))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopped() {
			started := time.Now()
			var busy, frames, copied int
			if err := store.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil {
				failures.add("checkpoint", err)
			}
			checkpointLatency.add(time.Since(started))
			time.Sleep(150 * time.Millisecond)
		}
	}()
	time.Sleep(duration)
	close(stop)
	wg.Wait()
	drainStarted := time.Now()
	finishCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := recorder.Finish(finishCtx, capture.StopReasonRequested); err != nil {
		t.Fatalf("finish: %v", err)
	}
	drain := time.Since(drainStarted)
	process := measure.finish()
	if health := recorder.Health(); health.Lost != 0 || health.Stopped {
		t.Fatalf("recorder health = %+v", health)
	}
	counts, messages, failed := failures.snapshot()
	verify := func(store *Store) {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `SELECT e.started_at_ns, e.state, e.complete, e.damage_dealt,
			(SELECT count(*) FROM combat_event c WHERE c.encounter_id = e.id), (SELECT coalesce(sum(amount), 0) FROM combat_event c WHERE c.encounter_id = e.id)
			FROM encounter e`)
		if err != nil {
			t.Fatal(err)
		}
		stored := 0
		for rows.Next() {
			var started, damage, events, sum int64
			var state string
			var complete bool
			if err := rows.Scan(&started, &state, &complete, &damage, &events, &sum); err != nil {
				t.Fatal(err)
			}
			want, ok := expected[started]
			if !ok || state != "finished" || !complete || damage != want.damage || events != int64(want.events) || sum != want.damage {
				t.Fatalf("encounter started %d: state %s complete %v damage %d events %d sum %d, want %+v", started, state, complete, damage, events, sum, want)
			}
			stored++
		}
		rows.Close()
		if stored != len(expected) {
			t.Fatalf("%d encounters stored, %d submitted", stored, len(expected))
		}
		var wantEvents, wantChanges, wantInstances int
		for _, e := range expected {
			wantEvents, wantChanges, wantInstances = wantEvents+e.events, wantChanges+e.changes, wantInstances+e.instances
		}
		if got := countRows(t, store, "combat_event"); got != wantEvents {
			t.Fatalf("%d combat events stored, %d submitted", got, wantEvents)
		}
		if got := countRows(t, store, "effect_instance"); got != wantInstances {
			t.Fatalf("%d effect instances stored, %d submitted", got, wantInstances)
		}
		// Effect changes are stored in the tracker's order without a gap, each after the result it followed.
		var changes, first, last, misplaced int64
		if err := store.db.QueryRowContext(ctx, `SELECT count(*), min(sequence), max(sequence),
			(SELECT count(*) FROM effect_event v WHERE NOT EXISTS (SELECT 1 FROM combat_event c WHERE c.capture_session_id = v.capture_session_id AND c.sequence = v.after_combat_sequence))
			FROM effect_event`).Scan(&changes, &first, &last, &misplaced); err != nil {
			t.Fatal(err)
		}
		if changes != int64(wantChanges) || first != 1 || last != int64(wantChanges) || misplaced != 0 {
			t.Fatalf("effect changes: %d stored from %d to %d (%d after an unknown result), %d submitted", changes, first, last, misplaced, wantChanges)
		}
		assertIntegrity(t, store)
	}
	verify(store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	verify(openTestStore(t, path, nil))
	summaries := map[string]latencySummary{}
	for kind, l := range readLatency {
		summaries[kind] = l.summary()
	}
	writeStressReport(t, "recorder", map[string]any{
		"driver": driverVersion(), "readers": readers, "encounters": len(expected), "entries": entriesSubmitted,
		"entriesPerSecond": float64(entriesSubmitted) / process.Seconds, "drainSeconds": drain.Seconds(),
		"throttledWaits": throttled.Load(), "maxQueuedEntries": maxQueued.Load(), "readLatency": summaries,
		"checkpointLatency": checkpointLatency.summary(), "process": process, "failures": counts, "failureMessages": messages,
	})
	t.Logf("%s: %d encounters (%d entries), %d failed reads %v, drain %v", driverVersion(), len(expected), entriesSubmitted, failed, counts, drain)
	if failed > 0 {
		t.Fatalf("%d reads failed: %v", failed, messages)
	}
}

// TestEncryptedWALNewestRowsUnderFastCommits aims at the race itself, in the pattern upstream reproduced it with: one
// connection commits one row per transaction as fast as it can, and readers that each keep their own connection read the
// newest 64 rows in a tight loop, so reads of the last committed frames keep overlapping the rewrite of their sectors.
func TestEncryptedWALNewestRowsUnderFastCommits(t *testing.T) {
	duration := stressDuration(3 * time.Second)
	readers := stressReaders(8)
	path := testPath(t)
	store := openTestStore(t, path, nil)
	recorder := begin(t, store, liveStart)
	span := recorder.SessionID()
	ctx := context.Background()
	var committed, commits atomic.Int64
	commitLatency, readLatency := &latencies{}, &latencies{}
	var reads atomic.Int64
	failures := &failureLog{}
	stop := make(chan struct{})
	stopped := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	writerConn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	measure := startSampler(path)
	var wg sync.WaitGroup
	var writerErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		writerErr = stressWrite(ctx, writerConn, span, func(int64) int64 { return 1 }, stopped, func(_, last int64, latency time.Duration) {
			commits.Add(1)
			committed.Store(last)
			commitLatency.add(latency)
		})
	}()
	for i := 0; i < readers; i++ {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			for !stopped() {
				started := time.Now()
				_, err := stressCheckRange(ctx, conn, span, max(0, committed.Load()-64), 64)
				readLatency.add(time.Since(started))
				reads.Add(1)
				if err != nil {
					failures.add("newest", err)
				}
			}
		}()
	}
	time.Sleep(duration)
	close(stop)
	wg.Wait()
	process := measure.finish()
	_ = writerConn.Close()
	if writerErr != nil {
		t.Fatalf("writer: %v", writerErr)
	}
	last := committed.Load()
	var count int64
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM combat_event WHERE capture_session_id = ?", span).Scan(&count); err != nil || count != last {
		t.Fatalf("%d rows stored, %d committed (%v)", count, last, err)
	}
	if got, err := stressCheckRange(ctx, store.db, span, 0, last); err != nil || got != last {
		t.Fatalf("stored rows end at %d (%v)", got, err)
	}
	assertIntegrity(t, store)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	counts, messages, failed := failures.snapshot()
	writeStressReport(t, "newest", map[string]any{
		"driver": driverVersion(), "readers": readers, "rows": last, "commits": commits.Load(), "reads": reads.Load(),
		"commitsPerSecond": float64(commits.Load()) / process.Seconds, "readsPerSecond": float64(reads.Load()) / process.Seconds,
		"commitLatency": commitLatency.summary(), "readLatency": readLatency.summary(), "process": process, "failures": counts, "failureMessages": messages,
	})
	t.Logf("%s: %d single-row commits, %d reads of the newest rows by %d readers, %d failed reads %v", driverVersion(), commits.Load(), reads.Load(), readers, failed, counts)
	if failed > 0 {
		t.Fatalf("%d reads failed: %v", failed, messages)
	}
}
