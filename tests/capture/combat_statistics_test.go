package capture

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestDamageStatisticsResetClearsSnapshot(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.addDamageEvent(combatEvent{
		SessionNumber: 1, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 10, Timestamp: time.Unix(10, 0),
	}, playerRelationLocal, playerRelationUnrelated)
	statistics.addDamageEvent(combatEvent{
		SessionNumber: 1, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 20, Timestamp: time.Unix(20, 0),
	}, playerRelationLocal, playerRelationUnrelated)
	statistics.addDamageEvent(combatEvent{
		SessionNumber: 1, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 201, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 7,
	}, playerRelationLocal, playerRelationUnknown)
	statistics.addDamageEvent(combatEvent{
		SessionNumber: 1, SourceEntityID: 202, HasSourceEntity: true, TargetEntityID: 100, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 9,
	}, playerRelationUnknown, playerRelationLocal)
	before := statistics.Snapshot()
	if before.DamageSpanDPS.Status != DPSStatusAvailable || before.CaptureSessionID == 0 || before.StatisticsIntervalID != 1 ||
		before.UnattributedOutgoingDamage != 7 || before.UnattributedIncomingDamage != 9 {
		t.Fatalf("expected a valid initial interval before reset, got %+v", before)
	}
	statistics.Reset()
	snapshot := statistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.DamageTaken != 0 || snapshot.UnattributedOutgoingDamage != 0 || snapshot.UnattributedIncomingDamage != 0 || len(snapshot.ByPlayerEntity) != 0 ||
		snapshot.DamageSpanDPS.Status != DPSStatusInsufficientData || snapshot.DamageSpanDPS.Value != 0 {
		t.Fatalf("reset left stale statistics: %+v", snapshot)
	}
	if snapshot.ByPlayerEntity == nil {
		t.Fatal("empty snapshot must return an initialized entity list")
	}
	if snapshot.CaptureSessionID != before.CaptureSessionID || snapshot.StatisticsIntervalID != before.StatisticsIntervalID+1 {
		t.Fatalf("reset changed the capture session or failed to advance the interval: before=%+v after=%+v", before, snapshot)
	}
	if before.DamageDealt != 30 || before.DamageSpanDPS.Status != DPSStatusAvailable || before.DamageSpanDPS.Value != 3 {
		t.Fatalf("reset mutated the previously returned snapshot: %+v", before)
	}

	addStatisticsOutgoingDamage(statistics, 1, 5, time.Unix(100, 0))
	secondInterval := statistics.Snapshot()
	if secondInterval.StatisticsIntervalID != 2 || secondInterval.DamageDealt != 5 ||
		secondInterval.DamageSpanDPS.Status != DPSStatusInsufficientData || secondInterval.DamageSpanDPS.Damage != 5 {
		t.Fatalf("new interval inherited state from the previous interval: %+v", secondInterval)
	}
}

func TestDamageStatisticsCaptureSessionsAreIsolated(t *testing.T) {
	firstCapture := newProcessor(nil, nil, nil, nil).damageStatistics
	secondCapture := newProcessor(nil, nil, nil, nil).damageStatistics
	addStatisticsOutgoingDamage(firstCapture, 1, 10, time.Unix(10, 0))
	addStatisticsOutgoingDamage(secondCapture, 1, 20, time.Unix(20, 0))

	firstSnapshot := firstCapture.Snapshot()
	secondSnapshot := secondCapture.Snapshot()
	if firstSnapshot.CaptureSessionID == 0 || secondSnapshot.CaptureSessionID == 0 ||
		firstSnapshot.CaptureSessionID == secondSnapshot.CaptureSessionID ||
		firstSnapshot.StatisticsIntervalID != secondSnapshot.StatisticsIntervalID {
		t.Fatalf("capture runs did not receive separate interval identities: first=%+v second=%+v", firstSnapshot, secondSnapshot)
	}
	if firstSnapshot.DamageDealt != 10 || secondSnapshot.DamageDealt != 20 {
		t.Fatalf("damage crossed capture-session ownership: first=%+v second=%+v", firstSnapshot, secondSnapshot)
	}
}

