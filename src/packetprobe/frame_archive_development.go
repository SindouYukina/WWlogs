//go:build wwdev

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// frameDecodeStatus classifies a frame for the archive. Handshake and empty
// bodies take precedence over command support because neither path decrypts
// a payload.
func frameDecodeStatus(frame gameFrame) string {
	if frame.commandID == 112 && frame.direction == "in" {
		return frameStatusHandshake
	}
	if len(frame.body) == 0 {
		return frameStatusEmptyBody
	}
	if isImplementedCommand(frame.commandID) {
		return frameStatusImplemented
	}
	return frameStatusUndecoded
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
		DecodeStatus:     frameDecodeStatus(frame),
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
