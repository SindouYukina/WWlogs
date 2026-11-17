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
func frameDecodeStatus(frame GameFrame, readable bool, state ProtocolState) string {
	if frame.CommandID == CmdProtoKeyResponse && frame.Direction == "in" {
		return frameStatusHandshake
	}
	if len(frame.Body) == 0 {
		return frameStatusEmptyBody
	}
	if !readable && (state == ProtocolPending || state == ProtocolUnsupported) {
		return frameStatusNotCurrentProtocol
	}
	if readable && IsImplementedCommand(frame.CommandID) {
		return frameStatusImplemented
	}
	return frameStatusUndecoded
}

// archivedFrameStatus classifies frame in the current protocol session.
func (p *Processor) archivedFrameStatus(frame GameFrame) string {
	return frameDecodeStatus(frame, p.Protocol.Readable(frame.CommandID), p.Protocol.State)
}

// ArchiveFrame appends one record for the frame and advances the capture-order
// counter. The counter advances even without an archive writer so that
// frameOrdinal stays stable when the archive flag is toggled.
func (p *Processor) ArchiveFrame(frame GameFrame) {
	ordinal := p.Dev.frameOrdinal
	p.Dev.frameOrdinal++
	if p.Dev.frameArchive == nil {
		return
	}
	digest := sha256.Sum256(frame.Body)
	record := frameArchiveRecord{
		SchemaVersion:    frameArchiveSchemaVersion,
		FrameOrdinal:     ordinal,
		SessionNumber:    p.SessionNumber,
		Direction:        frame.Direction,
		MessageType:      frame.MessageType,
		Sequence:         frame.Sequence,
		Token:            frame.Token,
		CommandID:        frame.CommandID,
		Compressed:       frame.Compressed,
		CompressedLength: frame.CompressedLength,
		BodyLength:       len(frame.Body),
		BodySHA256:       hex.EncodeToString(digest[:]),
		SessionKeyReady:  len(p.Session) == 32,
		DecodeStatus:     p.archivedFrameStatus(frame),
		Timestamp:        frame.Timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
	}
	writeResearchRecord(p.Dev.frameArchive, record, &p.Dev.frameArchiveErrors)
}
