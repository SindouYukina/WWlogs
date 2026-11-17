package capture

import (
	"bytes"
	"testing"
	"time"
)

// Fault-injection tests for KCP delivery: gaps, reordering, duplicates, the pending bound, sequence wraparound, and
// broken fragment sequences. Every frame is a complete application frame carried by one KCP segment unless a test
// fragments it on purpose.

var transportGapBase = time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)

func newTransportGapHarness() (*processor, map[string]*stream, *captureStats) {
	return newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil), make(map[string]*stream), &captureStats{}
}

func makeKCPFragmentForTest(conv, sequence uint32, fragments byte, payload []byte) []byte {
	segment := makeKCPSegmentForTest(conv, sequence, payload)
	segment[5] = fragments
	return segment
}

func sendTransportGapFrame(p *processor, streams map[string]*stream, stats *captureStats, sequence uint32, at time.Duration) {
	frame := makeTransportTestFrame(false, false)
	consumeDatagram(makeKCPSegmentForTest(9, sequence, frame), "in", transportGapBase.Add(at), streams, p, stats)
}

func TestKCPDuplicatesAndRetransmissionsAreDeliveredOnce(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	sendTransportGapFrame(p, streams, stats, 0, time.Millisecond) // retransmission of a delivered segment
	sendTransportGapFrame(p, streams, stats, 2, 2*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 2, 3*time.Millisecond) // duplicate of a pending segment
	sendTransportGapFrame(p, streams, stats, 1, 4*time.Millisecond)
	if stats.frames != 3 || stats.kcpDuplicates != 2 || stats.kcpOrdered != 3 {
		t.Fatalf("duplicate handling = %+v", *stats)
	}
}

// Pending never exceeds maxPendingKCPSegments. Since Phase 1B a full buffer skips the gap at once instead of dropping
// newer segments, so delivery resumes immediately.
func TestKCPPendingIsBoundedBehindAnUnfilledGap(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	extra := 25
	for i := 0; i < maxPendingKCPSegments+extra; i++ {
		sendTransportGapFrame(p, streams, stats, uint32(2+i), time.Millisecond)
	}
	state := streams["00000009/in"]
	if len(state.pending) != 0 || stats.kcpPendingOverflow != 1 || stats.kcpPendingPeak != maxPendingKCPSegments ||
		stats.kcpGapsSkipped != 1 || stats.kcpSegmentsSkipped != 1 {
		t.Fatalf("pending=%d overflow=%d peak=%d skipped=%d/%d", len(state.pending), stats.kcpPendingOverflow,
			stats.kcpPendingPeak, stats.kcpGapsSkipped, stats.kcpSegmentsSkipped)
	}
	if want := 1 + maxPendingKCPSegments + extra; stats.frames != want {
		t.Fatalf("frames delivered after the forced skip = %d, want %d", stats.frames, want)
	}
}

func TestKCPSequenceWrapsAround(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, ^uint32(0)-1, 0)
	sendTransportGapFrame(p, streams, stats, ^uint32(0), time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 0, 2*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 1, 3*time.Millisecond)
	if stats.frames != 4 || stats.kcpDuplicates != 0 || stats.kcpGapsOpened != 0 {
		t.Fatalf("wraparound delivery = %+v", *stats)
	}
	sendTransportGapFrame(p, streams, stats, ^uint32(0), 4*time.Millisecond) // a late retransmission from before the wrap
	if stats.frames != 4 || stats.kcpDuplicates != 1 {
		t.Fatalf("late pre-wrap retransmission = %+v", *stats)
	}
	if !kcpSequenceBefore(^uint32(0), 0) || kcpSequenceBefore(0, ^uint32(0)) || kcpSequenceBefore(5, 5) {
		t.Fatal("kcpSequenceBefore does not follow 32-bit wraparound order")
	}
}

func TestKCPFragmentedMessageIsAssembled(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	frame := makeTransportTestFrame(false, false)
	consumeDatagram(makeKCPFragmentForTest(9, 0, 2, frame[:5]), "in", transportGapBase, streams, p, stats)
	consumeDatagram(makeKCPFragmentForTest(9, 1, 1, frame[5:10]), "in", transportGapBase, streams, p, stats)
	consumeDatagram(makeKCPFragmentForTest(9, 2, 0, frame[10:]), "in", transportGapBase, streams, p, stats)
	if stats.frames != 1 || stats.kcpMessages != 1 || stats.kcpFragmentResets != 0 || stats.kcpFragmentTailDrops != 0 {
		t.Fatalf("fragmented message = %+v", *stats)
	}
}

func TestKCPBrokenFragmentSequenceStartsANewMessage(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	frame := makeTransportTestFrame(false, false)
	// A four-fragment message (count 3) is followed by fragment count 1, which cannot continue it (count 2 is
	// expected); it begins a new two-fragment message that then completes.
	consumeDatagram(makeKCPFragmentForTest(9, 0, 3, []byte{0xAA, 0xBB}), "in", transportGapBase, streams, p, stats)
	consumeDatagram(makeKCPFragmentForTest(9, 1, 1, frame[:6]), "in", transportGapBase, streams, p, stats)
	if stats.kcpFragmentResets != 1 || stats.kcpMessages != 0 {
		t.Fatalf("after the broken sequence = %+v", *stats)
	}
	consumeDatagram(makeKCPFragmentForTest(9, 2, 0, frame[6:]), "in", transportGapBase, streams, p, stats)
	if stats.kcpMessages != 1 || stats.frames != 1 || stats.badCRC != 0 || stats.malformed != 0 {
		t.Fatalf("the new message did not assemble into one valid frame: %+v", *stats)
	}
}

func TestKCPFinalFragmentOfAnIncompleteMessageIsDropped(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	frame := makeTransportTestFrame(false, false)
	consumeDatagram(makeKCPFragmentForTest(9, 0, 2, []byte{0xAA, 0xBB}), "in", transportGapBase, streams, p, stats)
	consumeDatagram(makeKCPFragmentForTest(9, 1, 0, []byte{0xCC}), "in", transportGapBase, streams, p, stats)
	if stats.kcpFragmentTailDrops != 1 || stats.kcpMessages != 0 || stats.malformed != 0 {
		t.Fatalf("incomplete message = %+v", *stats)
	}
	consumeDatagram(makeKCPSegmentForTest(9, 2, frame), "in", transportGapBase, streams, p, stats)
	if stats.frames != 1 || stats.kcpMessages != 1 {
		t.Fatalf("a whole message after the dropped tail = %+v", *stats)
	}
}
