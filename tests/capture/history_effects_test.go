package capture

import (
	"encoding/json"
	"reflect"
	"testing"
)

// journaledTracker is an effect tracker that journals into statistics recording at the event level.
func journaledTracker(t *testing.T) (*EffectTracker, *DamageStatistics) {
	t.Helper()
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEvents)
	takeSpan(t, statistics)
	tracker := newJournaledEffectTracker(statistics)
	tracker.captureSessionID = effectTestCaptureSession
	return tracker, statistics
}

// effectEntries drains the journal and returns its effect changes in order.
func effectEntries(t *testing.T, statistics *DamageStatistics) []HistoryEffect {
	t.Helper()
	entries, dropped := statistics.TakeHistory()
	if dropped != 0 {
		t.Fatalf("the journal refused %d entries", dropped)
	}
	var effects []HistoryEffect
	for _, entry := range entries {
		if entry.Kind != HistoryEffectChanged || entry.Effect == nil {
			t.Fatalf("unexpected entry %s", entry.Kind)
		}
		effects = append(effects, *entry.Effect)
	}
	return effects
}

func changes(effects []HistoryEffect) []EffectChange {
	out := make([]EffectChange, 0, len(effects))
	for _, effect := range effects {
		out = append(out, effect.Change)
	}
	return out
}

func TestEffectJournalRepeatsOneInstanceLifecycleInOrder(t *testing.T) {
	tracker, statistics := journaledTracker(t)
	tracker.ObserveApply(testEffectApply(5, 1, func(event *EffectApplyEvent) {
		event.HolderActor = &EffectActorIdentity{Type: &CombatActorType{TemplateID: 34000, TemplateStatus: "CORRELATED"}}
		event.SourceActor = &EffectActorIdentity{Role: &CombatRole{PlayerID: 7, RoleID: 1505, Basis: "role_entity"}}
	}))
	tracker.ObserveApply(testEffectApply(5, 2, nil))
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, StackCount: 3, HasStackCount: true, ObservedAt: effectAt(3)})
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, DurationSeconds: effectFloat(8), RemainingSeconds: effectFloat(6), ObservedAt: effectAt(4)})
	// Two identical stack messages in a row are both lifecycle facts; neither is a duplicate of the other.
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, StackCount: 3, HasStackCount: true, ObservedAt: effectAt(5)})
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, StackCount: 3, HasStackCount: true, ObservedAt: effectAt(5.5)})
	tracker.ObserveRemove(testEffectRemove(5, 6))
	effects := effectEntries(t, statistics)
	want := []EffectChange{EffectChangeApply, EffectChangeRefresh, EffectChangeStack, EffectChangeDuration, EffectChangeStack, EffectChangeStack, EffectChangeRemove}
	if got := changes(effects); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for i, effect := range effects {
		if effect.Instance != 1 || effect.Sequence != uint64(i+1) || effect.EffectID != 100 || !effect.HasSource || effect.SourceEntityID != 20 {
			t.Fatalf("change %d = %+v", i, effect)
		}
		if effect.HolderActor == nil || effect.HolderActor.Type.TemplateID != 34000 || effect.SourceActor == nil || effect.SourceActor.Role.RoleID != 1505 {
			t.Fatalf("change %d lost the identities: %+v / %+v", i, effect.HolderActor, effect.SourceActor)
		}
	}
	if effects[2].Stack != 3 || !effects[2].HasStack || effects[3].Duration != 8 || !effects[3].TimeKnown || effects[3].Remaining != 6 || !effects[3].BasisAt.Equal(effectAt(4)) {
		t.Fatalf("state after the changes: %+v / %+v", effects[2], effects[3])
	}
	last := effects[len(effects)-1]
	if last.Observation != EffectObserved || last.Reason != string(EffectLifecycleConfirmedRemoved) || last.Evidence != EffectEvidenceServerNotify {
		t.Fatalf("remove = %+v", last)
	}
}

