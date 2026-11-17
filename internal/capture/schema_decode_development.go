//go:build wwdev

package capture

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

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

// decodeSchemaMessage decodes data as the named schema message. Nested message
// types are followed only when the schema names them, so arbitrary bytes are
// never interpreted as a nested Protobuf message without a schema.
func decodeSchemaMessage(schema *protocolSchema, typeName string, data []byte, depth int) (*decodedMessage, error) {
	message, ok := schema.Messages[typeName]
	if !ok {
		return nil, fmt.Errorf("schema has no message %q", typeName)
	}
	if depth > maxSchemaDepth {
		return &decodedMessage{Type: typeName, Unknown: []unknownField{{Reason: "depth limit reached", Hex: hex.EncodeToString(data)}}}, nil
	}
	fields, err := ParseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", typeName, err)
	}
	index := message.fieldIndex()
	result := &decodedMessage{Type: typeName}
	for _, raw := range fields {
		field, known := index[raw.Number]
		if !known {
			result.Unknown = append(result.Unknown, rawUnknown(raw, "field number not in schema"))
			continue
		}
		if field.Kind == "map" {
			entry, ok := decodeMapEntry(schema, field, raw, depth)
			if !ok {
				result.Unknown = append(result.Unknown, rawUnknown(raw, "map entry does not match schema"))
				continue
			}
			result.Fields = append(result.Fields, entry)
			continue
		}
		if packed, ok := decodePackedScalars(schema, field, raw, depth); ok {
			result.Fields = append(result.Fields, packed...)
			continue
		}
		decoded, ok := decodeSchemaField(schema, field, raw, depth)
		if !ok {
			result.Unknown = append(result.Unknown, rawUnknown(raw, "wire type does not match schema kind"))
			continue
		}
		result.Fields = append(result.Fields, decoded)
	}
	return result, nil
}

func rawUnknown(raw ProtoField, reason string) unknownField {
	out := unknownField{Number: raw.Number, Wire: raw.Wire, Reason: reason}
	if raw.Wire == 0 {
		out.Varint = raw.Value
	} else if raw.Data != nil {
		out.Hex = hex.EncodeToString(raw.Data)
	}
	return out
}

// decodeSchemaField decodes one occurrence. It reports false when the wire type
// cannot carry the schema kind, leaving the caller to keep the raw field.
func decodeSchemaField(schema *protocolSchema, field schemaField, raw ProtoField, depth int) (decodedField, bool) {
	out := decodedField{Number: field.Number, Name: field.Name, Kind: field.Kind}
	kind := field.schemaType
	if field.Kind == "repeated" && field.Element != nil {
		out.Repeated = true
		kind = *field.Element
	}
	value, message, ok := decodeSchemaValue(schema, kind, raw, depth)
	if !ok {
		return decodedField{}, false
	}
	out.Value = value
	out.Message = message
	if out.Repeated {
		out.Kind = kind.Kind
	}
	return out, true
}

