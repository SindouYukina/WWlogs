package capture

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Context validity and encounter integrity after transport loss.

func newIntegrityProcessor() *processor {
	return newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
}

func TestInboundLossMakesAttributionSuspectUntilLocalRoleReportAfterLossEnd(t *testing.T) {
	p := newIntegrityProcessor()
	addTestPlayerRoleGroupEntity(p, 1001, 77, 1102)
	p.playerRelevance.observeRoleEntity(1001)
	p.playerRelevance.reconcileLocalPlayers(p.entities)
	local := testEncounterRecord(CombatFlowLocalOutgoing, 100, 5)
	if got := p.gateAttribution(local); got.Flow != CombatFlowLocalOutgoing {
		t.Fatalf("valid context must not downgrade: %+v", got)
	}

	id := p.beginTransportLoss("in", encounterAt(10), encounterAt(12), kcpSkipGapTimeout, 1)
	downgraded := p.gateAttribution(testEncounterRecord(CombatFlowLocalOutgoing, 100, 13))
	if downgraded.Flow != CombatFlowUnattributedOutgoing || downgraded.Confidence != AttributionUnattributed ||
		downgraded.ContextDowngrade != contextDowngradeAttribution {
		t.Fatalf("suspect attribution must keep the event unattributed: %+v", downgraded)
	}
	incoming := p.gateAttribution(testEncounterRecord(CombatFlowLocalIncoming, 50, 13))
	if incoming.Flow != CombatFlowUnattributedIncoming {
		t.Fatalf("incoming local damage must also be downgraded: %+v", incoming)
	}
	excluded := p.gateAttribution(testEncounterRecord(CombatFlowExcluded, 50, 13))
	if excluded.Flow != CombatFlowExcluded || excluded.ContextDowngrade != "" {
		t.Fatalf("non-local flows are unchanged: %+v", excluded)
	}

	// A role report while the inbound loss is still ongoing proves nothing about the lost data.
	p.currentFrameTime = encounterAt(14)
	p.observeLocalRoleEntity(1001)
	if !p.context.attribution.suspect {
		t.Fatal("attribution revalidated while the inbound loss was ongoing")
	}
	p.endTransportLoss(id, "in", encounterAt(15), transportLossDetails{Recovered: true, EstimatedLostFrames: -1})
	// An unknown entity is no evidence either.
	p.currentFrameTime = encounterAt(16)
	p.observeLocalRoleEntity(4242)
	if !p.context.attribution.suspect {
		t.Fatal("attribution revalidated by an entity the registry does not map to the local player")
	}
	p.currentFrameTime = encounterAt(17)
	p.observeLocalRoleEntity(1001)
	snapshot := p.context.snapshot()
	var attribution ContextComponentSnapshot
	for _, component := range snapshot.Components {
		if component.Component == contextAttribution {
			attribution = component
		}
	}
	if attribution.State != ContextValid || attribution.Basis != contextBasisLocalRoleReport || !attribution.RevalidatedAt.Equal(encounterAt(17)) {
		t.Fatalf("attribution after a matching role report = %+v", attribution)
	}
	if got := p.gateAttribution(testEncounterRecord(CombatFlowLocalOutgoing, 100, 18)); got.Flow != CombatFlowLocalOutgoing {
		t.Fatalf("revalidated attribution must confirm again: %+v", got)
	}
	// Entity registry and battle state need their own evidence; a role report does not revalidate them.
	if snapshot.State != ContextSuspect || snapshot.DowngradedEvents != 2 {
		t.Fatalf("context after attribution revalidation = %+v", snapshot)
	}
}

func TestOutboundLossKeepsAttributionValid(t *testing.T) {
	p := newIntegrityProcessor()
	p.beginTransportLoss("out", encounterAt(10), encounterAt(12), kcpSkipGapTimeout, 1)
	if p.context.attribution.suspect || p.context.entities.suspect || p.context.battleState.suspect {
		t.Fatal("outbound loss must not invalidate inbound-derived context")
	}
	if !p.context.effects.suspect {
		t.Fatal("outbound loss can drop client effect reports, so effects are suspect")
	}
	if got := p.gateAttribution(testEncounterRecord(CombatFlowLocalOutgoing, 100, 13)); got.Flow != CombatFlowLocalOutgoing {
		t.Fatalf("outbound loss must not downgrade attribution: %+v", got)
	}
}

