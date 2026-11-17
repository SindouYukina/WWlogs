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
	fields, err := parseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", typeName, err)
	}
	index := message.fieldIndex()
	result := &decodedMessage{Type: typeName}
	for _, raw := range fields {
		field, known := index[raw.number]
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

func rawUnknown(raw protoField, reason string) unknownField {
	out := unknownField{Number: raw.number, Wire: raw.wire, Reason: reason}
	if raw.wire == 0 {
		out.Varint = raw.value
	} else if raw.data != nil {
		out.Hex = hex.EncodeToString(raw.data)
	}
	return out
}

// decodeSchemaField decodes one occurrence. It reports false when the wire type
// cannot carry the schema kind, leaving the caller to keep the raw field.
func decodeSchemaField(schema *protocolSchema, field schemaField, raw protoField, depth int) (decodedField, bool) {
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

func decodeSchemaValue(schema *protocolSchema, kind schemaType, raw protoField, depth int) (any, *decodedMessage, bool) {
	switch kind.Kind {
	case "int32":
		return int32(raw.value), nil, raw.wire == 0
	case "int64":
		return int64(raw.value), nil, raw.wire == 0
	case "uint32":
		return uint32(raw.value), nil, raw.wire == 0
	case "uint64":
		return raw.value, nil, raw.wire == 0
	case "enum":
		return int32(raw.value), nil, raw.wire == 0
	case "bool":
		return raw.value != 0, nil, raw.wire == 0
	case "float":
		if raw.wire != 5 || len(raw.data) != 4 {
			return nil, nil, false
		}
		bits := uint32(raw.data[0]) | uint32(raw.data[1])<<8 | uint32(raw.data[2])<<16 | uint32(raw.data[3])<<24
		return float64(math.Float32frombits(bits)), nil, true
	case "double":
		if raw.wire != 1 || len(raw.data) != 8 {
			return nil, nil, false
		}
		var bits uint64
		for i := 7; i >= 0; i-- {
			bits = bits<<8 | uint64(raw.data[i])
		}
		return math.Float64frombits(bits), nil, true
	case "string":
		if raw.wire != 2 {
			return nil, nil, false
		}
		return string(raw.data), nil, true
	case "bytes":
		if raw.wire != 2 {
			return nil, nil, false
		}
		return base64.StdEncoding.EncodeToString(raw.data), nil, true
	case "class":
		if raw.wire != 2 {
			return nil, nil, false
		}
		if _, known := schema.Messages[kind.Type]; !known {
			return nil, &decodedMessage{Type: kind.Type, Unknown: []unknownField{{Reason: "message type not in schema", Hex: hex.EncodeToString(raw.data)}}}, true
		}
		nested, err := decodeSchemaMessage(schema, kind.Type, raw.data, depth+1)
		if err != nil {
			return nil, &decodedMessage{Type: kind.Type, Unknown: []unknownField{{Reason: "nested decode failed", Hex: hex.EncodeToString(raw.data)}}}, true
		}
		return nil, nested, true
	}
	return nil, nil, false
}

// archiveSchemaDecode writes a schema-directed decode of a decrypted frame when a
// schema is loaded and the command name has a schema message. It reads the same
// plaintext as the typed decoder and does not change typed results.
func (p *processor) archiveSchemaDecode(frame gameFrame, plain []byte) {
	if p.dev.protocolSchema == nil {
		return
	}
	// Commands 103 and 116 carry device and account identifiers beside the client
	// version fields. The census keeps only the version fields; this output does not
	// decode them at all.
	if frame.commandID == cmdLoginRequest || frame.commandID == cmdCheckClientPush {
		p.dev.schemaDecodeSkipped++
		return
	}
	name, known := p.schemaMessageFor(frame.commandID)
	if !known {
		p.dev.schemaDecodeSkipped++
		return
	}
	decoded, err := decodeSchemaMessage(p.dev.protocolSchema, name, plain, 0)
	if err != nil {
		p.dev.schemaDecodeErrors++
		return
	}
	p.observeTimerSchema(frame, name, decoded)
	p.archiveRoleConfigRecords(frame, decoded)
	if p.dev.schemaDecodeOut == nil {
		return
	}
	record := schemaDecodeRecord{
		SchemaVersion: schemaDecodeSchemaVersion,
		FrameOrdinal:  p.dev.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Direction:     frame.direction,
		CommandID:     frame.commandID,
		Decoded:       decoded,
	}
	writeResearchRecord(p.dev.schemaDecodeOut, record, &p.dev.schemaDecodeErrors)
}

// decodePackedScalars decodes a proto3 packed repeated scalar, where all elements
// share one length-delimited field. It reports false when the field is not a
// repeated numeric scalar or when the payload is not a whole number of elements,
// so the caller keeps the raw field.
func decodePackedScalars(schema *protocolSchema, field schemaField, raw protoField, depth int) ([]decodedField, bool) {
	if field.Kind != "repeated" || field.Element == nil || raw.wire != 2 {
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
	data := raw.data
	for len(data) > 0 {
		var item protoField
		item.number = raw.number
		if fixedSize == 0 {
			value, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, false
			}
			item.wire = 0
			item.value = value
			data = data[n:]
		} else {
			if len(data) < fixedSize {
				return nil, false
			}
			item.wire = 1
			if fixedSize == 4 {
				item.wire = 5
			}
			item.data = data[:fixedSize]
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
func decodeMapEntry(schema *protocolSchema, field schemaField, raw protoField, depth int) (decodedField, bool) {
	if field.Key == nil || field.Value == nil || raw.wire != 2 {
		return decodedField{}, false
	}
	parts, err := parseProtoFields(raw.data)
	if err != nil {
		return decodedField{}, false
	}
	var keyField, valueField *protoField
	for i := range parts {
		switch parts[i].number {
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
func (p *processor) schemaMessageFor(commandID uint16) (string, bool) {
	if name := schemaMessageName(commandName(commandID)); name != "" {
		if _, known := p.dev.protocolSchema.Messages[name]; known {
			return name, true
		}
	}
	for _, enum := range []string{"EMessageId", "EPushMessageId", "ENotifyMessageId"} {
		name, found := p.dev.protocolSchema.enumName(enum, int64(commandID))
		if !found {
			continue
		}
		if _, known := p.dev.protocolSchema.Messages[name]; known {
			return name, true
		}
	}
	return "", false
}

// archiveUndecodedSchema writes schema decodes for commands outside the typed
// decoder when no census is running. handleFrame calls it only for frames the
// protocol session reads, whose numbering is the current protocol the external
// schema describes. It decrypts and decompresses the frame the same way the
// census path does and writes only frames that parse as top-level Protobuf.
func (p *processor) archiveUndecodedSchema(frame gameFrame) {
	if p.dev.protocolSchema == nil || (p.dev.schemaDecodeOut == nil && p.dev.roleConfigOut == nil) || len(p.session) != 32 || len(frame.body) == 0 {
		return
	}
	plain, err := decryptGameBody(frame.sequence, frame.body, p.session)
	if err != nil {
		return
	}
	if frame.compressed {
		plain, err = decompressBody(plain, frame.compressedLength)
		if err != nil {
			return
		}
	}
	if _, err := parseProtoFields(plain); err != nil {
		return
	}
	p.archiveSchemaDecode(frame, plain)
}
