package capture

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
	"time"
)

func makeTransportTestFrame(compressed, includeLengthPrefixInCRC bool) []byte {
	body := []byte{0x31, 0x42, 0x53, 0x64}
	transmittedBody := body
	if compressed {
		transmittedBody = make([]byte, 4+len(body))
		binary.LittleEndian.PutUint32(transmittedBody[:4], uint32(len(body)))
		copy(transmittedBody[4:], body)
	}
	frame := make([]byte, 14+len(transmittedBody))
	binary.LittleEndian.PutUint16(frame[:2], uint16(len(frame)-3))
	frame[3] = 4
	if compressed {
		frame[3] |= 0x10
	}
	binary.LittleEndian.PutUint32(frame[4:8], 7)
	binary.LittleEndian.PutUint16(frame[8:10], 65000)
	crcBody := transmittedBody
	if compressed && !includeLengthPrefixInCRC {
		crcBody = transmittedBody[4:]
	}
	binary.LittleEndian.PutUint32(frame[10:14], crc32.ChecksumIEEE(crcBody))
	copy(frame[14:], transmittedBody)
	return frame
}

func makeKCPSegmentForTest(conv, sequence uint32, payload []byte) []byte {
	segment := make([]byte, 24+len(payload))
	binary.LittleEndian.PutUint32(segment[:4], conv)
	segment[4] = 81
	binary.LittleEndian.PutUint32(segment[12:16], sequence)
	binary.LittleEndian.PutUint32(segment[20:24], uint32(len(payload)))
	copy(segment[24:], payload)
	return segment
}

func TestConsumeDatagramReassemblesApplicationFrameAcrossKCPMessages(t *testing.T) {
	frame := makeTransportTestFrame(false, false)
	cut := len(frame) / 2
	var diagnostic bytes.Buffer
	processor := NewProcessor(nil, &diagnostic)
	streams := make(map[string]*Stream)
	stats := CaptureStats{}
	firstTime := time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC)
	secondTime := firstTime.Add(time.Second)

	ConsumeDatagram(makeKCPSegmentForTest(9, 0, frame[:cut]), "in", firstTime, streams, processor, &stats)
	if stats.Frames != 0 {
		t.Fatalf("frames after first partial KCP message = %d, want 0", stats.Frames)
	}
	state := streams["00000009/in"]
	if state == nil {
		t.Fatal("stream was not created after receiving a KCP message")
	}
	if !bytes.Equal(state.applicationBuffer, frame[:cut]) {
		t.Fatalf("buffer after first partial KCP message = %x, want %x", state.applicationBuffer, frame[:cut])
	}

	ConsumeDatagram(makeKCPSegmentForTest(9, 1, frame[cut:]), "in", secondTime, streams, processor, &stats)
	if stats.Frames != 1 || stats.BadCRC != 0 || stats.Malformed != 0 {
		t.Fatalf("transport stats = %+v, want one valid frame and no errors", stats)
	}
	if len(state.applicationBuffer) != 0 || len(state.applicationSpans) != 0 {
		t.Fatalf("application tail was not consumed: bytes=%d spans=%d", len(state.applicationBuffer), len(state.applicationSpans))
	}
}

func TestParseFramesChecksCompressedCRCAfterLengthPrefix(t *testing.T) {
	frame := makeTransportTestFrame(true, false)
	p := NewProcessor(nil, &bytes.Buffer{})
	span := []applicationSpan{{length: len(frame), timestamp: time.Unix(1, 0)}}

	frames, badCRC, malformed, consumed := parseFrames(frame, "in", span, p)
	if frames != 1 || badCRC != 0 || malformed != 0 || consumed != len(frame) {
		t.Fatalf("parse result = frames:%d crc:%d malformed:%d consumed:%d", frames, badCRC, malformed, consumed)
	}
}

// A CRC mismatch means the frame boundary can no longer be trusted: parsing stops one byte past the candidate and
// reports a desync so the stream resynchronizes (Phase 1B; earlier the whole declared length was skipped).
func TestParseFramesRejectsCompressedCRCIncludingLengthPrefix(t *testing.T) {
	frame := makeTransportTestFrame(true, true)
	p := NewProcessor(nil, &bytes.Buffer{})
	span := []applicationSpan{{length: len(frame)}}

	result := parseFrameStream(frame, "in", span, p, nil)
	if result.frames != 0 || result.badCRC != 1 || result.malformed != 0 || result.consumed != 1 || !result.desync ||
		result.desyncReason != frameDesyncCRC {
		t.Fatalf("parse result = %+v", result)
	}
}

func TestApplicationTimestampUsesFirstByteSegment(t *testing.T) {
	first := time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC)
	second := first.Add(time.Second)
	spans := []applicationSpan{{length: 5, timestamp: first}, {length: 7, timestamp: second}}
	if got := applicationTimestampAt(spans, 0); !got.Equal(first) {
		t.Fatalf("timestamp at frame start = %s, want %s", got, first)
	}
	if got := applicationTimestampAt(spans, 5); !got.Equal(second) {
		t.Fatalf("timestamp after first segment = %s, want %s", got, second)
	}
}

func TestConsumeApplicationSpansRetainsRemainderTimestamp(t *testing.T) {
	first := time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC)
	second := first.Add(time.Second)
	spans := []applicationSpan{{length: 5, timestamp: first}, {length: 7, timestamp: second}}
	remaining := consumeApplicationSpans(spans, 3)
	if len(remaining) != 2 || remaining[0].length != 2 || !remaining[0].timestamp.Equal(first) || remaining[1].length != 7 || !remaining[1].timestamp.Equal(second) {
		t.Fatalf("remaining timestamp spans = %+v", remaining)
	}
}
