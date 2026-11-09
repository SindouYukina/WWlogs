package capture

import (
	"io"
	"testing"
)

// newEffectViewProcessor returns a processor whose view reads its own tracker.
func newEffectViewProcessor() *processor {
	return newProcessor(nil, io.Discard, io.Discard, nil)
}

func effectViewAt(p *processor, seconds float64) ViewEffects {
	p.currentFrameTime = effectAt(seconds)
	return p.viewEffects()
}

// displayApply is a stat-modifier effect on enemy holder 10 from local role 20, on handle and effect id.
func displayApply(handle, effectID uint64, seconds float64, mutate func(*EffectApplyEvent)) EffectApplyEvent {
	return testEffectApply(handle, seconds, func(event *EffectApplyEvent) {
		event.EffectID = effectID
		event.CombatClass, event.CombatBasis = EffectCombatClassCombat, effectCombatBasisAttribute+"Atk"
		if mutate != nil {
			mutate(event)
		}
	})
}

func requireEffectCounts(t *testing.T, view ViewEffects, want ViewEffectCounts) {
	t.Helper()
	if err := view.Counts.accountingError(len(view.Active)); err != nil {
		t.Fatalf("counts %+v: %v", view.Counts, err)
	}
	if view.Counts != want {
		t.Fatalf("counts = %+v, want %+v", view.Counts, want)
	}
}

func TestClassifyEffectKind(t *testing.T) {
	tags := func(values ...string) *EffectStaticInfo { return &EffectStaticInfo{DeveloperTags: values} }
	cases := []struct {
		name   string
		class  EffectCombatClass
		basis  string
		static *EffectStaticInfo
		want   EffectKind
	}{
		{"ailment term", EffectCombatClassCombat, effectCombatBasisTerm, tags("战斗状态.异常状态.基础元素异常.风蚀效应"), EffectKindElementAilment},
		{"combat attribute without name or icon", EffectCombatClassCombat, effectCombatBasisAttribute + "CritDamage", nil, EffectKindStatModifier},
		{"an internal tag wins over an attribute", EffectCombatClassCombat, effectCombatBasisAttribute + "DamageReduce", tags("功能.逻辑状态标识.霸体"), EffectKindInternalState},
		{"a HUD cue keeps an attribute modifier with an internal tag", EffectCombatClassCombat, effectCombatBasisAttribute + "Atk", &EffectStaticInfo{DeveloperTags: []string{"角色.x.状态标识.y"}, HUDCueTypes: []int32{2}}, EffectKindStatModifier},
		{"an unrelated tag leaves an attribute modifier", EffectCombatClassCombat, effectCombatBasisAttribute + "DamageChangeElement1", tags("装备.武器.8d690dd3.5阶"), EffectKindStatModifier},
		{"role buff marker", EffectCombatClassCombat, effectCombatBasisTag + "buff标识", tags("角色.R2T1ShouanrenMd10011.buff标识.退场技.buff1"), EffectKindBuffMarker},
		{"buff marker wins over a function flag", EffectCombatClassCombat, effectCombatBasisTag + "QTE", tags("功能.功能制作.QTE.激活QTE", "角色.Common.BUFF通用标识.协奏充能完成"), EffectKindBuffMarker},
		{"buff marker without combat class", EffectCombatClassUnknown, effectCombatBasisNoEvidence, tags("角色.Common.buff标识.x"), EffectKindBuffMarker},
		{"dodge invulnerability flag", EffectCombatClassCombat, effectCombatBasisTag + "无敌", tags("功能.逻辑状态标识.无敌.角色闪避"), EffectKindInternalState},
		{"super armour flag", EffectCombatClassCombat, effectCombatBasisTag + "霸体", tags("功能.逻辑状态标识.霸体"), EffectKindInternalState},
		{"skill restriction", EffectCombatClassCombat, effectCombatBasisTag + "战斗状态", tags("战斗状态.技能限制.禁止大招"), EffectKindInternalState},
		{"aggro flag", EffectCombatClassCombat, effectCombatBasisTag + "战斗状态", tags("战斗状态.AI仇恨.不引发仇恨"), EffectKindInternalState},
		{"role state flag", EffectCombatClassUnknown, effectCombatBasisNoEvidence, tags("角色.R2T1SanhuaMd10011.状态标识.蓄力状态中"), EffectKindInternalState},
		{"camera on the team", EffectCombatClassCombat, effectCombatBasisMonsterOnLocal, tags("怪物.ML1QiShiMd00001.常态镜头"), EffectKindInternalState},
		{"hashed segments do not match", EffectCombatClassUnknown, effectCombatBasisNoEvidence, tags("角色.dc777977.fd108eb0.83569a6a"), EffectKindUnknown},
		{"non-combat wins over its family", EffectCombatClassNonCombat, effectCombatBasisTag + "跌落无伤", tags("功能.逻辑状态标识.跌落无伤"), EffectKindNonCombat},
		{"other combat word", EffectCombatClassCombat, effectCombatBasisTag + "异常状态", tags("战斗状态.异常状态.免疫.风蚀效应"), EffectKindCombatState},
		{"runtime link on a monster", EffectCombatClassCombat, effectCombatBasisLocalOnMonster, nil, EffectKindRuntimeLink},
		{"runtime link from a monster", EffectCombatClassCombat, effectCombatBasisMonsterOnLocal, tags(), EffectKindRuntimeLink},
		{"no evidence", EffectCombatClassUnknown, effectCombatBasisNoEvidence, nil, EffectKindUnknown},
		{"no definition", EffectCombatClassUnknown, effectCombatBasisDefinitionMissing, nil, EffectKindUnknown},
	}
	for _, tc := range cases {
		if got, basis := classifyEffectKind(tc.class, tc.basis, tc.static); got != tc.want || basis == "" {
			t.Errorf("%s: kind = %s (%s), want %s", tc.name, got, basis, tc.want)
		}
	}
}

