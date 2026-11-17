package capture

import (
	"reflect"
	"testing"
	"time"
)

func infinitePolicy() *int64 {
	policy := int64(1)
	return &policy
}

func finitePolicy() *int64 {
	policy := int64(2)
	return &policy
}

// passiveApply is a permanent modifier without a HUD cue on local role 10.
func passiveApply(handle, effectID uint64, seconds float64, mutate func(*EffectApplyEvent)) EffectApplyEvent {
	return displayApply(handle, effectID, seconds, func(e *EffectApplyEvent) {
		e.HolderRelation = EffectRelationLocalRole
		e.DurationSeconds, e.RemainingSeconds = nil, nil
		e.DefinitionLinked, e.Static = true, &EffectStaticInfo{DurationPolicy: infinitePolicy()}
		if mutate != nil {
			mutate(e)
		}
	})
}

func TestViewEffectsCountPermanentAndMonsterEffectsWithoutAHUDCueWithoutListingThem(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(passiveApply(1, 500, 0, nil))
	p.Effects.ObserveApply(passiveApply(2, 501, 0, func(e *EffectApplyEvent) {
		e.Static = &EffectStaticInfo{DurationPolicy: infinitePolicy(), HUDCueTypes: []int32{2}, HUDPolarity: EffectPolarityBuff}
	}))
	// A monster's effect on the team that no buff bar can draw: only its combat context is known.
	p.Effects.ObserveApply(displayApply(3, 502, 0, func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation = EffectRelationLocalRole, EffectRelationNonPlayer
		e.CombatClass, e.CombatBasis = EffectCombatClassUnknown, effectCombatBasisNoEvidence
		e.HolderMonster, e.SourceMonster = false, true
		e.Static = noHUDStatic()
	}))
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 3, Active: 3, Local: 3, NonHUD: 2, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	if len(view.Active) != 1 || *view.Active[0].EffectID != 501 || view.Active[0].Polarity != EffectPolarityBuff {
		t.Fatalf("listed = %+v", view.Active)
	}
	// Leaving an instance out does not end it: it stays tracked with its raw identity.
	if snapshot := p.Effects.SnapshotAt(effectQuery(1)); len(snapshot.Effects) != 3 {
		t.Fatalf("tracked instances = %d", len(snapshot.Effects))
	}
}

func TestViewEffectsPredictedExpiryNeverDeletesAndARefreshBringsItBack(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(displayApply(1, 500, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(10), effectFloat(10) }))

	// Past the expiry but inside the grace it is still shown, without a remaining time.
	view := effectViewAt(p, 11)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	if view.Active[0].RemainingStatus != EffectRemainingOverdue {
		t.Fatalf("status inside grace = %s", view.Active[0].RemainingStatus)
	}
	// Beyond the grace it is no longer shown as active, but the tracker keeps it: no REMOVE was seen.
	view = effectViewAt(p, 10+EffectDisplayExpiryGrace.Seconds()+0.5)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, PredictedExpired: 1})
	if snapshot := p.Effects.SnapshotAt(effectQuery(13)); len(snapshot.Effects) != 1 {
		t.Fatalf("a predicted expiry deleted the instance: %d", len(snapshot.Effects))
	}
	// The game refreshes it: the same instance is shown again with a real time.
	p.Effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, RemainingSeconds: effectFloat(10), DurationSeconds: effectFloat(10), ObservedAt: effectAt(13)})
	view = effectViewAt(p, 13)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	requireNear(t, "remaining after refresh", view.Active[0].RemainingSeconds, 10)
	// A later REMOVE still ends it exactly once.
	p.Effects.ObserveRemove(testEffectRemove(1, 14))
	requireEffectCounts(t, effectViewAt(p, 14), ViewEffectCounts{})
}