func TestDamageStatisticsConcurrentSnapshotResetIsConsistent(t *testing.T) {
	statistics := NewDamageStatistics()
	const iterations = 200
	start := make(chan struct{})
	snapshots := make(chan DamageStatisticsSnapshot, iterations)
	var workers sync.WaitGroup
	workers.Add(3)

	go func() {
		defer workers.Done()
		<-start
		for i := 1; i <= iterations; i++ {
			addStatisticsOutgoingDamage(statistics, 1, 1, time.Unix(int64(i), 0))
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < iterations/2; i++ {
			statistics.Reset()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < iterations; i++ {
			snapshots <- statistics.Snapshot()
		}
	}()
	close(start)
	workers.Wait()
	close(snapshots)

	for snapshot := range snapshots {
		if snapshot.CaptureSessionID == 0 || snapshot.StatisticsIntervalID == 0 {
			t.Fatalf("snapshot omitted its interval identity: %+v", snapshot)
		}
		result := snapshot.DamageSpanDPS
		if result.Status == DPSStatusAvailable {
			if result.Damage != snapshot.DamageDealt || result.Duration <= 0 || result.Value != float64(snapshot.DamageDealt)/result.Duration.Seconds() {
				t.Fatalf("snapshot contains a partial interval result: %+v", snapshot)
			}
		} else if result.Status != DPSStatusInsufficientData {
			t.Fatalf("snapshot contains an unexpected interval state: %+v", snapshot)
		}
	}
}

func TestDamageStatisticsRollingWindowsUseFixedDenominators(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(100, 0)
	addStatisticsOutgoingDamage(statistics, 1, 100, base.Add(2*time.Second))
	addStatisticsOutgoingDamage(statistics, 1, 250, base.Add(7*time.Second))
	snapshot := rollingSnapshotAt(statistics, base.Add(10*time.Second))
	if snapshot.Rolling5sDPS.Status != DPSStatusAvailable || snapshot.Rolling5sDPS.Damage != 250 ||
		snapshot.Rolling5sDPS.Duration != 5*time.Second || snapshot.Rolling5sDPS.Value != 50 {
		t.Fatalf("unexpected rolling 5s result: %+v", snapshot.Rolling5sDPS)
	}
	if snapshot.Rolling10sDPS.Status != DPSStatusAvailable || snapshot.Rolling10sDPS.Damage != 350 ||
		snapshot.Rolling10sDPS.Duration != 10*time.Second || snapshot.Rolling10sDPS.Value != 35 {
		t.Fatalf("unexpected rolling 10s result: %+v", snapshot.Rolling10sDPS)
	}
}

func TestDamageStatisticsRollingWindowBoundsAndExpiry(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(200, 0)
	addStatisticsOutgoingDamage(statistics, 1, 10, base)
	addStatisticsOutgoingDamage(statistics, 1, 20, base.Add(5*time.Second))
	addStatisticsOutgoingDamage(statistics, 1, 30, base.Add(10*time.Second))
	snapshot := rollingSnapshotAt(statistics, base.Add(10*time.Second))
	if snapshot.Rolling5sDPS.Damage != 30 || snapshot.Rolling10sDPS.Damage != 50 {
		t.Fatalf("window boundaries were not left-open and right-closed: 5s=%+v 10s=%+v", snapshot.Rolling5sDPS, snapshot.Rolling10sDPS)
	}
	quiet := rollingSnapshotAt(statistics, base.Add(21*time.Second))
	if quiet.Rolling5sDPS.Status != DPSStatusAvailable || quiet.Rolling5sDPS.Damage != 0 || quiet.Rolling5sDPS.Value != 0 ||
		quiet.Rolling10sDPS.Damage != 0 || len(statistics.rollingEvents) != 0 {
		t.Fatalf("quiet query did not expire old events to zero: %+v retained=%d", quiet, len(statistics.rollingEvents))
	}
}

func TestDamageStatisticsRollingEventsAcceptOutOfOrderAndEqualTimes(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(300, 0)
	addStatisticsOutgoingDamage(statistics, 1, 10, base.Add(2*time.Second))
	addStatisticsOutgoingDamage(statistics, 1, 20, base.Add(time.Second))
	addStatisticsOutgoingDamage(statistics, 1, 30, base.Add(time.Second))
	snapshot := rollingSnapshotAt(statistics, base.Add(3*time.Second))
	if snapshot.Rolling10sDPS.Status != DPSStatusAvailable || snapshot.Rolling10sDPS.Damage != 60 || len(statistics.rollingEvents) != 3 {
		t.Fatalf("out-of-order or same-time events were lost: %+v events=%+v", snapshot.Rolling10sDPS, statistics.rollingEvents)
	}
}

func TestDamageStatisticsRollingFutureEventsAreExcludedAndQueryCannotGoBackwards(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(400, 0)
	addStatisticsOutgoingDamage(statistics, 1, 25, base.Add(5*time.Second))
	early := rollingSnapshotAt(statistics, base)
	if early.Rolling5sDPS.Status != DPSStatusAvailable || early.Rolling5sDPS.Damage != 0 {
		t.Fatalf("future event affected an earlier query: %+v", early.Rolling5sDPS)
	}
	later := rollingSnapshotAt(statistics, base.Add(5*time.Second))
	if later.Rolling5sDPS.Damage != 25 {
		t.Fatalf("event was not included at its timestamp: %+v", later.Rolling5sDPS)
	}
	backwards := rollingSnapshotAt(statistics, base.Add(4*time.Second))
	if backwards.Rolling5sDPS.Status != DPSStatusUnavailable || backwards.Rolling5sDPS.Reason != "query_time_moved_backwards" {
		t.Fatalf("backwards query did not return an explicit unavailable result: %+v", backwards.Rolling5sDPS)
	}
	forwardAgain := rollingSnapshotAt(statistics, base.Add(6*time.Second))
	if forwardAgain.Rolling5sDPS.Status != DPSStatusAvailable || forwardAgain.Rolling5sDPS.Damage != 25 {
		t.Fatalf("a rejected backwards query corrupted later results: %+v", forwardAgain.Rolling5sDPS)
	}
}

func TestDamageStatisticsRollingRejectsQueryBeforeRetainedWindow(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(450, 0)
	addStatisticsOutgoingDamage(statistics, 1, 10, base)
	addStatisticsOutgoingDamage(statistics, 1, 20, base.Add(20*time.Second))
	snapshot := rollingSnapshotAt(statistics, base.Add(time.Second))
	if snapshot.Rolling10sDPS.Status != DPSStatusUnavailable || snapshot.Rolling10sDPS.Reason != "query_before_retained_window" {
		t.Fatalf("query before pruned history returned a plausible value: %+v", snapshot.Rolling10sDPS)
	}
}

func TestDamageStatisticsRollingRejectsMissingOrMismatchedQueryTime(t *testing.T) {
	statistics := NewDamageStatistics()
	initial := statistics.Snapshot()
	if initial.Rolling5sDPS.Status != DPSStatusUnavailable || initial.Rolling5sDPS.Reason != "query_time_required" {
		t.Fatalf("Snapshot without a query time returned a plausible rolling value: %+v", initial.Rolling5sDPS)
	}
	missing := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: initial.CaptureSessionID})
	if missing.Rolling5sDPS.Status != DPSStatusUnavailable || missing.Rolling5sDPS.Reason != "query_time_unavailable" {
		t.Fatalf("zero query time was not rejected: %+v", missing.Rolling5sDPS)
	}
	mismatch := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: initial.CaptureSessionID + 1, Timestamp: time.Unix(500, 0)})
	if mismatch.Rolling5sDPS.Status != DPSStatusUnavailable || mismatch.Rolling5sDPS.Reason != "capture_session_mismatch" {
		t.Fatalf("query from another capture was accepted: %+v", mismatch.Rolling5sDPS)
	}
}

