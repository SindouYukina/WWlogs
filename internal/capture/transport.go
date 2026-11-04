package capture

import (
	"crypto/aes"
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

// KCP delivery and application-frame recovery.
//
// Each (conversation, direction) KCP stream delivers PUSH segments strictly in sequence order. A missing sequence is
// first waited on, because a captured retransmission or reordering usually fills it. When waiting can no longer help
// (the gap outlived kcpGapSkipTimeout, the pending buffer reached its bound, or the capture ended), the stream skips
// to the next captured sequence. Skipped data is lost, never replaced: the partial frame and partial fragment message
// before the gap are discarded, the loss is reported to the processor, and the application stream searches for the
// next complete, CRC-valid frame before it parses again (resynchronizeStream).
//
// Thresholds come from the archived captures (research note transport-recovery-backlog, section 5):
//   - Captured retransmissions on live connections arrived at most 1.18 s after the first copy (p99 0.83 s); filled
//     reorder gaps waited at most 72 ms. Two seconds therefore covers every observed refill with margin.
//   - Segment payloads were at most 976 bytes and a stream carried at most 246 segments in one second, so two seconds
//     of waiting holds about 480 segments; the pending bounds below are roughly eight times that.
//   - Captures used at most four KCP streams (two conversations).
const (
	kcpGapSkipTimeout     = 2 * time.Second
	maxPendingKCPSegments = 4096
	maxPendingKCPBytes    = 4 << 20
	maxKCPStreams         = 16
)

// Frame resynchronization bounds. Searches found the next frame within a few hundred bytes in offline fault
// injection; these limits only stop pathological input from consuming unbounded CPU or memory.
const (
	// maxResyncScanBytes is how many bytes one recovery episode may discard before the stream is reported
	// unrecoverable. The search continues afterwards within maxResyncRetainedBytes, so a later frame still recovers it.
	maxResyncScanBytes = 1 << 20
	// maxResyncCandidatesPerPass and maxResyncCRCBytesPerPass bound the CRC work of one search pass.
	maxResyncCandidatesPerPass = 1024
	maxResyncCRCBytesPerPass   = 4 << 20
	// maxResyncRetainedBytes bounds the buffer held while searching: one maximal frame plus one more. Because older
	// bytes are dropped as new ones arrive, a search over noise reaches newly arriving frames within this many bytes
	// of input even when the per-pass budget stops each pass early.
	maxResyncRetainedBytes = 2 * maxApplicationFrameLength
)

const maxApplicationFrameLength = int(^uint16(0)) + 3

// Reasons a stream skipped a KCP gap.
const (
	kcpSkipGapTimeout   = "gap_timeout"
	kcpSkipPendingLimit = "pending_limit"
	kcpSkipCaptureEnd   = "capture_end"
	// kcpLossStreamEvicted: a stream left the bounded table while data still waited behind its gap.
	kcpLossStreamEvicted = "stream_evicted"
)

type segment struct {
	data      []byte
	fragments byte
	timestamp time.Time
}

type stream struct {
	key               string
	direction         string
	next              uint32
	initialized       bool
	pending           map[uint32]segment
	pendingBytes      int
	assembling        [][]byte
	assemblingAt      time.Time
	want              byte
	applicationBuffer []byte
	applicationSpans  []applicationSpan
	// gapOpen is set while a later segment waits for the missing sequence next; gapSince is the capture time of the
	// segment that first revealed the gap.
	gapOpen  bool
	gapSince time.Time
	// lastDeliveredAt is the capture time of the last segment delivered in order; a loss begins after it.
	lastDeliveredAt time.Time
	lastActivityAt  time.Time
	// lastFrameSequence is the header sequence of the last valid frame parsed from this stream.
	lastFrameSequence uint32
	hasFrameSequence  bool
	pendingCRCCensus  []int
	recovery          streamRecovery
}

// streamRecovery is the recovery episode of one stream. An episode begins when data is known lost (a skipped gap or
// a framing failure) and ends when a valid frame is found again.
type streamRecovery struct {
	active bool
	// failed is set once the episode discarded maxResyncScanBytes without finding a frame.
	failed          bool
	lossID          uint64
	reason          string
	discardedBytes  int
	skippedSegments int
	// sequenceBeforeLoss is the last frame sequence parsed before the loss, the reference for the outbound check.
	sequenceBeforeLoss    uint32
	hasSequenceBeforeLoss bool
	episodes              uint64
}

// kcpSequenceBefore compares KCP sequence numbers with 32-bit wraparound, as KCP itself does.
func kcpSequenceBefore(a, b uint32) bool {
	return int32(a-b) < 0
}

func consumeDatagram(payload []byte, direction string, capturedAt time.Time, streams map[string]*stream, processor *processor, stats *captureStats) {
	processor.kcpStreams = streams
	if capturedAt.After(stats.transportClock) {
		stats.transportClock = capturedAt
	}
	skipTimedOutGaps(stats.transportClock, streams, processor, stats)
	hasKCP := false
	for len(payload) >= 24 {
		dataLen := int(binary.LittleEndian.Uint32(payload[20:24]))
		cmd := payload[4]
		if cmd != 81 && cmd != 82 && cmd != 83 && cmd != 84 {
			if !hasKCP {
				stats.nonKCPUDP++
			} else {
				stats.invalidKCP++
			}
			return
		}
		if dataLen < 0 || 24+dataLen > len(payload) {
			stats.invalidKCP++
			return
		}
		hasKCP = true
		conv := binary.LittleEndian.Uint32(payload[:4])
		fragments := payload[5]
		seq := binary.LittleEndian.Uint32(payload[12:16])
		data := append([]byte(nil), payload[24:24+dataLen]...)
		payload = payload[24+dataLen:]
		if cmd != 81 { // KCP PUSH
			stats.kcpControl++
			continue
		}
		stats.kcpPush++
		state := streamFor(fmt.Sprintf("%08x/%s", conv, direction), direction, capturedAt, streams, processor, stats)
		acceptKCPSegment(state, seq, segment{data: data, fragments: fragments, timestamp: capturedAt}, streams, processor, stats)
	}
	if !hasKCP && len(payload) > 0 {
		stats.nonKCPUDP++
	}
}

// streamFor returns the stream for key, creating it and evicting the least recently active stream past maxKCPStreams.
func streamFor(key, direction string, at time.Time, streams map[string]*stream, processor *processor, stats *captureStats) *stream {
	state, ok := streams[key]
	if !ok {
		if len(streams) >= maxKCPStreams {
			evictIdlestStream(streams, processor, stats)
		}
		state = &stream{key: key, direction: direction, pending: make(map[uint32]segment)}
		streams[key] = state
	}
	if at.After(state.lastActivityAt) {
		state.lastActivityAt = at
	}
	return state
}

func evictIdlestStream(streams map[string]*stream, processor *processor, stats *captureStats) {
	var oldest *stream
	for _, state := range sortedStreams(streams) {
		if oldest == nil || state.lastActivityAt.Before(oldest.lastActivityAt) {
			oldest = state
		}
	}
	if oldest == nil {
		return
	}
	stats.kcpStreamsEvicted++
	stats.kcpEvictedPendingSegments += len(oldest.pending)
	processor.captureLayer.evictedSegments += uint64(len(oldest.pending))
	if oldest.gapOpen {
		closeKCPGap(oldest, processor)
	}
	if len(oldest.pending) > 0 && !oldest.recovery.active {
		// Data waiting behind a gap is discarded with the stream: that is a loss, and it can never recover.
		oldest.recovery.active = true
		oldest.recovery.lossID = processor.beginTransportLoss(oldest.direction, oldest.lastDeliveredAt, oldest.lastActivityAt, kcpLossStreamEvicted, len(oldest.pending))
	}
	if oldest.recovery.active {
		// The episode can never finish; it stays reported as an unrecovered loss.
		processor.failTransportLoss(oldest.recovery.lossID, oldest.direction)
	}
	delete(streams, oldest.key)
}

func sortedStreams(streams map[string]*stream) []*stream {
	list := make([]*stream, 0, len(streams))
	for _, state := range streams {
		list = append(list, state)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].key < list[j].key })
	return list
}