func TestSceneJoinRevalidatesContextAfterInboundLossEnded(t *testing.T) {
	p := newIntegrityProcessor()
	id := p.beginTransportLoss("in", encounterAt(10), encounterAt(12), kcpSkipGapTimeout, 1)
	p.endTransportLoss(id, "in", encounterAt(13), transportLossDetails{Recovered: true, EstimatedLostFrames: -1})
	p.observeContextSceneJoin(encounterAt(20))
	if snapshot := p.context.snapshot(); snapshot.State != ContextValid {
		t.Fatalf("a scene join after the loss rebuilds every component: %+v", snapshot)
	}
}

func TestBattleSignalRevalidatesBattleStateOnlyAfterLossEnd(t *testing.T) {
	p := newIntegrityProcessor()
	id := p.beginTransportLoss("in", encounterAt(10), encounterAt(12), kcpSkipGapTimeout, 1)
	p.observeContextBattleSignal(encounterAt(11))
	if !p.context.battleState.suspect {
		t.Fatal("a signal during the loss must not revalidate")
	}
	p.endTransportLoss(id, "in", encounterAt(13), transportLossDetails{Recovered: true, EstimatedLostFrames: -1})
	p.observeContextBattleSignal(encounterAt(13))
	if !p.context.battleState.suspect {
		t.Fatal("a signal at the loss end is not after it")
	}
	p.observeContextBattleSignal(encounterAt(14))
	if p.context.battleState.suspect || p.damageStatistics.encounters.battleStateUnverified {
		t.Fatal("a signal after the loss must revalidate the battle state")
	}
}

// confirmedEncounter runs a battle-state encounter from 10 s to 20 s with 100 damage at 12 s and 16 s.
func confirmedEncounter(statistics *DamageStatistics, during func()) EncounterSnapshot {
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 12)
	if during != nil {
		during()
	}
	outgoing(statistics, 100, 16)
	statistics.observeBattleState(false, encounterAt(20), 1)
	statistics.observeCaptureTime(encounterAt(21.5))
	snapshot := statistics.Snapshot()
	return snapshot.Encounters.Completed[len(snapshot.Encounters.Completed)-1]
}

func TestInboundLossDuringEncounterIsUnreliableAndKeepsRealDuration(t *testing.T) {
	statistics := NewDamageStatistics()
	encounter := confirmedEncounter(statistics, func() {
		statistics.beginTransportLoss(1, "in", encounterAt(13))
		statistics.endTransportLoss(1, encounterAt(15))
	})
	dps := encounter.EncounterDPS
	if encounter.Outcome != EncounterOutcomeConfirmed || encounter.State != EncounterFinished {
		t.Fatalf("the lifecycle outcome must stay separate from integrity: %+v", encounter)
	}
	if encounter.Integrity.Status != IntegrityUnreliable || !containsRisk(encounter.Integrity.Risks, riskBoundarySignalsLost) ||
		!containsRisk(encounter.Integrity.Risks, riskCombatResultsLost) || encounter.Integrity.TransportLosses != 1 {
		t.Fatalf("inbound loss integrity = %+v", encounter.Integrity)
	}
	if dps.Status != DPSStatusUnreliable || dps.Value != 0 || dps.Damage != 200 || dps.Duration != 10*time.Second {
		t.Fatalf("unreliable DPS must keep the real duration and observed damage but no value: %+v", dps)
	}
}

func TestOutboundLossDuringEncounterIsLowerBound(t *testing.T) {
	statistics := NewDamageStatistics()
	encounter := confirmedEncounter(statistics, func() {
		statistics.beginTransportLoss(1, "out", encounterAt(13))
		statistics.endTransportLoss(1, encounterAt(14))
	})
	dps := encounter.EncounterDPS
	if encounter.Integrity.Status != IntegrityDegraded || !containsRisk(encounter.Integrity.Risks, riskCombatRequestsLost) ||
		containsRisk(encounter.Integrity.Risks, riskBoundarySignalsLost) {
		t.Fatalf("outbound loss integrity = %+v", encounter.Integrity)
	}
	if dps.Status != DPSStatusLowerBound || dps.Value != 20 || dps.Duration != 10*time.Second {
		t.Fatalf("outbound loss DPS must be a lower bound over the real duration: %+v", dps)
	}
	if formatDPS(dps) != "lower_bound:20.0" {
		t.Fatalf("console form of a lower bound = %q", formatDPS(dps))
	}
}

