package capture

import (
	"math"
	"sync"
	"testing"
	"time"
)

const effectTestCaptureSession = 7

var effectTestBase = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func effectAt(seconds float64) time.Time {
	return effectTestBase.Add(time.Duration(seconds * float64(time.Second)))
}

func effectQuery(seconds float64) CaptureQueryTime {
	return CaptureQueryTime{CaptureSessionID: effectTestCaptureSession, Timestamp: effectAt(seconds)}
}

func effectFloat(value float64) *float64 { return &value }

func newTestEffectTracker() *EffectTracker { return NewEffectTracker(effectTestCaptureSession) }

// testEffectApply is a finite two-second effect on holder 10, applied by local role 20.
func testEffectApply(handle uint64, seconds float64, mutate func(*EffectApplyEvent)) EffectApplyEvent {
	event := EffectApplyEvent{
		SessionNumber: 1, HolderEntityID: 10, HandleID: handle,
		EffectID: 100, HasEffectID: true, SourceEntityID: 20, HasSource: true,
		HolderRelation: EffectRelationNonPlayer, SourceRelation: EffectRelationLocalRole,
		StackCount: 1, HasStackCount: true,
		DurationSeconds: effectFloat(2), RemainingSeconds: effectFloat(2),
		ObservedAt: effectAt(seconds),
	}
	if mutate != nil {
		mutate(&event)
	}
	return event
}

func testEffectRemove(handle uint64, seconds float64) EffectRemoveEvent {
	return EffectRemoveEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: handle, ObservedAt: effectAt(seconds)}
}

func requireNear(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", label, want)
	}
	if math.Abs(*got-want) > 1e-6 {
		t.Fatalf("%s = %v, want %v", label, *got, want)
	}
}

func onlyEffect(t *testing.T, snapshot EffectSnapshot) EffectInstanceSnapshot {
	t.Helper()
	if len(snapshot.Effects) != 1 {
		t.Fatalf("effects = %+v, want exactly one", snapshot.Effects)
	}
	return snapshot.Effects[0]
}

func TestEffectTrackerApplyThenConfirmedRemove(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 0, nil))

	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(0.5)))
	if effect.Lifecycle != EffectLifecycleActive || effect.RemainingStatus != EffectRemainingEstimated {
		t.Fatalf("active effect = %+v", effect)
	}
	requireNear(t, "remaining", effect.RemainingSeconds, 1.5)
	if effect.ExpectedExpiry == nil || !effect.ExpectedExpiry.Equal(effectAt(2)) {
		t.Fatalf("expected expiry = %v, want %v", effect.ExpectedExpiry, effectAt(2))
	}
	if effect.EffectID == nil || *effect.EffectID != 100 || effect.SourceEntityID == nil || *effect.SourceEntityID != 20 ||
		effect.HolderRelation != EffectRelationNonPlayer || effect.SourceRelation != EffectRelationLocalRole ||
		effect.Polarity != EffectPolarityUnclassified || effect.StackCount == nil || *effect.StackCount != 1 {
		t.Fatalf("effect identity fields = %+v", effect)
	}

	tracker.ObserveRemove(testEffectRemove(5, 2.03))
	snapshot := tracker.SnapshotAt(effectQuery(3))
	if len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 1 {
		t.Fatalf("after remove: effects=%d ended=%d", len(snapshot.Effects), len(snapshot.RecentEnded))
	}
	ended := snapshot.RecentEnded[0]
	if ended.Lifecycle != EffectLifecycleConfirmedRemoved || !ended.EndedAt.Equal(effectAt(2.03)) {
		t.Fatalf("ended record = %+v", ended)
	}
	requireNear(t, "expiry delta", ended.ExpiryDeltaSeconds, 0.03)
	if snapshot.Counters.Applies != 1 || snapshot.Counters.Removes != 1 {
		t.Fatalf("counters = %+v", snapshot.Counters)
	}
}

