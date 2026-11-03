package capture

import (
	"testing"
	"time"
)

// Boundary and clock tests for the encounter engine. Time is injected as capture timestamps through
// observeCaptureTime, battle-state signals, events, and SnapshotAt; nothing here waits on a real clock.

func assertEncountersDoNotOverlap(t *testing.T, snapshot DamageStatisticsSnapshot) {
	t.Helper()
	encounters := snapshot.Encounters.Completed
	if snapshot.Encounters.Current != nil {
		encounters = append(append([]EncounterSnapshot(nil), encounters...), *snapshot.Encounters.Current)
	}
	for i := 1; i < len(encounters); i++ {
		previous, next := encounters[i-1], encounters[i]
		if previous.ProtocolSession == next.ProtocolSession && !next.StartedAt.After(previous.EndedAt) {
			t.Fatalf("encounter %s starts at %s, not after %s ended at %s", next.EncounterID, next.StartedAt, previous.EncounterID, previous.EndedAt)
		}
	}
}

func finishedEncounterThenSignalMode(statistics *DamageStatistics) {
	statistics.observeBattleState(true, encounterAt(1), 1)
	outgoing(statistics, 100, 5)
	statistics.observeBattleState(false, encounterAt(10), 1)
	statistics.observeCaptureTime(encounterAt(11.2))
}

func TestPreRollLateEventCannotReachIntoPreviousEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	finishedEncounterThenSignalMode(statistics)
	// A response stamped at the previous exit, delivered after that encounter finished.
	late := testEncounterRecord(CombatFlowLocalOutgoing, 40, 10)
	if assignment := statistics.addCombatEvent(late); assignment != encounterAssignmentOutside {
		t.Fatalf("a late event inside the finished encounter's span must stay outside, got %q", assignment)
	}
	statistics.observeBattleState(true, encounterAt(11.5), 1)
	outgoing(statistics, 60, 12)
	snapshot := statistics.Snapshot()
	current := snapshot.Encounters.Current
	if current == nil || !current.StartedAt.Equal(encounterAt(11.5)) || current.Totals.DamageDealt != 60 ||
		snapshot.Encounters.OutsideEncounters.DamageDealt != 40 {
		t.Fatalf("the next encounter must not absorb the late event or start before the previous end: %+v", snapshot.Encounters)
	}
	assertEncountersDoNotOverlap(t, snapshot)
	assertEncounterTotalsConserved(t, snapshot)
}

func TestPreRollOutOfOrderEventAfterOpenCannotOverlap(t *testing.T) {
	statistics := NewDamageStatistics()
	finishedEncounterThenSignalMode(statistics)
	statistics.observeBattleState(true, encounterAt(11.5), 1)
	outgoing(statistics, 60, 12)
	// Out of order: within the 2 s pre-roll of the new start, but inside the previous encounter.
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 25, 9.8))
	// Out of order and after the previous end: it may move the start back, but not past that end.
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 15, 10.5))
	snapshot := statistics.Snapshot()
	current := snapshot.Encounters.Current
	if current == nil || !current.StartedAt.Equal(encounterAt(10.5)) || current.Totals.DamageDealt != 75 ||
		snapshot.Encounters.OutsideEncounters.DamageDealt != 25 || !current.BattleEntered.Equal(encounterAt(11.5)) {
		t.Fatalf("unexpected pre-roll result for out-of-order events: %+v", snapshot.Encounters)
	}
	assertEncountersDoNotOverlap(t, snapshot)
	assertEncounterTotalsConserved(t, snapshot)
}

func TestPreRollEventDuringEndingCandidateBeforeStartRespectsWindow(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(false, encounterAt(0), 1)
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 11)
	statistics.observeBattleState(false, encounterAt(15), 1)
	// Delivered while the encounter is an ending candidate, but stamped long before it started.
	if assignment := statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 30, 5)); assignment != encounterAssignmentOutside {
		t.Fatalf("an event older than the pre-roll window must stay outside even during the grace period, got %q", assignment)
	}
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 20, 9))
	statistics.observeCaptureTime(encounterAt(17))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	if !encounter.StartedAt.Equal(encounterAt(9)) || !encounter.FirstActivityAt.Equal(encounterAt(9)) || encounter.Totals.DamageDealt != 120 {
		t.Fatalf("an in-window event during the grace period must move the start consistently: %+v", encounter)
	}
}

func TestPreRollWaveFlickerJoinsTheSameEncounter(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(1), 1)
	outgoing(statistics, 100, 5)
	statistics.observeBattleState(false, encounterAt(10), 1)
	outgoing(statistics, 30, 10.1)
	statistics.observeBattleState(true, encounterAt(10.3), 1)
	outgoing(statistics, 70, 11)
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current == nil || snapshot.Encounters.Current.Totals.DamageDealt != 200 || len(snapshot.Encounters.Completed) != 0 ||
		snapshot.Encounters.Current.Reentries != 1 {
		t.Fatalf("hits between a wave exit and the next wave belong to the same encounter: %+v", snapshot.Encounters)
	}
}

