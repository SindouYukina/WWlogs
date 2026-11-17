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
		readable bool
		state    protocolState
		want     string
	}{
		{"handshake", gameFrame{commandID: cmdProtoKeyResponse, direction: "in", body: []byte{1}}, false, protocolNone, frameStatusHandshake},
		{"empty body", gameFrame{commandID: 1651, direction: "in"}, true, protocolCurrent, frameStatusEmptyBody},
		{"implemented in the current protocol", gameFrame{commandID: cmdCombatSendPackRequest, direction: "out", body: []byte{1}}, true, protocolCurrent, frameStatusImplemented},
		{"undecoded", gameFrame{commandID: 1651, direction: "in", body: []byte{1}}, true, protocolCurrent, frameStatusUndecoded},
		{"another game version", gameFrame{commandID: cmdCombatSendPackRequest, direction: "out", body: []byte{1}}, false, protocolUnsupported, frameStatusNotCurrentProtocol},
		{"before the version report", gameFrame{commandID: cmdCombatSendPackRequest, direction: "out", body: []byte{1}}, false, protocolPending, frameStatusNotCurrentProtocol},
		{"command 112 outbound is not handshake", gameFrame{commandID: cmdProtoKeyResponse, direction: "out", body: []byte{1}}, false, protocolNone, frameStatusUndecoded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := frameDecodeStatus(tt.frame, tt.readable, tt.state); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArchiveRecordsOrderDigestsAndNoKeyMaterial(t *testing.T) {
	key := bytes.Repeat([]byte{0xA5}, 32)
	body := []byte("ciphertext-bytes-that-are-not-plaintext")
	var out bytes.Buffer
	p := &processor{session: key, sessionNumber: 2, dev: processorDiagnostics{frameArchive: &out}}
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
	p := &processor{dev: processorDiagnostics{frameArchive: failingWriter{}}}
	p.archiveFrame(gameFrame{commandID: 1651, direction: "in", body: []byte{1}})
	if p.dev.frameArchiveErrors != 1 || p.dev.frameOrdinal != 1 {
		t.Fatalf("errors=%d ordinal=%d, want 1 and 1", p.dev.frameArchiveErrors, p.dev.frameOrdinal)
	}
}
