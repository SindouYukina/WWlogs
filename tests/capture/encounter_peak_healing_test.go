package capture

import (
	"testing"
	"time"
)

// Synthetic fixtures for the peak 1-second DPS and the local healing totals. They test the algorithms; the evidence
// that real captures carry these values is in the healing inventory collector and the replay regression.

func peakOf(t *testing.T, statistics *DamageStatistics) DPSResult {
	t.Helper()
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current != nil {
		return snapshot.Encounters.Current.PeakDPS
	}
	if n := len(snapshot.Encounters.Completed); n > 0 {
		return snapshot.Encounters.Completed[n-1].PeakDPS
	}
	t.Fatal("no encounter")
	return DPSResult{}
}

func requirePeak(t *testing.T, peak DPSResult, damage int64) {
	t.Helper()
	if peak.Status != DPSStatusAvailable || peak.Damage != damage || peak.Value != float64(damage) || peak.Duration != time.Second {
		t.Fatalf("peak = %+v, want %d per 1 s", peak, damage)
	}
}

func TestPeakDPSIsTheBestOneSecondInterval(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 500, 10)
	requirePeak(t, peakOf(t, statistics), 500)
	// Several hits in one interval add up; hits of two roles in the same interval add up too.
	outgoing(statistics, 300, 11.1)
	roleOutgoing(statistics, 1205, 400, 11.5)
	roleOutgoing(statistics, 1409, 600, 11.9)
	requirePeak(t, peakOf(t, statistics), 1300)
	// 12.95 and 13.05 fall in two intervals on either side of a boundary.
	outgoing(statistics, 900, 12.95)
	outgoing(statistics, 900, 13.05)
	requirePeak(t, peakOf(t, statistics), 1300)
	// A quiet stretch never lowers the peak; a better interval raises it.
	statistics.observeCaptureTime(encounterAt(30))
	requirePeak(t, peakOf(t, statistics), 1300)
	outgoing(statistics, 2000, 31.2)
	requirePeak(t, peakOf(t, statistics), 2000)
	if peak := peakOf(t, statistics); !peak.StartObservedAt.Equal(encounterAt(31)) || !peak.EndObservedAt.Equal(encounterAt(31.2)) {
		t.Fatalf("peak interval [%s, %s]", peak.StartObservedAt, peak.EndObservedAt)
	}
}

func TestPeakDPSNeverScalesUpAShortInterval(t *testing.T) {
	// An encounter of 0.4 s: its only interval is reported per 1 s, not divided by 0.4 s.
	statistics := NewDamageStatistics()
	statistics.ObserveBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 800, 10.2)
	statistics.ObserveBattleState(false, encounterAt(10.4), 1)
	statistics.observeCaptureTime(encounterAt(15))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	requirePeak(t, encounter.PeakDPS, 800)
	if encounter.EncounterDPS.Value <= encounter.PeakDPS.Value {
		t.Fatalf("Encounter DPS over 0.4 s (%v) should exceed the per-second peak (%v)", encounter.EncounterDPS.Value, encounter.PeakDPS.Value)
	}
}

func TestPeakDPSIsUnavailableWithoutDamageDealt(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalIncoming, 500, 10))
	if peak := peakOf(t, statistics); peak.Status != DPSStatusUnavailable || peak.Reason != "no_confirmed_damage" {
		t.Fatalf("damage taken only: %+v", peak)
	}
}

func TestPeakDPSStartsOverWithEachEncounterAndSurvivesAnIncompleteOne(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 5000, 10)
	statistics.observeCaptureTime(encounterAt(10 + encounterInactivityTimeout.Seconds() + 5))
	outgoing(statistics, 700, 100)
	snapshot := statistics.Snapshot()
	if len(snapshot.Encounters.Completed) != 1 || snapshot.Encounters.Current == nil {
		t.Fatalf("expected one finished and one open encounter: %+v", snapshot.Encounters)
	}
	requirePeak(t, snapshot.Encounters.Completed[0].PeakDPS, 5000)
	requirePeak(t, snapshot.Encounters.Current.PeakDPS, 700)
	statistics.finishCapture()
	final := statistics.Snapshot().Encounters.Completed
	if final[1].Outcome != EncounterOutcomeIncomplete {
		t.Fatalf("outcome = %s", final[1].Outcome)
	}
	requirePeak(t, final[1].PeakDPS, 700)
}

