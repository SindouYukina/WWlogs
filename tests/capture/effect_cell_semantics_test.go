package capture

import (
	"reflect"
	"testing"
)

// cellInstance is one team member's timed HUD status. Its expiry is seconds after applied at remaining; every other
// field is the same across instances unless mutate changes it.
func cellInstance(handle, effectID, holder uint64, applied, remaining float64, mutate func(*EffectApplyEvent)) EffectApplyEvent {
	return displayApply(handle, effectID, applied, func(e *EffectApplyEvent) {
		e.HolderEntityID, e.HolderRelation = holder, EffectRelationLocalRole
		e.DurationSeconds, e.RemainingSeconds = effectFloat(20), effectFloat(remaining)
		e.DefinitionLinked, e.Static = true, hudStatic()
		if mutate != nil {
			mutate(e)
		}
	})
}

func oneCell(t *testing.T, view ViewEffects) ViewEffect {
	t.Helper()
	if len(view.Active) != 1 {
		t.Fatalf("cells = %d, want 1: %+v", len(view.Active), view.Active)
	}
	return view.Active[0]
}

func cellRemainings(view ViewEffects) []float64 {
	var out []float64
	for _, cell := range view.Active {
		out = append(out, *cell.RemainingSeconds)
	}
	return out
}

func TestEffectCellSameEffectOnSeveralHoldersIsOneCellWithoutSummingStacks(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11, 12} {
		p.effects.ObserveApply(cellInstance(uint64(handle+1), 500, holder, 0, 15, nil))
	}
	cell := oneCell(t, effectViewAt(p, 1))
	if cell.Instances != 3 || cell.StackCount == nil || *cell.StackCount != 1 {
		t.Fatalf("three holders with one stack each read as %+v; stacks must never add up across holders", cell)
	}
	if cell.HolderRoleID != nil {
		t.Fatalf("a merged cell named one holder: %+v", cell)
	}
}

func TestEffectCellKeepsTeamAndEnemyApart(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 500, 10, 0, 15, nil))
	p.effects.ObserveApply(cellInstance(2, 500, 30, 0, 15, func(e *EffectApplyEvent) { e.HolderRelation = EffectRelationNonPlayer }))
	view := effectViewAt(p, 1)
	if view.Counts.Cells != 2 {
		t.Fatalf("the same effect on the team and on an enemy shared a cell: %+v", view.Active)
	}
}

func TestEffectCellSourceDoesNotSplitHoldersButTwoInstancesOnOneHolderStayApart(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 500, 10, 0, 15, func(e *EffectApplyEvent) { e.SourceEntityID = 10 }))
	p.effects.ObserveApply(cellInstance(2, 500, 11, 0, 15, func(e *EffectApplyEvent) { e.SourceEntityID = 11 }))
	if cell := oneCell(t, effectViewAt(p, 1)); cell.Instances != 2 {
		t.Fatalf("each holder's own copy = %+v", cell)
	}
	p = newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 500, 10, 0, 15, func(e *EffectApplyEvent) { e.SourceEntityID = 20 }))
	p.effects.ObserveApply(cellInstance(2, 500, 10, 0, 15, func(e *EffectApplyEvent) { e.SourceEntityID = 21 }))
	if view := effectViewAt(p, 1); view.Counts.Cells != 2 {
		t.Fatalf("two independent instances on one holder shared a cell: %+v", view.Active)
	}
}

func TestEffectCellDifferentDurationsKeepTheirOwnProgress(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 500, 10, 0, 10, func(e *EffectApplyEvent) { e.DurationSeconds = effectFloat(20) }))
	p.effects.ObserveApply(cellInstance(2, 500, 11, 0, 10, func(e *EffectApplyEvent) { e.DurationSeconds = effectFloat(10) }))
	if view := effectViewAt(p, 1); view.Counts.Cells != 2 {
		t.Fatalf("a 10 s and a 20 s effect ending together shared one progress: %+v", view.Active)
	}
}