func TestEffectTrackerKeepsInstancesApartByHolderHandleAndSource(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, nil))
	tracker.ObserveApply(testEffectApply(2, 0, func(e *EffectApplyEvent) { e.EffectID = 200 }))
	// The same handle value and effect ID on another holder is another instance.
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) { e.HolderEntityID = 11 }))
	// The same effect ID on the same holder from another source arrives under its own handle.
	tracker.ObserveApply(testEffectApply(3, 0, func(e *EffectApplyEvent) { e.SourceEntityID = 21; e.SourceRelation = EffectRelationUnknown }))

	snapshot := tracker.SnapshotAt(effectQuery(0.1))
	if len(snapshot.Effects) != 4 {
		t.Fatalf("effects = %d, want 4: %+v", len(snapshot.Effects), snapshot.Effects)
	}
	tracker.ObserveRemove(testEffectRemove(1, 1))
	snapshot = tracker.SnapshotAt(effectQuery(1.1))
	if len(snapshot.Effects) != 3 {
		t.Fatalf("removing holder 10 handle 1 changed %d instances", 4-len(snapshot.Effects))
	}
	var holder11 bool
	var sources []uint64
	for _, effect := range snapshot.Effects {
		if effect.HolderEntityID == 11 && effect.HandleID == 1 {
			holder11 = true
		}
		if effect.HolderEntityID == 10 && effect.EffectID != nil && *effect.EffectID == 100 && effect.SourceEntityID != nil {
			sources = append(sources, *effect.SourceEntityID)
		}
	}
	if !holder11 {
		t.Fatal("holder 11 instance was removed by a holder 10 REMOVE")
	}
	if len(sources) != 1 || sources[0] != 21 {
		t.Fatalf("effect 100 sources on holder 10 = %v, want only 21 left", sources)
	}
}

func TestEffectTrackerDuplicateApplyRefreshesWithoutSecondInstance(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 0, nil))
	tracker.ObserveApply(testEffectApply(5, 1, nil))
	tracker.ObserveApply(testEffectApply(5, 1, nil))

	snapshot := tracker.SnapshotAt(effectQuery(1.5))
	effect := onlyEffect(t, snapshot)
	requireNear(t, "remaining after repeated apply", effect.RemainingSeconds, 1.5)
	if snapshot.Counters.Applies != 1 || snapshot.Counters.DuplicateApplies != 2 {
		t.Fatalf("counters = %+v", snapshot.Counters)
	}
}

func TestEffectTrackerConflictingApplyReplacesInstanceAndIsCounted(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 0, nil))
	tracker.ObserveApply(testEffectApply(5, 1, func(e *EffectApplyEvent) { e.EffectID = 300 }))

	snapshot := tracker.SnapshotAt(effectQuery(1.5))
	effect := onlyEffect(t, snapshot)
	if effect.EffectID == nil || *effect.EffectID != 300 || snapshot.Counters.ConflictingApplies != 1 || snapshot.Counters.Applies != 2 {
		t.Fatalf("conflict result = %+v counters=%+v", effect, snapshot.Counters)
	}
}

func TestEffectTrackerUpdateRebasesRemainingAndTracksStack(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 0, func(e *EffectApplyEvent) {
		e.DurationSeconds, e.RemainingSeconds = effectFloat(14.8), effectFloat(14.8)
	}))
	update := func(seconds float64, stack uint64, hasStack bool, remaining float64) {
		tracker.ObserveUpdate(EffectUpdateEvent{
			SessionNumber: 1, HolderEntityID: 10, HandleID: 5, StackCount: stack, HasStackCount: hasStack,
			DurationSeconds: effectFloat(14.8), RemainingSeconds: effectFloat(remaining), ObservedAt: effectAt(seconds),
		})
	}
	update(4, 2, true, 14.8)
	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(5)))
	requireNear(t, "remaining after refresh", effect.RemainingSeconds, 13.8)
	if effect.StackCount == nil || *effect.StackCount != 2 {
		t.Fatalf("stack = %v, want 2", effect.StackCount)
	}
	update(6, 3, true, 14.8)
	update(8, 2, true, 14.8) // layers can fall as well as rise
	update(9, 0, false, 13.8)
	effect = onlyEffect(t, tracker.SnapshotAt(effectQuery(9)))
	if effect.StackCount == nil || *effect.StackCount != 2 {
		t.Fatalf("stack after decrease and stack-less update = %v, want 2", effect.StackCount)
	}
	requireNear(t, "remaining after duration-only update", effect.RemainingSeconds, 13.8)
	if !effect.LastUpdatedAt.Equal(effectAt(9)) || !effect.AppliedAt.Equal(effectAt(0)) {
		t.Fatalf("timestamps applied=%v updated=%v", effect.AppliedAt, effect.LastUpdatedAt)
	}
}