func TestPeakDPSMatchesTheTimelineForAnyArrivalOrderAndIgnoresRepeats(t *testing.T) {
	events := []struct {
		amount  int64
		seconds float64
	}{{100, 10}, {250, 10.5}, {400, 11.2}, {50, 11.9}, {700, 13.4}, {120, 13.8}, {300, 15}}
	peaks := map[int64]bool{}
	for _, order := range [][]int{{0, 1, 2, 3, 4, 5, 6}, {6, 5, 4, 3, 2, 1, 0}, {3, 0, 6, 1, 5, 2, 4}} {
		statistics := NewDamageStatistics()
		for _, i := range order {
			record := testEncounterRecord(CombatFlowLocalOutgoing, events[i].amount, events[i].seconds)
			record.Key.RPCID = uint64(1000 + i)
			statistics.addCombatEvent(record)
			statistics.addCombatEvent(record) // a repeated response is applied once
		}
		peak := peakOf(t, statistics)
		peaks[peak.Damage] = true
		timeline := timelineNow(statistics, 15)
		best := int64(0)
		for _, point := range timeline.Points {
			best = max(best, point.Damage)
		}
		if peak.Damage != best || timeline.IntervalSeconds != 1 {
			t.Fatalf("peak %d, timeline best interval %d (interval %v s)", peak.Damage, best, timeline.IntervalSeconds)
		}
	}
	if len(peaks) != 1 || !peaks[820] {
		t.Fatalf("peaks for different arrival orders: %v, want only 820", peaks)
	}
}

// healing builds a local healing record: from a local source to a local target unless flow says otherwise.
func healing(flow CombatFlow, reported, applied int64, seconds float64, healerRole uint64) CombatEventRecord {
	record := testEncounterRecord(flow, reported, seconds)
	record.Kind, record.AppliedHealing = CombatEventHealing, applied
	if healerRole != 0 {
		record.SourceRole = &CombatRole{PlayerID: 7, RoleID: healerRole, Basis: roleBasisRoleEntity}
	}
	return record
}

// heal applies a healing record the way the processor does: the frame's capture time is observed before its results.
func heal(statistics *DamageStatistics, record CombatEventRecord) {
	statistics.observeCaptureTime(record.ObservedAt)
	statistics.addCombatEvent(record)
}

func currentHealing(t *testing.T, statistics *DamageStatistics) HealingResult {
	t.Helper()
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Current == nil {
		t.Fatal("no open encounter")
	}
	return snapshot.Encounters.Current.Healing
}

func TestEncounterHealingSumsAppliedHealingByHealer(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 1000, 10)
	heal(statistics, healing(CombatFlowLocalInternal, 3000, 3000, 11, 1209)) // a single heal on a teammate
	heal(statistics, healing(CombatFlowLocalInternal, 1200, 500, 12, 1209))  // partly overhealed
	heal(statistics, healing(CombatFlowLocalInternal, 900, 0, 13, 1209))     // target at full HP
	heal(statistics, healing(CombatFlowLocalInternal, 200, 200, 13.5, 1505)) // a second healer (multi-hit)
	heal(statistics, healing(CombatFlowLocalInternal, 200, 200, 13.7, 1505)) //
	heal(statistics, healing(CombatFlowLocalInternal, 400, 400, 14, 0))      // healer role unresolved
	got := currentHealing(t, statistics)
	if got.Status != DPSStatusAvailable || got.Effective != 4300 || got.Reported != 5900 || got.Overheal != 1600 || got.Events != 6 ||
		got.UnknownHealerEffective != 400 || got.UnknownHealerEvents != 1 || len(got.ByHealer) != 2 {
		t.Fatalf("encounter healing = %+v", got)
	}
	if got.ByHealer[0].RoleID != 1209 || got.ByHealer[0].Effective != 3500 || got.ByHealer[0].Events != 3 || got.ByHealer[1].RoleID != 1505 || got.ByHealer[1].Effective != 400 {
		t.Fatalf("by healer = %+v", got.ByHealer)
	}
	// The totals are conserved: the healers and the unknown part add up to the encounter's effective healing.
	sum := got.UnknownHealerEffective
	for _, healer := range got.ByHealer {
		sum += healer.Effective
	}
	if sum != got.Effective || statistics.Snapshot().Healing.Effective != got.Effective {
		t.Fatalf("healers %d, interval %d, encounter %d", sum, statistics.Snapshot().Healing.Effective, got.Effective)
	}
}

func TestSelfHealingCountsAndOtherResultsAreNeverHealing(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 1000, 10)
	self := healing(CombatFlowLocalInternal, 600, 450, 11, 1209)
	self.Target = self.Source // the healer heals itself
	heal(statistics, self)
	// A local result of any other calculate type is never healing, whatever its amount. Shields are not results at all:
	// they arrive in ShieldUpdateNotify, which the healing statistics never read.
	other := healing(CombatFlowLocalInternal, 900, 900, 12, 1209)
	other.Kind = CombatEventOther
	heal(statistics, other)
	snapshot := statistics.Snapshot()
	got := snapshot.Encounters.Current.Healing
	if got.Effective != 450 || got.Reported != 600 || got.Events != 1 || len(got.ByHealer) != 1 || got.ByHealer[0].RoleID != 1209 ||
		snapshot.Healing.Effective != 450 || snapshot.DamageDealt != 1000 {
		t.Fatalf("self healing and another result: %+v, interval %+v, damage dealt %d", got, snapshot.Healing, snapshot.DamageDealt)
	}
}