func TestEffectCellDifferentRemainingTimesStayApartAndSoonestComesFirst(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 500, 12, 0, 12, nil))
	p.effects.ObserveApply(cellInstance(2, 500, 11, 0, 7, nil))
	p.effects.ObserveApply(cellInstance(3, 500, 10, 0, 2, nil))
	view := effectViewAt(p, 0)
	if !reflect.DeepEqual(cellRemainings(view), []float64{2, 7, 12}) {
		t.Fatalf("remaining = %v, want each holder's own time with the soonest first", cellRemainings(view))
	}
}

func TestEffectCellDifferentStacksStayApart(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 500, 10, 0, 15, nil))
	p.effects.ObserveApply(cellInstance(2, 500, 11, 0, 15, func(e *EffectApplyEvent) { e.StackCount = 3 }))
	view := effectViewAt(p, 1)
	if view.Counts.Cells != 2 {
		t.Fatalf("different stacks shared a cell: %+v", view.Active)
	}
	got := map[uint64]bool{}
	for _, cell := range view.Active {
		got[*cell.StackCount] = true
	}
	if !got[1] || !got[3] {
		t.Fatalf("stacks = %v", got)
	}
}

func TestEffectCellExpiryToleranceIsMeasuredFromTheEarliestMember(t *testing.T) {
	p := newEffectViewProcessor()
	// Each is 90 ms after the previous: a chained comparison would put all three in one cell, 180 ms apart.
	p.effects.ObserveApply(cellInstance(1, 500, 10, 0, 10, nil))
	p.effects.ObserveApply(cellInstance(2, 500, 11, 0.09, 10, nil))
	p.effects.ObserveApply(cellInstance(3, 500, 12, 0.18, 10, nil))
	view := effectViewAt(p, 1)
	if view.Counts.Cells != 2 {
		t.Fatalf("cells = %d, want 2 (within 100 ms of the earliest, then the rest)", view.Counts.Cells)
	}
	requireNear(t, "countdown of the merged cell", view.Active[0].RemainingSeconds, 9)
}

func TestEffectCellCountdownNeverReadsLaterThanItsFirstMember(t *testing.T) {
	p := newEffectViewProcessor()
	// Whichever member happens to carry the cell's identity, the time shown is the earliest end.
	for handle := uint64(1); handle <= 8; handle++ {
		p.effects.ObserveApply(cellInstance(handle, 500, 9+handle, float64(handle-1)*0.01, 10, nil))
	}
	cell := oneCell(t, effectViewAt(p, 1))
	requireNear(t, "countdown", cell.RemainingSeconds, 9)
	if want := effectAt(10); cell.ExpectedExpiry == nil || !cell.ExpectedExpiry.Equal(want) {
		t.Fatalf("expected expiry = %v, want %v", cell.ExpectedExpiry, want)
	}
}

func TestEffectCellMemberEndingLeavesTheOthersWithTheirTime(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11, 12} {
		p.effects.ObserveApply(cellInstance(uint64(handle+1), 500, holder, 0, 15, nil))
	}
	remove := testEffectRemove(1, 3)
	p.effects.ObserveRemove(remove)
	cell := oneCell(t, effectViewAt(p, 3))
	if cell.Instances != 2 {
		t.Fatalf("after one REMOVE instances = %d", cell.Instances)
	}
	requireNear(t, "countdown after a partial end", cell.RemainingSeconds, 12)
}

func TestEffectCellRefreshingOneMemberSplitsItsNewTimeOut(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11, 12} {
		p.effects.ObserveApply(cellInstance(uint64(handle+1), 500, holder, 0, 15, nil))
	}
	p.effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 11, HandleID: 2, RemainingSeconds: effectFloat(15), DurationSeconds: effectFloat(20), ObservedAt: effectAt(5)})
	view := effectViewAt(p, 5)
	if !reflect.DeepEqual(cellRemainings(view), []float64{10, 15}) || view.Active[0].Instances != 2 || view.Active[1].Instances != 1 {
		t.Fatalf("after refreshing one member: %v, %+v", cellRemainings(view), view.Active)
	}
}