func TestEffectTrackerOutOfOrderEventsAreIgnoredSafely(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 5, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(10), effectFloat(10) }))
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, RemainingSeconds: effectFloat(99), ObservedAt: effectAt(3)})
	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(6)))
	requireNear(t, "remaining after stale update", effect.RemainingSeconds, 9)

	// A REMOVE older than the APPLY belongs to an earlier instance of a reused handle.
	tracker.ObserveRemove(testEffectRemove(5, 4))
	if len(tracker.Snapshot().Effects) != 1 {
		t.Fatal("a REMOVE older than the APPLY ended the instance")
	}

	// A REMOVE with no instance, then an older APPLY that arrives late: the APPLY is stale.
	tracker.ObserveRemove(testEffectRemove(8, 4))
	tracker.ObserveApply(testEffectApply(8, 3, nil))
	snapshot := tracker.Snapshot()
	if len(snapshot.Effects) != 1 {
		t.Fatalf("stale APPLY created an instance: %+v", snapshot.Effects)
	}
	tracker.ObserveApply(testEffectApply(8, 6, nil)) // a newer APPLY of the same handle is a new instance
	if got := len(tracker.Snapshot().Effects); got != 2 {
		t.Fatalf("effects after newer reuse = %d, want 2", got)
	}
	counters := tracker.Snapshot().Counters
	if counters.StaleUpdates != 1 || counters.StaleRemoves != 1 || counters.StaleApplies != 1 || counters.RemovesWithoutInstance != 1 {
		t.Fatalf("counters = %+v", counters)
	}
}

func TestEffectTrackerHandlesMissingApplyAndMissingRemove(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 9, RemainingSeconds: effectFloat(5), ObservedAt: effectAt(1)})
	tracker.ObserveRemove(testEffectRemove(9, 2))
	snapshot := tracker.Snapshot()
	if len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 0 {
		t.Fatalf("messages without an APPLY created state: %+v", snapshot)
	}
	if snapshot.Counters.UpdatesWithoutApply != 1 || snapshot.Counters.RemovesWithoutInstance != 1 {
		t.Fatalf("counters = %+v", snapshot.Counters)
	}

	// A missing REMOVE is never reported as a confirmed removal.
	tracker.ObserveApply(testEffectApply(1, 10, nil))
	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(10.5)))
	if effect.RemainingStatus != EffectRemainingEstimated {
		t.Fatalf("before expiry = %+v", effect)
	}
	effect = onlyEffect(t, tracker.SnapshotAt(effectQuery(13)))
	if effect.Lifecycle != EffectLifecycleActive || effect.RemainingStatus != EffectRemainingOverdue {
		t.Fatalf("just after expiry = %+v", effect)
	}
	requireNear(t, "overdue remaining", effect.RemainingSeconds, 0)
	effect = onlyEffect(t, tracker.SnapshotAt(effectQuery(12+effectOverdueRetentionSeconds+1)))
	if effect.Lifecycle != EffectLifecycleEstimatedExpired || effect.RemainingStatus != EffectRemainingOverdue {
		t.Fatalf("long after expiry = %+v", effect)
	}
	// Queries do not mutate: an earlier query time still sees the countdown.
	effect = onlyEffect(t, tracker.SnapshotAt(effectQuery(10.5)))
	if effect.Lifecycle != EffectLifecycleActive || effect.RemainingStatus != EffectRemainingEstimated {
		t.Fatalf("earlier query after a later one = %+v", effect)
	}
}

const effectOverdueRetentionSeconds = 120