func TestHealingOutsideTheLocalTeamIsNotCounted(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 1000, 10)
	heal(statistics, healing(CombatFlowUnattributedOutgoing, 847, 847, 11, 0)) // an enemy healed by an unknown source
	heal(statistics, healing(CombatFlowUnresolved, 500, 500, 11.5, 0))         // neither side known
	held := healing(CombatFlowLocalInternal, 600, 600, 12, 1209)
	held.ContextDowngrade = contextDowngradeAttribution // held back by the attribution gate
	heal(statistics, held)
	snapshot := statistics.Snapshot()
	if got := snapshot.Encounters.Current.Healing; got.Effective != 0 || got.Events != 0 || snapshot.HealingNotLocalEvents != 3 {
		t.Fatalf("not local: %+v, not-local events %d", got, snapshot.HealingNotLocalEvents)
	}
}

func TestRepeatedHealingIsCountedOnceAndHPLossIsNeverHealing(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 1000, 10)
	repeated := healing(CombatFlowLocalInternal, 700, 700, 11, 1209)
	heal(statistics, repeated)
	heal(statistics, repeated)
	inconsistent := healing(CombatFlowLocalInternal, 300, 0, 12, 1209)
	inconsistent.HealingInconsistent = true
	heal(statistics, inconsistent)
	snapshot := statistics.Snapshot()
	got := snapshot.Encounters.Current.Healing
	if got.Effective != 700 || got.Events != 2 || snapshot.HealingDuplicateEvents != 1 || got.Status != DPSStatusLowerBound || got.Reason != "healing_result_lowered_hp" {
		t.Fatalf("repeat and HP loss: %+v, duplicates %d", got, snapshot.HealingDuplicateEvents)
	}
	if snapshot.DuplicateEvents != 0 {
		t.Fatalf("a repeated healing result was counted as a repeated damage result: %d", snapshot.DuplicateEvents)
	}
}

func TestHealingNeverOpensMovesOrEndsAnEncounter(t *testing.T) {
	run := func(withHealing bool) DamageStatisticsSnapshot {
		statistics := NewDamageStatistics()
		if withHealing {
			heal(statistics, healing(CombatFlowLocalInternal, 500, 500, 5, 1209)) // before any combat
		}
		outgoing(statistics, 1000, 10)
		if withHealing {
			heal(statistics, healing(CombatFlowLocalInternal, 500, 500, 15, 1209)) // during the fight
		}
		outgoing(statistics, 1000, 20)
		if withHealing {
			// After the last hit: the encounter ends at its last activity, so this is outside it.
			heal(statistics, healing(CombatFlowLocalInternal, 500, 500, 39, 1209))
		}
		statistics.observeCaptureTime(encounterAt(70))
		if withHealing {
			heal(statistics, healing(CombatFlowLocalInternal, 500, 500, 80, 1209)) // after the end
		}
		return statistics.Snapshot()
	}
	plain, healed := run(false), run(true)
	a, b := requireOneCompleted(t, plain), requireOneCompleted(t, healed)
	if !a.StartedAt.Equal(b.StartedAt) || !a.EndedAt.Equal(b.EndedAt) || a.EncounterDPS != b.EncounterDPS || a.PeakDPS != b.PeakDPS ||
		a.Totals != b.Totals || healed.DamageDealt != plain.DamageDealt || healed.Encounters.Current != nil {
		t.Fatalf("healing changed the encounter: %+v / %+v", a, b)
	}
	// Only the healing inside [start, end] belongs to the encounter; the interval counts all four.
	if b.Healing.Effective != 500 || b.Healing.Events != 1 || healed.Healing.Effective != 2000 {
		t.Fatalf("encounter healing %+v, interval %+v", b.Healing, healed.Healing)
	}
	// Healing alone opens no encounter.
	quiet := NewDamageStatistics()
	heal(quiet, healing(CombatFlowLocalInternal, 500, 500, 5, 1209))
	if snapshot := quiet.Snapshot(); snapshot.Encounters.Current != nil || len(snapshot.Encounters.Completed) != 0 || snapshot.Healing.Effective != 500 {
		t.Fatalf("healing alone: %+v", snapshot.Encounters)
	}
}