func TestEffectJournalNeverStoresAnEstimateAsARemoval(t *testing.T) {
	tracker, statistics := journaledTracker(t)
	tracker.ObserveApply(testEffectApply(5, 1, nil))
	// Far past the estimated expiry, the instance is still only estimated to have expired: nothing is journaled for it.
	tracker.SnapshotAt(effectQuery(500))
	if effects := effectEntries(t, statistics); len(effects) != 1 || effects[0].Change != EffectChangeApply {
		t.Fatalf("an estimate was journaled: %v", changes(effects))
	}
	// A scene boundary drops it: untracked, not removed.
	tracker.EndLiveStateFor(EffectUntrackedSceneChange)
	effects := effectEntries(t, statistics)
	if len(effects) != 1 || effects[0].Change != EffectChangeUntracked || effects[0].Observation != EffectUntracked || effects[0].Reason != EffectUntrackedSceneChange || effects[0].Instance != 1 {
		t.Fatalf("boundary = %+v", effects)
	}
}

func TestEffectJournalLabelsDerivedEnds(t *testing.T) {
	tracker, statistics := journaledTracker(t)
	tracker.ObserveApply(testEffectApply(5, 1, nil))
	tracker.ObserveApply(testEffectApply(6, 1, nil))
	tracker.ObserveHolderRemoved(1, 10, effectAt(2))
	// The REMOVE that arrives after the holder removal names the instance that already ended.
	tracker.ObserveRemove(testEffectRemove(5, 2.5))
	// A reused handle for another effect ends the earlier instance: its REMOVE was missed.
	tracker.ObserveApply(testEffectApply(7, 3, nil))
	tracker.ObserveApply(testEffectApply(7, 4, func(event *EffectApplyEvent) { event.EffectID = 200 }))
	effects := effectEntries(t, statistics)
	want := []EffectChange{EffectChangeApply, EffectChangeApply, EffectChangeHolderRemoved, EffectChangeHolderRemoved, EffectChangeRemove, EffectChangeApply, EffectChangeSuperseded, EffectChangeApply}
	if got := changes(effects); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	if effects[2].Observation != EffectDerived || effects[3].Observation != EffectDerived || effects[4].Instance != effects[2].Instance && effects[4].Instance != effects[3].Instance {
		t.Fatalf("holder removal = %+v %+v, late remove = %+v", effects[2], effects[3], effects[4])
	}
	if effects[6].Observation != EffectDerived || effects[6].Reason != EffectSupersededHandleReused || effects[6].Instance != effects[5].Instance || effects[7].Instance == effects[5].Instance || effects[7].EffectID != 200 {
		t.Fatalf("superseded = %+v, new = %+v", effects[6], effects[7])
	}
}

func TestEffectJournalSkipsWhatTheTrackerRejects(t *testing.T) {
	tracker, statistics := journaledTracker(t)
	// Both ends non-player: excluded by the tracker, so not journaled.
	tracker.ObserveApply(testEffectApply(5, 1, func(event *EffectApplyEvent) { event.SourceRelation = EffectRelationNonPlayer }))
	// An update or remove without a tracked instance carries no identity.
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 9, StackCount: 2, HasStackCount: true, ObservedAt: effectAt(2)})
	tracker.ObserveRemove(testEffectRemove(9, 3))
	// A stale update (older than the instance's last change) changes nothing and is not journaled.
	tracker.ObserveApply(testEffectApply(6, 5, nil))
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 6, StackCount: 4, HasStackCount: true, ObservedAt: effectAt(4)})
	// An instant effect (no handle) is journaled once as instant.
	tracker.ObserveApply(testEffectApply(^uint64(1), 6, nil))
	effects := effectEntries(t, statistics)
	if got, want := changes(effects), []EffectChange{EffectChangeApply, EffectChangeInstant}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	if effects[0].Instance == effects[1].Instance {
		t.Fatal("an instant effect shares an instance number")
	}
}