func acceptKCPSegment(state *stream, seq uint32, part segment, streams map[string]*stream, processor *processor, stats *captureStats) {
	if !state.initialized {
		state.initialized = true
		state.next = seq
	}
	if kcpSequenceBefore(seq, state.next) {
		stats.kcpDuplicates++
		return
	}
	if _, exists := state.pending[seq]; exists {
		stats.kcpDuplicates++
		return
	}
	if seq != state.next && (len(state.pending) >= maxPendingKCPSegments || state.pendingBytes+len(part.data) > maxPendingKCPBytes) {
		// The stream cannot hold more while it waits: give up on the missing sequence now.
		stats.kcpPendingOverflow++
		skipKCPGap(state, part.timestamp, kcpSkipPendingLimit, streams, processor, stats)
		if kcpSequenceBefore(seq, state.next) {
			stats.kcpDuplicates++
			return
		}
	}
	if seq != state.next {
		stats.kcpOutOfOrder++
		if !state.gapOpen {
			openKCPGap(state, part.timestamp, processor, stats)
		}
	}
	state.pending[seq] = part
	state.pendingBytes += len(part.data)
	if len(state.pending) > stats.kcpPendingPeak {
		stats.kcpPendingPeak = len(state.pending)
	}
	if seq == state.next && state.gapOpen {
		// The missing sequence arrived: the gap was reordering or a captured retransmission, and nothing was lost.
		stats.kcpGapsFilled++
		if wait := part.timestamp.Sub(state.gapSince); wait > stats.kcpMaxGapWait {
			stats.kcpMaxGapWait = wait
		}
		closeKCPGap(state, processor)
	}
	deliverKCPInOrder(state, streams, processor, stats)
}

