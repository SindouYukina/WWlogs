package capture

import (
	"reflect"
	"testing"
)

// rowApply is one timed HUD status of the given direction on a role of the local team.
func rowApply(handle, effectID uint64, polarity EffectPolarity, remaining float64) EffectApplyEvent {
	return displayApply(handle, effectID, 0, func(e *EffectApplyEvent) {
		e.HolderEntityID, e.HolderRelation = handle+100, EffectRelationLocalRole
		e.DurationSeconds, e.RemainingSeconds = effectFloat(remaining), effectFloat(remaining)
		e.Static = &EffectStaticInfo{DurationPolicy: finitePolicy(), HUDCueTypes: []int32{2}, HUDPolarity: polarity}
	})
}

func rowIDs(row ViewEffectRow) []uint64 {
	ids := []uint64{}
	for _, effect := range row.Effects {
		ids = append(ids, *effect.EffectID)
	}
	return ids
}

func TestViewEffectsSplitsCellsIntoTwoRowsEachInItsOwnOrder(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityDebuff, 9))
	p.Effects.ObserveApply(rowApply(2, 502, EffectPolarityBuff, 8))
	p.Effects.ObserveApply(rowApply(3, 503, EffectPolarityDebuff, 1))
	p.Effects.ObserveApply(rowApply(4, 504, EffectPolarityBuff, 2))
	view := effectViewAt(p, 0)
	if got, want := rowIDs(view.Buffs), []uint64{504, 502}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buff row = %v, want %v", got, want)
	}
	if got, want := rowIDs(view.Debuffs), []uint64{503, 501}; !reflect.DeepEqual(got, want) {
		t.Fatalf("debuff row = %v, want %v", got, want)
	}
	if len(view.Active) != 4 {
		t.Fatalf("listed = %d, want both rows", len(view.Active))
	}
}

func TestViewEffectsOnlyOneDirectionLeavesTheOtherRowEmpty(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 5))
	view := effectViewAt(p, 0)
	if len(view.Buffs.Effects) != 1 || view.Debuffs.Effects == nil || len(view.Debuffs.Effects) != 0 {
		t.Fatalf("rows = %+v / %+v", view.Buffs, view.Debuffs)
	}
}

func TestViewEffectsCellsWithoutAReliableTimeSortLastWithinTheirRow(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 5))
	untimed := rowApply(2, 502, EffectPolarityBuff, 5)
	untimed.DurationSeconds, untimed.RemainingSeconds = nil, nil
	p.Effects.ObserveApply(untimed)
	p.Effects.ObserveApply(rowApply(3, 503, EffectPolarityBuff, 2))
	if got, want := rowIDs(effectViewAt(p, 0).Buffs), []uint64{503, 501, 502}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buff row = %v, want %v", got, want)
	}
}

func TestViewEffectsTimePassingNeverReordersARow(t *testing.T) {
	p := newEffectViewProcessor()
	for handle := uint64(1); handle <= 6; handle++ {
		p.Effects.ObserveApply(rowApply(handle, 500+handle, EffectPolarityDebuff, float64(handle)+10))
	}
	first := rowIDs(effectViewAt(p, 0).Debuffs)
	for tick := 1; tick <= 8; tick++ {
		if got := rowIDs(effectViewAt(p, float64(tick)).Debuffs); !reflect.DeepEqual(got, first) {
			t.Fatalf("tick %d: order %v, want %v", tick, got, first)
		}
	}
}

func TestViewEffectsTruncatedCellsAreCountedOnTheirOwnRow(t *testing.T) {
	p := newEffectViewProcessor()
	for handle := uint64(1); handle <= maxViewEffects+2; handle++ {
		p.Effects.ObserveApply(rowApply(handle, 1000+handle, EffectPolarityBuff, float64(handle)+1))
	}
	p.Effects.ObserveApply(rowApply(maxViewEffects+10, 5000, EffectPolarityDebuff, 900))
	view := effectViewAt(p, 0)
	if view.Buffs.Truncated+view.Debuffs.Truncated != view.Counts.TruncatedCells || view.Counts.TruncatedCells != 3 {
		t.Fatalf("truncated = buff %d debuff %d, cells %d", view.Buffs.Truncated, view.Debuffs.Truncated, view.Counts.TruncatedCells)
	}
	if view.Debuffs.Truncated != 1 || len(view.Debuffs.Effects) != 0 {
		t.Fatalf("the latest-ending debuff must be the one left out: %+v", view.Debuffs)
	}
}

func TestViewEffectsShowCountdownIsTheEnginesDecision(t *testing.T) {
	shown := func(view ViewEffects) bool { return len(view.Active) == 1 && view.Active[0].ShowCountdown }
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 20))
	if !shown(effectViewAt(p, 1)) {
		t.Fatal("a fresh timed status must show its countdown")
	}
	p.ViewClockStalled = true
	if shown(effectViewAt(p, 1)) {
		t.Fatal("a stalled capture clock must not show a countdown")
	}
	p.ViewClockStalled = false
	p.Effects.MarkTransportLoss(effectAt(1))
	if shown(effectViewAt(p, 2)) {
		t.Fatal("a status the transport loss left unconfirmed must not show a countdown")
	}
	p = newEffectViewProcessor()
	untimed := rowApply(1, 501, EffectPolarityBuff, 20)
	untimed.DurationSeconds, untimed.RemainingSeconds = nil, nil
	p.Effects.ObserveApply(untimed)
	if shown(effectViewAt(p, 1)) {
		t.Fatal("a status without a duration has no countdown")
	}
}

func TestViewEffectsTargetSeparatesDebuffsOnTheTeamFromDebuffsOnEnemies(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 500, EffectPolarityDebuff, 15))
	onEnemy := rowApply(2, 500, EffectPolarityDebuff, 15)
	onEnemy.HolderRelation, onEnemy.HolderEntityID = EffectRelationNonPlayer, 900
	p.Effects.ObserveApply(onEnemy)
	view := effectViewAt(p, 1)
	if len(view.Debuffs.Effects) != 2 {
		t.Fatalf("the same status on the team and on an enemy shared a cell: %+v", view.Debuffs.Effects)
	}
	targets := map[EffectDisplayTarget]int{}
	for _, effect := range view.Debuffs.Effects {
		targets[effect.Display.Target]++
	}
	if targets[EffectTargetTeam] != 1 || targets[EffectTargetEnemy] != 1 {
		t.Fatalf("targets = %v", targets)
	}
}

func TestViewEffectsPlayerEntityDebuffIsAListedTeamStatus(t *testing.T) {
	p := newEffectViewProcessor()
	cold := rowApply(1, 10010000, EffectPolarityDebuff, 15)
	cold.HolderRelation, cold.SourceRelation = EffectRelationLocalPlayer, EffectRelationNonPlayer
	cold.StackCount = 7
	p.Effects.ObserveApply(cold)
	view := effectViewAt(p, 1)
	if len(view.Debuffs.Effects) != 1 || view.Debuffs.Effects[0].Display.Target != EffectTargetTeam {
		t.Fatalf("debuff row = %+v", view.Debuffs.Effects)
	}
	if got := view.Debuffs.Effects[0].StackCount; got == nil || *got != 7 {
		t.Fatalf("stack = %v, want 7", got)
	}
}
