//go:build wwdev

package main

import (
	"strings"
	"testing"
)

func TestSchemaDecodeFollowsNamedNestedMessages(t *testing.T) {
	// Outer.Count = 3; Outer.Data[0] = Inner{StackCount=1, Handle=68, EntityId=-1, Duration=2.5}
	inner := []byte{0x08, 0x01, 0x10, 0x44, 0x38, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01, 0x7d, 0x00, 0x00, 0x20, 0x40}
	outer := append([]byte{0x08, 0x03, 0x2a, byte(len(inner))}, inner...)

	decoded, err := decodeSchemaMessage(testSchema(t), "Outer", outer, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Fields) != 2 || len(decoded.Unknown) != 0 {
		t.Fatalf("fields=%d unknown=%d, want 2 and 0: %+v", len(decoded.Fields), len(decoded.Unknown), decoded)
	}
	if decoded.Fields[0].Value != int32(3) {
		t.Fatalf("Count = %v", decoded.Fields[0].Value)
	}
	nested := decoded.Fields[1].Message
	if nested == nil || nested.Type != "Inner" {
		t.Fatalf("Data message = %+v", decoded.Fields[1])
	}
	want := map[string]any{"StackCount": int32(1), "Handle": int32(68), "EntityId": int64(-1), "Duration": float64(2.5)}
	for _, field := range nested.Fields {
		if want[field.Name] != field.Value {
			t.Fatalf("%s = %#v, want %#v", field.Name, field.Value, want[field.Name])
		}
	}
	if len(nested.Fields) != len(want) {
		t.Fatalf("nested fields = %d, want %d", len(nested.Fields), len(want))
	}
}

func TestSchemaDecodeKeepsUnknownAndMismatchedFieldsRaw(t *testing.T) {
	// Field 99 varint 42 is not in the schema. Field 2 arrives as length-delimited, not varint.
	body := []byte{0x98, 0x06, 0x2a, 0x12, 0x01, 0x41}
	decoded, err := decodeSchemaMessage(testSchema(t), "Inner", body, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Fields) != 0 || len(decoded.Unknown) != 2 {
		t.Fatalf("fields=%d unknown=%d, want 0 and 2", len(decoded.Fields), len(decoded.Unknown))
	}
	if decoded.Unknown[0].Number != 99 || decoded.Unknown[0].Reason != "field number not in schema" {
		t.Fatalf("unknown[0] = %+v", decoded.Unknown[0])
	}
	if decoded.Unknown[1].Number != 2 || decoded.Unknown[1].Reason != "wire type does not match schema kind" || decoded.Unknown[1].Hex != "41" {
		t.Fatalf("unknown[1] = %+v", decoded.Unknown[1])
	}
}

func TestSchemaDecodeDoesNotInterpretUnnamedBytesAsMessages(t *testing.T) {
	// Field 16 is a map with no key or value type in the schema. It must stay raw and must not be parsed as nested Protobuf.
	body := []byte{0x82, 0x01, 0x03, 0x08, 0x01, 0x10}
	decoded, err := decodeSchemaMessage(testSchema(t), "Inner", body, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Fields) != 0 || len(decoded.Unknown) != 1 || decoded.Unknown[0].Reason != "map entry does not match schema" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestSchemaDecodeReportsMissingMessageType(t *testing.T) {
	if _, err := decodeSchemaMessage(testSchema(t), "Missing", nil, 0); err == nil || !strings.Contains(err.Error(), "Missing") {
		t.Fatalf("error = %v, want missing message type", err)
	}
}

func TestSchemaDecodeStopsAtDepthLimit(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"Loop": {Fields: []schemaField{{Number: 1, Name: "Next", schemaType: schemaType{Kind: "class", Type: "Loop"}}}},
	}}
	body := []byte{0x0a, 0x00}
	for i := 0; i < maxSchemaDepth+3; i++ {
		body = append([]byte{0x0a, byte(len(body))}, body...)
	}
	decoded, err := decodeSchemaMessage(schema, "Loop", body, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	node := decoded
	depth := 0
	for node.Fields != nil && node.Fields[0].Message != nil {
		node = node.Fields[0].Message
		depth++
	}
	// Messages at depths 0..maxSchemaDepth are decoded; the node one level
	// deeper is the raw placeholder.
	if depth > maxSchemaDepth+1 {
		t.Fatalf("nested depth = %d, exceeds limit %d", depth, maxSchemaDepth+1)
	}
	if len(node.Unknown) == 0 || node.Unknown[0].Reason != "depth limit reached" {
		t.Fatalf("deepest node = %+v, want depth-limit raw field", node)
	}
}

