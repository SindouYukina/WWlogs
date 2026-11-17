package capture

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
	"time"
)

// Recovery and frame resynchronization: controlled gap skipping, bounds, consecutive gaps, wraparound, fragment loss,
// mid-frame gaps, noise, candidate choice, scan budgets, and the outbound sequence evidence.

// makeRecoveryFrame builds a type-1 frame (the client's outbound header layout, with a token) carrying body.
func makeRecoveryFrame(sequence uint32, body []byte) []byte {
	frame := make([]byte, 16+len(body))
	binary.LittleEndian.PutUint16(frame[:2], uint16(len(frame)-3))
	frame[3] = 1
	binary.LittleEndian.PutUint32(frame[4:8], sequence)
	binary.LittleEndian.PutUint16(frame[8:10], 1)
	binary.LittleEndian.PutUint16(frame[10:12], 65000)
	binary.LittleEndian.PutUint32(frame[12:16], crc32.ChecksumIEEE(body))
	copy(frame[16:], body)
	return frame
}

func recoveryBody(seed, length int) []byte {
	body := make([]byte, length)
	for i := range body {
		body[i] = byte(seed*31 + i*7)
	}
	return body
}

func sendRecoverySegment(p *Processor, streams map[string]*Stream, stats *CaptureStats, direction string, sequence uint32, payload []byte, at time.Duration) {
	ConsumeDatagram(makeKCPSegmentForTest(9, sequence, payload), direction, transportGapBase.Add(at), streams, p, stats)
}

func directionHealth(t *testing.T, p *Processor, direction string) TransportDirectionHealth {
	t.Helper()
	for _, health := range p.TransportHealthSnapshot().Directions {
		if health.Direction == direction {
			return health
		}
	}
	t.Fatalf("no health for direction %s", direction)
	return TransportDirectionHealth{}
}

func TestKCPGapIsSkippedAfterTimeoutAndDeliveryResumes(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	for sequence := uint32(2); sequence <= 4; sequence++ {
		sendTransportGapFrame(p, streams, stats, sequence, time.Duration(sequence-1)*100*time.Millisecond)
	}
	if stats.Frames != 1 || stats.KCPGapsSkipped != 0 {
		t.Fatalf("before the timeout = %+v", *stats)
	}
	// 2.2 s is more than kcpGapSkipTimeout after the gap opened at 0.1 s.
	sendTransportGapFrame(p, streams, stats, 5, 2200*time.Millisecond)
	if stats.Frames != 5 || stats.KCPGapsSkipped != 1 || stats.KCPSegmentsSkipped != 1 || stats.ResyncSuccesses != 1 {
		t.Fatalf("after the timeout = %+v", *stats)
	}
	health := directionHealth(t, p, "in")
	if health.State != TransportRecovered || health.Losses != 1 || health.Recoveries != 1 || health.SkippedSegments != 1 ||
		health.LastLossReason != kcpSkipGapTimeout {
		t.Fatalf("inbound health after recovery = %+v", health)
	}
	if got := p.TransportHealthSnapshot().State; got != TransportRecovered {
		t.Fatalf("capture transport state = %s", got)
	}
}

func TestKCPRetransmissionWithinTimeoutIsNotSkipped(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	sendTransportGapFrame(p, streams, stats, 2, 100*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 1, 1900*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 3, 2500*time.Millisecond)
	if stats.Frames != 4 || stats.KCPGapsSkipped != 0 || stats.KCPGapsFilled != 1 {
		t.Fatalf("a refilled gap must not be skipped: %+v", *stats)
	}
	if health := directionHealth(t, p, "in"); health.State != TransportHealthy || health.Losses != 0 {
		t.Fatalf("health after a refilled gap = %+v", health)
	}
	if snapshot := p.DamageStatisticsSnapshot(); snapshot.Integrity.Status != IntegrityIntact {
		t.Fatalf("a refilled gap must not touch integrity: %+v", snapshot.Integrity)
	}
}

