package capture

import (
	"bytes"
	"testing"
	"time"
)

func TestDamageStatisticsAccumulatesAndSeparatesPlayerEntities(t *testing.T) {
	p := newStatisticsTestProcessor()
	p.Masters = &GameMaster{Damage: map[uint64]DamageMasterRecord{1: {ID: 1, CalculateType: 0}}}
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
	addTestRuntimeEntity(p, 201, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9002, true })

	dealt := testDamageEvent(p, 100, 200)
	dealt.SessionNumber, dealt.EventTypeCandidate, dealt.Timestamp = 1, "DAMAGE_CANDIDATE", time.Unix(10, 0)
	dealt.Damage = 900
	p.WriteDamageEvent(dealt)

	taken := testDamageEvent(p, 200, 100)
	taken.SessionNumber, taken.EventTypeCandidate, taken.Timestamp = 1, "DAMAGE_CANDIDATE", time.Unix(11, 0)
	taken.Damage = 125
	p.WriteDamageEvent(taken)

	secondEntity := testDamageEvent(p, 100, 201)
	secondEntity.SessionNumber, secondEntity.EventTypeCandidate = 2, "DAMAGE_CANDIDATE"
	secondEntity.Damage = 75
	p.WriteDamageEvent(secondEntity)

	snapshot := p.DamageStatistics.Snapshot()
	if snapshot.DamageDealt != 975 || snapshot.DamageTaken != 125 || snapshot.OutgoingEvents != 2 || snapshot.IncomingEvents != 1 {
		t.Fatalf("unexpected confirmed totals: %+v", snapshot)
	}
	if len(snapshot.ByPlayerEntity) != 2 {
		t.Fatalf("player entities were mixed across sessions: %+v", snapshot.ByPlayerEntity)
	}
	first, second := snapshot.ByPlayerEntity[0], snapshot.ByPlayerEntity[1]
	if first.SessionNumber != 1 || first.EntityID != 100 || first.DamageDealt != 900 || first.DamageTaken != 125 || first.FirstObserved != time.Unix(10, 0) || first.LastObserved != time.Unix(11, 0) {
		t.Fatalf("unexpected first entity statistics: %+v", first)
	}
	if second.SessionNumber != 2 || second.EntityID != 100 || second.DamageDealt != 75 {
		t.Fatalf("unexpected second session statistics: %+v", second)
	}
}

func TestDamageStatisticsIgnoresNPCOnlyAndNonDamageEvents(t *testing.T) {
	p := newStatisticsTestProcessor()
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
	addTestRuntimeEntity(p, 201, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9002, true })

	npcOnly := testDamageEvent(p, 200, 201)
	npcOnly.EventTypeCandidate, npcOnly.Damage = "DAMAGE_CANDIDATE", 300
	p.WriteDamageEvent(npcOnly)

	healing := testDamageEvent(p, 100, 200)
	healing.EventTypeCandidate, healing.Damage = "HEALING_CANDIDATE", 500
	p.WriteDamageEvent(healing)

	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 0 || got.DamageTaken != 0 || len(got.ByPlayerEntity) != 0 {
		t.Fatalf("NPC-only or healing event changed damage totals: %+v", got)
	}
}

func TestDamageStatisticsKeepsUnresolvedSummonDamageUnattributed(t *testing.T) {
	p := newStatisticsTestProcessor()
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
		entity.SummonerPlayerID, entity.HasSummonerPlayerID = 5001, true
	})
	addTestRuntimeEntity(p, 102, func(entity *RuntimeEntity) {
		entity.SummonerPlayerID, entity.HasSummonerPlayerID = 5002, true
	})
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate, event.Damage = "DAMAGE_CANDIDATE", 400
	p.WriteDamageEvent(event)

	incoming := testDamageEvent(p, 102, 100)
	incoming.EventTypeCandidate, incoming.Damage = "DAMAGE_CANDIDATE", 250
	p.WriteDamageEvent(incoming)

	snapshot := p.DamageStatistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.DamageTaken != 0 || len(snapshot.ByPlayerEntity) != 0 ||
		snapshot.UnattributedOutgoingDamage != 400 || snapshot.UnattributedOutgoingEvents != 1 ||
		snapshot.UnattributedIncomingDamage != 250 || snapshot.UnattributedIncomingEvents != 1 {
		t.Fatalf("unresolved summon damage was credited instead of kept unattributed: %+v", snapshot)
	}
	if snapshot.DamageSpanDPS.Status != DPSStatusInsufficientData || snapshot.DamageSpanDPS.Damage != 0 {
		t.Fatalf("unresolved summon damage changed confirmed DPS: %+v", snapshot.DamageSpanDPS)
	}
}