// Command 103 carries account and device identifiers. Schema decoding must not
// write it even when the schema names its message.
func TestSchemaDecodeExcludesLoginIdentifiers(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"LoginRequest": {Fields: []schemaField{{Number: 2, Name: "Account", schemaType: schemaType{Kind: "string"}}}},
	}}
	var out strings.Builder
	p := &processor{protocolSchema: schema, schemaDecodeOut: &out}
	p.archiveSchemaDecode(gameFrame{commandID: 103, direction: "out", body: []byte{0x12, 0x03, 'a', 'b', 'c'}}, []byte{0x12, 0x03, 'a', 'b', 'c'})
	if out.Len() != 0 {
		t.Fatalf("command 103 wrote %d bytes of schema output", out.Len())
	}
	if p.schemaDecodeSkipped != 1 {
		t.Fatalf("skipped = %d, want 1", p.schemaDecodeSkipped)
	}
}

func TestSchemaDecodePacksRepeatedScalars(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"Packed": {Fields: []schemaField{
			{Number: 1, Name: "Ids", schemaType: schemaType{Kind: "repeated", Element: &schemaType{Kind: "int64"}}},
		}},
	}}
	// Field 1, length 3: varints 1 and 300 (0xac 0x02). Packed, so wire type 2.
	body := []byte{0x0a, 0x03, 0x01, 0xac, 0x02}
	decoded, err := decodeSchemaMessage(schema, "Packed", body, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Unknown) != 0 || len(decoded.Fields) != 2 {
		t.Fatalf("fields=%d unknown=%+v, want 2 and none", len(decoded.Fields), decoded.Unknown)
	}
	if decoded.Fields[0].Value != int64(1) || decoded.Fields[1].Value != int64(300) || !decoded.Fields[1].Repeated {
		t.Fatalf("fields = %+v", decoded.Fields)
	}
}

func TestSchemaDecodeRejectsTruncatedPackedPayload(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"Packed": {Fields: []schemaField{
			{Number: 1, Name: "Ids", schemaType: schemaType{Kind: "repeated", Element: &schemaType{Kind: "int64"}}},
		}},
	}}
	// The varint 0xac has its continuation bit set and no following byte.
	body := []byte{0x0a, 0x01, 0xac}
	decoded, err := decodeSchemaMessage(schema, "Packed", body, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Fields) != 0 || len(decoded.Unknown) != 1 {
		t.Fatalf("decoded = %+v, want the raw field kept", decoded)
	}
}

func TestSchemaDecodeMapEntries(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"Holder": {Fields: []schemaField{
			{Number: 3, Name: "Scenes", schemaType: schemaType{Kind: "map", Key: &schemaType{Kind: "int32"}, Value: &schemaType{Kind: "class", Type: "Inner"}}},
		}},
		"Inner": {Fields: []schemaField{{Number: 1, Name: "StackCount", schemaType: schemaType{Kind: "int32"}}}},
	}}
	// Entry: key 7 (field 1), value Inner{StackCount=2} (field 2).
	entry := []byte{0x08, 0x07, 0x12, 0x02, 0x08, 0x02}
	body := append([]byte{0x1a, byte(len(entry))}, entry...)
	decoded, err := decodeSchemaMessage(schema, "Holder", body, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Unknown) != 0 || len(decoded.Fields) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
	got, ok := decoded.Fields[0].Value.(mapEntry)
	if !ok || got.Key != int32(7) || got.Message == nil || got.Message.Fields[0].Value != int32(2) {
		t.Fatalf("map entry = %#v", decoded.Fields[0].Value)
	}
}

func TestSchemaMessageNameDropsCensusAnnotation(t *testing.T) {
	cases := map[string]string{
		"EntityAddNotify (candidate)":     "EntityAddNotify",
		"CombatRequest (outer transport)": "CombatRequest",
		"LoginRequest":                    "LoginRequest",
	}
	for in, want := range cases {
		if got := schemaMessageName(in); got != want {
			t.Fatalf("schemaMessageName(%q) = %q, want %q", in, got, want)
		}
	}
}
