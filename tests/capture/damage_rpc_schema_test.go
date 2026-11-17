//go:build wwdev

package capture

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDamageRPCSchemaExtractsResponsePayloadByRPCID(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"DamageExecuteResponse": {Fields: []schemaField{
			{Number: fieldDamageExecuteResponseDamage, Name: "Damage", schemaType: schemaType{Kind: "int32"}},
			{Number: fieldDamageExecuteResponseIsCrit, Name: "IsCrit", schemaType: schemaType{Kind: "bool"}},
		}},
	}}
	payload := appendVarintField(appendVarintField(nil, fieldDamageExecuteResponseDamage, 100), fieldDamageExecuteResponseIsCrit, 1)
	item := appendBytesField(appendVarintField(nil, fieldCombatResponseDataRequestID, 5), fieldCombatResponseDataDamageExecuteResponse, payload)
	batch := appendBytesField(nil, fieldCombatReceiveDataCombatResponseData, item)
	wrapped := appendBytesField(nil, fieldCombatReceivePackNotifyData, batch)
	body := appendBytesField(nil, fieldCombatSendPackResponseReceivePackNotify, wrapped)

	var out strings.Builder
	p := &processor{
		sessionNumber: 2,
		dev: processorDiagnostics{
			protocolSchema:  schema,
			schemaDecodeOut: &out,
			frameOrdinal:    1,
		},
	}
	p.archiveDamageRPCSchema(gameFrame{commandID: cmdCombatSendPackResponse, direction: "in"}, body)
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
