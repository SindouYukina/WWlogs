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
	var stdout, diagnostic bytes.Buffer
	processor := newProcessor(nil, &stdout, &diagnostic, nil)
	streams := make(map[string]*stream)
	stats := captureStats{}
	firstTime := time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC)
	secondTime := firstTime.Add(time.Second)

	consumeDatagram(makeKCPSegmentForTest(9, 0, frame[:cut]), "in", firstTime, streams, processor, &stats)
	if stats.frames != 0 {
		t.Fatalf("frames after first partial KCP message = %d, want 0", stats.frames)
	}
	state := streams["00000009/in"]
	if state == nil {
		t.Fatal("stream was not created after receiving a KCP message")
	}
	if !bytes.Equal(state.applicationBuffer, frame[:cut]) {
		t.Fatalf("buffer after first partial KCP message = %x, want %x", state.applicationBuffer, frame[:cut])
	}

	consumeDatagram(makeKCPSegmentForTest(9, 1, frame[cut:]), "in", secondTime, streams, processor, &stats)
	if stats.frames != 1 || stats.badCRC != 0 || stats.malformed != 0 {
		t.Fatalf("transport stats = %+v, want one valid frame and no errors", stats)
	}
	if len(state.applicationBuffer) != 0 || len(state.applicationSpans) != 0 {
		t.Fatalf("application tail was not consumed: bytes=%d spans=%d", len(state.applicationBuffer), len(state.applicationSpans))
	}
}

func TestParseFramesChecksCompressedCRCAfterLengthPrefix(t *testing.T) {
	frame := makeTransportTestFrame(true, false)
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	span := []applicationSpan{{length: len(frame), timestamp: time.Unix(1, 0)}}

	frames, badCRC, malformed, consumed := parseFrames(frame, "in", span, p)
	if frames != 1 || badCRC != 0 || malformed != 0 || consumed != len(frame) {
		t.Fatalf("parse result = frames:%d crc:%d malformed:%d consumed:%d", frames, badCRC, malformed, consumed)
	}
}

func TestParseFramesRejectsCompressedCRCIncludingLengthPrefix(t *testing.T) {
	frame := makeTransportTestFrame(true, true)
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	span := []applicationSpan{{length: len(frame)}}

	frames, badCRC, malformed, consumed := parseFrames(frame, "in", span, p)
	if frames != 0 || badCRC != 1 || malformed != 0 || consumed != len(frame) {
		t.Fatalf("parse result = frames:%d crc:%d malformed:%d consumed:%d", frames, badCRC, malformed, consumed)
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

func TestNewSessionKeyClearsOtherStreamApplicationTails(t *testing.T) {
	current := &stream{applicationBuffer: []byte{1, 2}, applicationSpans: []applicationSpan{{length: 2}}}
	other := &stream{applicationBuffer: []byte{3, 4}, applicationSpans: []applicationSpan{{length: 2}}}
	clearOtherApplicationBuffers(map[string]*stream{"current": current, "other": other}, current)
	if len(current.applicationBuffer) != 2 || len(current.applicationSpans) != 1 {
		t.Fatalf("current stream tail was cleared: %+v", current)
	}
	if len(other.applicationBuffer) != 0 || len(other.applicationSpans) != 0 {
		t.Fatalf("other stream retained an old-session tail: %+v", other)
	}
}