func openKCPGap(state *stream, since time.Time, processor *processor, stats *captureStats) {
	state.gapOpen, state.gapSince = true, since
	stats.kcpGapsOpened++
	processor.observeTransportGap(state.key, state.direction, since, true)
}

func closeKCPGap(state *stream, processor *processor) {
	state.gapOpen, state.gapSince = false, time.Time{}
	processor.observeTransportGap(state.key, state.direction, time.Time{}, false)
}

// deliverKCPInOrder delivers every pending segment that continues the stream, then records whether a new gap holds
// later segments back.
func deliverKCPInOrder(state *stream, streams map[string]*stream, processor *processor, stats *captureStats) {
	for {
		part, found := state.pending[state.next]
		if !found {
			break
		}
		delete(state.pending, state.next)
		state.pendingBytes -= len(part.data)
		state.next++
		stats.kcpOrdered++
		if part.timestamp.After(state.lastDeliveredAt) {
			state.lastDeliveredAt = part.timestamp
		}
		if part.fragments > 0 {
			switch {
			case len(state.assembling) == 0:
				state.assembling = append(state.assembling, part.data)
				state.assemblingAt = part.timestamp
			case part.fragments == state.want:
				state.assembling = append(state.assembling, part.data)
			default:
				// The message being assembled cannot complete; this fragment can only begin another one.
				stats.kcpFragmentResets++
				state.assembling = [][]byte{part.data}
				state.assemblingAt = part.timestamp
			}
			state.want = part.fragments - 1
			continue
		}
		if len(state.assembling) > 0 && state.want != 0 {
			// A final fragment while earlier fragments are still expected belongs to an incomplete message.
			stats.kcpFragmentTailDrops++
			state.assembling = nil
			state.assemblingAt = time.Time{}
			continue
		}
		app := part.data
		appTimestamp := part.timestamp
		if len(state.assembling) > 0 {
			state.assembling = append(state.assembling, part.data)
			app = make([]byte, 0)
			for _, fragment := range state.assembling {
				app = append(app, fragment...)
			}
			appTimestamp = state.assemblingAt
			state.assembling = nil
			state.assemblingAt = time.Time{}
		}
		stats.kcpMessages++
		processApplicationData(app, appTimestamp, state.direction, streams, state, processor, stats)
	}
	if len(state.pending) > 0 && !state.gapOpen {
		// Delivery stopped at another missing sequence; it is a new gap, open since its earliest waiting segment.
		openKCPGap(state, earliestPendingTimestamp(state.pending), processor, stats)
	}
}

