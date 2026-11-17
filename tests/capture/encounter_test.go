package capture

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

var encounterEpoch = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func encounterAt(seconds float64) time.Time {
	return encounterEpoch.Add(time.Duration(seconds * float64(time.Second)))
}

var encounterTestRPC uint64

func testEncounterRecord(flow CombatFlow, amount int64, seconds float64) CombatEventRecord {
	encounterTestRPC++
	return CombatEventRecord{
		Kind: CombatEventDamage, Key: CombatEventKey{ProtocolSession: 1, RPCID: encounterTestRPC}, ObservedAt: encounterAt(seconds),
		Evidence: combatEvidenceDamageRPCResponse, Source: CombatEndpoint{EntityID: 1}, Target: CombatEndpoint{EntityID: 2},
		Flow: flow, Confidence: flow.confidence(), Amount: amount,
	}
}

func outgoing(statistics *DamageStatistics, amount int64, seconds float64) {
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, amount, seconds))
}

func requireOneCompleted(t *testing.T, snapshot DamageStatisticsSnapshot) EncounterSnapshot {
	t.Helper()
	if len(snapshot.Encounters.Completed) != 1 || snapshot.Encounters.Current != nil {
		t.Fatalf("expected exactly one finished encounter and none open, got %+v", snapshot.Encounters)
	}
	return snapshot.Encounters.Completed[0]
}

func TestEncounterConfirmedByBattleStateSignals(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 12)
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalIncoming, 50, 13))
	outgoing(statistics, 100, 14)
	statistics.ObserveBattleState(false, encounterAt(20), 1)

	candidate := statistics.Snapshot().Encounters
	if candidate.State != EncounterEndingCandidate || candidate.Current == nil || candidate.Current.EncounterDPS.Confidence != "ending_candidate" ||
		candidate.Current.EncounterDPS.Value != 20 {
		t.Fatalf("an exit signal must first make the encounter an ending candidate valued at the exit time: %+v", candidate)
	}
	statistics.observeCaptureTime(encounterAt(21.5))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	dps := encounter.EncounterDPS
	if encounter.Outcome != EncounterOutcomeConfirmed || !encounter.StartConfirmed || encounter.StartBasis != encounterStartBattleState ||
		encounter.EndBasis != EncounterEndBattleState || !encounter.StartedAt.Equal(encounterAt(10)) || !encounter.EndedAt.Equal(encounterAt(20)) ||
		!encounter.EndDecidedAt.Equal(encounterAt(21.5)) || encounter.Totals.DamageDealt != 200 || encounter.Totals.DamageTaken != 50 ||
		dps.Status != DPSStatusAvailable || dps.Value != 20 || dps.Duration != 10*time.Second || dps.Confidence != "confirmed" ||
		encounter.EncounterID != encounterIDFor(statistics, 1) {
		t.Fatalf("unexpected confirmed encounter: %+v", encounter)
	}
	if statistics.Snapshot().EncounterDPS != dps {
		t.Fatal("the headline Encounter DPS must be the last finished encounter when none is open")
	}
}

func encounterIDFor(statistics *DamageStatistics, sequence uint64) string {
	snapshot := statistics.Snapshot()
	return fmt.Sprintf("%d-%d-%d", snapshot.CaptureSessionID, snapshot.StatisticsIntervalID, sequence)
}

func TestEncounterReentryWithinGraceKeepsOneEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.ObserveBattleState(false, encounterAt(15), 1)
	statistics.ObserveBattleState(true, encounterAt(15.3), 1)
	outgoing(statistics, 300, 16)
	statistics.ObserveBattleState(false, encounterAt(20), 1)
	statistics.observeCaptureTime(encounterAt(30))

	encounter := requireOneCompleted(t, statistics.Snapshot())
	if encounter.Reentries != 1 || encounter.Totals.DamageDealt != 400 || !encounter.StartedAt.Equal(encounterAt(10)) || !encounter.EndedAt.Equal(encounterAt(20)) ||
		encounter.EncounterDPS.Value != 40 || encounter.Outcome != EncounterOutcomeConfirmed {
		t.Fatalf("a wave transition inside the grace window must not split the encounter: %+v", encounter)
	}
}

