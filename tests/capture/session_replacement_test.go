package capture

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"hash/crc32"
	"io"
	"testing"
	"time"
)

// replacementFrame builds one CRC-valid application frame. Message type 4 carries no token.
func replacementFrame(messageType byte, sequence uint32, command uint16, body []byte) []byte {
	header := 8
	if messageType != 4 {
		header += 2
	}
	frame := make([]byte, header+6+len(body))
	binary.LittleEndian.PutUint16(frame[:2], uint16(len(frame)-3))
	frame[3] = messageType
	binary.LittleEndian.PutUint32(frame[4:8], sequence)
	binary.LittleEndian.PutUint16(frame[header:header+2], command)
	binary.LittleEndian.PutUint32(frame[header+2:header+6], crc32.ChecksumIEEE(body))
	copy(frame[header+6:], body)
	return frame
}

func replacementProcessor(t *testing.T) (*Processor, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := NewProcessor(nil, io.Discard)
	p.RSA = &StaticRSA{E: key.E, N: key.N, D: key.D}
	p.IntervalPerProtocolSession = true
	return p, key
}

func handshakeBody(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return appendBytesField(appendVarintField(nil, FieldProtoKeyResponseType, 2), FieldProtoKeyResponseKey, ciphertext)
}

// A capture that began after the login sees one connection without a key. The game returns to the login screen while
// that connection still holds a loss, and the next login arrives on a new conversation. The new session must not inherit
// the dead connection's loss: no open loss, no recovering transport, no incomplete window after the handshake.
func TestReloginRetiresTheSupersededConnection(t *testing.T) {
	p, key := replacementProcessor(t)
	streams := make(map[string]*Stream)
	stats := CaptureStats{}
	base := time.Date(2026, 11, 12, 1, 59, 1, 0, time.UTC)
	const oldConv, newConv = 0x15bd, 0x1788

	// The missed connection: a frame mid-session, then a later segment behind a gap that never fills.
	ConsumeDatagram(makeKCPSegmentForTest(oldConv, 199, replacementFrame(4, 665, 22070, bytes.Repeat([]byte{7}, 32))), "in", base, streams, p, &stats)
	ConsumeDatagram(makeKCPSegmentForTest(oldConv, 201, bytes.Repeat([]byte{0xee}, 40)), "in", base.Add(time.Second), streams, p, &stats)
	// Its gap times out on a later datagram: the stream skips it and searches the garbage for a frame it never finds.
	ConsumeDatagram(makeKCPSegmentForTest(oldConv, 0, nil)[:0], "in", base.Add(4*time.Second), streams, p, &stats)
	skipTimedOutGaps(base.Add(4*time.Second), streams, p, &stats)
	old := streams["000015bd/in"]
	if old == nil || !old.Recovery.Active {
		t.Fatalf("setup: the missed connection should be recovering, got %+v", old)
	}
	if p.TransportHealthSnapshot().State != TransportRecovering {
		t.Fatalf("setup: transport state %s", p.TransportHealthSnapshot().State)
	}

	// The re-login: the client's key request on the new conversation, then the server's key response.
	login := base.Add(50 * time.Second)
	ConsumeDatagram(makeKCPSegmentForTest(newConv, 0, replacementFrame(1, 1, 111, []byte{1, 2, 3})), "out", login, streams, p, &stats)
	ConsumeDatagram(makeKCPSegmentForTest(newConv, 0, replacementFrame(2, 0, 112, handshakeBody(t, key))), "in", login.Add(10*time.Millisecond), streams, p, &stats)

	if p.Stats.HandshakeOK != 1 || len(p.Session) != 32 {
		t.Fatal("the new login's key was not taken")
	}
	if state := p.TransportHealthSnapshot().State; state == TransportRecovering || state == TransportUnrecoverable || state == TransportGapDetected {
		t.Fatalf("the new session inherited transport state %s", state)
	}
	statistics := p.DamageStatistics
	statistics.mu.Lock()
	ongoing := statistics.Encounters.ongoingLossIDs()
	window := statistics.windowIntegrityLocked(login.Add(time.Second), time.Time{})
	before := statistics.windowIntegrityLocked(base, base.Add(3*time.Second))
	statistics.mu.Unlock()
	if len(ongoing) != 0 {
		t.Fatalf("losses still open after the handshake: %v", ongoing)
	}
	if window != "" {
		t.Fatalf("a window of the new session is marked %q", window)
	}
	if before == "" {
		t.Fatal("the previous connection's loss disappeared instead of ending at the handshake")
	}
	if _, kept := streams["000015bd/in"]; kept {
		t.Fatal("the superseded connection's stream stayed in the table")
	}
	if streams["00001788/out"] == nil || streams["00001788/in"] == nil {
		t.Fatal("a stream of the new connection was retired")
	}
	if stats.KCPStreamsRetired != 1 || stats.KCPRetiredPendingSegments != 0 {
		t.Fatalf("retired %d streams with %d pending segments", stats.KCPStreamsRetired, stats.KCPRetiredPendingSegments)
	}
	direction := p.TransportHealthSnapshot().Directions[0]
	if direction.Direction != "in" || direction.Losses != 1 || direction.Unrecovered != 1 {
		t.Fatalf("inbound transport health %+v, want one unrecovered loss", direction)
	}
	if p.Context.Snapshot().State != ContextValid {
		t.Fatal("the superseded loss invalidated the new session's context")
	}

	// Stragglers of the dead connection are ignored; a new connection on the same conversation number starts at 0.
	ConsumeDatagram(makeKCPSegmentForTest(oldConv, 202, bytes.Repeat([]byte{0xee}, 8)), "in", login.Add(time.Second), streams, p, &stats)
	if streams["000015bd/in"] != nil || stats.KCPRetiredStragglers != 1 {
		t.Fatalf("a straggler reopened the dead conversation (stragglers %d)", stats.KCPRetiredStragglers)
	}
	ConsumeDatagram(makeKCPSegmentForTest(oldConv, 0, replacementFrame(4, 1, 1651, nil)), "in", login.Add(2*time.Second), streams, p, &stats)
	if streams["000015bd/in"] == nil {
		t.Fatal("a new connection reusing the conversation number was ignored")
	}
}

