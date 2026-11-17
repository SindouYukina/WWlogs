package devcapture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestLiveDiagnosticSnapshotUsesCaptureTimeForDPS(t *testing.T) {
	observedAt := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	sampledAt := observedAt.Add(1500 * time.Millisecond)
	statistics := capture.NewDamageStatistics()
	processor := observed(&processor{Processor: &capture.Processor{DamageStatistics: statistics, Effects: capture.NewEffectTracker(statistics.CaptureSessionID), CurrentFrameTime: observedAt}})

	snapshot := processor.liveDiagnosticSnapshot(sampledAt)
	if snapshot.DamageStatistics.CaptureSessionID != statistics.CaptureSessionID ||
		snapshot.DamageStatistics.StatisticsIntervalID != 1 {
		t.Fatalf("capture and interval identity missing: %+v", snapshot.DamageStatistics)
	}
	if snapshot.DamageStatistics.Rolling5sDPS.Status != capture.DPSStatusAvailable ||
		!snapshot.DamageStatistics.Rolling5sDPS.EndObservedAt.Equal(observedAt) {
		t.Fatalf("damage query did not use capture observation time: %+v", snapshot.DamageStatistics.Rolling5sDPS)
	}
	if snapshot.Effects.QueryTime == nil || !snapshot.Effects.QueryTime.Equal(observedAt) {
		t.Fatalf("effect query did not use capture observation time: %+v", snapshot.Effects.QueryTime)
	}
	if snapshot.ClockDifferenceSeconds == nil || *snapshot.ClockDifferenceSeconds != 1.5 {
		t.Fatalf("clock comparison missing: %v", snapshot.ClockDifferenceSeconds)
	}
	if !strings.Contains(snapshot.ClockDifferenceInterpretation, "equivalence is not established") {
		t.Fatalf("clock comparison was presented as authoritative: %q", snapshot.ClockDifferenceInterpretation)
	}

	var output bytes.Buffer
	if err := writeLiveDiagnosticSnapshot(&output, snapshot); err != nil {
		t.Fatal(err)
	}
	var decoded liveDiagnosticSnapshot
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("diagnostic JSONL is not valid JSON: %v", err)
	}
	if decoded.Type != "LIVE_DIAGNOSTIC_SNAPSHOT" || decoded.Effects.Status != capture.EffectSnapshotComplete {
		t.Fatalf("unexpected diagnostic record: %+v", decoded)
	}
}