func TestEffectTrackerTimeInterpretation(t *testing.T) {
	cases := []struct {
		name               string
		duration, remain   *float64
		status             EffectRemainingStatus
		reason             string
		wantRemainingAtOne float64
		invalid            uint64
	}{
		{name: "finite duration and remaining", duration: effectFloat(5), remain: effectFloat(3), status: EffectRemainingEstimated, wantRemainingAtOne: 2},
		{name: "duration only", duration: effectFloat(4), status: EffectRemainingEstimated, wantRemainingAtOne: 3},
		{name: "remaining only", remain: effectFloat(6), status: EffectRemainingEstimated, wantRemainingAtOne: 5},
		{name: "server -1 means no finite duration", duration: effectFloat(-1), remain: effectFloat(-1), status: EffectRemainingUnknown, reason: "no_server_duration"},
		{name: "remaining -1 overrides duration", duration: effectFloat(5), remain: effectFloat(-1), status: EffectRemainingUnknown, reason: "no_server_duration"},
		{name: "absent fields", status: EffectRemainingUnknown, reason: "duration_absent"},
		{name: "zero duration", duration: effectFloat(0), remain: effectFloat(0), status: EffectRemainingUnknown, reason: "zero_duration"},
		{name: "negative other than -1", duration: effectFloat(-5), remain: effectFloat(-5), status: EffectRemainingUnknown, reason: "invalid_duration", invalid: 1},
		{name: "NaN", duration: effectFloat(math.NaN()), status: EffectRemainingUnknown, reason: "invalid_duration", invalid: 1},
		{name: "infinity", remain: effectFloat(math.Inf(1)), status: EffectRemainingUnknown, reason: "invalid_duration", invalid: 1},
		{name: "implausibly large", duration: effectFloat(2e7), status: EffectRemainingUnknown, reason: "invalid_duration", invalid: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newTestEffectTracker()
			tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = tc.duration, tc.remain }))
			snapshot := tracker.SnapshotAt(effectQuery(1))
			effect := onlyEffect(t, snapshot)
			if effect.RemainingStatus != tc.status || effect.RemainingReason != tc.reason {
				t.Fatalf("status/reason = %s/%q, want %s/%q", effect.RemainingStatus, effect.RemainingReason, tc.status, tc.reason)
			}
			if tc.status == EffectRemainingEstimated {
				requireNear(t, "remaining", effect.RemainingSeconds, tc.wantRemainingAtOne)
			} else if effect.RemainingSeconds != nil || effect.ExpectedExpiry != nil {
				t.Fatalf("an effect with unknown time carries a countdown: %+v", effect)
			}
			if snapshot.Counters.InvalidTimeValues != tc.invalid {
				t.Fatalf("InvalidTimeValues = %d, want %d", snapshot.Counters.InvalidTimeValues, tc.invalid)
			}
			// The effect itself stays tracked and active whatever its time says.
			if effect.Lifecycle != EffectLifecycleActive {
				t.Fatalf("lifecycle = %s", effect.Lifecycle)
			}
		})
	}
}

func TestEffectTrackerUpdateKeepsEstimateOnInvalidValueAndDropsItOnSentinel(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(10), effectFloat(10) }))
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, RemainingSeconds: effectFloat(math.NaN()), ObservedAt: effectAt(2)})
	snapshot := tracker.SnapshotAt(effectQuery(4))
	requireNear(t, "remaining after unreadable update", onlyEffect(t, snapshot).RemainingSeconds, 6)
	if snapshot.Counters.InvalidTimeValues != 1 {
		t.Fatalf("InvalidTimeValues = %d, want 1", snapshot.Counters.InvalidTimeValues)
	}
	// The server now says there is no finite duration: do not keep counting down.
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, RemainingSeconds: effectFloat(-1), ObservedAt: effectAt(5)})
	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(6)))
	if effect.RemainingStatus != EffectRemainingUnknown || effect.RemainingReason != "no_server_duration" || effect.RemainingSeconds != nil {
		t.Fatalf("after -1 update = %+v", effect)
	}
}

func TestEffectTrackerSnapshotAtAnswersWithoutNewPackets(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(10), effectFloat(10) }))
	for _, step := range []struct {
		at, want float64
	}{{0, 10}, {2.5, 7.5}, {9.999, 0.001}} {
		requireNear(t, "remaining", onlyEffect(t, tracker.SnapshotAt(effectQuery(step.at))).RemainingSeconds, step.want)
	}
}

