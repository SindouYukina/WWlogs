package capture

import (
	"encoding/binary"
	"io"
	"testing"
	"time"
)

func encounterWireVarint(field, value uint64) []byte {
	out := binary.AppendUvarint(nil, field<<3)
	return binary.AppendUvarint(out, value)
}

func encounterWireBytes(field uint64, data []byte) []byte {
	out := binary.AppendUvarint(nil, field<<3|2)
	out = binary.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

// battleStateReceivePack builds CombatReceivePackNotify{Data[]{CombatNotifyData{PlayerBattleStateChangeNotify}}}.
func battleStateReceivePack(inBattle bool, playerID uint64) []byte {
	var notify []byte
	if inBattle {
		notify = append(notify, encounterWireVarint(fieldPlayerBattleStateChangeNotifyInBattle, 1)...)
	}
	notify = append(notify, encounterWireVarint(fieldPlayerBattleStateChangeNotifyPlayerID, playerID)...)
	notifyData := encounterWireBytes(fieldCombatNotifyDataPlayerBattleStateChangeNotify, notify)
	receiveData := encounterWireBytes(fieldCombatReceiveDataCombatNotifyData, notifyData)
	return encounterWireBytes(fieldCombatReceivePackNotifyData, receiveData)
}

func singlePlayerJoinScene(playerID uint64) []byte {
	playerInfo := encounterWireVarint(fieldScenePlayerInformationPlayerID, playerID)
	sceneInfo := encounterWireBytes(fieldSceneInformationPlayerInfos, playerInfo)
	return encounterWireBytes(fieldJoinSceneNotifySceneInfo, sceneInfo)
}

func newEncounterTestProcessor() *processor {
	return newProcessor(nil, io.Discard, io.Discard, nil)
}

func TestProcessorBattleStateNotifyDrivesEncounter(t *testing.T) {
	p := newEncounterTestProcessor()
	p.sessionNumber = 1
	p.observeSceneJoin(singlePlayerJoinScene(42), encounterAt(1))
	enter := gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in", timestamp: encounterAt(10)}
	if err := p.handleCombatReceivePackNotify(enter, battleStateReceivePack(true, 42)); err != nil {
		t.Fatal(err)
	}
	if snapshot := p.damageStatisticsSnapshot().Encounters; snapshot.State != EncounterActive || snapshot.BattleState != "in_battle" {
		t.Fatalf("an anchored enter notify must open an encounter: %+v", snapshot)
	}
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 300, 12))

	// The exit arrives embedded in a CombatSendPackResponse, the container seen for most captured exits.
	exit := gameFrame{commandID: cmdCombatSendPackResponse, direction: "in", timestamp: encounterAt(13)}
	if err := p.handleEmbeddedCombatReceivePackNotify(exit, encounterWireBytes(fieldCombatSendPackResponseReceivePackNotify, battleStateReceivePack(false, 42))); err != nil {
		t.Fatal(err)
	}
	p.observeEncounterClock(encounterAt(15))
	encounter := requireOneCompleted(t, p.damageStatisticsSnapshot())
	if encounter.Outcome != EncounterOutcomeConfirmed || !encounter.EndedAt.Equal(encounterAt(13)) || encounter.EncounterDPS.Value != 100 {
		t.Fatalf("enter and exit notifies must bound a confirmed encounter: %+v", encounter)
	}
}

func TestProcessorRejectsOtherPlayersBattleState(t *testing.T) {
	p := newEncounterTestProcessor()
	p.sessionNumber = 1
	p.observeSceneJoin(singlePlayerJoinScene(42), encounterAt(1))
	frame := gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in", timestamp: encounterAt(10)}
	if err := p.handleCombatReceivePackNotify(frame, battleStateReceivePack(true, 77)); err != nil {
		t.Fatal(err)
	}
	snapshot := p.damageStatisticsSnapshot().Encounters
	if snapshot.State != EncounterIdle || snapshot.BattleState != "unknown" || snapshot.UnanchoredBattleStateSignals != 1 || snapshot.BattleStateSignals != 0 {
		t.Fatalf("a battle state for a player not proven local must not drive encounters: %+v", snapshot)
	}
}

func TestProcessorSceneJoinInterruptsEncounter(t *testing.T) {
	p := newEncounterTestProcessor()
	p.sessionNumber = 1
	p.observeSceneJoin(singlePlayerJoinScene(42), encounterAt(1))
	if err := p.handleCombatReceivePackNotify(gameFrame{commandID: cmdCombatReceivePackNotify, timestamp: encounterAt(10)}, battleStateReceivePack(true, 42)); err != nil {
		t.Fatal(err)
	}
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 100, 11))
	p.observeSceneJoin(singlePlayerJoinScene(42), encounterAt(20))
	encounter := requireOneCompleted(t, p.damageStatisticsSnapshot())
	if encounter.Outcome != EncounterOutcomeInterrupted || encounter.EndBasis != encounterEndSceneTransition {
		t.Fatalf("a scene join during battle must interrupt the encounter: %+v", encounter)
	}
}

