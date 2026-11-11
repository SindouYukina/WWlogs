package capture

import (
	"reflect"
	"testing"
	"time"
)

// takeSpan drains the journal and checks that it opens with the span marker of a capture that records from its start.
func takeSpan(t *testing.T, statistics *DamageStatistics) ([]HistoryEntry, uint64) {
	t.Helper()
	entries, dropped := statistics.TakeHistory()
	if len(entries) == 0 || entries[0].Kind != HistoryRecordingStarted || entries[0].Recording == nil || entries[0].Recording.Reason != HistoryStartCapture {
		t.Fatalf("the journal does not open with the capture's span: %v", journalKinds(entries))
	}
	return entries[1:], dropped
}

// journalKinds lists the kinds of the entries in order.
func journalKinds(entries []HistoryEntry) []HistoryEntryKind {
	kinds := make([]HistoryEntryKind, 0, len(entries))
	for _, entry := range entries {
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

func enemyRecord(flow CombatFlow, amount int64, seconds float64, enemy uint64, monster uint64) CombatEventRecord {
	record := testEncounterRecord(flow, amount, seconds)
	actor := &CombatActorType{MonsterID: monster, MonsterStatus: string(resolutionCorrelated), TemplateStatus: string(resolutionUnknown)}
	if monster == 0 {
		actor.MonsterStatus = string(resolutionUnknown)
	}
	switch flow {
	case CombatFlowLocalOutgoing:
		record.Target = CombatEndpoint{EntityID: enemy, Relation: "UNRELATED"}
		record.TargetActor = actor
	case CombatFlowLocalIncoming:
		record.Source = CombatEndpoint{EntityID: enemy, Relation: "UNRELATED"}
		record.SourceActor = actor
	}
	return record
}

func TestHistoryJournalIsOffByDefault(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(1), 1)
	outgoing(statistics, 100, 2)
	statistics.finishCapture()
	if entries, dropped := statistics.TakeHistory(); len(entries) != 0 || dropped != 0 {
		t.Fatalf("a journal nobody enabled recorded %d entries", len(entries))
	}
}

func TestHistoryJournalRepeatsTheEncounterLifecycle(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEvents)
	statistics.observeScene(2117, true)
	statistics.observeBattleState(true, encounterAt(10), 1)
	// An unattributed hit joins the open encounter but is no confirmed activity.
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedOutgoing, 5, 10.5))
	statistics.addCombatEvent(enemyRecord(CombatFlowLocalOutgoing, 100, 11, 501, 340000040))
	statistics.addCombatEvent(enemyRecord(CombatFlowLocalOutgoing, 40, 12, 502, 340000040))
	statistics.addCombatEvent(enemyRecord(CombatFlowLocalIncoming, 30, 13, 501, 340000040))
	statistics.observeBattleState(false, encounterAt(20), 1)
	statistics.observeCaptureTime(encounterAt(22))
	entries, dropped := takeSpan(t, statistics)
	if dropped != 0 {
		t.Fatalf("dropped %d", dropped)
	}
	want := []HistoryEntryKind{
		HistoryEncounterOpened,
		HistoryEventAccepted, HistoryEventAssigned,
		HistoryEventAccepted, HistoryEventAssigned, HistoryEncounterActive,
		HistoryEventAccepted, HistoryEventAssigned,
		HistoryEventAccepted, HistoryEventAssigned,
		HistoryEncounterFinished,
	}
	if got := journalKinds(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("journal = %v\nwant %v", got, want)
	}
	accepted := uint64(0)
	for i, entry := range entries {
		if entry.Kind == HistoryEventAccepted {
			accepted++
			if entry.Sequence != accepted || entries[i+1].Sequence != accepted {
				t.Fatalf("entry %d has sequence %d and its assignment %d, want %d", i, entry.Sequence, entries[i+1].Sequence, accepted)
			}
			continue
		}
		if entry.Kind != HistoryEventAccepted && entry.Encounter != "1-1" {
			t.Fatalf("entry %d (%s) names encounter %q", i, entry.Kind, entry.Encounter)
		}
	}
	opened := entries[0].Opened
	if opened == nil || opened.Scene != (SceneContext{InstanceID: 2117, Known: true}) || !opened.StartedAt.Equal(encounterAt(10)) {
		t.Fatalf("opening = %+v", opened)
	}
	finished := entries[len(entries)-1].Finished
	snapshot := requireOneCompleted(t, statistics.Snapshot())
	// The journaled result is the engine's own, before integrity, which is what the engine renders back.
	if rendered := ApplyEncounterIntegrity(finished.Snapshot, finished.Snapshot.Integrity); !reflect.DeepEqual(rendered, snapshot) {
		t.Fatalf("journaled encounter differs from the engine's:\n%+v\n%+v", rendered, snapshot)
	}
	if finished.Scene != opened.Scene || len(finished.Enemies) != 2 || finished.EnemiesOverflow != 0 {
		t.Fatalf("finished = %+v", finished)
	}
	first, second := finished.Enemies[0], finished.Enemies[1]
	if first.Ordinal != 0 || first.MonsterID != 340000040 || first.DamageTaken != 100 || first.HitsTaken != 1 || first.DamageDealt != 30 || first.HitsDealt != 1 ||
		second.Ordinal != 1 || second.MonsterID != 340000040 || second.DamageTaken != 40 || second.DamageDealt != 0 {
		t.Fatalf("two enemies of the same type must stay two instances: %+v", finished.Enemies)
	}
	timeline, ok := finished.Timeline.Render("x", snapshot.StartedAt, snapshot.EndedAt, 0)
	live := statistics.encounterTimelines()[0]
	live.EncounterID = "x"
	if !ok || !reflect.DeepEqual(timeline, live) {
		t.Fatalf("the stored timeline does not render as the engine's:\n%+v\n%+v", timeline, live)
	}
}

