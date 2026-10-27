//go:build wwdev

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuffOverlapQueryUsesHalfOpenIntervals(t *testing.T) {
	base := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	p := &processor{}
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", resolutionCorrelated, 27472, 69, base))
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_REMOVE", resolutionCorrelated, 27472, 69, base.Add(2*time.Second)))
	// Damage at the apply instant is inside; at the remove instant it is outside.
	p.recordDamageForOverlap(combatEvent{SessionNumber: 1, RPCID: 10, Timestamp: base, Damage: 5, Target: entityIdentity{Runtime: runtimeEntity{entityID: 27472}}})
	p.recordDamageForOverlap(combatEvent{SessionNumber: 1, RPCID: 11, Timestamp: base.Add(2 * time.Second), Damage: 6, Target: entityIdentity{Runtime: runtimeEntity{entityID: 27472}}})
	p.recordDamageForOverlap(combatEvent{SessionNumber: 1, RPCID: 12, Timestamp: base.Add(time.Second), Damage: 7, Target: entityIdentity{Runtime: runtimeEntity{entityID: 99999}}})

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
	p := &processor{}
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", resolutionCorrelated, 27472, 72, base))
	p.recordDamageForOverlap(combatEvent{SessionNumber: 1, RPCID: 20, Timestamp: base.Add(time.Hour), Damage: 1, Target: entityIdentity{Runtime: runtimeEntity{entityID: 27472}}})
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
	p := &processor{}
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", resolutionCorrelated, 27472, 69, base))
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", resolutionCorrelated, 27472, 69, base.Add(time.Second))) // duplicate
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_REMOVE", resolutionCorrelated, 27472, 69, base.Add(2*time.Second)))
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_REMOVE", resolutionCorrelated, 27472, 68, base))                   // no apply
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", resolutionUnknown, 27472, 70, base))                       // target not correlated
	p.observeBuffInterval(overlapRow("ApplyBuffS2cRequestNotify", resolutionCorrelated, 27472, 71, base))                // request, not applied state
	p.observeBuffInterval(overlapRow("GAMEPLAY_EFFECT_APPLY", resolutionCorrelated, 27472, 72, base.Add(3*time.Second))) // open at end

	if len(p.buffIntervals) != 2 {
		t.Fatalf("intervals = %d, want 2 (handles 69 and 72)", len(p.buffIntervals))
	}
	if p.buffDuplicateApplies != 1 || p.buffUnmatchedRemoves != 1 || p.buffOverlapSkipped != 1 {
		t.Fatalf("duplicates=%d unmatched=%d skipped=%d, want 1,1,1", p.buffDuplicateApplies, p.buffUnmatchedRemoves, p.buffOverlapSkipped)
	}
	if !p.buffIntervals[0].closed || p.buffIntervals[1].closed {
		t.Fatalf("closed flags = %v,%v, want true,false", p.buffIntervals[0].closed, p.buffIntervals[1].closed)
	}
}