func earliestPendingTimestamp(pending map[uint32]segment) time.Time {
	var earliest time.Time
	for _, part := range pending {
		if earliest.IsZero() || part.timestamp.Before(earliest) {
			earliest = part.timestamp
		}
	}
	return earliest
}

// skipTimedOutGaps skips every gap that stayed open longer than kcpGapSkipTimeout on the capture clock. Streams are
// visited in key order so a replay always delivers in the same order.
func skipTimedOutGaps(now time.Time, streams map[string]*stream, processor *processor, stats *captureStats) {
	due := false
	for _, state := range streams {
		if state.gapOpen && now.Sub(state.gapSince) >= kcpGapSkipTimeout {
			due = true
			break
		}
	}
	if !due {
		return
	}
	for _, state := range sortedStreams(streams) {
		if state.gapOpen && now.Sub(state.gapSince) >= kcpGapSkipTimeout {
			skipKCPGap(state, now, kcpSkipGapTimeout, streams, processor, stats)
		}
	}
}

// finishTransport ends a capture: no retransmission can arrive any more, so every open gap is skipped and its
// waiting data delivered. A stream still searching for a frame afterwards ends its episode unrecovered.
func finishTransport(streams map[string]*stream, processor *processor, stats *captureStats) {
	for _, state := range sortedStreams(streams) {
		if state.gapOpen {
			skipKCPGap(state, stats.transportClock, kcpSkipCaptureEnd, streams, processor, stats)
		}
	}
	for _, state := range sortedStreams(streams) {
		if state.recovery.active && !state.recovery.failed {
			state.recovery.failed = true
			stats.resyncFailures++
			processor.failTransportLoss(state.recovery.lossID, state.direction)
		}
	}
}

// skipKCPGap gives up on the missing sequences before the earliest pending segment. The skipped data is lost: the
// partial fragment message and the partial application frame before the gap are discarded, the processor records
// the loss, and the stream resynchronizes before it parses frames again.
func skipKCPGap(state *stream, at time.Time, reason string, streams map[string]*stream, processor *processor, stats *captureStats) {
	if len(state.pending) == 0 {
		if state.gapOpen {
			closeKCPGap(state, processor)
		}
		return
	}
	first, found := uint32(0), false
	for seq := range state.pending {
		if !found || kcpSequenceBefore(seq, first) {
			first, found = seq, true
		}
	}
	skipped := int(first - state.next)
	stats.kcpGapsSkipped++
	stats.kcpSegmentsSkipped += skipped
	state.next = first
	if state.gapOpen {
		closeKCPGap(state, processor)
	}
	if len(state.assembling) > 0 {
		stats.kcpFragmentsDiscarded += len(state.assembling)
		state.assembling, state.assemblingAt = nil, time.Time{}
	}
	discarded := len(state.applicationBuffer)
	state.applicationBuffer, state.applicationSpans = nil, nil
	beginStreamRecovery(state, reason, discarded, skipped, at, processor, stats)
	deliverKCPInOrder(state, streams, processor, stats)
}

// beginStreamRecovery opens (or extends) the stream's recovery episode for a loss detected at at.
func beginStreamRecovery(state *stream, reason string, discardedBytes, skippedSegments int, at time.Time, processor *processor, stats *captureStats) {
	stats.resyncBytesDiscarded += discardedBytes
	if state.recovery.active {
		// A further loss before the stream recovered from the previous one belongs to the same episode.
		state.recovery.discardedBytes += discardedBytes
		state.recovery.skippedSegments += skippedSegments
		processor.extendTransportLoss(state.recovery.lossID, skippedSegments)
		return
	}
	from := state.lastDeliveredAt
	if from.IsZero() || from.After(at) {
		from = at
	}
	stats.resyncAttempts++
	state.recovery = streamRecovery{
		active: true, reason: reason, discardedBytes: discardedBytes, skippedSegments: skippedSegments,
		sequenceBeforeLoss: state.lastFrameSequence, hasSequenceBeforeLoss: state.hasFrameSequence,
		episodes: state.recovery.episodes + 1,
	}
	state.recovery.lossID = processor.beginTransportLoss(state.direction, from, at, reason, skippedSegments)
}

