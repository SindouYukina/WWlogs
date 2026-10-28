package main

import (
	"testing"
	"time"
)

func overlapRow(kind string, status resolutionStatus, target, handle uint64, at time.Time) buffLifecycleAuditRow {
	row := buffLifecycleAuditRow{EventKind: kind, TargetStatus: status, SessionNumber: 1, Timestamp: at}
	row.TargetEntityID = &target
	row.HandleID = &handle
	return row
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