func TestLossReportedAfterEncounterFinishedStillMarksIt(t *testing.T) {
	statistics := NewDamageStatistics()
	encounter := confirmedEncounter(statistics, nil)
	if encounter.Integrity.Status != IntegrityIntact || encounter.EncounterDPS.Status != DPSStatusAvailable {
		t.Fatalf("an encounter without loss is intact: %+v", encounter)
	}
	// The outbound stream waited behind a gap that opened at 19 s; the skip is only decided later.
	statistics.beginTransportLoss(1, "out", encounterAt(19))
	completed := statistics.Snapshot().Encounters.Completed[0]
	if completed.Integrity.Status != IntegrityDegraded || completed.EncounterDPS.Status != DPSStatusLowerBound {
		t.Fatalf("a late loss report must still mark the encounter it overlaps: %+v", completed)
	}
}

func TestEncounterStartingAfterLossEndIsReleased(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.beginTransportLoss(1, "out", encounterAt(5))
	encounter := confirmedEncounter(statistics, func() {})
	if encounter.Integrity.Status != IntegrityDegraded {
		t.Fatalf("an encounter opening during an ongoing loss inherits it: %+v", encounter.Integrity)
	}
	statistics.endTransportLoss(1, encounterAt(8))
	released := statistics.Snapshot().Encounters.Completed[0]
	if released.Integrity.Status != IntegrityIntact || released.EncounterDPS.Status != DPSStatusAvailable {
		t.Fatalf("data after the loss end is complete again for an encounter that started later: %+v", released)
	}
}

func TestEncounterOpenedByActivityWhileBattleStateUnverifiedIsUnreliable(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.setBattleStateUnverified(true)
	outgoing(statistics, 100, 12)
	outgoing(statistics, 100, 14)
	statistics.finishCapture()
	encounter := statistics.Snapshot().Encounters.Completed[0]
	if encounter.Integrity.Status != IntegrityUnreliable || !containsRisk(encounter.Integrity.Risks, riskBattleStateUnverified) {
		t.Fatalf("a start without a signal after inbound loss = %+v", encounter.Integrity)
	}

	signalled := NewDamageStatistics()
	signalled.setBattleStateUnverified(true)
	encounter = confirmedEncounter(signalled, nil)
	if encounter.Integrity.Status != IntegrityIntact {
		t.Fatalf("an encounter opened by an observed enter signal re-establishes integrity: %+v", encounter.Integrity)
	}
}

func TestUnrecoveredLossAtFinishIsUnreliable(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 12)
	statistics.beginTransportLoss(1, "out", encounterAt(13))
	statistics.failTransportLoss(1)
	statistics.finishCapture()
	encounter := statistics.Snapshot().Encounters.Completed[0]
	if encounter.Outcome != EncounterOutcomeIncomplete || encounter.Integrity.Status != IntegrityUnreliable ||
		!containsRisk(encounter.Integrity.Risks, riskTransportUnrecovered) || encounter.EncounterDPS.Status != DPSStatusUnreliable {
		t.Fatalf("unrecovered loss at capture end = %+v", encounter)
	}
	if statistics.Snapshot().Integrity.Status != IntegrityUnreliable {
		t.Fatal("an unrecovered loss leaves the interval totals unreliable")
	}
}