func TestEffectDisplayDecision(t *testing.T) {
	active := func(holder, source EffectEntityRelation, kind EffectKind) EffectInstanceSnapshot {
		return EffectInstanceSnapshot{Lifecycle: EffectLifecycleActive, HolderRelation: holder, SourceRelation: source, Kind: kind}
	}
	cases := []struct {
		name   string
		effect EffectInstanceSnapshot
		want   EffectDisplay
	}{
		{"team buff from teammate", active(EffectRelationLocalRole, EffectRelationLocalRole, EffectKindBuffMarker), EffectDisplay{EffectDisplayEligible, EffectTargetTeam}},
		{"summon holder", active(EffectRelationLocalOwned, EffectRelationUnknown, EffectKindStatModifier), EffectDisplay{EffectDisplayEligible, EffectTargetTeam}},
		{"enemy ailment from team", active(EffectRelationNonPlayer, EffectRelationLocalRole, EffectKindElementAilment), EffectDisplay{EffectDisplayEligible, EffectTargetEnemy}},
		{"monster effect of unknown meaning on team", active(EffectRelationLocalRole, EffectRelationNonPlayer, EffectKindRuntimeLink), EffectDisplay{EffectDisplayUnclassified, EffectTargetTeam}},
		{"team effect of unknown meaning on an enemy", active(EffectRelationNonPlayer, EffectRelationLocalRole, EffectKindRuntimeLink), EffectDisplay{EffectDisplayUnclassified, EffectTargetEnemy}},
		{"unresolved holder from team", active(EffectRelationUnknown, EffectRelationLocalRole, EffectKindCombatState), EffectDisplay{EffectDisplayEligible, EffectTargetUnresolved}},
		{"unknown kind stays unclassified", active(EffectRelationLocalRole, EffectRelationLocalRole, EffectKindUnknown), EffectDisplay{EffectDisplayUnclassified, EffectTargetTeam}},
		{"empty kind is unclassified", active(EffectRelationLocalRole, EffectRelationUnknown, ""), EffectDisplay{EffectDisplayUnclassified, EffectTargetTeam}},
		{"internal state on team", active(EffectRelationLocalRole, EffectRelationLocalRole, EffectKindInternalState), EffectDisplay{EffectDisplayInternal, EffectTargetTeam}},
		{"non-combat on team", active(EffectRelationLocalRole, EffectRelationLocalRole, EffectKindNonCombat), EffectDisplay{EffectDisplayNonCombat, EffectTargetTeam}},
		{"no local end", active(EffectRelationUnknown, EffectRelationNonPlayer, EffectKindStatModifier), EffectDisplay{Eligibility: EffectDisplayNotLocal}},
		{"estimated expired first", EffectInstanceSnapshot{Lifecycle: EffectLifecycleEstimatedExpired, HolderRelation: EffectRelationLocalRole, Kind: EffectKindStatModifier}, EffectDisplay{Eligibility: EffectDisplayEstimatedExpired}},
	}
	for _, tc := range cases {
		if got := effectDisplayFor(tc.effect); got != tc.want {
			t.Errorf("%s: display = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestEffectPolarityOnlyFromAilmentTerm(t *testing.T) {
	term := &EffectStaticInfo{Term: &EffectTermInfo{TermID: 850009, Name: "term", Status: string(resolutionCorrelated)}}
	if got := effectPolarityFor(EffectInstanceSnapshot{Kind: EffectKindElementAilment, Static: term}); got != EffectPolarityDebuff {
		t.Fatalf("ailment term polarity = %s", got)
	}
	for _, effect := range []EffectInstanceSnapshot{
		{Kind: EffectKindStatModifier},
		{Kind: EffectKindBuffMarker},
		{Kind: EffectKindElementAilment},
		{Kind: EffectKindRuntimeLink},
		{Kind: EffectKindInternalState, Static: term},
	} {
		if got := effectPolarityFor(effect); got != EffectPolarityUnclassified {
			t.Fatalf("%s polarity = %s, want unclassified", effect.Kind, got)
		}
	}
}

func TestEffectTrackerSnapshotCarriesKindAndPolarity(t *testing.T) {
	tracker := newTestEffectTracker()
	term := &EffectStaticInfo{DeveloperTags: []string{"战斗状态.异常状态.基础元素异常.风蚀效应"}, Term: &EffectTermInfo{TermID: 850009, Name: "term", Status: string(resolutionCorrelated)}}
	tracker.ObserveApply(testEffectApply(1, 0, func(e *EffectApplyEvent) {
		e.DefinitionLinked, e.Static, e.CombatClass, e.CombatBasis = true, term, EffectCombatClassCombat, effectCombatBasisTerm
	}))
	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(0.5)))
	if effect.Kind != EffectKindElementAilment || effect.KindBasis != effectKindBasisTerm || effect.Polarity != EffectPolarityDebuff {
		t.Fatalf("ailment instance = kind %s basis %s polarity %s", effect.Kind, effect.KindBasis, effect.Polarity)
	}
}

func TestViewEffectsListsOnlyEligibleAndAccountsForTheRest(t *testing.T) {
	p := newEffectViewProcessor()
	// Eligible: an unnamed, icon-less stat modifier; a short one; a stacked one; the same effect id from two sources
	// on one holder, which stay two cells because they are independent instances; the same effect id on another holder,
	// which shares a cell with one of them.
	p.effects.ObserveApply(displayApply(1, 500, 0, nil))
	p.effects.ObserveApply(displayApply(2, 501, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(0.5), effectFloat(0.5) }))
	p.effects.ObserveApply(displayApply(3, 502, 0, func(e *EffectApplyEvent) { e.StackCount = 4 }))
	p.effects.ObserveApply(displayApply(4, 503, 0, nil))
	p.effects.ObserveApply(displayApply(5, 503, 0, func(e *EffectApplyEvent) { e.SourceEntityID = 21 }))
	p.effects.ObserveApply(displayApply(6, 503, 0, func(e *EffectApplyEvent) { e.HolderEntityID = 11 }))
	// Not eligible, each for one reason.
	p.effects.ObserveApply(displayApply(7, 600, 0, func(e *EffectApplyEvent) {
		e.CombatClass, e.CombatBasis = EffectCombatClassUnknown, effectCombatBasisNoEvidence
	}))
	p.effects.ObserveApply(displayApply(8, 601, 0, func(e *EffectApplyEvent) {
		e.CombatClass, e.CombatBasis = EffectCombatClassNonCombat, effectCombatBasisTag+"跌落无伤"
	}))
	p.effects.ObserveApply(displayApply(9, 602, 0, func(e *EffectApplyEvent) { e.SourceRelation = EffectRelationUnknown }))
	p.effects.ObserveApply(displayApply(10, 603, 0, func(e *EffectApplyEvent) {
		e.CombatClass, e.CombatBasis = EffectCombatClassCombat, effectCombatBasisTag+"无敌"
		e.DefinitionLinked, e.Static = true, &EffectStaticInfo{DeveloperTags: []string{"功能.逻辑状态标识.无敌.角色闪避"}}
	}))

	view := effectViewAt(p, 0.25)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 10, Active: 10, NotLocal: 1, Local: 9, NonCombat: 1, Internal: 1, Unclassified: 1, Eligible: 6, Listed: 6, Cells: 5, ListedCells: 5})
	ids := map[uint64]int{}
	for _, effect := range view.Active {
		if effect.Display.Eligibility != EffectDisplayEligible || effect.Display.Target != EffectTargetEnemy || effect.Kind != EffectKindStatModifier {
			t.Fatalf("listed display = %+v kind %s", effect.Display, effect.Kind)
		}
		ids[*effect.EffectID] += effect.Instances
	}
	if ids[503] != 3 || ids[500] != 1 || ids[600] != 0 || ids[601] != 0 || ids[602] != 0 || ids[603] != 0 {
		t.Fatalf("listed effect ids = %v", ids)
	}

	// 500 seconds later every finite effect is long past its estimate: none of them is listed or counted as active.
	view = effectViewAt(p, 500)
	if view.Counts.EstimatedExpired != 10 || view.Counts.Eligible != 0 || len(view.Active) != 0 {
		t.Fatalf("long-expired counts = %+v", view.Counts)
	}
}

