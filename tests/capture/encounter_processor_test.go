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
		notify = append(notify, encounterWireVarint(5, 1)...)
	}
	notify = append(notify, encounterWireVarint(15, playerID)...)
	notifyData := encounterWireBytes(combatNotifyPlayerBattleState, notify)
	receiveData := encounterWireBytes(2, notifyData)
	return encounterWireBytes(5, receiveData)
}

func singlePlayerJoinScene(playerID uint64) []byte {
	playerInfo := encounterWireVarint(1, playerID)
	sceneInfo := encounterWireBytes(4, playerInfo)
	return encounterWireBytes(5, sceneInfo)
}

func newEncounterTestProcessor() *processor {
	return newProcessor(nil, io.Discard, io.Discard, nil)
}

func TestProcessorBattleStateNotifyDrivesEncounter(t *testing.T) {
	p := newEncounterTestProcessor()
	p.sessionNumber = 1
	p.observeSceneJoin(singlePlayerJoinScene(42), encounterAt(1))
	enter := gameFrame{commandID: 25706, direction: "in", timestamp: encounterAt(10)}
	if err := p.handleCombatReceivePackNotify(enter, battleStateReceivePack(true, 42)); err != nil {
		t.Fatal(err)
	}
	if snapshot := p.damageStatisticsSnapshot().Encounters; snapshot.State != EncounterActive || snapshot.BattleState != "in_battle" {
		t.Fatalf("an anchored enter notify must open an encounter: %+v", snapshot)
	}
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 300, 12))

	// The exit arrives embedded in a CombatSendPackResponse, the container seen for most captured exits.
	exit := gameFrame{commandID: 20332, direction: "in", timestamp: encounterAt(13)}
	if err := p.handleEmbeddedCombatReceivePackNotify(exit, encounterWireBytes(14, battleStateReceivePack(false, 42))); err != nil {
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
	frame := gameFrame{commandID: 25706, direction: "in", timestamp: encounterAt(10)}
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
	if err := p.handleCombatReceivePackNotify(gameFrame{commandID: 25706, timestamp: encounterAt(10)}, battleStateReceivePack(true, 42)); err != nil {
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
	for i, source := range []uint64{101, 102, 103, 101} {
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