func TestDamageStatisticsRollingUsesConfirmedOutgoingEventsOnly(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(600, 0)
	addStatisticsOutgoingDamage(statistics, 1, 100, base)
	statistics.addDamageEvent(combatEvent{SessionNumber: 1, SourceEntityID: 200, HasSourceEntity: true, TargetEntityID: 100, HasTargetEntity: true, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 50, Timestamp: base}, playerRelationUnrelated, playerRelationLocal)
	statistics.addDamageEvent(combatEvent{SessionNumber: 1, SourceEntityID: 300, HasSourceEntity: true, TargetEntityID: 400, HasTargetEntity: true, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 75, Timestamp: base}, playerRelationUnrelated, playerRelationUnrelated)
	statistics.addDamageEvent(combatEvent{SessionNumber: 1, SourceEntityID: 101, HasSourceEntity: true, TargetEntityID: 400, HasTargetEntity: true, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 125, Timestamp: base}, playerRelationUnknown, playerRelationUnrelated)
	snapshot := rollingSnapshotAt(statistics, base)
	if snapshot.Rolling5sDPS.Damage != 100 || snapshot.DamageTaken != 50 || snapshot.UnattributedOutgoingDamage != 125 {
		t.Fatalf("rolling output included incoming, NPC-only, or unresolved damage: %+v", snapshot)
	}
}