func TestEffectTrackerQueryValidation(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 5, nil))

	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(4)))
	if effect.RemainingStatus != EffectRemainingUnavailable || effect.RemainingReason != "query_before_observation" || effect.RemainingSeconds != nil {
		t.Fatalf("query before the update = %+v", effect)
	}
	mismatch := tracker.SnapshotAt(CaptureQueryTime{CaptureSessionID: effectTestCaptureSession + 1, Timestamp: effectAt(6)})
	if e := onlyEffect(t, mismatch); e.RemainingStatus != EffectRemainingUnavailable || e.RemainingReason != "capture_session_mismatch" || mismatch.QueryTime != nil {
		t.Fatalf("session mismatch = %+v query=%v", e, mismatch.QueryTime)
	}
	zero := tracker.SnapshotAt(CaptureQueryTime{CaptureSessionID: effectTestCaptureSession})
	if e := onlyEffect(t, zero); e.RemainingStatus != EffectRemainingUnavailable || e.RemainingReason != "query_time_unavailable" {
		t.Fatalf("zero query time = %+v", e)
	}
	plain := onlyEffect(t, tracker.Snapshot())
	if plain.RemainingStatus != EffectRemainingUnavailable || plain.RemainingReason != "query_time_required" {
		t.Fatalf("Snapshot without a query = %+v", plain)
	}
}

func TestEffectTrackerNeverReportsNonFiniteOrNegativeRemaining(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(1e7), effectFloat(1e7) }))
	tracker.ObserveApply(testEffectApply(2, 0, nil))
	for _, at := range []float64{-1e9, -1, 0, 1, 2, 3, 1e3, 1e6, 1e7, 1e9, 4e9} {
		for _, effect := range tracker.SnapshotAt(effectQuery(at)).Effects {
			if effect.RemainingSeconds != nil && (math.IsNaN(*effect.RemainingSeconds) || math.IsInf(*effect.RemainingSeconds, 0) || *effect.RemainingSeconds < 0) {
				t.Fatalf("query %v produced remaining %v", at, *effect.RemainingSeconds)
			}
		}
	}
}

func TestEffectTrackerSnapshotIsIndependentOfLaterState(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, nil))
	tracker.ObserveApply(testEffectApply(2, 0, nil))
	tracker.ObserveRemove(testEffectRemove(2, 1))
	snapshot := tracker.SnapshotAt(effectQuery(0.5))
	want := tracker.SnapshotAt(effectQuery(0.5))

	snapshot.Effects[0].HandleID = 999
	*snapshot.Effects[0].EffectID = 12345
	*snapshot.Effects[0].RemainingSeconds = -5
	snapshot.RecentEnded[0].HandleID = 998
	snapshot.Effects = append(snapshot.Effects[:0], EffectInstanceSnapshot{})
	snapshot.Counters.Applies = 77

	got := tracker.SnapshotAt(effectQuery(0.5))
	if len(got.Effects) != len(want.Effects) || got.Effects[0].HandleID != want.Effects[0].HandleID ||
		*got.Effects[0].EffectID != *want.Effects[0].EffectID || *got.Effects[0].RemainingSeconds != *want.Effects[0].RemainingSeconds ||
		got.RecentEnded[0].HandleID != want.RecentEnded[0].HandleID || got.Counters != want.Counters {
		t.Fatalf("tracker state changed through a snapshot: %+v vs %+v", got, want)
	}
	// A later event must not alter a snapshot taken earlier either.
	before := tracker.SnapshotAt(effectQuery(0.5))
	tracker.ObserveRemove(testEffectRemove(1, 1))
	if len(before.Effects) != 1 || before.Effects[0].Lifecycle != EffectLifecycleActive {
		t.Fatalf("earlier snapshot changed after a later event: %+v", before.Effects)
	}
}