func processApplicationData(data []byte, capturedAt time.Time, direction string, streams map[string]*stream, state *stream, processor *processor, stats *captureStats) {
	if len(data) == 0 {
		return
	}
	state.applicationBuffer = append(state.applicationBuffer, data...)
	if spanCount := len(state.applicationSpans); spanCount > 0 && state.applicationSpans[spanCount-1].timestamp.Equal(capturedAt) {
		state.applicationSpans[spanCount-1].length += len(data)
	} else {
		state.applicationSpans = append(state.applicationSpans, applicationSpan{length: len(data), timestamp: capturedAt})
	}

	previousSession := processor.sessionGeneration
	for {
		if state.recovery.active && !resynchronizeStream(state, processor, stats) {
			break
		}
		result := parseFrameStream(state.applicationBuffer, direction, state.applicationSpans, processor, state)
		stats.frames += result.frames
		stats.badCRC += result.badCRC
		stats.malformed += result.malformed
		discardApplicationPrefix(state, result.consumed)
		if !result.desync {
			break
		}
		// The framing failed inside data that arrived in order: the boundary is lost, so resynchronize from here.
		stats.frameDesyncs++
		beginStreamRecovery(state, result.desyncReason, 0, 0, applicationTimestampAt(state.applicationSpans, 0), processor, stats)
	}
	if len(state.applicationBuffer) > maxApplicationFrameLength-1 && !state.recovery.active {
		state.applicationBuffer = nil
		state.applicationSpans = nil
		stats.malformed++
	}
	if processor.sessionGeneration != previousSession {
		clearOtherApplicationBuffers(streams, state)
	}
}

func discardApplicationPrefix(state *stream, count int) {
	if count <= 0 {
		return
	}
	state.applicationBuffer = append([]byte(nil), state.applicationBuffer[count:]...)
	state.applicationSpans = consumeApplicationSpans(state.applicationSpans, count)
}

// resynchronizeStream searches the stream's buffer for the first complete frame whose header is plausible, whose CRC
// matches, and whose body is a valid AES block sequence under the current session key. It never waits on a candidate
// whose declared length has not fully arrived when a later candidate already validates. Bytes before the first
// position that may still start a frame are discarded. It returns true when parsing may continue.
func resynchronizeStream(state *stream, processor *processor, stats *captureStats) bool {
	buffer := state.applicationBuffer
	offset, found, discardable := findResyncFrame(buffer, processor.session, stats)
	if found {
		state.recovery.discardedBytes += offset
		stats.resyncBytesDiscarded += offset
		at := applicationTimestampAt(state.applicationSpans, offset)
		sequence := binary.LittleEndian.Uint32(buffer[offset+4 : offset+8])
		discardApplicationPrefix(state, offset)
		evidence := outboundSequenceEvidence(state, sequence)
		stats.resyncSuccesses++
		processor.endTransportLoss(state.recovery.lossID, state.direction, at, transportLossDetails{
			DiscardedBytes: state.recovery.discardedBytes, SkippedSegments: state.recovery.skippedSegments,
			SequenceEvidence: evidence.status, EstimatedLostFrames: evidence.lostFrames, Recovered: true,
			Unrecoverable: state.recovery.failed,
		})
		state.recovery.active, state.recovery.failed = false, false
		return true
	}
	if discardable > 0 {
		state.recovery.discardedBytes += discardable
		stats.resyncBytesDiscarded += discardable
		discardApplicationPrefix(state, discardable)
	}
	if excess := len(state.applicationBuffer) - maxResyncRetainedBytes; excess > 0 {
		state.recovery.discardedBytes += excess
		stats.resyncBytesDiscarded += excess
		discardApplicationPrefix(state, excess)
	}
	if !state.recovery.failed && state.recovery.discardedBytes > maxResyncScanBytes {
		state.recovery.failed = true
		stats.resyncFailures++
		processor.failTransportLoss(state.recovery.lossID, state.direction)
	}
	return false
}

