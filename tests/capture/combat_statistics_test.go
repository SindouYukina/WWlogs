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
	if snapshot.EncounterDPS.Status != DPSStatusUnavailable || snapshot.EncounterDPS.Value != 0 ||
		snapshot.GameTimeDPS.Status != DPSStatusUnavailable || snapshot.GameTimeDPS.Value != 0 {
		t.Fatalf("unavailable time models were exposed as calculated: %+v", snapshot)
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
