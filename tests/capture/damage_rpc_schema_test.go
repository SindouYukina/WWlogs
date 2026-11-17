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
			{Number: FieldDamageExecuteResponseDamage, Name: "Damage", schemaType: schemaType{Kind: "int32"}},
			{Number: fieldDamageExecuteResponseIsCrit, Name: "IsCrit", schemaType: schemaType{Kind: "bool"}},
		}},
	}}
	payload := appendVarintField(appendVarintField(nil, FieldDamageExecuteResponseDamage, 100), fieldDamageExecuteResponseIsCrit, 1)
	item := appendBytesField(appendVarintField(nil, FieldCombatResponseDataRequestID, 5), FieldCombatResponseDataDamageExecuteResponse, payload)
	batch := appendBytesField(nil, FieldCombatReceiveDataCombatResponseData, item)
	wrapped := appendBytesField(nil, FieldCombatReceivePackNotifyData, batch)
	body := appendBytesField(nil, FieldCombatSendPackResponseReceivePackNotify, wrapped)

	var out strings.Builder
	p := &Processor{
		SessionNumber: 2,
		Dev: processorDiagnostics{
			protocolSchema:  schema,
			schemaDecodeOut: &out,
			frameOrdinal:    1,
		},
	}
	p.ArchiveDamageRPCSchema(GameFrame{CommandID: CmdCombatSendPackResponse, Direction: "in"}, body)
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
