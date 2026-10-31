package capture

// frameArchiveSchemaVersion identifies the layout of frameArchiveRecord.
// Bump it when a field changes meaning; readers must not mix versions.
const frameArchiveSchemaVersion = 1

// Decode statuses describe what the archive can say about a frame without
// the session key or any plaintext. They do not describe semantic meaning.
const (
	frameStatusHandshake   = "handshake"
	frameStatusEmptyBody   = "empty_body"
	frameStatusImplemented = "implemented_command"
	frameStatusUndecoded   = "undecoded_command"
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
