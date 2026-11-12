//go:build wwdev

package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// frameDecodeStatus classifies a frame for the archive. Handshake and empty
// bodies take precedence over command support because neither path decrypts
// a payload. internal is the command the protocol session reads the wire
// command as, 0 when it cannot (protocolSession.internalCommand); the record
// itself keeps the wire command.
func frameDecodeStatus(frame gameFrame, internal uint16, state protocolState) string {
	if frame.commandID == 112 && frame.direction == "in" {
		return frameStatusHandshake
	}
	if len(frame.body) == 0 {
		return frameStatusEmptyBody
	}
	if internal == 0 && (state == protocolPending || state == protocolUnsupported) {
		return frameStatusNotCurrentProtocol
	}
	if isImplementedCommand(internal) {
		return frameStatusImplemented
	}
	return frameStatusUndecoded
}

// archivedFrameStatus classifies frame, still carrying its wire command, in the current protocol session.
func (p *processor) archivedFrameStatus(frame gameFrame) string {
	internal, _ := p.protocol.internalCommand(frame.commandID)
	return frameDecodeStatus(frame, internal, p.protocol.state)
}

// archiveFrame appends one record for the frame and advances the capture-order
// counter. The counter advances even without an archive writer so that
// frameOrdinal stays stable when the archive flag is toggled.
func (p *processor) archiveFrame(frame gameFrame) {
	ordinal := p.frameOrdinal
	p.frameOrdinal++
	if p.frameArchive == nil {
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
	line, err := json.Marshal(record)
	if err != nil {
		p.frameArchiveErrors++
		return
	}
	line = append(line, '\n')
	if _, err := p.frameArchive.Write(line); err != nil {
		p.frameArchiveErrors++
	}
}