func TestDamageStatisticsRollingResetAndSnapshotAndResetPreserveCopies(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(700, 0)
	addStatisticsOutgoingDamage(statistics, 1, 90, base)
	statistics.addDamageEvent(combatEvent{SessionNumber: 1, SourceEntityID: 101, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 12, Timestamp: base}, playerRelationUnknown, playerRelationUnrelated)
	before := rollingSnapshotAt(statistics, base)
	cutover := statistics.snapshotAndResetAt(CaptureQueryTime{CaptureSessionID: before.CaptureSessionID, Timestamp: base})
	if cutover.Rolling5sDPS.Damage != 90 || cutover.StatisticsIntervalID != 1 || cutover.UnattributedOutgoingDamage != 12 {
		t.Fatalf("snapshot-and-reset missed the prior interval: %+v", cutover)
	}
	after := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: before.CaptureSessionID, Timestamp: base.Add(time.Second)})
	if after.StatisticsIntervalID != 2 || after.DamageDealt != 0 || after.UnattributedOutgoingDamage != 0 || after.Rolling5sDPS.Damage != 0 {
		t.Fatalf("reset carried old interval state forward: %+v", after)
	}
	statistics.Reset()
	reset := statistics.Snapshot()
	if reset.StatisticsIntervalID != 3 || reset.DamageDealt != 0 || len(statistics.rollingEvents) != 0 || !statistics.rollingLastQuery.IsZero() {
		t.Fatalf("Reset did not clear window state and advance interval: %+v", reset)
	}
	if before.Rolling5sDPS.Damage != 90 || before.UnattributedOutgoingDamage != 12 || before.StatisticsIntervalID != 1 {
		t.Fatalf("subsequent resets mutated an old snapshot: %+v", before)
	}
}

func TestDamageStatisticsRollingSessionAndIntervalIsolation(t *testing.T) {
	first := NewDamageStatistics()
	base := time.Unix(800, 0)
	addStatisticsOutgoingDamage(first, 1, 10, base)
	firstResult := rollingSnapshotAt(first, base)
	second := NewDamageStatistics()
	secondResult := second.SnapshotAt(CaptureQueryTime{CaptureSessionID: second.Snapshot().CaptureSessionID, Timestamp: base})
	if firstResult.Rolling5sDPS.Damage != 10 || secondResult.Rolling5sDPS.Damage != 0 || firstResult.CaptureSessionID == secondResult.CaptureSessionID {
		t.Fatalf("rolling values crossed capture sessions: first=%+v second=%+v", firstResult.Rolling5sDPS, secondResult.Rolling5sDPS)
	}
	addStatisticsOutgoingDamage(first, 1, 20, base.Add(time.Second))
	first.Reset()
	newInterval := rollingSnapshotAt(first, base.Add(2*time.Second))
	if newInterval.StatisticsIntervalID != 2 || newInterval.Rolling5sDPS.Damage != 0 {
		t.Fatalf("Reset did not isolate the new interval: %+v", newInterval)
	}
}

