//go:build wwdev

package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
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
			result.Unknown = append(result.Unknown, rawUnknown(raw, "map field not decoded"))
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
	if p.protocolSchema == nil || p.schemaDecodeOut == nil {
		return
	}
	// Commands 103 and 116 carry device and account identifiers beside the client
	// version fields. The census keeps only the version fields; this output does not
	// decode them at all.
	if frame.commandID == 103 || frame.commandID == 116 {
		p.schemaDecodeSkipped++
		return
	}
	name := commandName(frame.commandID)
	if _, known := p.protocolSchema.Messages[name]; !known {
		p.schemaDecodeSkipped++
		return
	}
	decoded, err := decodeSchemaMessage(p.protocolSchema, name, plain, 0)
	if err != nil {
		p.schemaDecodeErrors++
		return
	}
	record := schemaDecodeRecord{
		SchemaVersion: schemaDecodeSchemaVersion,
		FrameOrdinal:  p.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Direction:     frame.direction,
		CommandID:     frame.commandID,
		Decoded:       decoded,
	}
	line, err := json.Marshal(record)
	if err != nil {
		p.schemaDecodeErrors++
		return
	}
	if _, err := p.schemaDecodeOut.Write(append(line, '\n')); err != nil {
		p.schemaDecodeErrors++
	}
}