// findResyncFrame returns the offset of the first validated frame. discardable is how many leading bytes are proven
// not to start a frame: every offset before it either fails the header checks or holds a complete candidate whose
// CRC or body failed. A candidate that has not fully arrived keeps the bytes from its offset.
func findResyncFrame(buffer []byte, sessionKey []byte, stats *captureStats) (offset int, found bool, discardable int) {
	firstIncomplete := -1
	candidates, crcBytes := 0, 0
	stop := len(buffer) - 3 // the flag byte frame[3] must be present
	for off := 0; off < stop; off++ {
		if !plausibleFrameFlags(buffer[off+3]) {
			continue
		}
		total := int(binary.LittleEndian.Uint16(buffer[off:off+2])) + 3
		if total < 14 {
			continue
		}
		if off+total > len(buffer) {
			if firstIncomplete < 0 {
				firstIncomplete = off
			}
			continue
		}
		if candidates >= maxResyncCandidatesPerPass || crcBytes+total > maxResyncCRCBytesPerPass {
			stats.resyncBudgetStops++
			stop = off
			break
		}
		candidates++
		crcBytes += total
		stats.resyncCandidatesChecked++
		if validResyncCandidate(buffer[off:off+total], sessionKey) {
			return off, true, off
		}
	}
	discardable = stop
	if discardable < 0 {
		discardable = 0
	}
	if firstIncomplete >= 0 && firstIncomplete < discardable {
		discardable = firstIncomplete
	}
	return 0, false, discardable
}

// plausibleFrameFlags accepts the message types and the compression flag seen in every archived frame: types 1, 2,
// and 4, optionally with 0x10 (101,113 frames in 21 captures; no other value occurred at a frame boundary).
func plausibleFrameFlags(flags byte) bool {
	switch flags &^ 0x10 {
	case 1, 2, 4:
		return true
	}
	return false
}

// validResyncCandidate checks one complete frame candidate: header layout, CRC, and, when a session key is known,
// that a non-empty body is a whole number of AES blocks whose last block carries valid PKCS#7 padding.
func validResyncCandidate(frame []byte, sessionKey []byte) bool {
	total := len(frame)
	offset := 8
	if frame[3]&0x0f != 4 {
		offset += 2
	}
	if offset+6 > total {
		return false
	}
	bodyStart := offset + 6
	if frame[3]&0x10 != 0 {
		bodyStart += 4
		if bodyStart > total {
			return false
		}
	}
	if crc32IEEE(frame[bodyStart:]) != binary.LittleEndian.Uint32(frame[offset+2:offset+6]) {
		return false
	}
	body := frame[bodyStart:]
	if len(sessionKey) != 32 || len(body) == 0 {
		return true
	}
	if len(body)%aes.BlockSize != 0 {
		return false
	}
	block, err := aes.NewCipher(sessionKey)
	if err != nil {
		return false
	}
	var last [aes.BlockSize]byte
	block.Decrypt(last[:], body[len(body)-aes.BlockSize:])
	defer clear(last[:])
	pad := int(last[aes.BlockSize-1])
	if pad == 0 || pad > aes.BlockSize {
		return false
	}
	for _, b := range last[aes.BlockSize-pad:] {
		if int(b) != pad {
			return false
		}
	}
	return true
}

// Outbound frame-sequence evidence for a recovery. The client numbers its frames with one counter that rose by
// exactly one between every pair of adjacent frames in 21 archived captures, so the jump across a gap counts the
// frames lost. Inbound sequences rose by one only 99.6% of the time and are not used.
const (
	sequenceEvidenceConsistent   = "consistent"
	sequenceEvidenceInconsistent = "inconsistent"
	sequenceEvidenceUnavailable  = "unavailable"
	// maxOutboundSequenceJump bounds a jump still read as lost frames rather than a counter reset.
	maxOutboundSequenceJump = 1 << 20
)