func TestDamageStatisticsRollingRejectsMissingEventTimeAndProtocolSessionMix(t *testing.T) {
	missingTime := NewDamageStatistics()
	addStatisticsOutgoingDamage(missingTime, 1, 10, time.Time{})
	missing := rollingSnapshotAt(missingTime, time.Unix(900, 0))
	if missing.Rolling5sDPS.Status != DPSStatusUnavailable || missing.Rolling5sDPS.Reason != "outgoing_event_missing_observed_time" {
		t.Fatalf("missing event time produced a number: %+v", missing.Rolling5sDPS)
	}
	mixed := NewDamageStatistics()
	addStatisticsOutgoingDamage(mixed, 1, 10, time.Unix(900, 0))
	addStatisticsOutgoingDamage(mixed, 2, 20, time.Unix(901, 0))
	result := rollingSnapshotAt(mixed, time.Unix(901, 0))
	if result.Rolling5sDPS.Status != DPSStatusUnavailable || result.Rolling5sDPS.Reason != "multiple_sessions_require_separate_statistics" {
		t.Fatalf("protocol sessions were mixed in one rolling result: %+v", result.Rolling5sDPS)
	}
}

func TestDamageStatisticsRollingRetentionIsBoundedAndCapacityFailureIsExplicit(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(1000, 0)
	for second := int64(0); second < 100; second++ {
		addStatisticsOutgoingDamage(statistics, 1, 1, base.Add(time.Duration(second)*time.Second))
	}
	snapshot := rollingSnapshotAt(statistics, base.Add(99*time.Second))
	if snapshot.Rolling10sDPS.Damage != 10 || len(statistics.rollingEvents) > 11 {
		t.Fatalf("rolling retention exceeded the 10s horizon: dps=%+v retained=%d", snapshot.Rolling10sDPS, len(statistics.rollingEvents))
	}
	statistics = NewDamageStatistics()
	for i := 0; i <= maxRollingDamageEvents; i++ {
		addStatisticsOutgoingDamage(statistics, 1, 1, base)
	}
	overflow := rollingSnapshotAt(statistics, base)
	if overflow.Rolling5sDPS.Status != DPSStatusUnavailable || overflow.Rolling5sDPS.Reason != "event_capacity_exceeded" || len(statistics.rollingEvents) != 0 {
		t.Fatalf("capacity overflow returned a plausible result or retained unbounded data: %+v retained=%d", overflow.Rolling5sDPS, len(statistics.rollingEvents))
	}
}

func TestDamageStatisticsRollingSnapshotAndResetIsAtomic(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(1100, 0)
	addStatisticsOutgoingDamage(statistics, 1, 40, base)
	old := statistics.snapshotAndResetAt(CaptureQueryTime{CaptureSessionID: statistics.Snapshot().CaptureSessionID, Timestamp: base})
	current := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: old.CaptureSessionID, Timestamp: base.Add(time.Second)})
	if old.Rolling5sDPS.Damage != 40 || old.StatisticsIntervalID != 1 || current.Rolling5sDPS.Damage != 0 || current.StatisticsIntervalID != 2 {
		t.Fatalf("atomic snapshot-and-reset mixed interval state: old=%+v current=%+v", old.Rolling5sDPS, current.Rolling5sDPS)
	}
}