func decodeSchemaValue(schema *protocolSchema, kind schemaType, raw ProtoField, depth int) (any, *decodedMessage, bool) {
	switch kind.Kind {
	case "int32":
		return int32(raw.Value), nil, raw.Wire == 0
	case "int64":
		return int64(raw.Value), nil, raw.Wire == 0
	case "uint32":
		return uint32(raw.Value), nil, raw.Wire == 0
	case "uint64":
		return raw.Value, nil, raw.Wire == 0
	case "enum":
		return int32(raw.Value), nil, raw.Wire == 0
	case "bool":
		return raw.Value != 0, nil, raw.Wire == 0
	case "float":
		if raw.Wire != 5 || len(raw.Data) != 4 {
			return nil, nil, false
		}
		bits := uint32(raw.Data[0]) | uint32(raw.Data[1])<<8 | uint32(raw.Data[2])<<16 | uint32(raw.Data[3])<<24
		return float64(math.Float32frombits(bits)), nil, true
	case "double":
		if raw.Wire != 1 || len(raw.Data) != 8 {
			return nil, nil, false
		}
		var bits uint64
		for i := 7; i >= 0; i-- {
			bits = bits<<8 | uint64(raw.Data[i])
		}
		return math.Float64frombits(bits), nil, true
	case "string":
		if raw.Wire != 2 {
			return nil, nil, false
		}
		return string(raw.Data), nil, true
	case "bytes":
		if raw.Wire != 2 {
			return nil, nil, false
		}
		return base64.StdEncoding.EncodeToString(raw.Data), nil, true
	case "class":
		if raw.Wire != 2 {
			return nil, nil, false
		}
		if _, known := schema.Messages[kind.Type]; !known {
			return nil, &decodedMessage{Type: kind.Type, Unknown: []unknownField{{Reason: "message type not in schema", Hex: hex.EncodeToString(raw.Data)}}}, true
		}
		nested, err := decodeSchemaMessage(schema, kind.Type, raw.Data, depth+1)
		if err != nil {
			return nil, &decodedMessage{Type: kind.Type, Unknown: []unknownField{{Reason: "nested decode failed", Hex: hex.EncodeToString(raw.Data)}}}, true
		}
		return nil, nested, true
	}
	return nil, nil, false
}

// ArchiveSchemaDecode writes a schema-directed decode of a decrypted frame when a
// schema is loaded and the command name has a schema message. It reads the same
// plaintext as the typed decoder and does not change typed results.
func (p *Processor) ArchiveSchemaDecode(frame GameFrame, plain []byte) {
	if p.Dev.protocolSchema == nil {
		return
	}
	// Commands 103 and 116 carry device and account identifiers beside the client
	// version fields. The census keeps only the version fields; this output does not
	// decode them at all.
	if frame.CommandID == CmdLoginRequest || frame.CommandID == CmdCheckClientPush {
		p.Dev.schemaDecodeSkipped++
		return
	}
	name, known := p.schemaMessageFor(frame.CommandID)
	if !known {
		p.Dev.schemaDecodeSkipped++
		return
	}
	decoded, err := decodeSchemaMessage(p.Dev.protocolSchema, name, plain, 0)
	if err != nil {
		p.Dev.schemaDecodeErrors++
		return
	}
	p.observeTimerSchema(frame, name, decoded)
	p.archiveRoleConfigRecords(frame, decoded)
	if p.Dev.schemaDecodeOut == nil {
		return
	}
	record := schemaDecodeRecord{
		SchemaVersion: schemaDecodeSchemaVersion,
		FrameOrdinal:  p.Dev.frameOrdinal - 1,
		SessionNumber: p.SessionNumber,
		Direction:     frame.Direction,
		CommandID:     frame.CommandID,
		Decoded:       decoded,
	}
	writeResearchRecord(p.Dev.schemaDecodeOut, record, &p.Dev.schemaDecodeErrors)
}

// decodePackedScalars decodes a proto3 packed repeated scalar, where all elements
// share one length-delimited field. It reports false when the field is not a
// repeated numeric scalar or when the payload is not a whole number of elements,
// so the caller keeps the raw field.
func decodePackedScalars(schema *protocolSchema, field schemaField, raw ProtoField, depth int) ([]decodedField, bool) {
	if field.Kind != "repeated" || field.Element == nil || raw.Wire != 2 {
		return nil, false
	}
	element := *field.Element
	var fixedSize int
	switch element.Kind {
	case "int32", "int64", "uint32", "uint64", "enum", "bool":
		fixedSize = 0
	case "float":
		fixedSize = 4
	case "double":
		fixedSize = 8
	default:
		return nil, false
	}
	var out []decodedField
	data := raw.Data
	for len(data) > 0 {
		var item ProtoField
		item.Number = raw.Number
		if fixedSize == 0 {
			value, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, false
			}
			item.Wire = 0
			item.Value = value
			data = data[n:]
		} else {
			if len(data) < fixedSize {
				return nil, false
			}
			item.Wire = 1
			if fixedSize == 4 {
				item.Wire = 5
			}
			item.Data = data[:fixedSize]
			data = data[fixedSize:]
		}
		value, _, ok := decodeSchemaValue(schema, element, item, depth)
		if !ok {
			return nil, false
		}
		out = append(out, decodedField{Number: field.Number, Name: field.Name, Kind: element.Kind, Repeated: true, Value: value})
	}
	return out, true
}