// The other direction of the handshake's own connection is the same connection: it stays, only its stale tail goes.
func TestHandshakeKeepsItsOwnConversation(t *testing.T) {
	p, _ := replacementProcessor(t)
	current := &Stream{key: "00000001/in", conv: 1, direction: "in", applicationBuffer: []byte{1, 2}, applicationSpans: []applicationSpan{{length: 2}}}
	sibling := &Stream{key: "00000001/out", conv: 1, direction: "out", Pending: map[uint32]segment{}, applicationBuffer: []byte{3, 4}, applicationSpans: []applicationSpan{{length: 2}}}
	other := &Stream{key: "00000002/in", conv: 2, direction: "in", Pending: map[uint32]segment{}, applicationBuffer: []byte{5}}
	streams := map[string]*Stream{current.key: current, sibling.key: sibling, other.key: other}
	stats := CaptureStats{}
	retireSupersededStreams(streams, current, p, &stats)
	if len(current.applicationBuffer) != 2 || len(current.applicationSpans) != 1 {
		t.Fatalf("current stream tail was cleared: %+v", current)
	}
	if streams[sibling.key] != sibling || len(sibling.applicationBuffer) != 0 || len(sibling.applicationSpans) != 0 {
		t.Fatalf("sibling stream was retired or kept an old-session tail: %+v", sibling)
	}
	if streams[other.key] != nil || stats.KCPStreamsRetired != 1 {
		t.Fatal("the other conversation was not retired")
	}
	if len(p.transport.episodes) != 0 {
		t.Fatal("retiring a stream without a loss recorded one")
	}
}