func TestKCPPendingByteLimitForcesSkip(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendRecoverySegment(p, streams, stats, "in", 0, makeRecoveryFrame(1, recoveryBody(1, 16)), 0)
	big := makeRecoveryFrame(2, recoveryBody(2, 60000))
	state := streams["00000009/in"]
	for sequence := uint32(2); sequence < 90; sequence++ {
		sendRecoverySegment(p, streams, stats, "in", sequence, big, time.Millisecond)
		if state.PendingBytes > maxPendingKCPBytes {
			t.Fatalf("pending bytes %d exceed the bound %d", state.PendingBytes, maxPendingKCPBytes)
		}
	}
	if stats.KCPPendingOverflow != 1 || stats.KCPGapsSkipped != 1 || stats.KCPPendingPeak >= maxPendingKCPSegments {
		t.Fatalf("byte bound did not force exactly one skip: %+v", *stats)
	}
	if stats.Frames != 89 {
		t.Fatalf("frames after the byte-forced skip = %d, want 89", stats.Frames)
	}
}

func TestKCPConsecutiveGapsRecoverWithoutLooping(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	sendTransportGapFrame(p, streams, stats, 2, 100*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 4, 200*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 5, 2150*time.Millisecond) // skips the first hole only
	if stats.Frames != 2 || stats.KCPGapsSkipped != 1 {
		t.Fatalf("after the first timeout = %+v", *stats)
	}
	sendTransportGapFrame(p, streams, stats, 6, 2300*time.Millisecond) // the second hole has now waited 2.1 s
	if stats.Frames != 5 || stats.KCPGapsSkipped != 2 || stats.KCPSegmentsSkipped != 2 || stats.ResyncSuccesses != 2 {
		t.Fatalf("after the second timeout = %+v", *stats)
	}
	if health := directionHealth(t, p, "in"); health.Losses != 2 || health.Recoveries != 2 || health.State != TransportRecovered {
		t.Fatalf("health after two separate losses = %+v", health)
	}
}

func TestKCPSkipAcrossSequenceWraparound(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, ^uint32(0)-1, 0)
	sendTransportGapFrame(p, streams, stats, 0, 100*time.Millisecond) // ^uint32(0) is missing
	sendTransportGapFrame(p, streams, stats, 1, 150*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 2, 2200*time.Millisecond)
	if stats.Frames != 4 || stats.KCPSegmentsSkipped != 1 || stats.KCPGapsSkipped != 1 {
		t.Fatalf("skip across the wrap = %+v", *stats)
	}
}

func TestKCPCaptureEndSkipsOpenGap(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	sendTransportGapFrame(p, streams, stats, 0, 0)
	sendTransportGapFrame(p, streams, stats, 2, 100*time.Millisecond)
	sendTransportGapFrame(p, streams, stats, 3, 150*time.Millisecond)
	FinishTransport(streams, p, stats)
	if stats.Frames != 3 || stats.KCPGapsSkipped != 1 || stats.ResyncSuccesses != 1 {
		t.Fatalf("capture end must deliver the data behind an open gap: %+v", *stats)
	}
	if health := directionHealth(t, p, "in"); health.LastLossReason != kcpSkipCaptureEnd || health.State != TransportRecovered {
		t.Fatalf("health after capture end = %+v", health)
	}
}