// decodeMapEntry decodes one map entry, which is a length-delimited message with
// key in field 1 and value in field 2. Entries missing either part are rejected
// so that the raw field is kept instead of being guessed.
func decodeMapEntry(schema *protocolSchema, field schemaField, raw ProtoField, depth int) (decodedField, bool) {
	if field.Key == nil || field.Value == nil || raw.Wire != 2 {
		return decodedField{}, false
	}
	parts, err := ParseProtoFields(raw.Data)
	if err != nil {
		return decodedField{}, false
	}
	var keyField, valueField *ProtoField
	for i := range parts {
		switch parts[i].Number {
		case 1:
			keyField = &parts[i]
		case 2:
			valueField = &parts[i]
		}
	}
	if keyField == nil || valueField == nil {
		return decodedField{}, false
	}
	key, _, ok := decodeSchemaValue(schema, *field.Key, *keyField, depth)
	if !ok {
		return decodedField{}, false
	}
	value, message, ok := decodeSchemaValue(schema, *field.Value, *valueField, depth)
	if !ok {
		return decodedField{}, false
	}
	return decodedField{Number: field.Number, Name: field.Name, Kind: "map", Repeated: true, Value: mapEntry{Key: key, Value: value, Message: message}}, true
}

// schemaMessageName removes the census annotation from a command name. Names such
// as "EntityAddNotify (candidate)" describe the census status, not the message.
func schemaMessageName(commandName string) string {
	if index := strings.Index(commandName, " ("); index >= 0 {
		return commandName[:index]
	}
	return commandName
}

// schemaMessageFor returns the schema message for an outer command. The census
// name is tried first. Commands the census does not name are looked up in the
// schema's message-ID enums, which map every observed ID to its message name.
func (p *Processor) schemaMessageFor(commandID uint16) (string, bool) {
	if name := schemaMessageName(commandName(commandID)); name != "" {
		if _, known := p.Dev.protocolSchema.Messages[name]; known {
			return name, true
		}
	}
	for _, enum := range []string{"EMessageId", "EPushMessageId", "ENotifyMessageId"} {
		name, found := p.Dev.protocolSchema.enumName(enum, int64(commandID))
		if !found {
			continue
		}
		if _, known := p.Dev.protocolSchema.Messages[name]; known {
			return name, true
		}
	}
	return "", false
}

// ArchiveUndecodedSchema writes schema decodes for commands outside the typed
// decoder when no census is running. handleFrame calls it only for frames the
// protocol session reads, whose numbering is the current protocol the external
// schema describes. It decrypts and decompresses the frame the same way the
// census path does and writes only frames that parse as top-level Protobuf.
func (p *Processor) ArchiveUndecodedSchema(frame GameFrame) {
	if p.Dev.protocolSchema == nil || (p.Dev.schemaDecodeOut == nil && p.Dev.roleConfigOut == nil) || len(p.Session) != 32 || len(frame.Body) == 0 {
		return
	}
	plain, err := DecryptGameBody(frame.Sequence, frame.Body, p.Session)
	if err != nil {
		return
	}
	if frame.Compressed {
		plain, err = DecompressBody(plain, frame.CompressedLength)
		if err != nil {
			return
		}
	}
	if _, err := ParseProtoFields(plain); err != nil {
		return
	}
	p.ArchiveSchemaDecode(frame, plain)
}
