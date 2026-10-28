package main

// maxSchemaDepth bounds nested message recursion. Corrupted or unexpected
// bytes must not drive unbounded descent; the remainder is kept as raw data.
const maxSchemaDepth = 8

// decodedMessage is a schema-directed view of one protobuf message. Fields whose
// number is absent from the schema, or whose wire type disagrees with the
// schema kind, are kept in Unknown with their raw bytes.
type decodedMessage struct {
	Type    string         `json:"type"`
	Fields  []decodedField `json:"fields,omitempty"`
	Unknown []unknownField `json:"unknown,omitempty"`
}

type decodedField struct {
	Number   uint64          `json:"number"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Repeated bool            `json:"repeated,omitempty"`
	Value    any             `json:"value,omitempty"`
	Message  *decodedMessage `json:"message,omitempty"`
}

type unknownField struct {
	Number uint64 `json:"number"`
	Wire   uint64 `json:"wire"`
	Reason string `json:"reason"`
	Hex    string `json:"hex,omitempty"`
	Varint uint64 `json:"varint,omitempty"`
}

// schemaDecodeSchemaVersion identifies the layout of schemaDecodeRecord.
const schemaDecodeSchemaVersion = 1

// schemaDecodeRecord is one schema-decoded outer message. FrameOrdinal matches
// frameArchiveRecord.FrameOrdinal for the same replay, so the two outputs can
// be joined without a separate capture index.
type schemaDecodeRecord struct {
	SchemaVersion int             `json:"schemaVersion"`
	FrameOrdinal  uint64          `json:"frameOrdinal"`
	SessionNumber int             `json:"sessionNumber"`
	Direction     string          `json:"direction"`
	CommandID     uint16          `json:"commandId"`
	Decoded       *decodedMessage `json:"decoded"`
}

// mapEntry is one decoded map<K, V> entry. Message values are decoded through
// the schema like any other nested message.
type mapEntry struct {
	Key     any             `json:"key"`
	Value   any             `json:"value,omitempty"`
	Message *decodedMessage `json:"message,omitempty"`
}
