package devcapture

import (
	"encoding/json"
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

func TestDamageRPCSchemaExtractsResponsePayloadByRPCID(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"DamageExecuteResponse": {Fields: []schemaField{
			{Number: capture.FieldDamageExecuteResponseDamage, Name: "Damage", schemaType: schemaType{Kind: "int32"}},
			{Number: capture.FieldDamageExecuteResponseIsCrit, Name: "IsCrit", schemaType: schemaType{Kind: "bool"}},
		}},
	}}
	payload := appendVarintField(appendVarintField(nil, capture.FieldDamageExecuteResponseDamage, 100), capture.FieldDamageExecuteResponseIsCrit, 1)
	item := appendBytesField(appendVarintField(nil, capture.FieldCombatResponseDataRequestID, 5), capture.FieldCombatResponseDataDamageExecuteResponse, payload)
	batch := appendBytesField(nil, capture.FieldCombatReceiveDataCombatResponseData, item)
	wrapped := appendBytesField(nil, capture.FieldCombatReceivePackNotifyData, batch)
	body := appendBytesField(nil, capture.FieldCombatSendPackResponseReceivePackNotify, wrapped)

	var out strings.Builder
	p := observed(&processor{Processor: &capture.Processor{SessionNumber: 2}, dev: processorDiagnostics{
		protocolSchema:  schema,
		schemaDecodeOut: &out,
		frameOrdinal:    1,
	}})
	p.ArchiveDamageRPCSchema(capture.GameFrame{CommandID: capture.CmdCombatSendPackResponse, Direction: "in"}, body)
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
