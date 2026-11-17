package devcapture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestBuffOverlapQueryUsesHalfOpenIntervals(t *testing.T) {
	base := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	p := observed(&processor{Processor: &capture.Processor{}})
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", capture.ResolutionCorrelated, 27472, 69, base))
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_REMOVE", capture.ResolutionCorrelated, 27472, 69, base.Add(2*time.Second)))
	// Damage at the apply instant is inside; at the remove instant it is outside.
	p.recordDamageForOverlap(capture.CombatEvent{SessionNumber: 1, RPCID: 10, Timestamp: base, Damage: 5, Target: capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 27472}}})
	p.recordDamageForOverlap(capture.CombatEvent{SessionNumber: 1, RPCID: 11, Timestamp: base.Add(2 * time.Second), Damage: 6, Target: capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 27472}}})
	p.recordDamageForOverlap(capture.CombatEvent{SessionNumber: 1, RPCID: 12, Timestamp: base.Add(time.Second), Damage: 7, Target: capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 99999}}})

	var out bytes.Buffer
	if err := p.writeBuffOverlap(&out); err != nil {
		t.Fatalf("writeBuffOverlap: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("rows = %d, want 3 (one per damage result)", len(lines))
	}
	want := []int{1, 0, 0}
	for i, line := range lines {
		var row overlapRowJSONL
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("row %d is not JSON: %v", i, err)
		}
		if len(row.ActiveBuffs) != want[i] {
			t.Fatalf("row %d (rpc %d) active = %d, want %d", i, row.RPCID, len(row.ActiveBuffs), want[i])
		}
		if row.Precision != "PACKET_OBSERVED_TIME" {
			t.Fatalf("row %d precision = %q", i, row.Precision)
		}
	}
}

func TestBuffOverlapLabelsOpenIntervals(t *testing.T) {
	base := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	p := observed(&processor{Processor: &capture.Processor{}})
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", capture.ResolutionCorrelated, 27472, 72, base))
	p.recordDamageForOverlap(capture.CombatEvent{SessionNumber: 1, RPCID: 20, Timestamp: base.Add(time.Hour), Damage: 1, Target: capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 27472}}})
	var out bytes.Buffer
	if err := p.writeBuffOverlap(&out); err != nil {
		t.Fatalf("writeBuffOverlap: %v", err)
	}
	var row overlapRowJSONL
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatalf("row is not JSON: %v", err)
	}
	if len(row.ActiveBuffs) != 1 || !row.ActiveBuffs[0].OpenAtEnd || row.ActiveBuffs[0].End != nil {
		t.Fatalf("open interval row = %+v", row.ActiveBuffs)
	}
}

func TestBuffIntervalsUseOnlyCorrelatedApplyAndRemove(t *testing.T) {
	base := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	p := observed(&processor{Processor: &capture.Processor{}})
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", capture.ResolutionCorrelated, 27472, 69, base))
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", capture.ResolutionCorrelated, 27472, 69, base.Add(time.Second))) // duplicate
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_REMOVE", capture.ResolutionCorrelated, 27472, 69, base.Add(2*time.Second)))
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_REMOVE", capture.ResolutionCorrelated, 27472, 68, base))                   // no apply
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", capture.ResolutionUnknown, 27472, 70, base))                       // target not correlated
	p.ObserveBuffInterval(overlapRow("ApplyBuffS2cRequestNotify", capture.ResolutionCorrelated, 27472, 71, base))                // request, not applied state
	p.ObserveBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", capture.ResolutionCorrelated, 27472, 72, base.Add(3*time.Second))) // open at end

	if len(p.dev.buffIntervals) != 2 {
		t.Fatalf("intervals = %d, want 2 (handles 69 and 72)", len(p.dev.buffIntervals))
	}
	if p.dev.buffDuplicateApplies != 1 || p.dev.buffUnmatchedRemoves != 1 || p.dev.buffOverlapSkipped != 1 {
		t.Fatalf("duplicates=%d unmatched=%d skipped=%d, want 1,1,1", p.dev.buffDuplicateApplies, p.dev.buffUnmatchedRemoves, p.dev.buffOverlapSkipped)
	}
	if !p.dev.buffIntervals[0].closed || p.dev.buffIntervals[1].closed {
		t.Fatalf("closed flags = %v,%v, want true,false", p.dev.buffIntervals[0].closed, p.dev.buffIntervals[1].closed)
	}
}
