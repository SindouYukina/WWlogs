package capture

import (
	"bytes"
	"testing"
	"time"
)

// Capture-layer counters stay separate from KCP and diagnostic counters, and say when they cannot be read.

func TestCaptureLayerHealthOfflineReplayHasNoDriverCounters(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.observeCaptureSource(false)
	p.observePacketRead()
	p.observePacketRead()
	p.sampleCaptureStats(nil, time.Now(), true)
	health := p.captureLayerHealth()
	if health.Source != "offline" || health.PacketsRead != 2 {
		t.Fatalf("offline health = %+v", health)
	}
	if health.KernelDropped.Status != CaptureCounterUnavailable || health.KernelDropped.Reason != "offline_replay_has_no_driver_counters" ||
		health.KernelDropped.Value != 0 || health.KernelReceived.Status != CaptureCounterUnavailable {
		t.Fatalf("offline replay must not report a driver drop count: %+v", health.KernelDropped)
	}
	if health.InterfaceDropped.Status != CaptureCounterUnavailable {
		t.Fatalf("interface drops are not verified on Npcap: %+v", health.InterfaceDropped)
	}
	if health.DiagnosticDropped.Status != CaptureCounterNotApplicable {
		t.Fatalf("a processor without a diagnostic pipeline reports not_applicable: %+v", health.DiagnosticDropped)
	}
	if formatCaptureCounter(health.KernelDropped) != "unavailable" {
		t.Fatalf("console form = %q", formatCaptureCounter(health.KernelDropped))
	}
}

func TestCaptureLayerHealthLiveBeforeFirstSample(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.observeCaptureSource(true)
	health := p.captureLayerHealth()
	if health.Source != "live" || health.KernelDropped.Status != CaptureCounterUnavailable || health.KernelDropped.Reason != "not_sampled_yet" {
		t.Fatalf("live health before sampling = %+v", health)
	}
	p.captureLayer.statsKnown, p.captureLayer.received, p.captureLayer.dropped = true, 100, 7
	health = p.captureLayerHealth()
	if health.KernelDropped.Status != CaptureCounterAvailable || health.KernelDropped.Value != 7 || health.KernelReceived.Value != 100 ||
		formatCaptureCounter(health.KernelDropped) != "7" {
		t.Fatalf("sampled live health = %+v", health)
	}
}

func TestCaptureLayerApplicationDropsCountEvictedStreamData(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	frame := makeTransportTestFrame(false, false)
	// Conversation 0 waits behind a gap with two segments, then becomes the least recently active stream.
	consumeDatagram(makeKCPSegmentForTest(0, 0, frame), "in", transportGapBase, streams, p, stats)
	consumeDatagram(makeKCPSegmentForTest(0, 2, frame), "in", transportGapBase, streams, p, stats)
	consumeDatagram(makeKCPSegmentForTest(0, 3, frame), "in", transportGapBase, streams, p, stats)
	for conv := uint32(1); conv <= maxKCPStreams; conv++ {
		consumeDatagram(makeKCPSegmentForTest(conv, 0, frame), "in", transportGapBase.Add(time.Duration(conv)*time.Millisecond), streams, p, stats)
	}
	health := p.captureLayerHealth()
	if stats.kcpStreamsEvicted != 1 || health.ApplicationDropped.Status != CaptureCounterAvailable || health.ApplicationDropped.Value != 2 {
		t.Fatalf("evicted pending segments = %d, health = %+v", stats.kcpEvictedPendingSegments, health.ApplicationDropped)
	}
	// The KCP gap and the application discard are separate measures.
	if transport := directionHealth(t, p, "in"); transport.Losses != 1 || transport.Unrecovered != 1 {
		t.Fatalf("the evicted stream's open episode is reported unrecovered: %+v", transport)
	}
}