func TestEncounterReentryAfterGraceStartsNewEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.ObserveBattleState(false, encounterAt(15), 1)
	statistics.observeCaptureTime(encounterAt(16.5))
	statistics.ObserveBattleState(true, encounterAt(17), 1)
	outgoing(statistics, 50, 18)
	snapshot := statistics.Snapshot()
	if len(snapshot.Encounters.Completed) != 1 || snapshot.Encounters.Current == nil || snapshot.Encounters.Current.Sequence != 2 ||
		snapshot.Encounters.Completed[0].Totals.DamageDealt != 100 || snapshot.Encounters.Current.Totals.DamageDealt != 50 {
		t.Fatalf("a re-entry after the grace window must open a separate encounter: %+v", snapshot.Encounters)
	}
}

func TestEncounterPreRollJoinsHitsBeforeEnterSignal(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(false, encounterAt(1), 1)
	outgoing(statistics, 70, 30)
	if snapshot := statistics.Snapshot(); snapshot.Encounters.Current != nil || snapshot.Encounters.OutsideEncounters.DamageDealt != 70 {
		t.Fatalf("a hit out of battle must not open an encounter while the battle signal governs: %+v", snapshot.Encounters)
	}
	statistics.ObserveBattleState(true, encounterAt(30.2), 1)
	outgoing(statistics, 30, 31)
	current := statistics.Snapshot().Encounters.Current
	if current == nil || !current.StartedAt.Equal(encounterAt(30)) || current.StartBasis != encounterStartFirstActivity || !current.StartConfirmed ||
		!current.BattleEntered.Equal(encounterAt(30.2)) || current.Totals.DamageDealt != 100 {
		t.Fatalf("the hit that started the battle must join it and set the start: %+v", current)
	}
	if statistics.Snapshot().Encounters.OutsideEncounters.DamageDealt != 0 {
		t.Fatal("an absorbed prelude hit must leave the outside totals")
	}
}

func TestEncounterPreludeExpiresOutsideEncounters(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(false, encounterAt(1), 1)
	outgoing(statistics, 70, 50)
	statistics.observeCaptureTime(encounterAt(53))
	statistics.ObserveBattleState(true, encounterAt(53.1), 1)
	outgoing(statistics, 10, 54)
	snapshot := statistics.Snapshot().Encounters
	if snapshot.OutsideEncounters.DamageDealt != 70 || snapshot.Current == nil || snapshot.Current.Totals.DamageDealt != 10 ||
		!snapshot.Current.StartedAt.Equal(encounterAt(53.1)) {
		t.Fatalf("a hit older than the pre-roll window must stay outside the next encounter: %+v", snapshot)
	}
}

func TestEncounterFallbackEndsAtLastActivityAfterInactivity(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 100, 10)
	outgoing(statistics, 100, 12)
	statistics.observeCaptureTime(encounterAt(41))
	if statistics.Snapshot().Encounters.Current == nil {
		t.Fatal("the inactivity timeout must not fire before it has elapsed")
	}
	statistics.observeCaptureTime(encounterAt(42.5))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	if encounter.Outcome != EncounterOutcomeInferred || encounter.StartConfirmed || encounter.StartBasis != encounterStartFirstActivity ||
		encounter.EndBasis != encounterEndInactivity || !encounter.EndedAt.Equal(encounterAt(12)) || !encounter.EndDecidedAt.Equal(encounterAt(42.5)) ||
		encounter.EncounterDPS.Value != 100 || encounter.EncounterDPS.Confidence != "inferred" {
		t.Fatalf("the inferred end must be the last confirmed activity, not the timeout time: %+v", encounter)
	}
}