func TestProcessorSnapshotAndResetReturnsOldIntervalAndPreservesProtocolState(t *testing.T) {
	p := newStatisticsTestProcessor()
	p.SessionNumber = 5
	p.Pending[42] = PendingDamageRequest{HasDamageID: true, DamageID: 1}
	p.consumed[99] = consumedResponse{sequence: 1}
	addStatisticsOutgoingDamage(p.DamageStatistics, 5, 10, time.Unix(10, 0))
	addStatisticsOutgoingDamage(p.DamageStatistics, 5, 20, time.Unix(20, 0))

	oldInterval := p.SnapshotAndResetDamageStatistics()
	newInterval := p.DamageStatisticsSnapshot()
	if oldInterval.CaptureSessionID != newInterval.CaptureSessionID ||
		oldInterval.StatisticsIntervalID != 1 || newInterval.StatisticsIntervalID != 2 ||
		oldInterval.DamageDealt != 30 || oldInterval.DamageSpanDPS.Status != DPSStatusAvailable ||
		newInterval.DamageDealt != 0 || newInterval.OutgoingEvents != 0 ||
		newInterval.DamageSpanDPS.Status != DPSStatusInsufficientData {
		t.Fatalf("snapshot-and-reset did not switch intervals atomically: old=%+v new=%+v", oldInterval, newInterval)
	}
	if p.SessionNumber != 5 || p.Pending[42].DamageID != 1 || p.consumed[99].sequence != 1 {
		t.Fatalf("interval switch modified protocol session or RPC pairing state: session=%d pending=%+v consumed=%v", p.SessionNumber, p.Pending, p.consumed)
	}
	addStatisticsOutgoingDamage(p.DamageStatistics, 5, 7, time.Unix(30, 0))
	if oldInterval.DamageDealt != 30 || oldInterval.DamageSpanDPS.Value != 3 {
		t.Fatalf("subsequent events mutated the returned interval snapshot: %+v", oldInterval)
	}
}

func TestProcessorStatisticsAccessorsReturnCopiesAndResetSharedState(t *testing.T) {
	p := newStatisticsTestProcessor()
	p.SessionNumber = 7
	p.Pending[42] = PendingDamageRequest{HasDamageID: true, DamageID: 1}
	p.consumed[99] = consumedResponse{sequence: 1}
	p.DamageStatistics.addDamageEvent(CombatEvent{
		SessionNumber: 1, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 321,
	}, PlayerRelationLocal, PlayerRelationUnrelated)

	snapshot := p.DamageStatisticsSnapshot()
	if snapshot.DamageDealt != 321 || len(snapshot.ByPlayerEntity) != 1 {
		t.Fatalf("processor snapshot did not expose the current totals: %+v", snapshot)
	}
	snapshot.ByPlayerEntity[0].DamageDealt = 0
	if got := p.DamageStatisticsSnapshot(); got.ByPlayerEntity[0].DamageDealt != 321 {
		t.Fatalf("mutating a returned snapshot changed internal state: %+v", got)
	}

	p.resetDamageStatistics()
	if got := p.DamageStatisticsSnapshot(); got.DamageDealt != 0 || len(got.ByPlayerEntity) != 0 {
		t.Fatalf("processor reset did not clear the shared statistics: %+v", got)
	}
	if p.SessionNumber != 7 || p.Pending[42].DamageID != 1 || p.consumed[99].sequence != 1 {
		t.Fatalf("statistics reset modified protocol session or RPC pairing state: session=%d pending=%+v consumed=%v", p.SessionNumber, p.Pending, p.consumed)
	}
}

func TestDamageResponseDeduplicatesStatisticsThroughParser(t *testing.T) {
	var diagnostic bytes.Buffer
	p := NewProcessor(nil, &diagnostic)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
	p.Masters = &GameMaster{Damage: map[uint64]DamageMasterRecord{1: {ID: 1, CalculateType: 0}}}
	p.Pending[42] = PendingDamageRequest{
		AttackerEntityID: 100, HasAttackerEntityID: true,
		TargetEntityID: 200, HasTargetEntityID: true,
		DamageID: 1, HasDamageID: true,
	}
	response := appendVarintField(nil, FieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, FieldDamageExecuteResponseImmuneType, 0)
	response = appendVarintField(response, FieldDamageExecuteResponseDamage, 9001)
	p.HandleOneDamageResult(42, response)
	p.HandleOneDamageResult(42, response)

	snapshot := p.DamageStatistics.Snapshot()
	if snapshot.DamageDealt != 9001 || snapshot.OutgoingEvents != 1 || p.Stats.DuplicateResponses != 1 {
		t.Fatalf("duplicate response changed statistics: snapshot=%+v stats=%+v", snapshot, p.Stats)
	}
}

// newStatisticsTestProcessor is a core processor with an empty entity registry.
func newStatisticsTestProcessor() *Processor {
	p := NewProcessor(nil, &bytes.Buffer{})
	p.Entities = NewEntityResolver()
	return p
}

// observeTestLocalRole reports entityID as the current role through the core role decoder, as every build reads it.
func observeTestLocalRole(p *Processor, entityID uint64) {
	p.ObserveClientPushFrame(GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out"}, localRoleReportBody(1409, entityID))
}