func TestEffectTrackerResetStartsCleanInterval(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, nil))
	tracker.ObserveApply(testEffectApply(2, 0, nil))
	tracker.ObserveRemove(testEffectRemove(2, 1))
	if tracker.Snapshot().StatisticsIntervalID != 1 {
		t.Fatalf("initial interval = %d", tracker.Snapshot().StatisticsIntervalID)
	}
	tracker.Reset()
	snapshot := tracker.Snapshot()
	if snapshot.StatisticsIntervalID != 2 || snapshot.CaptureSessionID != effectTestCaptureSession ||
		len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 0 || snapshot.Counters != (EffectCounters{}) {
		t.Fatalf("snapshot after reset = %+v", snapshot)
	}
	// Messages about instances from the previous interval cannot revive them.
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, RemainingSeconds: effectFloat(5), ObservedAt: effectAt(2)})
	tracker.ObserveRemove(testEffectRemove(1, 3))
	snapshot = tracker.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.UpdatesWithoutApply != 1 || snapshot.Counters.RemovesWithoutInstance != 1 {
		t.Fatalf("old-interval messages polluted the new interval: %+v", snapshot)
	}
}

func TestEffectTrackerSnapshotAndResetAtLosesNoConcurrentApply(t *testing.T) {
	tracker := newTestEffectTracker()
	const writers, perWriter = 8, 400
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				tracker.ObserveApply(testEffectApply(uint64(w*perWriter+i+1), float64(i)/100, func(e *EffectApplyEvent) { e.HolderEntityID = uint64(w + 1) }))
			}
		}(w)
	}
	var total uint64
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
loop:
	for {
		select {
		case <-done:
			break loop
		default:
			total += tracker.SnapshotAndResetAt(effectQuery(1)).Counters.Applies
		}
	}
	total += tracker.SnapshotAndResetAt(effectQuery(1)).Counters.Applies
	if total != writers*perWriter {
		t.Fatalf("applies across intervals = %d, want %d", total, writers*perWriter)
	}
}

func TestEffectTrackerSessionIsolation(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, nil))
	tracker.ObserveApply(testEffectApply(1, 1, func(e *EffectApplyEvent) { e.SessionNumber = 2 }))
	snapshot := tracker.Snapshot()
	effect := onlyEffect(t, snapshot)
	if effect.SessionNumber != 2 || snapshot.Counters.BoundaryDrops != 1 {
		t.Fatalf("after a newer session: %+v counters=%+v", effect, snapshot.Counters)
	}
	// Events of the older session no longer apply.
	tracker.ObserveRemove(testEffectRemove(1, 2))
	tracker.ObserveApply(testEffectApply(5, 2, nil))
	snapshot = tracker.Snapshot()
	if len(snapshot.Effects) != 1 || snapshot.Counters.StaleSessionEvents != 2 {
		t.Fatalf("older session leaked in: %+v", snapshot)
	}
}

func TestEffectTrackerEndLiveStateDropsInstancesAtBoundaries(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, nil))
	tracker.ObserveApply(testEffectApply(2, 0, nil))
	tracker.EndLiveState()
	snapshot := tracker.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.BoundaryDrops != 2 || snapshot.StatisticsIntervalID != 1 {
		t.Fatalf("after boundary: %+v", snapshot)
	}
}

func TestEffectTrackerTreatsNegativeHandleAsInstantEffect(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(^uint64(1), 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = nil, nil }))
	tracker.ObserveApply(testEffectApply(math.MaxUint64, 0, nil))
	snapshot := tracker.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.InstantApplies != 2 || snapshot.Counters.Applies != 0 {
		t.Fatalf("negative handles created lifecycle state: %+v", snapshot)
	}
}

func TestEffectTrackerRelations(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation = EffectRelationNonPlayer, EffectRelationNonPlayer
	}))
	if snapshot := tracker.Snapshot(); len(snapshot.Effects) != 0 || snapshot.Counters.ExcludedUnrelated != 1 {
		t.Fatalf("NPC-versus-NPC effect was tracked: %+v", snapshot)
	}

	tracker.ObserveApply(testEffectApply(2, 0, func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation, e.HasSource = EffectRelationUnknown, EffectRelationUnknown, false
	}))
	tracker.ObserveApply(testEffectApply(3, 0, func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation = EffectRelationLocalRole, EffectRelationNonPlayer
	}))
	effects := tracker.Snapshot().Effects
	if len(effects) != 2 || effects[0].HolderRelation != EffectRelationUnknown || effects[0].SourceRelation != EffectRelationUnknown || effects[0].SourceEntityID != nil {
		t.Fatalf("an unknown relation was forced: %+v", effects)
	}

	resolve := func(entityID uint64) EffectEntityRelation {
		if entityID == 10 {
			return EffectRelationNonPlayer
		}
		return EffectRelationUnknown
	}
	tracker.RefreshRelations(1, 10, resolve)
	effects = tracker.Snapshot().Effects
	if effects[0].HolderRelation != EffectRelationNonPlayer {
		t.Fatalf("relation was not upgraded: %+v", effects[0])
	}
	// Refreshing with weaker evidence never downgrades a known relation.
	tracker.RefreshRelations(1, 10, func(uint64) EffectEntityRelation { return EffectRelationUnknown })
	if got := tracker.Snapshot().Effects[1].HolderRelation; got != EffectRelationLocalRole {
		t.Fatalf("known relation downgraded to %s", got)
	}
}