func TestEncounterFallbackConfirmedByFirstSignal(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 100, 10)
	statistics.ObserveBattleState(true, encounterAt(10.3), 1)
	outgoing(statistics, 100, 12)
	statistics.ObserveBattleState(false, encounterAt(14), 1)
	statistics.finishCapture()
	encounter := requireOneCompleted(t, statistics.Snapshot())
	if encounter.Outcome != EncounterOutcomeConfirmed || !encounter.StartedAt.Equal(encounterAt(10)) || !encounter.BattleEntered.Equal(encounterAt(10.3)) ||
		encounter.Totals.DamageDealt != 200 || !encounter.EndedAt.Equal(encounterAt(14)) {
		t.Fatalf("a first hit just before the first signal must be confirmed by it: %+v", encounter)
	}
}

func TestEncounterCaptureStartedMidBattleIsInferred(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 100, 10)
	statistics.ObserveBattleState(false, encounterAt(12), 1)
	statistics.observeCaptureTime(encounterAt(14))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	if encounter.Outcome != EncounterOutcomeInferred || encounter.EndBasis != EncounterEndBattleState || encounter.StartConfirmed {
		t.Fatalf("an encounter whose start was not observed cannot be confirmed: %+v", encounter)
	}
}

func TestEncounterSceneTransitionInterrupts(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.observeSceneTransition(encounterAt(15))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	if encounter.Outcome != EncounterOutcomeInterrupted || encounter.EndBasis != encounterEndSceneTransition || !encounter.EndedAt.Equal(encounterAt(15)) ||
		encounter.EncounterDPS.Confidence != "interrupted" {
		t.Fatalf("a scene change during battle must interrupt the encounter: %+v", encounter)
	}
	if snapshot := statistics.Snapshot().Encounters; snapshot.BattleState != "out_of_battle" {
		t.Fatalf("a scene change must clear the battle state: %+v", snapshot)
	}

	statistics = NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.ObserveBattleState(false, encounterAt(12), 1)
	statistics.observeSceneTransition(encounterAt(12.5))
	if encounter := requireOneCompleted(t, statistics.Snapshot()); encounter.Outcome != EncounterOutcomeConfirmed || !encounter.EndedAt.Equal(encounterAt(12)) {
		t.Fatalf("a scene change after the exit signal must keep the confirmed end: %+v", encounter)
	}
}

func TestEncounterProtocolSessionChangeInterrupts(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 100, 10)
	next := testEncounterRecord(CombatFlowLocalOutgoing, 40, 11)
	next.Key.ProtocolSession = 2
	statistics.addCombatEvent(next)
	snapshot := statistics.Snapshot().Encounters
	if len(snapshot.Completed) != 1 || snapshot.Completed[0].Outcome != EncounterOutcomeInterrupted ||
		snapshot.Completed[0].EndBasis != encounterEndSessionChange || snapshot.Current == nil || snapshot.Current.ProtocolSession != 2 ||
		snapshot.Current.Totals.DamageDealt != 40 {
		t.Fatalf("events of a new protocol session must not join the previous session's encounter: %+v", snapshot)
	}
}

func TestEncounterCaptureEndIsIncomplete(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.observeCaptureTime(encounterAt(20))
	statistics.finishCapture()
	encounter := requireOneCompleted(t, statistics.Snapshot())
	if encounter.Outcome != EncounterOutcomeIncomplete || encounter.EndBasis != encounterEndCaptureEnd || !encounter.EndedAt.Equal(encounterAt(20)) ||
		encounter.EncounterDPS.Confidence != "incomplete" || encounter.EncounterDPS.Value != 10 {
		t.Fatalf("a capture that stops mid-battle must report an incomplete encounter: %+v", encounter)
	}
}