func TestKCPFragmentLostAcrossSkipIsDiscarded(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	frame := makeRecoveryFrame(10, recoveryBody(3, 60))
	sendRecoverySegment(p, streams, stats, "in", 0, makeRecoveryFrame(9, recoveryBody(4, 20)), 0)
	ConsumeDatagram(makeKCPFragmentForTest(9, 1, 2, frame[:20]), "in", transportGapBase.Add(10*time.Millisecond), streams, p, stats)
	// Fragment 2 of 3 (sequence 2) is lost; the final fragment and a whole frame wait behind it.
	ConsumeDatagram(makeKCPFragmentForTest(9, 3, 0, frame[40:]), "in", transportGapBase.Add(20*time.Millisecond), streams, p, stats)
	sendRecoverySegment(p, streams, stats, "in", 4, makeRecoveryFrame(11, recoveryBody(5, 30)), 30*time.Millisecond)
	sendRecoverySegment(p, streams, stats, "in", 5, makeRecoveryFrame(12, recoveryBody(6, 30)), 2100*time.Millisecond)
	if stats.Frames != 3 || stats.KCPFragmentsDiscarded != 1 || stats.ResyncSuccesses != 1 || stats.BadCRC != 0 {
		t.Fatalf("a message missing a fragment must be dropped whole: %+v", *stats)
	}
}

func TestKCPStreamTableIsBounded(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	frame := makeTransportTestFrame(false, false)
	for conv := uint32(0); conv < MaxKCPStreams+2; conv++ {
		ConsumeDatagram(makeKCPSegmentForTest(conv, 0, frame), "in", transportGapBase.Add(time.Duration(conv)*time.Second), streams, p, stats)
	}
	if len(streams) != MaxKCPStreams || stats.KCPStreamsEvicted != 2 {
		t.Fatalf("streams=%d evicted=%d", len(streams), stats.KCPStreamsEvicted)
	}
	if _, kept := streams["00000000/in"]; kept {
		t.Fatal("the least recently active stream was not evicted")
	}
}

// buildRecoveryStream concatenates frames with outbound sequences first.. and cuts the bytes into segments.
func buildRecoveryStream(first uint32, count, segmentSize int) (frames [][]byte, segments [][]byte, starts []int) {
	var stream []byte
	for i := 0; i < count; i++ {
		frame := makeRecoveryFrame(first+uint32(i), recoveryBody(i, 30+(i*17)%70))
		starts = append(starts, len(stream))
		frames = append(frames, frame)
		stream = append(stream, frame...)
	}
	for offset := 0; offset < len(stream); offset += segmentSize {
		end := offset + segmentSize
		if end > len(stream) {
			end = len(stream)
		}
		segments = append(segments, stream[offset:end])
	}
	return frames, segments, starts
}

func TestResyncAfterMidFrameGapFindsNextFrame(t *testing.T) {
	const segmentSize = 50
	frames, segments, starts := buildRecoveryStream(100, 12, segmentSize)
	drop := 5
	lostFrom, lostTo := drop*segmentSize, (drop+1)*segmentSize
	if !(starts[3] < lostFrom && lostFrom < starts[3]+len(frames[3])) {
		// The test needs the dropped segment to begin inside a frame.
		t.Fatalf("fixture does not start the gap mid-frame: lost %d..%d starts %v", lostFrom, lostTo, starts)
	}
	lost := 0
	for i, start := range starts {
		if start < lostTo && start+len(frames[i]) > lostFrom {
			lost++
		}
	}
	p, streams, stats := newTransportGapHarness()
	for i, payload := range segments {
		if i == drop {
			continue
		}
		sendRecoverySegment(p, streams, stats, "out", uint32(i), payload, time.Duration(i)*10*time.Millisecond)
	}
	sendRecoverySegment(p, streams, stats, "out", uint32(len(segments)), makeRecoveryFrame(112, recoveryBody(99, 20)), 3*time.Second)
	if want := len(frames) - lost + 1; stats.Frames != want || stats.BadCRC != 0 || stats.ResyncSuccesses != 1 {
		t.Fatalf("frames=%d want %d (lost %d): %+v", stats.Frames, want, lost, *stats)
	}
	health := directionHealth(t, p, "out")
	if health.EstimatedLostFrames != uint64(lost) || health.LostFramesUnknown != 0 || health.DiscardedBytes == 0 {
		t.Fatalf("outbound sequence evidence = %+v, want %d lost frames", health, lost)
	}
}