func TestHistoryJournalDiscardsAnEmptyEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEncounters)
	statistics.observeBattleState(true, encounterAt(10), 1)
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedOutgoing, 5, 11))
	statistics.observeBattleState(false, encounterAt(12), 1)
	statistics.observeCaptureTime(encounterAt(20))
	entries, _ := takeSpan(t, statistics)
	// The encounter level journals no event entries.
	if got, want := journalKinds(entries), []HistoryEntryKind{HistoryEncounterOpened, HistoryEncounterDiscarded}; !reflect.DeepEqual(got, want) {
		t.Fatalf("journal = %v, want %v", got, want)
	}
}

func TestHistoryJournalFollowsALateLossIntoAFinishedEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEncounters)
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.observeBattleState(false, encounterAt(20), 1)
	statistics.observeCaptureTime(encounterAt(25))
	statistics.TakeHistory()
	// A loss detected after the end, but starting inside the encounter, marks it; a failure raises it further.
	statistics.beginTransportLoss(1, "in", encounterAt(15))
	statistics.endTransportLoss(1, encounterAt(26))
	statistics.failTransportLoss(1)
	entries, _ := statistics.TakeHistory()
	want := []HistoryEntryKind{HistoryLossBegan, HistoryEncounterIntegrity, HistoryLossEnded, HistoryLossFailed, HistoryEncounterIntegrity}
	if got := journalKinds(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("journal = %v, want %v", got, want)
	}
	last := entries[len(entries)-1].Integrity
	if engine := requireOneCompleted(t, statistics.Snapshot()).Integrity; last == nil || !reflect.DeepEqual(*last, engine) || last.Status != IntegrityUnreliable {
		t.Fatalf("journaled integrity %+v, engine %+v", last, engine)
	}
}

func TestHistoryJournalSurvivesAnIntervalReset(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEncounters)
	statistics.observeScene(8, true)
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	// A new protocol session ends the encounter and forgets the scene; the reset keeps the undrained journal.
	statistics.observeSessionChange(encounterAt(12))
	statistics.Reset()
	statistics.observeBattleState(true, encounterAt(30), 2)
	record := testEncounterRecord(CombatFlowLocalOutgoing, 50, 31)
	record.Key.ProtocolSession = 2
	statistics.addCombatEvent(record)
	statistics.finishCapture()
	entries, _ := statistics.TakeHistory()
	var finished []*HistoryEncounter
	for _, entry := range entries {
		if entry.Kind == HistoryEncounterFinished {
			finished = append(finished, entry.Finished)
		}
	}
	if len(finished) != 2 || finished[0].Snapshot.StatisticsIntervalID != 1 || finished[1].Snapshot.StatisticsIntervalID != 2 {
		t.Fatalf("finished = %+v", finished)
	}
	if finished[0].Scene != (SceneContext{InstanceID: 8, Known: true}) || finished[1].Scene.Known {
		t.Fatalf("scenes = %+v / %+v", finished[0].Scene, finished[1].Scene)
	}
	if finished[1].Snapshot.Outcome != EncounterOutcomeIncomplete {
		t.Fatalf("an encounter cut off by the capture end must stay incomplete, got %s", finished[1].Snapshot.Outcome)
	}
}

func TestHistoryJournalBoundCountsWhatItRefuses(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEvents)
	takeSpan(t, statistics)
	for i := 0; i < maxHistoryJournalEntries+5; i++ {
		statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedIncoming, 1, float64(i)*0.001))
	}
	entries, dropped := statistics.TakeHistory()
	if len(entries) != maxHistoryJournalEntries || dropped != 5 {
		t.Fatalf("kept %d, dropped %d", len(entries), dropped)
	}
}

func TestEnemyInstanceWithConflictingTypesIsUnknown(t *testing.T) {
	var enemies enemyAggregate
	enemies.add(enemyRecord(CombatFlowLocalOutgoing, 10, 1, 9, 111))
	enemies.add(enemyRecord(CombatFlowLocalOutgoing, 10, 2, 9, 222))
	enemies.add(enemyRecord(CombatFlowLocalOutgoing, 10, 3, 9, 111))
	got := enemies.snapshot()
	if len(got) != 1 || !got[0].IdentityConflict || got[0].MonsterID != 0 || got[0].MonsterStatus != "UNKNOWN" || got[0].DamageTaken != 30 {
		t.Fatalf("conflicting instance = %+v", got)
	}
	enemies.add(enemyRecord(CombatFlowUnattributedOutgoing, 10, 4, 10, 333))
	if len(enemies.snapshot()) != 1 {
		t.Fatal("an unattributed hit has no confirmed enemy")
	}
}

func TestStoredTimelineRejectsWhatNoEngineStores(t *testing.T) {
	for _, stored := range []StoredTimeline{
		{ScaleSeconds: 0},
		{ScaleSeconds: 3},
		{ScaleSeconds: 1, Buckets: []StoredTimelineBucket{{Index: 0, Contributor: 0, Damage: 5}}},
		{ScaleSeconds: 1, Contributors: []TimelineContributor{{Kind: "role", RoleID: 1}}, Buckets: []StoredTimelineBucket{{Index: 0, Contributor: 0, Damage: -1}}},
	} {
		if _, ok := stored.Render("x", time.Unix(0, 0), time.Unix(10, 0), 0); ok {
			t.Fatalf("an invalid timeline rendered: %+v", stored)
		}
	}
}