func TestPreRollSameTimestampAndUnknownEvents(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(false, encounterAt(0), 1)
	for i := 0; i < 3; i++ {
		outgoing(statistics, 10, 20)
	}
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedOutgoing, 999, 19.5))
	statistics.observeBattleState(true, encounterAt(20.2), 1)
	snapshot := statistics.Snapshot()
	current := snapshot.Encounters.Current
	if current == nil || !current.StartedAt.Equal(encounterAt(20)) || current.Totals.DamageDealt != 30 || current.Totals.OutgoingEvents != 3 ||
		current.Totals.UnattributedOutgoingDamage != 999 || current.EncounterDPS.Damage != 30 {
		t.Fatalf("simultaneous hits must each count once and an unattributed hit must not move the start or the numerator: %+v", current)
	}
	assertEncounterTotalsConserved(t, snapshot)
}

func TestPreRollDuplicateResponseCountsOnce(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(false, encounterAt(0), 1)
	record := testEncounterRecord(CombatFlowLocalOutgoing, 50, 20)
	statistics.addCombatEvent(record)
	if assignment := statistics.addCombatEvent(record); assignment != statisticsAssignmentDuplicate {
		t.Fatalf("a repeated response in the pre-roll window must be rejected, got %q", assignment)
	}
	statistics.observeBattleState(true, encounterAt(20.1), 1)
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current.Totals.DamageDealt != 50 || snapshot.DamageDealt != 50 || snapshot.DuplicateEvents != 1 {
		t.Fatalf("a pre-roll event must enter the encounter once: %+v", snapshot)
	}
}

func TestPreRollDoesNotCrossProtocolSessions(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(false, encounterAt(0), 1)
	outgoing(statistics, 50, 20)
	statistics.observeSessionChange(encounterAt(20.5))
	statistics.observeBattleState(true, encounterAt(21), 2)
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current == nil || snapshot.Encounters.Current.Totals.DamageDealt != 0 ||
		snapshot.Encounters.OutsideEncounters.DamageDealt != 50 || !snapshot.Encounters.Current.StartedAt.Equal(encounterAt(21)) {
		t.Fatalf("a hit from the previous protocol session must not join the new session's encounter: %+v", snapshot.Encounters)
	}
}

func TestPreRollDoesNotCrossReset(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(false, encounterAt(0), 1)
	outgoing(statistics, 50, 20)
	statistics.observeCaptureTime(encounterAt(20.4))
	statistics.Reset()
	// Delivered after the reset but stamped before it.
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 9, 20.3))
	statistics.observeBattleState(true, encounterAt(21), 1)
	outgoing(statistics, 40, 21.5)
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current == nil || snapshot.Encounters.Current.Totals.DamageDealt != 40 ||
		!snapshot.Encounters.Current.StartedAt.Equal(encounterAt(21)) || snapshot.Encounters.OutsideEncounters.DamageDealt != 9 {
		t.Fatalf("events from before a reset must not start or extend an encounter of the new interval: %+v", snapshot.Encounters)
	}
	assertEncounterTotalsConserved(t, snapshot)
}

func TestPreRollStartTimestampsStayDistinct(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(false, encounterAt(0), 1)
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalIncoming, 5, 29.9))
	outgoing(statistics, 100, 30.1)
	statistics.observeBattleState(true, encounterAt(30.3), 1)
	outgoing(statistics, 100, 32.9)
	current := statistics.Snapshot().Encounters.Current
	// Server battle-state start, first included confirmed activity, and the effective start are reported separately.
	if !current.BattleEntered.Equal(encounterAt(30.3)) || !current.FirstActivityAt.Equal(encounterAt(29.9)) ||
		!current.StartedAt.Equal(encounterAt(29.9)) || current.EncounterDPS.Duration != 3*time.Second {
		t.Fatalf("unexpected start timestamps: %+v", current)
	}
}

// The owner's reference example: 100,000 damage, no further hits, the encounter still ACTIVE.
func TestLiveEncounterDPSDecaysWithoutDamage(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	outgoing(statistics, 100000, 2)
	session := statistics.Snapshot().CaptureSessionID
	for _, step := range []struct {
		seconds float64
		want    float64
	}{{5, 20000}, {10, 10000}, {20, 5000}} {
		// Engine with an explicit query time.
		if got := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(step.seconds)}).EncounterDPS; got.Value != step.want ||
			got.Confidence != "live" || got.Damage != 100000 {
			t.Fatalf("at %vs: got %+v, want %v", step.seconds, got, step.want)
		}
	}
	// Engine driven by frame time only (heartbeat frames, no damage).
	statistics.observeCaptureTime(encounterAt(40))
	if got := statistics.Snapshot().EncounterDPS; got.Value != 2500 || got.Duration != 40*time.Second {
		t.Fatalf("the newest frame time must advance the live denominator: %+v", got)
	}
}

