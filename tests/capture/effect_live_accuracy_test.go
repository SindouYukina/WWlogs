package capture

import (
	"reflect"
	"testing"
	"time"
)

func infinitePolicy() *int64 {
	policy := int64(effectDurationPolicyInfinite)
	return &policy
}

func finitePolicy() *int64 {
	policy := int64(2)
	return &policy
}

// passiveApply is a permanent stat modifier from a scene snapshot on local role 10.
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

func TestEffectIsPassive(t *testing.T) {
	seconds := 5.0
	cases := []struct {
		name   string
		effect EffectInstanceSnapshot
		want   bool
	}{
		{"permanent modifier", EffectInstanceSnapshot{Kind: EffectKindStatModifier, Static: &EffectStaticInfo{DurationPolicy: infinitePolicy()}}, true},
		{"snapshot buff marker", EffectInstanceSnapshot{Kind: EffectKindBuffMarker, Origin: EffectOriginSnapshot, Static: &EffectStaticInfo{DurationPolicy: infinitePolicy()}}, true},
		{"marker applied in combat", EffectInstanceSnapshot{Kind: EffectKindBuffMarker, Origin: EffectOriginApply, Static: &EffectStaticInfo{DurationPolicy: infinitePolicy()}}, false},
		{"HUD icon means the game shows it", EffectInstanceSnapshot{Kind: EffectKindStatModifier, Static: &EffectStaticInfo{DurationPolicy: infinitePolicy(), HUDCueTypes: []int32{2}}}, false},
		{"a reported duration is a timer", EffectInstanceSnapshot{Kind: EffectKindStatModifier, ServerDurationSeconds: &seconds, Static: &EffectStaticInfo{DurationPolicy: infinitePolicy()}}, false},
		{"finite by definition", EffectInstanceSnapshot{Kind: EffectKindStatModifier, Static: &EffectStaticInfo{DurationPolicy: finitePolicy()}}, false},
		{"no definition proves nothing", EffectInstanceSnapshot{Kind: EffectKindStatModifier}, false},
		{"other kinds are not passive", EffectInstanceSnapshot{Kind: EffectKindCombatState, Static: &EffectStaticInfo{DurationPolicy: infinitePolicy()}}, false},
	}
	for _, tc := range cases {
		if got := effectIsPassive(tc.effect); got != tc.want {
			t.Errorf("%s: passive = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEffectIsMaintained(t *testing.T) {
	duration := func(value float64) *float64 { return &value }
	cases := []struct {
		name   string
		effect EffectInstanceSnapshot
		want   bool
	}{
		{"refresh-period modifier", EffectInstanceSnapshot{Kind: EffectKindStatModifier, ServerDurationSeconds: duration(1.2)}, true},
		{"the limit is inclusive", EffectInstanceSnapshot{Kind: EffectKindStatModifier, ServerDurationSeconds: duration(effectMaintainedDurationLimit)}, true},
		{"a longer modifier is timed", EffectInstanceSnapshot{Kind: EffectKindStatModifier, ServerDurationSeconds: duration(2.5)}, false},
		{"a HUD icon keeps the countdown", EffectInstanceSnapshot{Kind: EffectKindStatModifier, ServerDurationSeconds: duration(1.2), Static: &EffectStaticInfo{HUDCueTypes: []int32{2}}}, false},
		{"only modifiers", EffectInstanceSnapshot{Kind: EffectKindBuffMarker, ServerDurationSeconds: duration(1.2)}, false},
		{"no duration is not maintained", EffectInstanceSnapshot{Kind: EffectKindStatModifier}, false},
	}
	for _, tc := range cases {
		if got := effectIsMaintained(tc.effect); got != tc.want {
			t.Errorf("%s: maintained = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestViewEffectsCountPassiveAndUnknownMeaningWithoutListingThem(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(passiveApply(1, 500, 0, nil))
	p.effects.ObserveApply(passiveApply(2, 501, 0, func(e *EffectApplyEvent) {
		e.Static = &EffectStaticInfo{DurationPolicy: infinitePolicy(), HUDCueTypes: []int32{2}}
	}))
	// A monster's effect on the team with no definition evidence: only its combat context is known.
	p.effects.ObserveApply(displayApply(3, 502, 0, func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation = EffectRelationLocalRole, EffectRelationNonPlayer
		e.CombatClass, e.CombatBasis = EffectCombatClassUnknown, effectCombatBasisNoEvidence
		e.HolderMonster, e.SourceMonster = false, true
	}))
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 3, Active: 3, Local: 3, Unclassified: 1, Passive: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	if len(view.Active) != 1 || *view.Active[0].EffectID != 501 {
		t.Fatalf("listed = %+v", view.Active)
	}
	// Counting a passive instance does not end it: it stays tracked with its raw identity.
	if snapshot := p.effects.SnapshotAt(effectQuery(1)); len(snapshot.Effects) != 3 {
		t.Fatalf("tracked instances = %d", len(snapshot.Effects))
	}
}

func TestViewEffectsPredictedExpiryNeverDeletesAndARefreshBringsItBack(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(displayApply(1, 500, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(10), effectFloat(10) }))

	// Past the expiry but inside the grace it is still shown, without a remaining time.
	view := effectViewAt(p, 11)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	if view.Active[0].RemainingStatus != EffectRemainingOverdue {
		t.Fatalf("status inside grace = %s", view.Active[0].RemainingStatus)
	}
	// Beyond the grace it is no longer shown as active, but the tracker keeps it: no REMOVE was seen.
	view = effectViewAt(p, 10+effectDisplayExpiryGrace.Seconds()+0.5)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, PredictedExpired: 1})
	if snapshot := p.effects.SnapshotAt(effectQuery(13)); len(snapshot.Effects) != 1 {
		t.Fatalf("a predicted expiry deleted the instance: %d", len(snapshot.Effects))
	}
	// The game refreshes it: the same instance is shown again with a real time.
	p.effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, RemainingSeconds: effectFloat(10), DurationSeconds: effectFloat(10), ObservedAt: effectAt(13)})
	view = effectViewAt(p, 13)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	requireNear(t, "remaining after refresh", view.Active[0].RemainingSeconds, 10)
	// A later REMOVE still ends it exactly once.
	p.effects.ObserveRemove(testEffectRemove(1, 14))
	requireEffectCounts(t, effectViewAt(p, 14), ViewEffectCounts{})
}

