//go:build wwdev

package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func decodeArchiveRecords(t *testing.T, data []byte) []frameArchiveRecord {
	t.Helper()
	var records []frameArchiveRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record frameArchiveRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("archive line is not valid JSON: %v", err)
		}
		records = append(records, record)
	}
	return records
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("test write failure") }

func TestFrameDecodeStatusClassifiesWithoutDecrypting(t *testing.T) {
	tests := []struct {
		name     string
		frame    GameFrame
		readable bool
		state    ProtocolState
		want     string
	}{
		{"handshake", GameFrame{CommandID: CmdProtoKeyResponse, Direction: "in", Body: []byte{1}}, false, ProtocolNone, frameStatusHandshake},
		{"empty body", GameFrame{CommandID: 1651, Direction: "in"}, true, ProtocolCurrent, frameStatusEmptyBody},
		{"implemented in the current protocol", GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out", Body: []byte{1}}, true, ProtocolCurrent, frameStatusImplemented},
		{"undecoded", GameFrame{CommandID: 1651, Direction: "in", Body: []byte{1}}, true, ProtocolCurrent, frameStatusUndecoded},
		{"another game version", GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out", Body: []byte{1}}, false, ProtocolUnsupported, frameStatusNotCurrentProtocol},
		{"before the version report", GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out", Body: []byte{1}}, false, ProtocolPending, frameStatusNotCurrentProtocol},
		{"command 112 outbound is not handshake", GameFrame{CommandID: CmdProtoKeyResponse, Direction: "out", Body: []byte{1}}, false, ProtocolNone, frameStatusUndecoded},
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
	p := &Processor{Session: key, SessionNumber: 2, Dev: processorDiagnostics{frameArchive: &out}}
	stamp := time.Date(2026, 10, 28, 1, 2, 3, 4, time.UTC)

	p.HandleFrame(GameFrame{CommandID: 1651, Direction: "in", Sequence: 7, Token: 9, Body: nil, Timestamp: stamp})
	p.ArchiveFrame(GameFrame{CommandID: 1651, Direction: "in", Sequence: 8, Body: body, Timestamp: stamp})

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
	p := &Processor{Dev: processorDiagnostics{frameArchive: failingWriter{}}}
	p.ArchiveFrame(GameFrame{CommandID: 1651, Direction: "in", Body: []byte{1}})
	if p.Dev.frameArchiveErrors != 1 || p.Dev.frameOrdinal != 1 {
		t.Fatalf("errors=%d ordinal=%d, want 1 and 1", p.Dev.frameArchiveErrors, p.Dev.frameOrdinal)
	}
}
