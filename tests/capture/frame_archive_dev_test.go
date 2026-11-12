//go:build wwdev

package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestFrameDecodeStatusClassifiesWithoutDecrypting(t *testing.T) {
	tests := []struct {
		name     string
		frame    gameFrame
		internal uint16
		state    protocolState
		want     string
	}{
		{"handshake", gameFrame{commandID: 112, direction: "in", body: []byte{1}}, 0, protocolNone, frameStatusHandshake},
		{"empty body", gameFrame{commandID: 1651, direction: "in"}, 0, protocolCurrent, frameStatusEmptyBody},
		{"implemented through the current protocol", gameFrame{commandID: 26497, direction: "out", body: []byte{1}}, 19470, protocolCurrent, frameStatusImplemented},
		{"undecoded", gameFrame{commandID: 1651, direction: "in", body: []byte{1}}, 0, protocolCurrent, frameStatusUndecoded},
		{"another game version", gameFrame{commandID: 19470, direction: "out", body: []byte{1}}, 0, protocolUnsupported, frameStatusNotCurrentProtocol},
		{"before the version report", gameFrame{commandID: 26497, direction: "out", body: []byte{1}}, 0, protocolPending, frameStatusNotCurrentProtocol},
		{"command 112 outbound is not handshake", gameFrame{commandID: 112, direction: "out", body: []byte{1}}, 0, protocolNone, frameStatusUndecoded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := frameDecodeStatus(tt.frame, tt.internal, tt.state); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArchiveRecordsOrderDigestsAndNoKeyMaterial(t *testing.T) {
	key := bytes.Repeat([]byte{0xA5}, 32)
	body := []byte("ciphertext-bytes-that-are-not-plaintext")
	var out bytes.Buffer
	p := &processor{session: key, frameArchive: &out, sessionNumber: 2}
	stamp := time.Date(2026, 10, 28, 1, 2, 3, 4, time.UTC)

	p.handleFrame(gameFrame{commandID: 1651, direction: "in", sequence: 7, token: 9, body: nil, timestamp: stamp})
	p.archiveFrame(gameFrame{commandID: 1651, direction: "in", sequence: 8, body: body, timestamp: stamp})

	raw := out.String()
	if strings.Contains(raw, hex.EncodeToString(key)) || strings.Contains(raw, string(body)) {
		t.Fatal("archive contains session key or body bytes")
	}

	records := decodeArchiveRecords(t, out.Bytes())
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].FrameOrdinal != 0 || records[1].FrameOrdinal != 1 {
		t.Fatalf("ordinals = %d,%d, want 0,1", records[0].FrameOrdinal, records[1].FrameOrdinal)
	}
	if records[0].DecodeStatus != frameStatusEmptyBody || records[0].BodyLength != 0 {
		t.Fatalf("empty record = %+v", records[0])
	}
	wantDigest := sha256.Sum256(body)
	if records[1].BodySHA256 != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("body digest = %s", records[1].BodySHA256)
	}
	if records[1].SessionNumber != 2 || !records[1].SessionKeyReady || records[1].SchemaVersion != frameArchiveSchemaVersion {
		t.Fatalf("record metadata = %+v", records[1])
	}
}

func TestArchiveCountsWriteErrorsWithoutPanicking(t *testing.T) {
	p := &processor{frameArchive: failingWriter{}}
	p.archiveFrame(gameFrame{commandID: 1651, direction: "in", body: []byte{1}})
	if p.frameArchiveErrors != 1 || p.frameOrdinal != 1 {
		t.Fatalf("errors=%d ordinal=%d, want 1 and 1", p.frameArchiveErrors, p.frameOrdinal)
	}
}
