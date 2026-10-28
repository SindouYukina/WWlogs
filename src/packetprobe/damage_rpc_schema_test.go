//go:build wwdev

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDamageRPCSchemaExtractsResponsePayloadByRPCID(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"DamageExecuteResponse": {Fields: []schemaField{
			{Number: 10, Name: "Damage", schemaType: schemaType{Kind: "int32"}},
			{Number: 11, Name: "IsCrit", schemaType: schemaType{Kind: "bool"}},
		}},
	}}
	// payload: Damage=100 (field 10), IsCrit=true (field 11)
	payload := []byte{0x50, 0x64, 0x58, 0x01}
	// item: rpcId=5 (field 2), payload (field 5)
	item := append([]byte{0x10, 0x05, 0x2a, byte(len(payload))}, payload...)
	// batch: item as field 3
	batch := append([]byte{0x1a, byte(len(item))}, item...)
	// response: batch as field 5 inside wrapper field 14
	wrapped := append([]byte{0x2a, byte(len(batch))}, batch...)
	body := append([]byte{0x72, byte(len(wrapped))}, wrapped...)

	var out strings.Builder
	p := &processor{protocolSchema: schema, schemaDecodeOut: &out, frameOrdinal: 1, sessionNumber: 2}
	p.archiveDamageRPCSchema(gameFrame{commandID: 20332, direction: "in"}, body)
	var record damageRPCSchemaRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &record); err != nil {
		t.Fatalf("record is not valid JSON: %v (%q)", err, out.String())
	}
	if record.RPCID != 5 || record.Message != "DamageExecuteResponse" || record.FrameOrdinal != 0 {
		t.Fatalf("record = %+v", record)
	}
	if len(record.Decoded.Fields) != 2 || record.Decoded.Fields[1].Value != true {
		t.Fatalf("decoded = %+v", record.Decoded)
	}
}