type sequenceEvidence struct {
	status     string
	lostFrames int // -1 when unknown
}

func outboundSequenceEvidence(state *stream, sequence uint32) sequenceEvidence {
	if state.direction != "out" || !state.recovery.hasSequenceBeforeLoss {
		return sequenceEvidence{status: sequenceEvidenceUnavailable, lostFrames: -1}
	}
	jump := sequence - state.recovery.sequenceBeforeLoss
	if jump == 0 || jump > maxOutboundSequenceJump {
		return sequenceEvidence{status: sequenceEvidenceInconsistent, lostFrames: -1}
	}
	return sequenceEvidence{status: sequenceEvidenceConsistent, lostFrames: int(jump - 1)}
}

func clearOtherApplicationBuffers(streams map[string]*stream, current *stream) {
	for _, state := range streams {
		if state != current {
			state.applicationBuffer = nil
			state.applicationSpans = nil
		}
	}
}

func consumeApplicationSpans(spans []applicationSpan, consumed int) []applicationSpan {
	for consumed > 0 && len(spans) > 0 {
		if consumed >= spans[0].length {
			consumed -= spans[0].length
			spans = spans[1:]
			continue
		}
		spans[0].length -= consumed
		consumed = 0
	}
	return spans
}

// kcpTransportHealth summarizes KCP delivery at one moment.
type kcpTransportHealth struct {
	Streams         int
	OpenGaps        int
	Recovering      int
	PendingSegments int
	PendingBytes    int
	OldestGapSince  time.Time
}

func summarizeKCPTransport(streams map[string]*stream) kcpTransportHealth {
	var health kcpTransportHealth
	for _, state := range streams {
		health.Streams++
		health.PendingSegments += len(state.pending)
		health.PendingBytes += state.pendingBytes
		if state.recovery.active {
			health.Recovering++
		}
		if !state.gapOpen {
			continue
		}
		health.OpenGaps++
		if health.OldestGapSince.IsZero() || state.gapSince.Before(health.OldestGapSince) {
			health.OldestGapSince = state.gapSince
		}
	}
	return health
}

// transportHealthLine formats KCP delivery counters for development diagnostics. It carries counts and one capture
// time only: no conversation IDs, sequences, or payload bytes.
func transportHealthLine(streams map[string]*stream, stats captureStats) string {
	health := summarizeKCPTransport(streams)
	oldest := "none"
	if !health.OldestGapSince.IsZero() {
		oldest = health.OldestGapSince.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("transport_health kcp_streams=%d kcp_open_gaps=%d kcp_oldest_open_gap=%s kcp_pending_segments=%d kcp_pending_bytes=%d kcp_pending_peak=%d kcp_pending_overflow=%d kcp_out_of_order=%d kcp_duplicates=%d kcp_gaps_opened=%d kcp_gaps_filled=%d kcp_max_gap_wait=%s kcp_gaps_skipped=%d kcp_segments_skipped=%d kcp_fragment_resets=%d kcp_fragment_tail_drops=%d kcp_fragments_discarded=%d kcp_streams_evicted=%d frame_desyncs=%d resync_attempts=%d resync_successes=%d resync_failures=%d resync_recovering=%d resync_bytes_discarded=%d resync_candidates=%d resync_budget_stops=%d",
		health.Streams, health.OpenGaps, oldest, health.PendingSegments, health.PendingBytes, stats.kcpPendingPeak, stats.kcpPendingOverflow,
		stats.kcpOutOfOrder, stats.kcpDuplicates, stats.kcpGapsOpened, stats.kcpGapsFilled, stats.kcpMaxGapWait,
		stats.kcpGapsSkipped, stats.kcpSegmentsSkipped, stats.kcpFragmentResets, stats.kcpFragmentTailDrops, stats.kcpFragmentsDiscarded,
		stats.kcpStreamsEvicted, stats.frameDesyncs, stats.resyncAttempts, stats.resyncSuccesses, stats.resyncFailures, health.Recovering,
		stats.resyncBytesDiscarded, stats.resyncCandidatesChecked, stats.resyncBudgetStops)
}