// teamCopy is one team member's instance of a timed HUD status, optionally stating an element.
func teamCopy(handle, effectID, holder uint64, seconds float64, element uint64) EffectApplyEvent {
	return displayApply(handle, effectID, seconds, func(e *EffectApplyEvent) {
		e.HolderEntityID, e.HolderRelation = holder, EffectRelationLocalRole
		e.DurationSeconds, e.RemainingSeconds = effectFloat(20), effectFloat(20)
		static := hudStatic()
		if element != 0 {
			static.ElementID, static.ElementBasis = element, effectElementBasisAttribute
		}
		e.DefinitionLinked, e.Static = true, static
	})
}

func TestViewEffectsOneCellForTheSameEffectOnTheTeam(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11, 12} {
		p.Effects.ObserveApply(teamCopy(uint64(handle+1), 500, holder, 0, 0))
	}
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 3, Active: 3, Local: 3, Eligible: 3, Listed: 3, Cells: 1, ListedCells: 1})
	if view.Active[0].Instances != 3 || view.Active[0].HolderRoleID != nil {
		t.Fatalf("cell = %+v", view.Active[0])
	}
}

func TestViewEffectsKeepSeparateTimingsAndStacksApart(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(teamCopy(1, 500, 10, 0, 0))
	// Applied half a second later: it ends at a different time.
	p.Effects.ObserveApply(teamCopy(2, 500, 11, 0.5, 0))
	third := teamCopy(3, 500, 12, 0, 0)
	third.StackCount = 3
	p.Effects.ObserveApply(third)
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 3, Active: 3, Local: 3, Eligible: 3, Listed: 3, Cells: 3, ListedCells: 3})
}

func TestEffectDisplayCellsAreIndependentOfInputOrder(t *testing.T) {
	p := newEffectViewProcessor()
	handle := uint64(1)
	for _, holder := range []uint64{10, 11} {
		for element := uint64(1); element <= 6; element++ {
			p.Effects.ObserveApply(teamCopy(handle, 700+element, holder, 0, element))
			handle++
		}
		p.Effects.ObserveApply(teamCopy(handle, 900, holder, 0, 0))
		handle++
	}
	snapshot := p.Effects.SnapshotAt(effectQuery(1))
	reversed := append([]EffectInstanceSnapshot(nil), snapshot.Effects...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	keys := func(cells []EffectDisplayCell) map[string]int {
		out := map[string]int{}
		for _, cell := range cells {
			out[EffectViewKey(cell.Representative)] = cell.Instances()
		}
		return out
	}
	if a, b := keys(EffectDisplayCells(snapshot.Effects)), keys(EffectDisplayCells(reversed)); !reflect.DeepEqual(a, b) || len(a) != 7 {
		t.Fatalf("cells depend on input order: %v vs %v", a, b)
	}
}

func TestViewEffectsClockStallsOnlyAtTheLiveLimit(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(displayApply(1, 500, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(30), effectFloat(30) }))
	p.CurrentFrameTime = effectAt(1)

	// A replay reads capture time: no lead, no stall, and the remaining time is exactly the capture clock's.
	view := p.viewEffects()
	if view.ClockStalled {
		t.Fatal("a replay reported a stalled clock")
	}
	requireNear(t, "replay remaining", view.Active[0].RemainingSeconds, 29)

	// A live quiet gap keeps the countdown running.
	p.ViewClockLead = 3 * time.Second
	view = p.viewEffects()
	if view.ClockStalled {
		t.Fatal("a normal quiet gap reported a stalled clock")
	}
	requireNear(t, "live remaining", view.Active[0].RemainingSeconds, 26)

	// At the limit the capture no longer knows what the game is doing.
	p.ViewClockLead, p.ViewClockStalled = LiveFrameSilenceLimit, true
	if view = p.viewEffects(); !view.ClockStalled {
		t.Fatal("the limit did not report a stalled clock")
	}
	// Querying with a lead changes nothing in the tracker: the capture-time view is unchanged.
	p.ViewClockLead, p.ViewClockStalled = 0, false
	requireNear(t, "capture time after a lead", p.viewEffects().Active[0].RemainingSeconds, 29)
}