func TestEncounterWithoutLocalActivityIsDiscarded(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedOutgoing, 9, 11))
	statistics.ObserveBattleState(false, encounterAt(12), 1)
	statistics.observeCaptureTime(encounterAt(14))
	snapshot := statistics.Snapshot().Encounters
	if len(snapshot.Completed) != 0 || snapshot.DiscardedEmpty != 1 || snapshot.OutsideEncounters.UnattributedOutgoingDamage != 9 {
		t.Fatalf("a battle interval without local damage must be discarded and keep its unattributed totals: %+v", snapshot)
	}
	if statistics.Snapshot().EncounterDPS.Status != DPSStatusUnavailable {
		t.Fatal("no Encounter DPS may be reported without an encounter")
	}
}

func TestEncounterLiveDPSAdvancesWithCaptureTime(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	session := statistics.Snapshot().CaptureSessionID
	first := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(15)}).EncounterDPS
	second := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(20)}).EncounterDPS
	if first.Value != 20 || second.Value != 10 || first.Confidence != "live" || second.Duration != 10*time.Second {
		t.Fatalf("live Encounter DPS must use the elapsed capture time even without new damage: first=%+v second=%+v", first, second)
	}
	statistics.observeCaptureTime(encounterAt(25))
	if dps := statistics.Snapshot().EncounterDPS; dps.Value != 100.0/15 {
		t.Fatalf("a snapshot without query time must use the newest observed capture time: %+v", dps)
	}
}

func TestEncounterZeroDurationIsInvalid(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 10)
	if dps := statistics.Snapshot().EncounterDPS; dps.Status != DPSStatusInvalidDuration || dps.Value != 0 {
		t.Fatalf("a zero-length encounter must not produce a value: %+v", dps)
	}
	session := statistics.Snapshot().CaptureSessionID
	if dps := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(9)}).EncounterDPS; dps.Status != DPSStatusInvalidDuration {
		t.Fatalf("a query before the start must not produce a value: %+v", dps)
	}
}

func TestEncounterIsolatesUnattributedAndUntimedEvents(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedOutgoing, 500, 12))
	statistics.addCombatEvent(testEncounterRecord(CombatFlowExcluded, 900, 12))
	untimed := testEncounterRecord(CombatFlowLocalOutgoing, 7, 0)
	untimed.ObservedAt = time.Time{}
	statistics.addCombatEvent(untimed)
	statistics.ObserveBattleState(false, encounterAt(20), 1)
	statistics.finishCapture()
	snapshot := statistics.Snapshot()
	encounter := requireOneCompleted(t, snapshot)
	if encounter.Totals.DamageDealt != 100 || encounter.Totals.UnattributedOutgoingDamage != 500 || encounter.EncounterDPS.Damage != 100 ||
		snapshot.Encounters.EventsWithoutTime != 1 || snapshot.Encounters.OutsideEncounters.DamageDealt != 7 {
		t.Fatalf("unattributed, excluded, and untimed events must stay out of the Encounter DPS numerator: %+v", snapshot.Encounters)
	}
}

func TestEncounterHealingDoesNotEnterTotals(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	healing := testEncounterRecord(CombatFlowLocalInternal, 1000, 11)
	healing.Kind = CombatEventHealing
	statistics.addCombatEvent(healing)
	outgoing(statistics, 100, 12)
	if current := statistics.Snapshot().Encounters.Current; current == nil || current.Totals.DamageDealt != 100 || current.Totals.DamageTaken != 0 {
		t.Fatalf("healing has no verified statistics semantics and must not count: %+v", current)
	}
}

