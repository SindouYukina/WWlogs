package capture

import (
	"math"
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
	if got := statistics.Snapshot().DamageSpanDPS; got.Status != DPSStatusAvailable {
		t.Fatalf("expected a valid span before reset, got %+v", got)
	}
	statistics.Reset()
	snapshot := statistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.DamageTaken != 0 || snapshot.UnattributedOutgoingDamage != 0 || len(snapshot.ByPlayerEntity) != 0 ||
		snapshot.DamageSpanDPS.Status != DPSStatusInsufficientData || snapshot.DamageSpanDPS.Value != 0 {
		t.Fatalf("reset left stale statistics: %+v", snapshot)
	}
	if snapshot.ByPlayerEntity == nil {
		t.Fatal("empty snapshot must return an initialized entity list")
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
	if snapshot.DamageDealt != 300 || got.Status != DPSStatusAvailable || got.Damage != 300 ||
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
		name   string
		add    func(*DamageStatistics)
		status DPSStatus
		reason string
	}{
		{
			name:   "single event",
			add:    func(s *DamageStatistics) { addStatisticsOutgoingDamage(s, 1, 100, time.Unix(10, 0)) },
			status: DPSStatusInsufficientData, reason: "requires_multiple_damage_events",
		},
		{
			name: "same observed time",
			add: func(s *DamageStatistics) {
				at := time.Unix(10, 0)
				addStatisticsOutgoingDamage(s, 1, 100, at)
				addStatisticsOutgoingDamage(s, 1, 200, at)
			},
			status: DPSStatusInvalidDuration, reason: "non_positive_damage_span",
		},
		{
			name: "missing observed time",
			add: func(s *DamageStatistics) {
				addStatisticsOutgoingDamage(s, 1, 100, time.Unix(10, 0))
				addStatisticsOutgoingDamage(s, 1, 200, time.Time{})
			},
			status: DPSStatusUnavailable, reason: "outgoing_event_missing_observed_time",
		},
		{
			name: "multiple sessions",
			add: func(s *DamageStatistics) {
				addStatisticsOutgoingDamage(s, 1, 100, time.Unix(10, 0))
				addStatisticsOutgoingDamage(s, 2, 200, time.Unix(12, 0))
			},
			status: DPSStatusUnavailable, reason: "multiple_sessions_require_separate_statistics",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statistics := NewDamageStatistics()
			test.add(statistics)
			got := statistics.Snapshot().DamageSpanDPS
			if got.Status != test.status || got.Reason != test.reason || got.Value != 0 || math.IsNaN(got.Value) || math.IsInf(got.Value, 0) {
				t.Fatalf("unexpected invalid DPS result: %+v", got)
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