func TestViewEffectsListCountdownsFirstInTrackerOrder(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(displayApply(1, 900, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = nil, nil }))
	p.effects.ObserveApply(displayApply(2, 901, 0, nil))
	p.effects.ObserveApply(displayApply(3, 902, 0, func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(-1), effectFloat(-1) }))
	p.effects.ObserveApply(displayApply(4, 903, 0, nil))
	view := effectViewAt(p, 0.5)
	var order []uint64
	for _, effect := range view.Active {
		order = append(order, *effect.EffectID)
	}
	if len(order) != 4 || order[0] != 901 || order[1] != 903 || order[2] != 900 || order[3] != 902 {
		t.Fatalf("order = %v, want countdowns 901 903 then 900 902", order)
	}
}

func TestViewEffectsCountsAreStableUnderDuplicatesAndLifecycle(t *testing.T) {
	p := newEffectViewProcessor()
	ten := func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = effectFloat(10), effectFloat(10) }
	p.effects.ObserveApply(displayApply(1, 500, 0, ten))
	// A retransmitted apply and a refresh are the same instance.
	p.effects.ObserveApply(displayApply(1, 500, 0, ten))
	p.effects.ObserveApply(displayApply(1, 500, 1, ten))
	p.effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, StackCount: 3, HasStackCount: true, RemainingSeconds: effectFloat(10), DurationSeconds: effectFloat(10), ObservedAt: effectAt(2)})
	p.effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, StackCount: 2, HasStackCount: true, ObservedAt: effectAt(3)})
	view := effectViewAt(p, 3)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	if view.Active[0].StackCount == nil || *view.Active[0].StackCount != 2 {
		t.Fatalf("stack = %v", view.Active[0].StackCount)
	}
	requireNear(t, "remaining", view.Active[0].RemainingSeconds, 9)
	// An older update arriving late changes nothing.
	p.effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, StackCount: 5, HasStackCount: true, ObservedAt: effectAt(2.5)})
	if view = effectViewAt(p, 3); *view.Active[0].StackCount != 2 {
		t.Fatalf("stale update changed the stack to %d", *view.Active[0].StackCount)
	}

	// Remove ends it; the repeated remove and a late apply of the removed handle do not bring it back.
	p.effects.ObserveRemove(testEffectRemove(1, 4))
	p.effects.ObserveRemove(testEffectRemove(1, 4))
	p.effects.ObserveApply(displayApply(1, 500, 3.5, nil))
	requireEffectCounts(t, effectViewAt(p, 4), ViewEffectCounts{})
	// A new apply after the remove is a new instance, counted once.
	p.effects.ObserveApply(displayApply(1, 500, 5, nil))
	requireEffectCounts(t, effectViewAt(p, 5), ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})

	// Holder despawn ends every instance of that holder.
	p.effects.ObserveApply(displayApply(2, 501, 5, func(e *EffectApplyEvent) { e.HolderEntityID = 11 }))
	p.effects.ObserveHolderRemoved(1, 10, effectAt(5.5))
	view = effectViewAt(p, 5.5)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	if *view.Active[0].EffectID != 501 {
		t.Fatalf("listed after despawn = %d", *view.Active[0].EffectID)
	}

	// A new protocol session (scene change or reconnect) drops the live state, as does an interval reset.
	p.effects.ObserveApply(displayApply(3, 502, 6, func(e *EffectApplyEvent) { e.SessionNumber = 2 }))
	requireEffectCounts(t, effectViewAt(p, 6), ViewEffectCounts{Tracked: 1, Active: 1, Local: 1, Eligible: 1, Listed: 1, Cells: 1, ListedCells: 1})
	p.effects.Reset()
	requireEffectCounts(t, effectViewAt(p, 6), ViewEffectCounts{})
}