func TestEncounterResetStartsNewIntervalWithoutCarryover(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.Reset()
	empty := statistics.Snapshot()
	if empty.Encounters.Current != nil || len(empty.Encounters.Completed) != 0 || empty.Encounters.BattleState != "in_battle" ||
		empty.EncounterDPS.Status != DPSStatusUnavailable {
		t.Fatalf("a reset must not carry encounters into the next interval: %+v", empty.Encounters)
	}
	outgoing(statistics, 40, 12)
	statistics.ObserveBattleState(false, encounterAt(14), 1)
	statistics.finishCapture()
	encounter := requireOneCompleted(t, statistics.Snapshot())
	if encounter.StatisticsIntervalID != 2 || encounter.Sequence != 1 || encounter.Totals.DamageDealt != 40 || encounter.StartConfirmed ||
		encounter.Outcome != EncounterOutcomeInferred || !encounter.StartedAt.Equal(encounterAt(12)) {
		t.Fatalf("an encounter opened after a reset must be scoped to the new interval and not claim a confirmed start: %+v", encounter)
	}
}

func TestEncounterOutOfOrderEventsRespectPreRoll(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	outgoing(statistics, 20, 9.5)
	outgoing(statistics, 30, 5)
	current := statistics.Snapshot().Encounters.Current
	if current == nil || !current.StartedAt.Equal(encounterAt(9.5)) || current.Totals.DamageDealt != 120 ||
		statistics.Snapshot().Encounters.OutsideEncounters.DamageDealt != 30 {
		t.Fatalf("a late event inside the pre-roll window must move the start; an older one must stay outside: %+v", current)
	}
}

func TestEncounterCompletedListIsBoundedAndTotalsConserved(t *testing.T) {
	statistics := NewDamageStatistics()
	for i := 0; i < maxCompletedEncounters+6; i++ {
		start := float64(i * 10)
		statistics.ObserveBattleState(true, encounterAt(start+1), 1)
		outgoing(statistics, 10, start+2)
		statistics.ObserveBattleState(false, encounterAt(start+3), 1)
		statistics.observeCaptureTime(encounterAt(start + 5))
	}
	snapshot := statistics.Snapshot()
	if len(snapshot.Encounters.Completed) != maxCompletedEncounters || snapshot.Encounters.CompletedDropped != 6 ||
		snapshot.Encounters.CompletedDroppedTotals.DamageDealt != 60 || snapshot.Encounters.Completed[0].Sequence != 7 {
		t.Fatalf("the completed list must stay bounded and keep evicted totals: %+v", snapshot.Encounters)
	}
	assertEncounterTotalsConserved(t, snapshot)
}

func assertEncounterTotalsConserved(t *testing.T, snapshot DamageStatisticsSnapshot) {
	t.Helper()
	var sum EncounterTotals
	for _, encounter := range snapshot.Encounters.Completed {
		sum.Merge(encounter.Totals)
	}
	if snapshot.Encounters.Current != nil {
		sum.Merge(snapshot.Encounters.Current.Totals)
	}
	sum.Merge(snapshot.Encounters.OutsideEncounters)
	sum.Merge(snapshot.Encounters.CompletedDroppedTotals)
	if sum.DamageDealt != snapshot.DamageDealt || sum.DamageTaken != snapshot.DamageTaken ||
		sum.OutgoingEvents != snapshot.OutgoingEvents || sum.IncomingEvents != snapshot.IncomingEvents ||
		sum.UnattributedOutgoingDamage != snapshot.UnattributedOutgoingDamage || sum.UnattributedIncomingDamage != snapshot.UnattributedIncomingDamage {
		t.Fatalf("encounter partition does not add up to the interval totals: partition=%+v interval=%+v", sum, snapshot)
	}
}

func TestEncounterTotalsMatchIntervalForMixedFlows(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 5, 1)
	statistics.ObserveBattleState(true, encounterAt(3), 1)
	outgoing(statistics, 100, 4)
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalIncoming, 40, 5))
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedIncoming, 8, 6))
	statistics.ObserveBattleState(false, encounterAt(7), 1)
	outgoing(statistics, 9, 7.5)
	statistics.observeCaptureTime(encounterAt(12))
	outgoing(statistics, 11, 13)
	assertEncounterTotalsConserved(t, statistics.Snapshot())
	statistics.finishCapture()
	assertEncounterTotalsConserved(t, statistics.Snapshot())
}