// teamCopy is one team member's instance of a timed modifier of the given attribute family element.
func teamCopy(handle, effectID, holder uint64, seconds float64, element uint64) EffectApplyEvent {
	return displayApply(handle, effectID, seconds, func(e *EffectApplyEvent) {
		e.HolderEntityID, e.HolderRelation = holder, EffectRelationLocalRole
		e.DurationSeconds, e.RemainingSeconds = effectFloat(20), effectFloat(20)
		static := &EffectStaticInfo{DurationPolicy: finitePolicy()}
		if element != 0 {
			static.ElementID, static.ElementBasis, static.ElementFamily = element, effectElementBasisAttribute, 22
		}
		e.DefinitionLinked, e.Static = true, static
	})
}

func TestViewEffectsOneCellForTheSameEffectOnTheTeam(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11, 12} {
		p.effects.ObserveApply(teamCopy(uint64(handle+1), 500, holder, 0, 0))
	}
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 3, Active: 3, Local: 3, Eligible: 3, Listed: 3, Cells: 1, ListedCells: 1})
	if view.Active[0].Instances != 3 || view.Active[0].HolderRoleID != nil {
		t.Fatalf("cell = %+v", view.Active[0])
	}
}

func TestViewEffectsKeepSeparateTimingsAndStacksApart(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(teamCopy(1, 500, 10, 0, 0))
	// Applied half a second later: it ends at a different time.
	p.effects.ObserveApply(teamCopy(2, 500, 11, 0.5, 0))
	third := teamCopy(3, 500, 12, 0, 0)
	third.StackCount = 3
	p.effects.ObserveApply(third)
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 3, Active: 3, Local: 3, Eligible: 3, Listed: 3, Cells: 3, ListedCells: 3})
}

func TestViewEffectsOneCellForACompleteElementFanOut(t *testing.T) {
	holders := []uint64{10, 11, 12}
	apply := func(p *processor, elements []uint64) {
		handle := uint64(1)
		for _, holder := range holders {
			for _, element := range elements {
				p.effects.ObserveApply(teamCopy(handle, 700+element, holder, 0, element))
				handle++
			}
		}
	}
	p := newEffectViewProcessor()
	apply(p, []uint64{1, 2, 3, 4, 5, 6})
	view := effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 18, Active: 18, Local: 18, Eligible: 18, Listed: 18, Cells: 1, ListedCells: 1})
	if view.Active[0].Instances != 18 || view.Active[0].ElementID != nil {
		t.Fatalf("fan-out cell = %+v", view.Active[0])
	}

	// Five of six elements are five bonuses, not one.
	p = newEffectViewProcessor()
	apply(p, []uint64{1, 2, 3, 4, 5})
	view = effectViewAt(p, 1)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 15, Active: 15, Local: 15, Eligible: 15, Listed: 15, Cells: 5, ListedCells: 5})
	for _, cell := range view.Active {
		if cell.ElementID == nil {
			t.Fatalf("a separate element bonus lost its element: %+v", cell)
		}
	}
}

