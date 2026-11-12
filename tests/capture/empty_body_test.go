package capture

import (
	"testing"
)

// Zero-length command bodies must not be counted as AES decryption failures.
// The 2026-10-27 replays counted 428 such frames under "decrypt failed" on the
// Locking capture; they are empty payloads, not ciphertext errors.
func TestEmptyBodyIsCountedSeparatelyFromDecryptFailure(t *testing.T) {
	census := newCommandCensus()
	entry := census.entry(1651, "in")
	p := &processor{session: make([]byte, 32), census: census}

	p.inspectUndecodedFrame(gameFrame{commandID: 1651, direction: "in", messageType: 2}, nil, entry)

	if got := entry.FailureReasons[emptyBodyFailureReason]; got != 1 {
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
	census := newCommandCensus()
	entry := census.entry(26497, "out") // CombatSendPackRequest in the current wire protocol
	p := &processor{session: make([]byte, 32), census: census}
	p.protocol.beginSession()
	p.protocol.observeVersion(CurrentGameVersion())

	p.handleFrame(gameFrame{commandID: 26497, direction: "out", messageType: 2, body: nil})

	if entry.DecryptedFrames != 0 {
		t.Fatalf("DecryptedFrames = %d, want 0", entry.DecryptedFrames)
	}
	if got := entry.FailureReasons[emptyBodyFailureReason]; got != 1 {
		t.Fatalf("empty body count = %d, want 1", got)
	}
	if p.stats.decryptErrors != 0 {
		t.Fatalf("decryptErrors = %d, want 0 for an empty body", p.stats.decryptErrors)
	}
}