func TestConsoleLineDecaysWithHeartbeatFrames(t *testing.T) {
	p := newEncounterTestProcessor()
	p.damageStatistics.observeBattleState(true, encounterAt(0), 1)
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 100000, 2))
	for _, step := range []struct {
		seconds float64
		want    string
	}{{5, "encounter_dps=20000.0"}, {10, "encounter_dps=10000.0"}, {20, "encounter_dps=5000.0"}} {
		// handleFrame sets both values for every decoded frame, including non-combat heartbeats.
		p.currentFrameTime = encounterAt(step.seconds)
		p.observeEncounterClock(encounterAt(step.seconds))
		if line := p.currentStatisticsReport().String(); !containsAll(line, step.want, "encounter_state=ACTIVE", "encounter_confidence=live") {
			t.Fatalf("at %vs the console line %q lacks %q", step.seconds, line, step.want)
		}
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for i := 0; i+len(part) <= len(text); i++ {
			if text[i:i+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestFinishedEncounterDPSIsFixed(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	outgoing(statistics, 1000, 2)
	statistics.observeBattleState(false, encounterAt(10), 1)
	statistics.observeCaptureTime(encounterAt(12))
	first := requireOneCompleted(t, statistics.Snapshot()).EncounterDPS
	statistics.observeCaptureTime(encounterAt(500))
	session := statistics.Snapshot().CaptureSessionID
	later := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(900)})
	if first.Value != 100 || first.Confidence != "confirmed" || requireOneCompleted(t, later).EncounterDPS != first || later.EncounterDPS != first {
		t.Fatalf("a finished encounter's DPS must not change with time: first=%+v later=%+v", first, later.EncounterDPS)
	}
}

func TestIncompleteDPSIsNotFinal(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	outgoing(statistics, 1000, 2)
	statistics.observeCaptureTime(encounterAt(10))
	statistics.finishCapture()
	dps := statistics.Snapshot().EncounterDPS
	if dps.Confidence != "incomplete" || dps.Confidence == "confirmed" || dps.Confidence == "inferred" || dps.Value != 100 {
		t.Fatalf("an encounter cut by capture end must be labeled incomplete: %+v", dps)
	}
}

func TestEncountersDoNotShareTimeState(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	outgoing(statistics, 1000, 2)
	statistics.observeBattleState(false, encounterAt(10), 1)
	statistics.observeCaptureTime(encounterAt(12))
	statistics.observeBattleState(true, encounterAt(50), 1)
	outgoing(statistics, 600, 51)
	statistics.observeCaptureTime(encounterAt(56))
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current.EncounterDPS.Value != 100 || snapshot.Encounters.Current.EncounterDPS.Duration != 6*time.Second ||
		snapshot.Encounters.Completed[0].EncounterDPS.Value != 100 {
		t.Fatalf("each encounter must use its own start and end: %+v", snapshot.Encounters)
	}
	statistics.Reset()
	statistics.observeBattleState(true, encounterAt(60), 1)
	outgoing(statistics, 50, 61)
	statistics.observeCaptureTime(encounterAt(65))
	if dps := statistics.Snapshot().EncounterDPS; dps.Value != 10 || dps.Duration != 5*time.Second {
		t.Fatalf("after a reset the live encounter must start from its own signal: %+v", dps)
	}
}

func TestLiveEncounterDPSIgnoresBackwardTime(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	outgoing(statistics, 1000, 9)
	statistics.observeCaptureTime(encounterAt(10))
	// A multi-fragment frame keeps its first fragment's time, and a host clock step can move backwards.
	statistics.observeCaptureTime(encounterAt(4))
	session := statistics.Snapshot().CaptureSessionID
	backward := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(4)}).EncounterDPS
	if backward.Value != 100 || backward.Duration != 10*time.Second || !backward.EndObservedAt.Equal(encounterAt(10)) {
		t.Fatalf("a backward query must not shorten the live encounter: %+v", backward)
	}
	before := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: session, Timestamp: encounterAt(-1)}).EncounterDPS
	if before.Status != DPSStatusAvailable || before.Value != 100 {
		t.Fatalf("a query before the start must resolve to the newest observed time, not a negative span: %+v", before)
	}

	empty := NewDamageStatistics()
	empty.observeBattleState(true, encounterAt(3), 1)
	outgoing(empty, 10, 3)
	if dps := empty.Snapshot().EncounterDPS; dps.Status != DPSStatusInvalidDuration || dps.Value != 0 {
		t.Fatalf("a zero-length live encounter must not produce a value: %+v", dps)
	}
}