func TestEffectTrackerHolderRemovalEndsOnlyThatHolder(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, nil))
	tracker.ObserveApply(testEffectApply(2, 0, nil))
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) { e.HolderEntityID = 11 }))
	tracker.ObserveHolderRemoved(1, 10, effectAt(1))
	snapshot := tracker.SnapshotAt(effectQuery(1))
	if len(snapshot.Effects) != 1 || snapshot.Effects[0].HolderEntityID != 11 || len(snapshot.RecentEnded) != 2 {
		t.Fatalf("holder removal result = %+v", snapshot)
	}
	for _, ended := range snapshot.RecentEnded {
		if ended.Lifecycle != EffectLifecycleHolderRemoved {
			t.Fatalf("lifecycle = %s, want holder_removed (the effect REMOVE is inferred, not confirmed)", ended.Lifecycle)
		}
	}
}

func TestEffectTrackerCapacityDegradesExplicitly(t *testing.T) {
	tracker := newTestEffectTracker()
	noDuration := func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(-1), effectFloat(-1) }
	for i := 1; i <= 8192; i++ {
		tracker.ObserveApply(testEffectApply(uint64(i), 0, noDuration))
	}
	if snapshot := tracker.Snapshot(); snapshot.Status != EffectSnapshotComplete || len(snapshot.Effects) != 8192 {
		t.Fatalf("at capacity: status=%s effects=%d", snapshot.Status, len(snapshot.Effects))
	}
	tracker.ObserveApply(testEffectApply(9000, 0, noDuration))
	snapshot := tracker.Snapshot()
	if snapshot.Status != EffectSnapshotDegraded || snapshot.Reason != "capacity_exceeded" || snapshot.Counters.CapacityRejected != 1 || len(snapshot.Effects) != 8192 {
		t.Fatalf("over capacity: status=%s reason=%q counters=%+v effects=%d", snapshot.Status, snapshot.Reason, snapshot.Counters, len(snapshot.Effects))
	}
	tracker.Reset()
	if snapshot := tracker.Snapshot(); snapshot.Status != EffectSnapshotComplete {
		t.Fatalf("status after reset = %s", snapshot.Status)
	}
}

func TestEffectTrackerPrunesLongExpiredInstancesBeforeRejecting(t *testing.T) {
	tracker := newTestEffectTracker()
	for i := 1; i <= 8192; i++ {
		tracker.ObserveApply(testEffectApply(uint64(i), 0, nil))
	}
	tracker.ObserveApply(testEffectApply(9000, 500, nil))
	snapshot := tracker.Snapshot()
	if snapshot.Status != EffectSnapshotComplete || len(snapshot.Effects) != 1 || snapshot.Counters.PrunedExpired != 8192 || snapshot.Counters.CapacityRejected != 0 {
		t.Fatalf("pruning result: status=%s effects=%d counters=%+v", snapshot.Status, len(snapshot.Effects), snapshot.Counters)
	}
}