func TestResyncSkipsNoiseBadCRCAndIncompleteCandidates(t *testing.T) {
	stats := &CaptureStats{}
	valid := makeRecoveryFrame(5, recoveryBody(1, 40))
	bad := makeRecoveryFrame(6, recoveryBody(2, 40))
	bad[20] ^= 0xFF // body changed after the CRC was computed
	// No noise byte is a plausible flag byte, so no candidate starts inside it.
	noise := []byte{0x00, 0x13, 0x77, 0x09, 0x91, 0x0A, 0x05, 0x00, 0xFE, 0x3C}
	buffer := append(append(append([]byte(nil), noise...), bad...), valid...)
	offset, found, _ := FindResyncFrame(buffer, nil, stats)
	if !found || offset != len(noise)+len(bad) {
		t.Fatalf("offset=%d found=%v, want %d", offset, found, len(noise)+len(bad))
	}

	// A plausible candidate whose declared length has not arrived must not hold back a later valid frame.
	incomplete := []byte{0xF0, 0x0F, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	buffer = append(append([]byte(nil), incomplete...), valid...)
	offset, found, _ = FindResyncFrame(buffer, nil, stats)
	if !found || offset != len(incomplete) {
		t.Fatalf("incomplete candidate held back the valid frame: offset=%d found=%v", offset, found)
	}

	// Without a valid frame, bytes from the first incomplete candidate are kept for more data.
	buffer = append(append([]byte(nil), noise...), incomplete...)
	_, found, discardable := FindResyncFrame(buffer, nil, stats)
	if found || discardable != len(noise) {
		t.Fatalf("found=%v discardable=%d, want %d", found, discardable, len(noise))
	}

	// Truncated data never validates.
	_, found, _ = FindResyncFrame(valid[:len(valid)-1], nil, stats)
	if found {
		t.Fatal("a truncated frame validated")
	}
}

func TestResyncReportsUnrecoverableAfterScanBudgetThenRecovers(t *testing.T) {
	p, _, stats := newTransportGapHarness()
	state := &Stream{key: "00000009/in", direction: "in", Pending: make(map[uint32]segment)}
	p.kcpStreams = map[string]*Stream{state.key: state}
	streams := p.kcpStreams
	beginStreamRecovery(state, kcpSkipGapTimeout, 0, 1, transportGapBase, p, stats)
	random := rand.New(rand.NewSource(20261104))
	chunk := make([]byte, 60000)
	for sent := 0; sent <= maxResyncScanBytes+3*len(chunk); sent += len(chunk) {
		random.Read(chunk)
		processApplicationData(append([]byte(nil), chunk...), transportGapBase.Add(time.Second), "in", streams, state, p, stats)
		if len(state.applicationBuffer) > MaxResyncRetainedBytes {
			t.Fatalf("resync buffer %d exceeds %d", len(state.applicationBuffer), MaxResyncRetainedBytes)
		}
	}
	if stats.ResyncFailures != 1 || stats.Frames != 0 || stats.ResyncCandidatesChecked == 0 {
		t.Fatalf("noise must exhaust the scan budget without frames: %+v", *stats)
	}
	if health := directionHealth(t, p, "in"); health.State != TransportUnrecoverable || health.Unrecovered != 1 {
		t.Fatalf("health after the budget = %+v", health)
	}
	// Valid frames keep arriving; the bounded search window slides over the noise until it reaches them.
	for i := 0; i < 400 && stats.ResyncSuccesses == 0; i++ {
		processApplicationData(makeRecoveryFrame(uint32(i), recoveryBody(i, 400)), transportGapBase.Add(2*time.Second), "in", streams, state, p, stats)
	}
	if stats.ResyncSuccesses != 1 || stats.Frames == 0 {
		t.Fatalf("later frames must still recover the stream: %+v", *stats)
	}
	if health := directionHealth(t, p, "in"); health.State != TransportRecovered {
		t.Fatalf("health after late recovery = %+v", health)
	}
	if snapshot := p.DamageStatisticsSnapshot(); snapshot.Integrity.Status != IntegrityUnreliable ||
		!containsRisk(snapshot.Integrity.Risks, riskTransportUnrecovered) {
		t.Fatalf("an episode that once failed stays unrecovered in integrity: %+v", snapshot.Integrity)
	}
}

func TestInOrderCRCFailureTriggersResync(t *testing.T) {
	p, streams, stats := newTransportGapHarness()
	bad := makeRecoveryFrame(2, recoveryBody(2, 40))
	bad[30] ^= 0x55
	payload := append(append(append([]byte(nil), makeRecoveryFrame(1, recoveryBody(1, 40))...), bad...), makeRecoveryFrame(3, recoveryBody(3, 40))...)
	sendRecoverySegment(p, streams, stats, "in", 0, payload, 0)
	if stats.Frames != 2 || stats.BadCRC != 1 || stats.FrameDesyncs != 1 || stats.ResyncSuccesses != 1 {
		t.Fatalf("CRC failure handling = %+v", *stats)
	}
	if health := directionHealth(t, p, "in"); health.State != TransportRecovered || health.LastLossReason != frameDesyncCRC {
		t.Fatalf("health after a CRC failure = %+v", health)
	}
	if p.Context.Snapshot().State != ContextSuspect {
		t.Fatal("an inbound framing loss must make inbound context suspect")
	}
}

func TestResyncRequiresAESPaddingWhenSessionKeyKnown(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypt := func(plain []byte) []byte {
		out := make([]byte, len(plain))
		for i := 0; i < len(plain); i += aes.BlockSize {
			block.Encrypt(out[i:i+aes.BlockSize], plain[i:i+aes.BlockSize])
		}
		return out
	}
	badPadding := make([]byte, 16) // last byte 0 is never valid PKCS#7
	goodPadding := append(bytes.Repeat([]byte{0x11}, 12), 4, 4, 4, 4)
	bad := makeRecoveryFrame(1, encrypt(badPadding))
	good := makeRecoveryFrame(2, encrypt(goodPadding))
	if validResyncCandidate(bad, key) {
		t.Fatal("a CRC-valid frame with invalid padding passed with a session key")
	}
	if !validResyncCandidate(good, key) {
		t.Fatal("a properly padded frame failed")
	}
	if !validResyncCandidate(bad, nil) {
		t.Fatal("without a session key only header and CRC can be checked")
	}
	unaligned := makeRecoveryFrame(3, []byte{1, 2, 3})
	if validResyncCandidate(unaligned, key) {
		t.Fatal("a body that is not a whole number of AES blocks passed")
	}
}

func TestOutboundSequenceEvidence(t *testing.T) {
	out := &Stream{direction: "out", Recovery: streamRecovery{sequenceBeforeLoss: 7, hasSequenceBeforeLoss: true}}
	if evidence := outboundSequenceEvidence(out, 10); evidence.status != sequenceEvidenceConsistent || evidence.lostFrames != 2 {
		t.Fatalf("outbound jump of 3 = %+v", evidence)
	}
	if evidence := outboundSequenceEvidence(out, 7); evidence.status != sequenceEvidenceInconsistent {
		t.Fatalf("outbound repeat = %+v", evidence)
	}
	if evidence := outboundSequenceEvidence(out, 7+maxOutboundSequenceJump+1); evidence.status != sequenceEvidenceInconsistent {
		t.Fatalf("outbound reset-sized jump = %+v", evidence)
	}
	in := &Stream{direction: "in", Recovery: streamRecovery{sequenceBeforeLoss: 7, hasSequenceBeforeLoss: true}}
	if evidence := outboundSequenceEvidence(in, 8); evidence.status != sequenceEvidenceUnavailable || evidence.lostFrames != -1 {
		t.Fatalf("inbound sequences must not be used: %+v", evidence)
	}
}

func containsRisk(risks []string, risk string) bool {
	for _, candidate := range risks {
		if candidate == risk {
			return true
		}
	}
	return false
}