func TestNextEncounterAfterRecoveredLossIsIntact(t *testing.T) {
	statistics := NewDamageStatistics()
	confirmedEncounter(statistics, func() {
		statistics.beginTransportLoss(1, "out", encounterAt(13))
		statistics.endTransportLoss(1, encounterAt(14))
	})
	statistics.observeBattleState(true, encounterAt(30), 1)
	outgoing(statistics, 100, 32)
	statistics.observeBattleState(false, encounterAt(40), 1)
	statistics.observeCaptureTime(encounterAt(41.5))
	snapshot := statistics.Snapshot()
	if len(snapshot.Encounters.Completed) != 2 || snapshot.Encounters.Completed[0].Integrity.Status != IntegrityDegraded ||
		snapshot.Encounters.Completed[1].Integrity.Status != IntegrityIntact {
		t.Fatalf("the later encounter must not inherit the earlier loss: %+v", snapshot.Encounters.Completed)
	}
	if snapshot.Integrity.Status != IntegrityDegraded {
		t.Fatalf("the interval keeps the loss: %+v", snapshot.Integrity)
	}
}

func TestContextDowngradedEventsDegradeTheEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	encounter := confirmedEncounter(statistics, func() {
		record := testEncounterRecord(CombatFlowUnattributedOutgoing, 300, 14)
		record.ContextDowngrade = contextDowngradeAttribution
		statistics.addCombatEvent(record)
	})
	if encounter.Totals.DamageDealt != 200 || encounter.Totals.UnattributedOutgoingDamage != 300 {
		t.Fatalf("downgraded damage must stay out of confirmed output: %+v", encounter.Totals)
	}
	if encounter.Integrity.Status != IntegrityDegraded || encounter.Integrity.ContextDowngradedEvents != 1 ||
		encounter.EncounterDPS.Status != DPSStatusLowerBound {
		t.Fatalf("downgraded events make the encounter a lower bound: %+v", encounter)
	}
}

func TestRollingDPSIsLowerBoundWhenLossInWindow(t *testing.T) {
	statistics := NewDamageStatistics()
	for second := 1; second <= 10; second++ {
		outgoing(statistics, 100, float64(second))
	}
	statistics.beginTransportLoss(1, "out", encounterAt(8))
	statistics.endTransportLoss(1, encounterAt(9))
	session := statistics.Snapshot().CaptureSessionID
	snapshot := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(10)})
	if snapshot.Rolling5sDPS.Status != DPSStatusLowerBound || snapshot.Rolling5sDPS.Reason != "transport_loss_in_window" ||
		snapshot.Rolling5sDPS.Value != 100 {
		t.Fatalf("rolling window over a loss = %+v", snapshot.Rolling5sDPS)
	}
	later := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(25)})
	if later.Rolling10sDPS.Status != DPSStatusAvailable {
		t.Fatalf("a window after the loss is complete: %+v", later.Rolling10sDPS)
	}
}

func TestOpenGapMakesLiveValuesLowerBound(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(1), 1)
	outgoing(statistics, 100, 2)
	statistics.setTransportGap("00000009/out", encounterAt(3))
	session := statistics.Snapshot().CaptureSessionID
	snapshot := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(4)})
	if snapshot.Rolling5sDPS.Status != DPSStatusLowerBound || snapshot.Rolling5sDPS.Reason != "transport_gap_open" {
		t.Fatalf("rolling while data waits behind a gap = %+v", snapshot.Rolling5sDPS)
	}
	if snapshot.Encounters.Current == nil || !containsRisk(snapshot.Encounters.Current.Integrity.Risks, riskTransportGapOpen) ||
		snapshot.EncounterDPS.Status != DPSStatusLowerBound {
		t.Fatalf("live encounter while data waits behind a gap = %+v", snapshot.Encounters.Current)
	}
	statistics.setTransportGap("00000009/out", time.Time{})
	cleared := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(5)})
	if cleared.EncounterDPS.Status != DPSStatusAvailable || cleared.Rolling5sDPS.Status != DPSStatusAvailable {
		t.Fatalf("a filled gap lost nothing: %+v %+v", cleared.EncounterDPS, cleared.Rolling5sDPS)
	}
}

