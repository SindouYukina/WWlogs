//go:build wwdev

package capture

import (
	"crypto/sha256"
	"encoding/hex"
)

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