func TestDamageStatisticsRollingSnapshotAndResetRemainConsistentConcurrently(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(1200, 0)
	query := CaptureQueryTime{CaptureSessionID: statistics.Snapshot().CaptureSessionID, Timestamp: base}
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 500; i++ {
			addStatisticsOutgoingDamage(statistics, 1, 1, base)
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 100; i++ {
			statistics.Reset()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 500; i++ {
			snapshot := statistics.SnapshotAt(query)
			if snapshot.Rolling5sDPS.Status == DPSStatusAvailable &&
				(snapshot.Rolling5sDPS.Damage < 0 || snapshot.Rolling5sDPS.Damage > snapshot.DamageDealt || math.IsNaN(snapshot.Rolling5sDPS.Value) || math.IsInf(snapshot.Rolling5sDPS.Value, 0)) {
				t.Errorf("concurrent snapshot exposed inconsistent rolling state: %+v", snapshot)
				return
			}
		}
	}()
	close(start)
	workers.Wait()
}

func TestDamageStatisticsRollingOverflowIsUnavailableAndFinite(t *testing.T) {
	statistics := NewDamageStatistics()
	base := time.Unix(1300, 0)
	addStatisticsOutgoingDamage(statistics, 1, math.MaxInt64, base)
	addStatisticsOutgoingDamage(statistics, 1, 1, base.Add(time.Second))
	snapshot := rollingSnapshotAt(statistics, base.Add(time.Second))
	for _, result := range []DPSResult{snapshot.Rolling5sDPS, snapshot.Rolling10sDPS} {
		if result.Status != DPSStatusUnavailable || result.Reason != "damage_sum_overflow" || math.IsNaN(result.Value) || math.IsInf(result.Value, 0) {
			t.Fatalf("overflow did not produce a finite unavailable result: %+v", result)
		}
	}
}

func rollingSnapshotAt(statistics *DamageStatistics, timestamp time.Time) DamageStatisticsSnapshot {
	current := statistics.Snapshot()
	return statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: current.CaptureSessionID, Timestamp: timestamp})
}

