//go:build wwdev

package capture

import (
	"crypto/sha256"
	"encoding/hex"
)

// frameArchiveSchemaVersion identifies the layout of frameArchiveRecord.
// Bump it when a field changes meaning; readers must not mix versions.
// Version 2: DecodeStatus follows the protocol session (current game version only) and may be not_current_protocol.
const frameArchiveSchemaVersion = 2

// Decode statuses describe what the archive can say about a frame without
// the session key or any plaintext. They do not describe semantic meaning.
const (
	frameStatusHandshake   = "handshake"
	frameStatusEmptyBody   = "empty_body"
	frameStatusImplemented = "implemented_command"
	frameStatusUndecoded   = "undecoded_command"
	// frameStatusNotCurrentProtocol: the protocol session has not reported the current game version, so no decoder
	// reads the frame.
	frameStatusNotCurrentProtocol = "not_current_protocol"
)

// frameArchiveRecord is one CRC-valid reassembled frame in processing order.
// It stores the ciphertext body only as a SHA-256 digest, so the archive
// contains no session key, decrypted payload, or decompressed payload. A frame
// can be located again by replaying the same PCAP with the same flags and
// matching frameOrdinal.
type frameArchiveRecord struct {
	SchemaVersion    int    `json:"schemaVersion"`
	FrameOrdinal     uint64 `json:"frameOrdinal"`
	SessionNumber    int    `json:"sessionNumber"`
	Direction        string `json:"direction"`
	MessageType      byte   `json:"messageType"`
	Sequence         uint32 `json:"sequence"`
	Token            uint16 `json:"token"`
	CommandID        uint16 `json:"commandId"`
	Compressed       bool   `json:"compressed"`
	CompressedLength uint32 `json:"compressedLength"`
	BodyLength       int    `json:"bodyLength"`
	BodySHA256       string `json:"bodySha256"`
	SessionKeyReady  bool   `json:"sessionKeyReady"`
	DecodeStatus     string `json:"decodeStatus"`
	Timestamp        string `json:"timestamp"`
}

// frameDecodeStatus classifies a frame for the archive. Handshake and empty
// bodies take precedence over command support because neither path decrypts
// a payload. readable says whether the protocol session reads the frame's
// numbering (protocolSession.readable).
func frameDecodeStatus(frame gameFrame, readable bool, state protocolState) string {
	if frame.commandID == cmdProtoKeyResponse && frame.direction == "in" {
		return frameStatusHandshake
	}
	if len(frame.body) == 0 {
		return frameStatusEmptyBody
	}
	if !readable && (state == protocolPending || state == protocolUnsupported) {
		return frameStatusNotCurrentProtocol
	}
	if readable && isImplementedCommand(frame.commandID) {
		return frameStatusImplemented
	}
	return frameStatusUndecoded
}

// archivedFrameStatus classifies frame in the current protocol session.
func (p *processor) archivedFrameStatus(frame gameFrame) string {
	return frameDecodeStatus(frame, p.protocol.readable(frame.commandID), p.protocol.state)
}

// archiveFrame appends one record for the frame and advances the capture-order
// counter. The counter advances even without an archive writer so that
// frameOrdinal stays stable when the archive flag is toggled.
func (p *processor) archiveFrame(frame gameFrame) {
	ordinal := p.dev.frameOrdinal
	p.dev.frameOrdinal++
	if p.dev.frameArchive == nil {
		return
	}
	digest := sha256.Sum256(frame.body)
	record := frameArchiveRecord{
		SchemaVersion:    frameArchiveSchemaVersion,
		FrameOrdinal:     ordinal,
		SessionNumber:    p.sessionNumber,
		Direction:        frame.direction,
		MessageType:      frame.messageType,
		Sequence:         frame.sequence,
		Token:            frame.token,
		CommandID:        frame.commandID,
		Compressed:       frame.compressed,
		CompressedLength: frame.compressedLength,
		BodyLength:       len(frame.body),
		BodySHA256:       hex.EncodeToString(digest[:]),
		SessionKeyReady:  len(p.session) == 32,
		DecodeStatus:     p.archivedFrameStatus(frame),
		Timestamp:        frame.timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
	}
	writeResearchRecord(p.dev.frameArchive, record, &p.dev.frameArchiveErrors)
}