func TestProcessorFinishCaptureClosesOpenEncounter(t *testing.T) {
	p := newEncounterTestProcessor()
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 100, 11))
	p.observeEncounterClock(encounterAt(19))
	p.finishCapture()
	encounter := requireOneCompleted(t, p.damageStatisticsSnapshot())
	if encounter.Outcome != EncounterOutcomeIncomplete || !encounter.EndedAt.Equal(encounterAt(19)) {
		t.Fatalf("finishing the capture must close the open encounter as incomplete: %+v", encounter)
	}
}

func TestEncounterRoleSwitchKeepsOneEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(10), 1)
	for i, source := range []uint64{101, 102, cmdLoginRequest, 101} {
		record := testEncounterRecord(CombatFlowLocalOutgoing, 10, 11+float64(i))
		record.Source.EntityID = source
		statistics.addCombatEvent(record)
	}
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current == nil || snapshot.Encounters.Current.Sequence != 1 || snapshot.Encounters.Current.Totals.OutgoingEvents != 4 ||
		len(snapshot.ByPlayerEntity) != 3 {
		t.Fatalf("switching the active team member must not split the encounter: %+v", snapshot.Encounters)
	}
}

func TestEncounterLongQuietBattleStaysOpen(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.observeCaptureTime(encounterAt(200))
	outgoing(statistics, 100, 210)
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current == nil || len(snapshot.Encounters.Completed) != 0 || snapshot.Encounters.Current.Totals.DamageDealt != 200 ||
		snapshot.EncounterDPS.Duration != 200*time.Second {
		t.Fatalf("a quiet period inside a server-declared battle must not end the encounter: %+v", snapshot.Encounters)
	}
}

// Gameplay effects are game state, not statistics: they neither open nor close encounters, and an effect that
// outlives an encounter stays tracked across the boundary.
func TestEffectMessagesDoNotChangeEncounters(t *testing.T) {
	p := newEncounterTestProcessor()
	p.sessionNumber = 1
	p.observeSceneJoin(singlePlayerJoinScene(42), encounterAt(1))
	deliver := func(seconds float64, body []byte) {
		t.Helper()
		if err := p.handleCombatReceivePackNotify(gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in", timestamp: encounterAt(seconds)}, body); err != nil {
			t.Fatal(err)
		}
	}
	deliver(5, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 60, 60)))
	if snapshot := p.damageStatisticsSnapshot().Encounters; snapshot.State != EncounterIdle {
		t.Fatalf("an effect application must not open an encounter: %+v", snapshot)
	}
	deliver(10, battleStateReceivePack(true, 42))
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 100, 11))
	deliver(12, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(2, 43, 200, 100, 1, 3, 3)))
	deliver(14, battleStateReceivePack(false, 42))
	p.observeEncounterClock(encounterAt(20))
	encounter := requireOneCompleted(t, p.damageStatisticsSnapshot())
	if encounter.Outcome != EncounterOutcomeConfirmed || !encounter.StartedAt.Equal(encounterAt(10)) || !encounter.EndedAt.Equal(encounterAt(14)) {
		t.Fatalf("effects inside the battle must not move its boundaries: %+v", encounter)
	}
	effects := p.effects.SnapshotAt(CaptureQueryTime{CaptureSessionID: p.effects.captureSessionID, Timestamp: encounterAt(20)})
	if len(effects.Effects) == 0 || effects.Counters.Applies != 2 {
		t.Fatalf("effect tracking must continue across the encounter boundary: %+v", effects)
	}
	deliver(25, effectPackBody(200, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(1, 200, 100)))
	if snapshot := p.damageStatisticsSnapshot().Encounters; snapshot.State != EncounterIdle || len(snapshot.Completed) != 1 {
		t.Fatalf("an effect removal must not open or reopen an encounter: %+v", snapshot)
	}
	if removed := p.effects.Snapshot().Counters.Removes; removed != 1 {
		t.Fatalf("the removal must reach the effect tracker, got %d removes", removed)
	}
}

// The kill shot and the exit signal share a frame in captures, so an event at exactly the exit time belongs to
// the ending encounter; an event after it waits in the prelude.
func TestEncounterEventAtExitTimeStaysInEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 12)
	statistics.observeBattleState(false, encounterAt(15), 1)
	if assignment := statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 50, 15)); assignment != encounterIDFor(statistics, 1) {
		t.Fatalf("a hit at the exit time must join the ending encounter, got %q", assignment)
	}
	if assignment := statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 7, 15.5)); assignment != encounterAssignmentPrelude {
		t.Fatalf("a hit after the exit must wait outside the ending encounter, got %q", assignment)
	}
	statistics.observeCaptureTime(encounterAt(19))
	snapshot := statistics.Snapshot()
	encounter := requireOneCompleted(t, snapshot)
	if encounter.Totals.DamageDealt != 150 || snapshot.Encounters.OutsideEncounters.DamageDealt != 7 {
		t.Fatalf("unexpected split at the exit frame: encounter=%+v outside=%+v", encounter.Totals, snapshot.Encounters.OutsideEncounters)
	}
	assertEncounterTotalsConserved(t, snapshot)
}