func TestEffectCellDisappearsOnlyAfterEveryMemberIsPastTheGrace(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11} {
		p.effects.ObserveApply(cellInstance(uint64(handle+1), 500, holder, 0, 10, nil))
	}
	requireEffectCounts(t, effectViewAt(p, 11), ViewEffectCounts{Tracked: 2, Active: 2, Local: 2, Eligible: 2, Listed: 2, Cells: 1, ListedCells: 1})
	requireEffectCounts(t, effectViewAt(p, 10+effectDisplayExpiryGrace.Seconds()+0.5), ViewEffectCounts{Tracked: 2, Active: 2, Local: 2, PredictedExpired: 2})
}

func TestEffectCellStaleAndFreshInstancesStayApart(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 500, 10, 0, 15, nil))
	p.effects.MarkTransportLoss(effectAt(1))
	p.effects.ObserveApply(cellInstance(2, 500, 11, 2, 13, nil))
	view := effectViewAt(p, 3)
	if view.Counts.Cells != 2 {
		t.Fatalf("an instance the transport loss left unconfirmed shared a cell with a fresh one: %+v", view.Active)
	}
	stale := 0
	for _, cell := range view.Active {
		if cell.Stale {
			stale++
		}
	}
	if stale != 1 {
		t.Fatalf("stale cells = %d", stale)
	}
}

func TestEffectCellOnlyVerifiedHUDStatusesBecomeCells(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(cellInstance(1, 510, 10, 0, 15, nil))
	// Each reason an instance stays out: no HUD cue, no definition, and a cue the game draws without an arrow.
	p.effects.ObserveApply(cellInstance(2, 511, 10, 0, 15, func(e *EffectApplyEvent) { e.Static = noHUDStatic() }))
	p.effects.ObserveApply(cellInstance(3, 512, 10, 0, 15, func(e *EffectApplyEvent) { e.DefinitionLinked, e.Static = false, nil }))
	p.effects.ObserveApply(cellInstance(4, 513, 10, 0, 15, func(e *EffectApplyEvent) {
		e.Static = &EffectStaticInfo{DurationPolicy: finitePolicy(), HUDCueTypes: []int32{24}}
	}))
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 4, Active: 4, Local: 4, NonHUD: 1, HUDUnresolved: 2, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	if !view.Active[0].HUDIcon || *view.Active[0].EffectID != 510 || view.Active[0].Polarity != EffectPolarityDebuff {
		t.Fatalf("listed cell = %+v", view.Active[0])
	}
}

func TestEffectCellsAreDeterministicAcrossInputOrderAndRepetition(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11, 12} {
		p.effects.ObserveApply(cellInstance(uint64(handle+1), 500, holder, float64(handle)*0.04, 15, nil))
	}
	p.effects.ObserveApply(cellInstance(4, 500, 13, 3, 15, nil))
	snapshot := p.effects.SnapshotAt(effectQuery(4))
	signature := func(effects []EffectInstanceSnapshot) map[string]int {
		out := map[string]int{}
		for _, cell := range effectDisplayCells(append([]EffectInstanceSnapshot(nil), effects...)) {
			out[effectViewKey(cell.representative)+"/"+effectViewKey(cell.earliest)] = cell.instances()
		}
		return out
	}
	want := signature(snapshot.Effects)
	reversed := append([]EffectInstanceSnapshot(nil), snapshot.Effects...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	for attempt := 0; attempt < 3; attempt++ {
		if got := signature(reversed); !reflect.DeepEqual(got, want) {
			t.Fatalf("attempt %d: cells depend on order: %v vs %v", attempt, got, want)
		}
	}
	if len(want) != 2 {
		t.Fatalf("cells = %v", want)
	}
}