func TestEffectTrackerLongRunKeepsHistoryBounded(t *testing.T) {
	tracker := newTestEffectTracker()
	for i := 1; i <= 5000; i++ {
		tracker.ObserveApply(testEffectApply(uint64(i), float64(i), nil))
		tracker.ObserveRemove(testEffectRemove(uint64(i), float64(i)+2))
		tracker.ObserveRemove(testEffectRemove(uint64(100000+i), float64(i)+2)) // orphan REMOVEs
	}
	snapshot := tracker.Snapshot()
	if len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 128 {
		t.Fatalf("effects=%d ended=%d, want 0 and 128", len(snapshot.Effects), len(snapshot.RecentEnded))
	}
	if len(tracker.tombstones) > 512 || len(tracker.tombstoneOrder) > 512 {
		t.Fatalf("tombstones=%d order=%d exceed the bound", len(tracker.tombstones), len(tracker.tombstoneOrder))
	}
	if last := snapshot.RecentEnded[127]; last.HandleID != 100000+5000 && last.HandleID != 5000 {
		t.Fatalf("history does not keep the newest records: %+v", last)
	}
}

func TestEffectTrackerConcurrentWritersAndReaders(t *testing.T) {
	tracker := newTestEffectTracker()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				handle := uint64(w*1000 + i + 1)
				tracker.ObserveApply(testEffectApply(handle, float64(i)/10, func(e *EffectApplyEvent) { e.HolderEntityID = uint64(w + 1) }))
				tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: uint64(w + 1), HandleID: handle, RemainingSeconds: effectFloat(3), ObservedAt: effectAt(float64(i)/10 + 0.1)})
				if i%2 == 0 {
					tracker.ObserveRemove(EffectRemoveEvent{SessionNumber: 1, HolderEntityID: uint64(w + 1), HandleID: handle, ObservedAt: effectAt(float64(i)/10 + 0.2)})
				}
			}
		}(w)
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				snapshot := tracker.SnapshotAt(effectQuery(float64(i)))
				for _, effect := range snapshot.Effects {
					if effect.RemainingSeconds != nil && *effect.RemainingSeconds < 0 {
						t.Errorf("negative remaining %v", *effect.RemainingSeconds)
					}
				}
			}
		}()
	}
	wg.Wait()
	snapshot := tracker.Snapshot()
	if snapshot.Counters.Applies != 2000 || len(snapshot.Effects) != 1000 || snapshot.Counters.Removes != 1000 {
		t.Fatalf("final state: effects=%d counters=%+v", len(snapshot.Effects), snapshot.Counters)
	}
}

func TestNilEffectTrackerIsSafe(t *testing.T) {
	var tracker *EffectTracker
	tracker.ObserveApply(EffectApplyEvent{})
	tracker.ObserveUpdate(EffectUpdateEvent{})
	tracker.ObserveRemove(EffectRemoveEvent{})
	tracker.ObserveHolderRemoved(1, 1, time.Time{})
	tracker.RefreshRelations(1, 1, func(uint64) EffectEntityRelation { return EffectRelationUnknown })
	tracker.NoteSkippedUncorrelatedHolder()
	tracker.EndLiveState()
	tracker.Reset()
	if snapshot := tracker.Snapshot(); len(snapshot.Effects) != 0 || snapshot.Status != EffectSnapshotComplete {
		t.Fatalf("nil tracker snapshot = %+v", snapshot)
	}
	if snapshot := tracker.SnapshotAt(effectQuery(1)); len(snapshot.Effects) != 0 {
		t.Fatalf("nil tracker SnapshotAt = %+v", snapshot)
	}
	_ = tracker.SnapshotAndResetAt(effectQuery(1))
}

func TestEffectTrackerSeparatesMessagesOfExcludedInstancesFromOrphans(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation = EffectRelationNonPlayer, EffectRelationNonPlayer
	}))
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, RemainingSeconds: effectFloat(3), ObservedAt: effectAt(1)})
	tracker.ObserveRemove(testEffectRemove(1, 2))
	// A genuine orphan stays visible as an orphan.
	tracker.ObserveRemove(testEffectRemove(2, 2))
	counters := tracker.Snapshot().Counters
	if counters.ExcludedUnrelated != 1 || counters.ExcludedMessages != 2 || counters.UpdatesWithoutApply != 0 || counters.RemovesWithoutInstance != 1 {
		t.Fatalf("counters = %+v", counters)
	}
	tracker.Reset()
	tracker.ObserveRemove(testEffectRemove(1, 3))
	if got := tracker.Snapshot().Counters; got.ExcludedMessages != 0 || got.RemovesWithoutInstance != 1 {
		t.Fatalf("excluded keys survived a reset: %+v", got)
	}
}