func TestHealingStaysWithItsEncounterAcrossEndsRestartsAndResets(t *testing.T) {
	statistics := NewDamageStatistics()
	outgoing(statistics, 1000, 10)
	heal(statistics, healing(CombatFlowLocalInternal, 400, 400, 11, 1209))
	outgoing(statistics, 1000, 12)
	statistics.observeCaptureTime(encounterAt(12 + encounterInactivityTimeout.Seconds() + 5))
	outgoing(statistics, 1000, 100)
	heal(statistics, healing(CombatFlowLocalInternal, 250, 250, 101, 1209))
	snapshot := statistics.Snapshot()
	if snapshot.Encounters.Completed[0].Healing.Effective != 400 || snapshot.Encounters.Current.Healing.Effective != 250 {
		t.Fatalf("two encounters: %+v / %+v", snapshot.Encounters.Completed[0].Healing, snapshot.Encounters.Current.Healing)
	}
	// An encounter cut by the end of the capture keeps its healing.
	statistics.finishCapture()
	if completed := statistics.Snapshot().Encounters.Completed; completed[1].Healing.Effective != 250 || completed[1].Outcome != EncounterOutcomeIncomplete {
		t.Fatalf("incomplete encounter: %+v", completed[1])
	}
	// A new statistics interval (a capture restart) starts without healing.
	statistics.Reset()
	if after := statistics.Snapshot(); after.Healing.Effective != 0 || after.Healing.Events != 0 {
		t.Fatalf("healing after a reset: %+v", after.Healing)
	}
	// An encounter without healing reports zero, available: no healing result was observed in it.
	outgoing(statistics, 1000, 300)
	if got := currentHealing(t, statistics); got.Status != DPSStatusAvailable || got.Effective != 0 || got.Events != 0 {
		t.Fatalf("encounter without healing: %+v", got)
	}
}

func TestHealingRecordTakesTheAppliedHPFromChangeLife(t *testing.T) {
	event := CombatEvent{
		Damage: 1200, HasSourceEntity: true, SourceEntityID: 1, HasTargetEntity: true, TargetEntityID: 2, EventTypeCandidate: "HEALING_CANDIDATE",
	}
	for _, c := range []struct {
		name         string
		has          bool
		changeLife   int32
		applied      int64
		inconsistent bool
	}{
		{"HP restored in full", true, -1200, 1200, false},
		{"partly overhealed", true, -500, 500, false},
		{"absent: target at full HP", false, 0, 0, false},
		{"HP lowered: not healing", true, 300, 0, true},
	} {
		event.HasChangeLife, event.ChangeLifeRaw = c.has, c.changeLife
		record, ok := normalizeCombatEvent(event, PlayerRelationLocal, PlayerRelationLocal)
		if !ok || record.Kind != CombatEventHealing || record.Flow != CombatFlowLocalInternal || record.Amount != 1200 ||
			record.AppliedHealing != c.applied || record.HealingInconsistent != c.inconsistent {
			t.Fatalf("%s: %+v", c.name, record)
		}
	}
	// Damage records never carry applied healing.
	event.EventTypeCandidate, event.HasChangeLife, event.ChangeLifeRaw = "DAMAGE_CANDIDATE", true, -100
	if record, _ := normalizeCombatEvent(event, PlayerRelationLocal, PlayerRelationUnrelated); record.AppliedHealing != 0 || record.HealingInconsistent {
		t.Fatalf("damage record: %+v", record)
	}
}

func TestHealingIsHeldBackWhileLocalAttributionIsSuspect(t *testing.T) {
	p := NewProcessor(nil, nil, nil, nil)
	record := healing(CombatFlowLocalInternal, 500, 500, 10, 1209)
	if got := p.gateHealing(record); got.ContextDowngrade != "" {
		t.Fatalf("healing held back without a suspect attribution: %+v", got)
	}
	p.Context.attribution.suspect = true
	if got := p.gateHealing(record); got.ContextDowngrade != contextDowngradeAttribution || isLocalHealing(got) {
		t.Fatalf("suspect attribution: %+v", got)
	}
	p.Context.observeClientReference(record.Source.EntityID, record.Target.EntityID)
	if got := p.gateHealing(record); got.ContextDowngrade != "" || p.Context.downgradedEvents != 0 {
		t.Fatalf("both entities referenced again: %+v (damage gate counter %d)", got, p.Context.downgradedEvents)
	}
}

func TestViewHealingKeepsTheReportedAndEffectiveFiguresApart(t *testing.T) {
	view := viewHealing(HealingResult{Status: DPSStatusAvailable, HealingTotals: HealingTotals{Effective: 3464, Reported: 12500, Overheal: 9036, Events: 7}})
	if view.Reported != 12500 || view.Effective != 3464 || view.Overheal != 9036 || view.Events != 7 || view.Status != DPSStatusAvailable {
		t.Fatalf("view = %+v", view)
	}
}
