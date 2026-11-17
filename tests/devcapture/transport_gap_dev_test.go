package devcapture

import (
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestKCPReorderingFillsTheGapAndDeliversInOrder(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 10, 0)
	sendTransportGapFrame(p, streams, stats, 12, 100*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 13, 150*time.Millisecond)
	if stats.Frames != 1 || stats.KCPGapsOpened != 1 || stats.KCPOutOfOrder != 2 {
		t.Fatalf("before the gap fills: %+v", *stats)
	}
	if health := summarizeKCPTransport(streams); health.OpenGaps != 1 || health.PendingSegments != 2 ||
		!health.OldestGapSince.Equal(transportGapBase.Add(100*time.Millisecond)) {
		t.Fatalf("open gap health = %+v", health)
	}
	sendTransportGapFrame(p, streams, stats, 11, 400*time.Millisecond)
	if stats.Frames != 4 || stats.KCPGapsFilled != 1 || stats.KCPMaxGapWait != 300*time.Millisecond || stats.KCPPendingPeak != 3 {
		t.Fatalf("after the gap fills: %+v", *stats)
	}
	if health := summarizeKCPTransport(streams); health.OpenGaps != 0 || health.PendingSegments != 0 {
		t.Fatalf("health after the gap filled = %+v", health)
	}
}

// While a gap is younger than kcpGapSkipTimeout the stream waits: later segments are held and the gap is visible.
// (Phase 1B added the timeout; before it the stream waited forever.)
func TestKCPUnfilledGapStallsTheStreamAndStaysVisible(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	for sequence := uint32(2); sequence < 10; sequence++ {
		sendTransportGapFrame(p, streams, stats, sequence, time.Duration(sequence)*100*time.Millisecond)
	}
	if stats.Frames != 1 || stats.KCPGapsFilled != 0 || stats.KCPGapsSkipped != 0 {
		t.Fatalf("frames while an unfilled gap waits = %+v", *stats)
	}
	health := summarizeKCPTransport(streams)
	if health.OpenGaps != 1 || health.PendingSegments != 8 || !health.OldestGapSince.Equal(transportGapBase.Add(200*time.Millisecond)) {
		t.Fatalf("waiting stream health = %+v", health)
	}
	if got := p.TransportHealthSnapshot().State; got != capture.TransportGapDetected {
		t.Fatalf("transport state while waiting = %s, want %s", got, capture.TransportGapDetected)
	}
	line := transportHealthLine(streams, *stats)
	for _, want := range []string{"kcp_open_gaps=1", "kcp_pending_segments=8", "kcp_gaps_opened=1", "kcp_gaps_filled=0"} {
		if !strings.Contains(line, want) {
			t.Fatalf("transport health line %q lacks %q", line, want)
		}
	}
}

func TestKCPSecondHoleAfterAFilledGapOpensANewGap(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	sendTransportGapFrame(p, streams, stats, 3, 100*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 1, 200*time.Millisecond) // 2 is still missing
	if stats.Frames != 2 || stats.KCPGapsOpened != 2 || stats.KCPGapsFilled != 1 {
		t.Fatalf("after the first hole filled = %+v", *stats)
	}
	if health := summarizeKCPTransport(streams); health.OpenGaps != 1 || !health.OldestGapSince.Equal(transportGapBase.Add(100*time.Millisecond)) {
		t.Fatalf("second hole health = %+v", health)
	}
	sendTransportGapFrame(p, streams, stats, 2, 300*time.Millisecond)
	if stats.Frames != 4 || stats.KCPGapsFilled != 2 || stats.KCPMaxGapWait != 200*time.Millisecond || stats.KCPGapsSkipped != 0 {
		t.Fatalf("after both holes filled = %+v", *stats)
	}
}