func TestDamageSpanDPSWithLossInsideAndAtBoundary(t *testing.T) {
	inside := NewDamageStatistics()
	outgoing(inside, 100, 1)
	outgoing(inside, 100, 11)
	inside.beginTransportLoss(1, "in", encounterAt(4))
	inside.endTransportLoss(1, encounterAt(5))
	if dps := inside.Snapshot().DamageSpanDPS; dps.Status != DPSStatusLowerBound || dps.Value != 20 {
		t.Fatalf("a loss inside the span = %+v", dps)
	}
	boundary := NewDamageStatistics()
	outgoing(boundary, 100, 1)
	outgoing(boundary, 100, 11)
	boundary.beginTransportLoss(1, "in", encounterAt(10))
	boundary.endTransportLoss(1, encounterAt(12))
	if dps := boundary.Snapshot().DamageSpanDPS; dps.Status != DPSStatusUnreliable || dps.Value != 0 {
		t.Fatalf("a loss reaching the span end = %+v", dps)
	}
}

func TestResetCarriesOnlyOngoingLosses(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.beginTransportLoss(1, "out", encounterAt(1))
	statistics.endTransportLoss(1, encounterAt(2))
	statistics.beginTransportLoss(2, "in", encounterAt(3))
	statistics.Reset()
	snapshot := statistics.Snapshot()
	if snapshot.Integrity.TransportLosses != 1 || !containsRisk(snapshot.Integrity.Risks, riskCombatResultsLost) ||
		containsRisk(snapshot.Integrity.Risks, riskCombatRequestsLost) {
		t.Fatalf("the new interval keeps only the ongoing loss: %+v", snapshot.Integrity)
	}
	statistics.endTransportLoss(2, encounterAt(4))
	statistics.Reset()
	if snapshot := statistics.Snapshot(); snapshot.Integrity.Status != IntegrityIntact {
		t.Fatalf("an interval begun after every loss ended is intact: %+v", snapshot.Integrity)
	}
}

func TestEffectSnapshotDegradedAfterTransportLoss(t *testing.T) {
	tracker := NewEffectTracker(1)
	duration := 10.0
	tracker.ObserveApply(EffectApplyEvent{SessionNumber: 1, HolderEntityID: 5, HandleID: 1, EffectID: 9, HasEffectID: true,
		HolderRelation: EffectRelationLocalRole, DurationSeconds: &duration, ObservedAt: encounterAt(1)})
	tracker.ObserveApply(EffectApplyEvent{SessionNumber: 1, HolderEntityID: 5, HandleID: 2, EffectID: 9, HasEffectID: true,
		HolderRelation: EffectRelationLocalRole, DurationSeconds: &duration, ObservedAt: encounterAt(1)})
	tracker.MarkTransportLoss(encounterAt(2))
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 5, HandleID: 2, DurationSeconds: &duration, ObservedAt: encounterAt(3)})
	snapshot := tracker.Snapshot()
	if snapshot.Status != EffectSnapshotDegraded || snapshot.Reason != "transport_loss" || len(snapshot.Effects) != 2 {
		t.Fatalf("effects after a transport loss = %+v", snapshot)
	}
	if !snapshot.Effects[0].StaleSinceTransportLoss || snapshot.Effects[1].StaleSinceTransportLoss {
		t.Fatalf("only instances not updated since the loss are stale: %+v", snapshot.Effects)
	}
	tracker.EndLiveState()
	if tracker.Snapshot().Status != EffectSnapshotComplete {
		t.Fatal("a scene boundary rebuilds the tracked set")
	}
}

func TestStatisticsLineShowsTrustFields(t *testing.T) {
	p := newIntegrityProcessor()
	line := p.currentStatisticsReport().String()
	if !strings.Contains(line, " transport=healthy context=valid encounter_integrity=none data_integrity=intact kernel_drops=unavailable") {
		t.Fatalf("statistics line without loss = %q", line)
	}
	p.damageStatistics.observeBattleState(true, encounterAt(10), 1)
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 100, 12))
	id := p.beginTransportLoss("in", encounterAt(13), encounterAt(15), kcpSkipGapTimeout, 1)
	p.endTransportLoss(id, "in", encounterAt(16), transportLossDetails{Recovered: true, EstimatedLostFrames: -1})
	p.kcpStreams = map[string]*stream{}
	line = p.currentStatisticsReport().String()
	for _, want := range []string{"transport=recovered", "context=suspect", "encounter_integrity=unreliable", "data_integrity=degraded", "encounter_dps=unreliable"} {
		if !strings.Contains(line, want) {
			t.Fatalf("statistics line after inbound loss %q lacks %q", line, want)
		}
	}
}