func TestEncounterReplayIsDeterministic(t *testing.T) {
	run := func() EncounterTrackerSnapshot {
		statistics := NewDamageStatistics()
		statistics.ObserveBattleState(false, encounterAt(0), 1)
		for i := 0; i < 5; i++ {
			base := float64(i * 7)
			outgoing(statistics, 10, base+0.9)
			statistics.ObserveBattleState(true, encounterAt(base+1), 1)
			statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalIncoming, 3, base+2))
			outgoing(statistics, int64(10*i), base+3)
			statistics.ObserveBattleState(false, encounterAt(base+4), 1)
			statistics.observeCaptureTime(encounterAt(base + 6))
		}
		statistics.finishCapture()
		snapshot := statistics.Snapshot().Encounters
		for i := range snapshot.Completed {
			snapshot.Completed[i].CaptureSessionID = 0
			snapshot.Completed[i].EncounterID = ""
		}
		return snapshot
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) || len(first.Completed) != 5 {
		t.Fatalf("replaying the same observations must give the same segmentation:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestDecodePlayerBattleStateChange(t *testing.T) {
	enter := appendVarintField(appendVarintField(nil, fieldPlayerBattleStateChangeNotifyInBattle, 1), fieldPlayerBattleStateChangeNotifyPlayerID, 42)
	signal, err := decodePlayerBattleStateChange(enter)
	if err != nil || !signal.InBattle || signal.playerID != 42 || !signal.hasPlayerID {
		t.Fatalf("unexpected enter decode: %+v err=%v", signal, err)
	}
	exit := appendVarintField(nil, fieldPlayerBattleStateChangeNotifyPlayerID, 42)
	if signal, err := decodePlayerBattleStateChange(exit); err != nil || signal.InBattle || signal.playerID != 42 {
		t.Fatalf("an exit carries only the PlayerId: %+v err=%v", signal, err)
	}
	if _, err := decodePlayerBattleStateChange(appendTestVarint(nil, fieldPlayerBattleStateChangeNotifyPlayerID<<3)); err == nil {
		t.Fatal("a truncated message must be reported as a decode failure")
	}
}

func TestBattleStateAnchorRequiresLocalPlayer(t *testing.T) {
	p := NewProcessor(nil, nil, nil, nil)
	signal := PlayerBattleStateSignal{InBattle: true, playerID: 42, hasPlayerID: true}
	if anchor := p.battleStateAnchor(signal); anchor != "" {
		t.Fatalf("a signal with no scene or anchor must be rejected, got %q", anchor)
	}
	p.ScenePlayerIDs = []uint64{42}
	if anchor := p.battleStateAnchor(signal); anchor != battleStateAnchorSinglePlayer {
		t.Fatalf("the only scene player must be accepted before the role anchor exists, got %q", anchor)
	}
	p.ScenePlayerIDs = []uint64{42, 43}
	if anchor := p.battleStateAnchor(signal); anchor != "" {
		t.Fatalf("a multi-player scene without the role anchor must be rejected, got %q", anchor)
	}
	p.PlayerRelevance.LocalPlayerIDs[43] = struct{}{}
	if anchor := p.battleStateAnchor(signal); anchor != "" {
		t.Fatalf("another player's signal must be rejected once the local anchor is known, got %q", anchor)
	}
	p.PlayerRelevance.LocalPlayerIDs[42] = struct{}{}
	if anchor := p.battleStateAnchor(signal); anchor != battleStateAnchorLocalPlayer {
		t.Fatalf("the anchored local player must be accepted, got %q", anchor)
	}
	if anchor := p.battleStateAnchor(PlayerBattleStateSignal{InBattle: true}); anchor != "" {
		t.Fatalf("a signal without a PlayerId must be rejected, got %q", anchor)
	}
}