func TestDamageStatisticsConcurrentIntervalSwitchDoesNotLoseDamage(t *testing.T) {
	statistics := NewDamageStatistics()
	const iterations = 200
	start := make(chan struct{})
	var workers sync.WaitGroup
	var snapshotsMu sync.Mutex
	snapshots := make([]DamageStatisticsSnapshot, 0, iterations+1)
	workers.Add(2)

	go func() {
		defer workers.Done()
		<-start
		for i := 1; i <= iterations; i++ {
			addStatisticsOutgoingDamage(statistics, 1, 1, time.Unix(int64(i), 0))
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < iterations; i++ {
			snapshot := statistics.snapshotAndReset()
			snapshotsMu.Lock()
			snapshots = append(snapshots, snapshot)
			snapshotsMu.Unlock()
		}
	}()
	close(start)
	workers.Wait()
	snapshots = append(snapshots, statistics.snapshotAndReset())

	var totalDamage int64
	var totalEvents uint64
	for i, snapshot := range snapshots {
		if snapshot.StatisticsIntervalID != uint64(i+1) {
			t.Fatalf("interval sequence skipped or repeated: index=%d snapshot=%+v", i, snapshot)
		}
		totalDamage += snapshot.DamageDealt
		totalEvents += snapshot.OutgoingEvents
	}
	if totalDamage != iterations || totalEvents != iterations {
		t.Fatalf("concurrent interval switches lost or duplicated events: damage=%d events=%d", totalDamage, totalEvents)
	}
}

func TestDamageStatisticsDamageSpanDPSUsesConfirmedOutgoingDamage(t *testing.T) {
	statistics := NewDamageStatistics()
	addStatisticsOutgoingDamage(statistics, 1, 100, time.Unix(12, 0))
	addStatisticsOutgoingDamage(statistics, 1, 200, time.Unix(10, 0))
	statistics.addDamageEvent(combatEvent{
		SessionNumber: 1, SourceEntityID: 200, HasSourceEntity: true, TargetEntityID: 100, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 9000, Timestamp: time.Unix(11, 0),
	}, playerRelationUnrelated, playerRelationLocal)

	snapshot := statistics.Snapshot()
	got := snapshot.DamageSpanDPS
	if snapshot.StatisticsIntervalID != 1 || snapshot.DamageDealt != 300 || got.Status != DPSStatusAvailable || got.Damage != 300 ||
		got.Duration != 2*time.Second || got.Value != 150 || !got.StartObservedAt.Equal(time.Unix(10, 0)) || !got.EndObservedAt.Equal(time.Unix(12, 0)) {
		t.Fatalf("damage-span DPS did not use the full confirmed outgoing range: %+v", snapshot)
	}
	if snapshot.GameTimeDPS.Status != DPSStatusUnavailable || snapshot.GameTimeDPS.Value != 0 {
		t.Fatalf("game-time DPS has no verified timer source and must stay unavailable: %+v", snapshot)
	}
	// Without battle-state signals the encounter is open from the first confirmed activity; its live value is
	// evaluated at the newest observed capture time and is labeled live, not final.
	encounter := snapshot.EncounterDPS
	if encounter.Status != DPSStatusAvailable || encounter.Confidence != "live" || encounter.Damage != 300 ||
		!encounter.StartObservedAt.Equal(time.Unix(10, 0)) || !encounter.EndObservedAt.Equal(time.Unix(12, 0)) || encounter.Value != 150 ||
		snapshot.Encounters.Current == nil || snapshot.Encounters.Current.StartConfirmed || snapshot.Encounters.Current.Totals.DamageTaken != 9000 {
		t.Fatalf("encounter DPS must be the live value of the open encounter: %+v", snapshot.Encounters)
	}
}

func TestDamageStatisticsDamageSpanDPSValidityStatuses(t *testing.T) {
	tests := []struct {
		name            string
		add             func(*DamageStatistics)
		status          DPSStatus
		reason          string
		confirmedDamage int64
	}{
		{
			name:            "single event",
			add:             func(s *DamageStatistics) { addStatisticsOutgoingDamage(s, 1, 100, time.Unix(10, 0)) },
			status:          DPSStatusInsufficientData,
			reason:          "requires_multiple_damage_events",
			confirmedDamage: 100,
		},
		{
			name: "same observed time",
			add: func(s *DamageStatistics) {
				at := time.Unix(10, 0)
				addStatisticsOutgoingDamage(s, 1, 100, at)
				addStatisticsOutgoingDamage(s, 1, 200, at)
			},
			status:          DPSStatusInvalidDuration,
			reason:          "non_positive_damage_span",
			confirmedDamage: 300,
		},
		{
			name: "missing observed time",
			add: func(s *DamageStatistics) {
				addStatisticsOutgoingDamage(s, 1, 100, time.Unix(10, 0))
				addStatisticsOutgoingDamage(s, 1, 200, time.Time{})
			},
			status:          DPSStatusUnavailable,
			reason:          "outgoing_event_missing_observed_time",
			confirmedDamage: 300,
		},
		{
			name: "multiple protocol sessions",
			add: func(s *DamageStatistics) {
				addStatisticsOutgoingDamage(s, 1, 100, time.Unix(10, 0))
				addStatisticsOutgoingDamage(s, 2, 200, time.Unix(12, 0))
			},
			status:          DPSStatusUnavailable,
			reason:          "multiple_sessions_require_separate_statistics",
			confirmedDamage: 300,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statistics := NewDamageStatistics()
			test.add(statistics)
			snapshot := statistics.Snapshot()
			got := snapshot.DamageSpanDPS
			if got.Status != test.status || got.Reason != test.reason || got.Value != 0 || math.IsNaN(got.Value) || math.IsInf(got.Value, 0) {
				t.Fatalf("unexpected invalid DPS result: %+v", got)
			}
			if snapshot.StatisticsIntervalID != 1 || snapshot.DamageDealt != test.confirmedDamage {
				t.Fatalf("damage event started an interval or totals changed unexpectedly: %+v", snapshot)
			}
		})
	}
}

func addStatisticsOutgoingDamage(statistics *DamageStatistics, session int, amount int64, at time.Time) {
	statistics.addDamageEvent(combatEvent{
		SessionNumber: session, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: amount, Timestamp: at,
	}, playerRelationLocal, playerRelationUnrelated)
}
