package devcapture

import (
	"testing"

	"wwlogs/internal/capture"
)

// Zero-length command bodies must not be counted as AES decryption failures.
// The 2026-10-27 replays counted 428 such frames under "decrypt failed" on the
// Locking capture; they are empty payloads, not ciphertext errors.
func TestEmptyBodyIsCountedSeparatelyFromDecryptFailure(t *testing.T) {
	census := newCommandCensus(nil)
	entry := census.entry(1651, "in")
	p := observed(&processor{Processor: &capture.Processor{Session: make([]byte, 32), Census: census}})
	census.processor = p

	entry.InspectUndecoded(capture.GameFrame{CommandID: 1651, Direction: "in", MessageType: 2}, true)

	if got := entry.FailureReasons[capture.EmptyBodyFailureReason]; got != 1 {
		t.Fatalf("empty body count = %d, want 1", got)
	}
	if got := entry.FailureReasons["no implemented schema; decrypt failed"]; got != 0 {
		t.Fatalf("decrypt failed count = %d, want 0 for an empty body", got)
	}
	if entry.DecryptedFrames != 0 {
		t.Fatalf("DecryptedFrames = %d, want 0 because no ciphertext was decrypted", entry.DecryptedFrames)
	}
}

func TestEmptyBodyDoesNotInflateDecryptedFrames(t *testing.T) {
	census := newCommandCensus(nil)
	entry := census.entry(capture.CmdCombatSendPackRequest, "out")
	p := observed(&processor{Processor: &capture.Processor{Session: make([]byte, 32), Census: census}})
	census.processor = p
	p.Protocol.BeginSession()
	p.Protocol.ObserveVersion(capture.CurrentGameVersion())

	p.HandleFrame(capture.GameFrame{CommandID: capture.CmdCombatSendPackRequest, Direction: "out", MessageType: 2, Body: nil})

	if entry.DecryptedFrames != 0 {
		t.Fatalf("DecryptedFrames = %d, want 0", entry.DecryptedFrames)
	}
	if got := entry.FailureReasons[capture.EmptyBodyFailureReason]; got != 1 {
		t.Fatalf("empty body count = %d, want 1", got)
	}
	if p.Stats.DecryptErrors != 0 {
		t.Fatalf("decryptErrors = %d, want 0 for an empty body", p.Stats.DecryptErrors)
	}
}