func TestViewEffectsBoundAppliesToTheDisplaySetOnly(t *testing.T) {
	p := newEffectViewProcessor()
	// Unclassified instances sort ahead of the eligible ones in the tracker's order (lower holder ID); they must not
	// take the bounded rows.
	for handle := uint64(1); handle <= 50; handle++ {
		p.effects.ObserveApply(displayApply(handle, 700, 0, func(e *EffectApplyEvent) {
			e.HolderEntityID, e.HolderRelation = 5, EffectRelationLocalRole
			e.CombatClass, e.CombatBasis = EffectCombatClassUnknown, effectCombatBasisNoEvidence
		}))
	}
	for handle := uint64(1); handle <= maxViewEffects+3; handle++ {
		p.effects.ObserveApply(displayApply(handle, 800+handle, 0, nil))
	}
	total := 50 + maxViewEffects + 3
	view := effectViewAt(p, 0.5)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: total, Active: total, Local: total, Unclassified: 50, Eligible: maxViewEffects + 3, Listed: maxViewEffects, Truncated: 3, Cells: maxViewEffects + 3, ListedCells: maxViewEffects, TruncatedCells: 3})
	for _, effect := range view.Active {
		if *effect.EffectID <= 800 {
			t.Fatalf("an unclassified instance took a bounded row: %+v", effect)
		}
	}
}