func TestEffectJournalLeavesTheTrackerUnchanged(t *testing.T) {
	plain := newTestEffectTracker()
	journaled, statistics := journaledTracker(t)
	feed := func(tracker *EffectTracker) {
		tracker.ObserveApply(testEffectApply(5, 1, nil))
		tracker.ObserveSnapshot(testEffectApply(6, 1.5, nil))
		tracker.ObserveApply(testEffectApply(5, 2, nil))
		tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, StackCount: 2, HasStackCount: true, ObservedAt: effectAt(3)})
		tracker.ObserveApply(testEffectApply(^uint64(1), 3.5, nil))
		tracker.ObserveRemove(testEffectRemove(5, 4))
		tracker.ObserveHolderRemoved(1, 10, effectAt(5))
		tracker.ObserveRemove(testEffectRemove(6, 5.5))
		tracker.ObserveApply(testEffectApply(8, 6, nil))
		tracker.EndLiveStateFor(EffectUntrackedSessionChange)
	}
	feed(plain)
	feed(journaled)
	want, _ := json.Marshal(plain.SnapshotAt(effectQuery(7)))
	got, _ := json.Marshal(journaled.SnapshotAt(effectQuery(7)))
	if string(got) != string(want) {
		t.Fatalf("journaling changed the tracker:\n got %s\nwant %s", got, want)
	}
	if effects := effectEntries(t, statistics); len(effects) == 0 {
		t.Fatal("nothing was journaled")
	}
}

func TestEffectJournalPlacesChangesInTheCombatOrder(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEvents)
	tracker := newJournaledEffectTracker(statistics)
	outgoing(statistics, 100, 1)
	tracker.ObserveApply(testEffectApply(5, 1, nil))
	outgoing(statistics, 50, 2)
	outgoing(statistics, 50, 3)
	tracker.ObserveRemove(testEffectRemove(5, 3))
	entries, _ := statistics.TakeHistory()
	var after []uint64
	for _, entry := range entries {
		if entry.Kind == HistoryEffectChanged {
			after = append(after, entry.Effect.AfterCombat)
		}
	}
	if !reflect.DeepEqual(after, []uint64{1, 3}) {
		t.Fatalf("effect changes follow combat results %v, want [1 3]", after)
	}
}

func TestEffectJournalIsSilentWithoutRecording(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ConfigureHistory(HistoryEvents, false)
	tracker := newJournaledEffectTracker(statistics)
	tracker.ObserveApply(testEffectApply(5, 1, nil))
	if entries, _ := statistics.TakeHistory(); len(entries) != 0 {
		t.Fatalf("a paused journal recorded %v", journalKinds(entries))
	}
	// Turning recording on at a clean boundary starts a span; the instance is first stored by its next change.
	statistics.SetHistoryRecording(true)
	tracker.ObserveRemove(testEffectRemove(5, 2))
	entries, _ := statistics.TakeHistory()
	if got := journalKinds(entries); !reflect.DeepEqual(got, []HistoryEntryKind{HistoryRecordingStarted, HistoryEffectChanged}) ||
		entries[0].Recording.Reason != HistoryStartEnabled || entries[1].Effect.Change != EffectChangeRemove {
		t.Fatalf("journal = %v", got)
	}
}

func TestHistoryRecordingStopsAtOnceAndNamesTheEncounterInProgress(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEvents)
	takeSpan(t, statistics)
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.SetHistoryRecording(false)
	outgoing(statistics, 100, 12)
	statistics.observeBattleState(false, encounterAt(20), 1)
	statistics.observeCaptureTime(encounterAt(30))
	entries, _ := statistics.TakeHistory()
	kinds := journalKinds(entries)
	if kinds[len(kinds)-1] != HistoryRecordingStopped {
		t.Fatalf("entries after the stop were journaled: %v", kinds)
	}
	stop := entries[len(entries)-1].Recording
	if stop.Reason != HistoryStopDisabled || !reflect.DeepEqual(stop.Open, []string{"1-1"}) {
		t.Fatalf("stop = %+v", stop)
	}
	for _, entry := range entries {
		if entry.Kind == HistoryEncounterFinished {
			t.Fatal("an encounter cut by the stop was journaled as finished")
		}
	}
	if state := statistics.HistoryRecording(); state.Enabled || state.Recording {
		t.Fatalf("state = %+v", state)
	}
}