func TestViewEffectsFanOutRequiresTheSameHoldersAndEnd(t *testing.T) {
	p := newEffectViewProcessor()
	for element := uint64(1); element <= 6; element++ {
		seconds := 0.0
		if element == 6 {
			seconds = 1
		}
		p.effects.ObserveApply(teamCopy(element, 700+element, 10, seconds, element))
	}
	view := effectViewAt(p, 2)
	if view.Counts.Cells != 6 {
		t.Fatalf("one element ending later must not merge: cells = %d", view.Counts.Cells)
	}
	p = newEffectViewProcessor()
	for element := uint64(1); element <= 6; element++ {
		holder := uint64(10)
		if element == 1 {
			holder = 11
		}
		p.effects.ObserveApply(teamCopy(element, 700+element, holder, 0, element))
	}
	if view := effectViewAt(p, 1); view.Counts.Cells != 6 {
		t.Fatalf("different holders must not merge: cells = %d", view.Counts.Cells)
	}
}

func TestEffectDisplayCellsAreIndependentOfInputOrder(t *testing.T) {
	p := newEffectViewProcessor()
	handle := uint64(1)
	for _, holder := range []uint64{10, 11} {
		for element := uint64(1); element <= 6; element++ {
			p.effects.ObserveApply(teamCopy(handle, 700+element, holder, 0, element))
			handle++
		}
		p.effects.ObserveApply(teamCopy(handle, 900, holder, 0, 0))
		handle++
	}
	snapshot := p.effects.SnapshotAt(effectQuery(1))
	reversed := append([]EffectInstanceSnapshot(nil), snapshot.Effects...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	keys := func(cells []effectDisplayCell) map[string]int {
		out := map[string]int{}
		for _, cell := range cells {
			out[effectViewKey(cell.representative)] = cell.instances()
		}
		return out
	}
	if a, b := keys(effectDisplayCells(snapshot.Effects)), keys(effectDisplayCells(reversed)); !reflect.DeepEqual(a, b) || len(a) != 2 {
		t.Fatalf("cells depend on input order: %v vs %v", a, b)
	}
}

func TestViewEffectsMaintainedFlagFollowsTheRepresentative(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(displayApply(1, 500, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(1.2), effectFloat(1.2) }))
	p.effects.ObserveApply(displayApply(2, 501, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(15), effectFloat(15) }))
	view := effectViewAt(p, 0.5)
	if len(view.Active) != 2 {
		t.Fatalf("listed = %d", len(view.Active))
	}
	for _, cell := range view.Active {
		if want := *cell.EffectID == 500; cell.Maintained != want {
			t.Fatalf("effect %d maintained = %v, want %v", *cell.EffectID, cell.Maintained, want)
		}
	}
}

func TestLiveViewClockLeadIsBoundedAndNeverRunsWithoutAFrame(t *testing.T) {
	base := effectAt(0)
	cases := []struct {
		name string
		wall time.Time
		now  time.Time
		want time.Duration
	}{
		{"before the first frame", time.Time{}, base, 0},
		{"just after a frame", base, base.Add(200 * time.Millisecond), 200 * time.Millisecond},
		{"at the limit", base, base.Add(liveViewClockLeadLimit), liveViewClockLeadLimit},
		{"long after the last frame", base, base.Add(time.Minute), liveViewClockLeadLimit},
		{"a clock that went backwards", base, base.Add(-time.Second), 0},
	}
	for _, tc := range cases {
		if got := liveViewClockLead(tc.wall, tc.now); got != tc.want {
			t.Errorf("%s: lead = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestViewEffectsClockStallsOnlyAtTheLiveLimit(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(displayApply(1, 500, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(30), effectFloat(30) }))
	p.currentFrameTime = effectAt(1)

	// A replay reads capture time: no lead, no stall, and the remaining time is exactly the capture clock's.
	view := p.viewEffects()
	if view.ClockStalled {
		t.Fatal("a replay reported a stalled clock")
	}
	requireNear(t, "replay remaining", view.Active[0].RemainingSeconds, 29)

	// A live quiet gap keeps the countdown running.
	p.viewClockLead = 3 * time.Second
	view = p.viewEffects()
	if view.ClockStalled {
		t.Fatal("a normal quiet gap reported a stalled clock")
	}
	requireNear(t, "live remaining", view.Active[0].RemainingSeconds, 26)

	// At the limit the capture no longer knows what the game is doing.
	p.viewClockLead = liveViewClockLeadLimit
	if view = p.viewEffects(); !view.ClockStalled {
		t.Fatal("the limit did not report a stalled clock")
	}
	// Querying with a lead changes nothing in the tracker: the capture-time view is unchanged.
	p.viewClockLead = 0
	requireNear(t, "capture time after a lead", p.viewEffects().Active[0].RemainingSeconds, 29)
}
