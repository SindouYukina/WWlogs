package devcapture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestEffectDisplayAuditExplainsCellsAndExclusions(t *testing.T) {
	p := newEffectViewProcessor()
	for handle, holder := range []uint64{10, 11, 12} {
		p.Effects.ObserveApply(cellInstance(uint64(handle+1), 500, holder, 0, 15, nil))
	}
	p.Effects.ObserveApply(cellInstance(4, 501, 10, 0, 8, func(e *capture.EffectApplyEvent) {
		e.StackCount = 3
		e.Static = &capture.EffectStaticInfo{DurationPolicy: finitePolicy(), HUDCueTypes: []int32{14}, HUDPolarity: capture.EffectPolarityBuff}
	}))
	p.Effects.ObserveApply(passiveApply(5, 502, 0, nil))
	p.CurrentFrameTime = effectAt(1)
	row, cells, excluded := p.effectDisplayAudit()
	if len(row.Cells) != 2 || row.Counts.Eligible != 4 || row.Counts.NonHUD != 1 {
		t.Fatalf("audit = %+v", row)
	}
	byID := map[uint64]effectAuditCell{}
	for _, cell := range row.Cells {
		byID[*cell.EffectID] = cell
	}
	team := byID[500]
	if team.Merge != "same_effect_on_several_holders" || team.Instances != 3 || len(team.Members) != 3 {
		t.Fatalf("team cell = %+v", team)
	}
	if team.Shown != string(capture.EffectKindStatModifier) || team.Evidence != "CORRELATED" || team.Target != string(capture.EffectTargetTeam) || team.Icon != "hud_cue" {
		t.Fatalf("team cell reasons = %+v", team)
	}
	if team.Countdown != string(capture.EffectRemainingEstimated) || team.StackFrom != "every_member_equal" || team.Leaves != "server_remove_or_expiry_plus_grace" {
		t.Fatalf("team cell sources = %+v", team)
	}
	if want := effectAt(15).Add(capture.EffectDisplayExpiryGrace); team.HiddenAfter == nil || !team.HiddenAfter.Equal(want) {
		t.Fatalf("hidden after = %v, want %v", team.HiddenAfter, want)
	}
	hud := byID[501]
	if hud.Merge != "single_instance" || hud.Icon != "hud_cue" || *hud.Stack != 3 || hud.StackFrom != "single_instance" {
		t.Fatalf("HUD cell = %+v", hud)
	}
	if len(row.Excluded) != 1 || row.Excluded[0].Reason != string(capture.EffectDisplayNonHUD) || row.Excluded[0].DisplayBasis != capture.EffectDisplayBasisNoCue || row.Excluded[0].EffectID != 502 {
		t.Fatalf("excluded = %+v", row.Excluded)
	}
	// Time passing changes neither set: a countdown alone never produces a new row.
	p.CurrentFrameTime = effectAt(2)
	if _, laterCells, laterExcluded := p.effectDisplayAudit(); laterCells != cells || laterExcluded != excluded {
		t.Fatal("the audit signature depends on time")
	}
}

func TestEffectDisplayAuditWriterIsRateLimitedAndWritesOnlyChanges(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(cellInstance(1, 500, 10, 0, 15, nil))
	p.Effects.ObserveApply(passiveApply(2, 502, 0, nil))
	p.CurrentFrameTime = effectAt(1)
	var out bytes.Buffer
	writer := &effectDisplayAuditWriter{target: &out}
	lines := func() []string { return strings.Split(strings.TrimSpace(out.String()), "\n") }
	base := effectAt(0)

	writer.observe(p, base)
	writer.observe(p, base.Add(5*time.Second))
	if got := lines(); len(got) != 1 {
		t.Fatalf("an unchanged view wrote %d rows", len(got))
	}
	p.Effects.ObserveApply(cellInstance(3, 501, 10, 1, 15, nil))
	writer.observe(p, base.Add(5*time.Second+100*time.Millisecond))
	if got := lines(); len(got) != 2 {
		t.Fatalf("a changed view wrote %d rows", len(got))
	}
	var row effectAuditRow
	if err := json.Unmarshal([]byte(lines()[1]), &row); err != nil {
		t.Fatal(err)
	}
	if !row.ExcludedUnchanged || row.Excluded != nil {
		t.Fatalf("an unchanged excluded set was repeated: %+v", row)
	}
	p.Effects.ObserveApply(cellInstance(4, 503, 10, 1, 15, nil))
	writer.observe(p, base.Add(5*time.Second+200*time.Millisecond))
	if got := lines(); len(got) != 2 {
		t.Fatalf("the rate limit let %d rows through", len(got))
	}
}

func TestEffectDisplayAuditWriterIgnoresANilWriter(t *testing.T) {
	var writer *effectDisplayAuditWriter
	writer.observe(newEffectViewProcessor(), effectAt(0))
}
