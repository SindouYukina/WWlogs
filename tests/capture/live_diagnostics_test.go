//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLiveDiagnosticSnapshotUsesCaptureTimeForDPS(t *testing.T) {
	observedAt := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	sampledAt := observedAt.Add(1500 * time.Millisecond)
	statistics := NewDamageStatistics()
	processor := &processor{
		damageStatistics: statistics,
		effects:          NewEffectTracker(statistics.captureSessionID),
		currentFrameTime: observedAt,
	}

	snapshot := processor.liveDiagnosticSnapshot(sampledAt)
	if snapshot.DamageStatistics.CaptureSessionID != statistics.captureSessionID ||
		snapshot.DamageStatistics.StatisticsIntervalID != 1 {
		t.Fatalf("capture and interval identity missing: %+v", snapshot.DamageStatistics)
	}
	if snapshot.DamageStatistics.Rolling5sDPS.Status != DPSStatusAvailable ||
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
	if decoded.Type != "LIVE_DIAGNOSTIC_SNAPSHOT" || decoded.Effects.Status != EffectSnapshotComplete {
		t.Fatalf("unexpected diagnostic record: %+v", decoded)
	}
}