func TestHistoryRecordingStartsOnlyAtACleanBoundary(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.ConfigureHistory(HistoryEvents, false)
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.SetHistoryRecording(true)
	if state := statistics.HistoryRecording(); !state.Enabled || state.Recording {
		t.Fatalf("a span started inside an encounter: %+v", state)
	}
	outgoing(statistics, 100, 12)
	if entries, _ := statistics.TakeHistory(); len(entries) != 0 {
		t.Fatalf("journaled inside the encounter: %v", journalKinds(entries))
	}
	statistics.observeBattleState(false, encounterAt(20), 1)
	statistics.observeCaptureTime(encounterAt(25))
	entries, _ := statistics.TakeHistory()
	if got := journalKinds(entries); !reflect.DeepEqual(got, []HistoryEntryKind{HistoryRecordingStarted}) || entries[0].Recording.Reason != HistoryStartEnabled {
		t.Fatalf("journal = %v", got)
	}
	// The next encounter is recorded whole.
	statistics.observeBattleState(true, encounterAt(40), 1)
	outgoing(statistics, 70, 41)
	statistics.observeBattleState(false, encounterAt(45), 1)
	statistics.observeCaptureTime(encounterAt(50))
	entries, _ = statistics.TakeHistory()
	if kinds := journalKinds(entries); kinds[0] != HistoryEncounterOpened || kinds[len(kinds)-1] != HistoryEncounterFinished {
		t.Fatalf("journal = %v", kinds)
	}
}

func TestHistoryKeepsTheIntegrityOfARecordedEncounterAfterTheStop(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEvents)
	takeSpan(t, statistics)
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.observeBattleState(false, encounterAt(12), 1)
	statistics.observeCaptureTime(encounterAt(20))
	statistics.SetHistoryRecording(false)
	statistics.TakeHistory()
	// A loss detected late reaches into the recorded encounter: its integrity change is still journaled.
	statistics.beginTransportLoss(1, "in", encounterAt(11.5))
	entries, _ := statistics.TakeHistory()
	found := false
	for _, entry := range entries {
		if entry.Kind == HistoryEncounterIntegrity && entry.Encounter == "1-1" {
			found = true
		}
		if entry.Kind == HistoryLossBegan {
			t.Fatal("a loss was journaled between spans")
		}
	}
	if !found {
		t.Fatalf("the recorded encounter kept a stale integrity: %v", journalKinds(entries))
	}
}

func TestHistoryCheckpointIsTheEngineRenderingOfTheEncounterInProgress(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.EnableHistory(HistoryEvents)
	takeSpan(t, statistics)
	statistics.CheckpointHistory()
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	outgoing(statistics, 300, 14)
	statistics.TakeHistory()
	statistics.CheckpointHistory()
	// Nothing changed: no second checkpoint.
	statistics.CheckpointHistory()
	entries, _ := statistics.TakeHistory()
	if got := journalKinds(entries); !reflect.DeepEqual(got, []HistoryEntryKind{HistoryEncounterCheckpoint}) {
		t.Fatalf("journal = %v", got)
	}
	checkpoint := entries[0].Finished
	live := statistics.Snapshot().Encounters.Current
	if live == nil || checkpoint.Snapshot.Outcome != EncounterOutcomeOpen || !checkpoint.CheckpointAt.Equal(encounterAt(14)) {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}
	if rendered := ApplyEncounterIntegrity(checkpoint.Snapshot, checkpoint.Snapshot.Integrity); !reflect.DeepEqual(rendered.EncounterDPS, live.EncounterDPS) ||
		rendered.Totals != live.Totals || !reflect.DeepEqual(rendered.Roles, live.Roles) {
		t.Fatalf("the checkpoint is not the engine's live rendering:\n%+v\n%+v", rendered.EncounterDPS, live.EncounterDPS)
	}
	statistics.observeBattleState(false, encounterAt(20), 1)
	statistics.observeCaptureTime(encounterAt(30))
	entries, _ = statistics.TakeHistory()
	if kinds := journalKinds(entries); kinds[len(kinds)-1] != HistoryEncounterFinished {
		t.Fatalf("journal = %v", kinds)
	}
}
