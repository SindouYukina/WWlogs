package capture

import (
	"testing"
	"time"
)

func TestClassifyCombatFlowKeepsUnknownIsolated(t *testing.T) {
	cases := []struct {
		source, target playerRelation
		flow           CombatFlow
		confidence     AttributionConfidence
	}{
		{playerRelationLocal, playerRelationUnrelated, CombatFlowLocalOutgoing, AttributionConfirmed},
		{playerRelationUnrelated, playerRelationLocal, CombatFlowLocalIncoming, AttributionConfirmed},
		{playerRelationLocal, playerRelationUnknown, CombatFlowUnattributedOutgoing, AttributionUnattributed},
		{playerRelationUnknown, playerRelationLocal, CombatFlowUnattributedIncoming, AttributionUnattributed},
		{playerRelationUnknown, playerRelationUnrelated, CombatFlowUnattributedOutgoing, AttributionUnattributed},
		{playerRelationUnrelated, playerRelationUnknown, CombatFlowUnattributedIncoming, AttributionUnattributed},
		{playerRelationLocal, playerRelationLocal, CombatFlowLocalInternal, AttributionNotLocal},
		{playerRelationUnrelated, playerRelationUnrelated, CombatFlowExcluded, AttributionNotLocal},
		{playerRelationUnknown, playerRelationUnknown, CombatFlowUnresolved, AttributionUnattributed},
	}
	for _, c := range cases {
		flow := classifyCombatFlow(c.source, c.target)
		if flow != c.flow || flow.confidence() != c.confidence {
			t.Errorf("%s -> %s: got %s/%s, want %s/%s", c.source, c.target, flow, flow.confidence(), c.flow, c.confidence)
		}
	}
}

func TestNormalizeCombatEventRejectsIncompleteResults(t *testing.T) {
	base := combatEvent{
		SessionNumber: 2, RPCID: 77, SourceEntityID: 10, HasSourceEntity: true, TargetEntityID: 20, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 5, Timestamp: time.Unix(100, 0), DamageID: 9, SkillID: 8,
	}
	record, ok := normalizeCombatEvent(base, playerRelationLocal, playerRelationUnrelated)
	if !ok || record.Kind != CombatEventDamage || record.Key != (CombatEventKey{ProtocolSession: 2, RPCID: 77}) ||
		record.Flow != CombatFlowLocalOutgoing || record.Amount != 5 || !record.ObservedAt.Equal(time.Unix(100, 0)) ||
		record.Source.Relation != "LOCAL" || record.Target.Relation != "UNRELATED" || record.Evidence != combatEvidenceDamageRPCResponse {
		t.Fatalf("unexpected normalized record: ok=%v %+v", ok, record)
	}
	healing := base
	healing.EventTypeCandidate = "HEALING_CANDIDATE"
	if record, ok := normalizeCombatEvent(healing, playerRelationLocal, playerRelationLocal); !ok || record.Kind != CombatEventHealing {
		t.Fatalf("healing result must normalize as healing, got ok=%v %+v", ok, record)
	}
	for name, mutate := range map[string]func(*combatEvent){
		"zero amount":    func(e *combatEvent) { e.Damage = 0 },
		"missing source": func(e *combatEvent) { e.HasSourceEntity = false },
		"zero target":    func(e *combatEvent) { e.TargetEntityID = 0 },
	} {
		event := base
		mutate(&event)
		if _, ok := normalizeCombatEvent(event, playerRelationLocal, playerRelationUnrelated); ok {
			t.Errorf("%s: incomplete result must not enter statistics", name)
		}
	}
}

func TestDamageStatisticsRejectsRepeatedResponseKeys(t *testing.T) {
	statistics := NewDamageStatistics()
	event := combatEvent{
		SessionNumber: 1, RPCID: 41, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 30, Timestamp: time.Unix(10, 0),
	}
	statistics.addDamageEvent(event, playerRelationLocal, playerRelationUnrelated)
	statistics.addDamageEvent(event, playerRelationLocal, playerRelationUnrelated)
	otherSession := event
	otherSession.SessionNumber = 2
	statistics.addDamageEvent(otherSession, playerRelationLocal, playerRelationUnrelated)
	snapshot := statistics.Snapshot()
	if snapshot.DamageDealt != 60 || snapshot.OutgoingEvents != 2 || snapshot.DuplicateEvents != 1 {
		t.Fatalf("a repeated key must be counted once and the same RPC id in another session must count: %+v", snapshot)
	}

	statistics.Reset()
	statistics.addDamageEvent(event, playerRelationLocal, playerRelationUnrelated)
	afterReset := statistics.Snapshot()
	if afterReset.DamageDealt != 0 || afterReset.DuplicateEvents != 1 {
		t.Fatalf("a response applied before Reset must not re-enter the next interval: %+v", afterReset)
	}
}

func TestDamageStatisticsCountsEventsWithoutResponseKey(t *testing.T) {
	statistics := NewDamageStatistics()
	for i := 0; i < 3; i++ {
		addStatisticsOutgoingDamage(statistics, 1, 10, time.Unix(int64(10+i), 0))
	}
	if snapshot := statistics.Snapshot(); snapshot.DamageDealt != 30 || snapshot.DuplicateEvents != 0 {
		t.Fatalf("events without an RPC id are not deduplicated: %+v", snapshot)
	}
}

func TestDamageStatisticsAppliedKeysAreBounded(t *testing.T) {
	statistics := NewDamageStatistics()
	for rpc := uint64(1); rpc <= maxStatisticsAppliedKeys+10; rpc++ {
		statistics.addDamageEvent(combatEvent{
			SessionNumber: 1, RPCID: rpc, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
			EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 1, Timestamp: time.Unix(int64(rpc), 0),
		}, playerRelationLocal, playerRelationUnrelated)
	}
	statistics.mu.RLock()
	defer statistics.mu.RUnlock()
	if len(statistics.appliedKeys) != maxStatisticsAppliedKeys || len(statistics.appliedKeyOrder) != maxStatisticsAppliedKeys {
		t.Fatalf("applied key set grew past its bound: map=%d order=%d", len(statistics.appliedKeys), len(statistics.appliedKeyOrder))
	}
}
