//go:build wwdev

package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCombatNotifyAuditDecodesWhitelistedActivateBuffFieldsOnly(t *testing.T) {
	body := appendVarintField(nil, 1, 1)  // On
	body = appendVarintField(body, 8, 68) // Handle
	body = appendVarintField(body, 7, 987654321)
	var output bytes.Buffer
	entityID, originatorID, messageID := uint64(27472), uint64(1001), uint64(78)
	frame := gameFrame{commandID: 25706, direction: "in", timestamp: time.Date(2026, 10, 27, 15, 36, 18, 0, time.FixedZone("JST", 9*60*60))}
	if err := writeCombatNotifyAudit(&output, frame, protoField{number: 29, wire: 2, data: body}, &entityID, &originatorID, &messageID, 1); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	encoded := output.String()
	if strings.Contains(encoded, "987654321") {
		t.Fatal("audit emitted a non-allowlisted field value")
	}
	var row combatNotifyAuditRow
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	if row.InnerFieldNumber != 29 || row.InnerMessageName != "ActivateBuffNotify" || row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" {
		t.Fatalf("unexpected audit header: %+v", row)
	}
	if len(row.NestedRecords) != 1 || len(row.NestedRecords[0].Values) != 2 {
		t.Fatalf("unexpected nested values: %+v", row.NestedRecords)
	}
	if *row.NestedRecords[0].Values[0].Unsigned != 1 || *row.NestedRecords[0].Values[1].Unsigned != 68 {
		t.Fatalf("unexpected ActivateBuffNotify values: %+v", row.NestedRecords[0].Values)
	}
}

func TestCombatNotifyAuditDecodesAttributeValuesAndAttributeType(t *testing.T) {
	attribute := appendFixed32TestField(nil, 1, math.Float32bits(42.5))
	attribute = appendFixed32TestField(attribute, 2, math.Float32bits(37.25))
	attribute = appendVarintField(attribute, 3, 17)
	body := appendBytesField(nil, 8, attribute)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, gameFrame{commandID: 25706}, protoField{number: 22, wire: 2, data: body}, nil, nil, nil, 1); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	var row combatNotifyAuditRow
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	if len(row.NestedRecords) != 1 || len(row.NestedRecords[0].Values) != 3 {
		t.Fatalf("unexpected attribute record: %+v", row.NestedRecords)
	}
	values := row.NestedRecords[0].Values
	if *values[0].Float != 42.5 || *values[1].Float != 37.25 || *values[2].Unsigned != 17 {
		t.Fatalf("unexpected attribute values: %+v", values)
	}
}

func TestCombatNotifyAuditDecodesRecoverAttributes(t *testing.T) {
	attribute := appendVarintField(nil, 1, 6)
	attribute = appendFixed32TestField(attribute, 2, math.Float32bits(0.5))
	attribute = appendFixed32TestField(attribute, 4, math.Float32bits(100))
	attribute = appendFixed32TestField(attribute, 5, math.Float32bits(45))
	body := appendBytesField(nil, 6, attribute)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, gameFrame{commandID: 25706}, protoField{number: 58, wire: 2, data: body}, nil, nil, nil, 1); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	var row combatNotifyAuditRow
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	if len(row.NestedRecords) != 1 || len(row.NestedRecords[0].Values) != 4 {
		t.Fatalf("unexpected recovery attribute: %+v", row.NestedRecords)
	}
	if *row.NestedRecords[0].Values[0].Unsigned != 6 || *row.NestedRecords[0].Values[1].Float != 0.5 || *row.NestedRecords[0].Values[2].Float != 100 || *row.NestedRecords[0].Values[3].Float != 45 {
		t.Fatalf("unexpected recovery values: %+v", row.NestedRecords[0].Values)
	}
}

func TestCombatNotifyAuditDecodesSquadAttributeValues(t *testing.T) {
	attribute := appendVarintField(nil, 1, 42000)
	attribute = appendVarintField(attribute, 2, 50000)
	attribute = appendVarintField(attribute, 3, 3)
	body := appendBytesField(nil, 13, attribute)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, gameFrame{commandID: 25706}, protoField{number: 87, wire: 2, data: body}, nil, nil, nil, 1); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	var row combatNotifyAuditRow
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	if row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" || len(row.NestedRecords) != 1 || len(row.NestedRecords[0].Values) != 3 {
		t.Fatalf("unexpected squad attribute record: %+v", row)
	}
	values := row.NestedRecords[0].Values
	if *values[0].Unsigned != 42000 || *values[1].Unsigned != 50000 || *values[2].Unsigned != 3 {
		t.Fatalf("unexpected squad attribute values: %+v", values)
	}
}

func TestCombatNotifyAuditRejectsMalformedNestedPayload(t *testing.T) {
	malformed := []byte{0x42, 0x05, 0x01}
	var output bytes.Buffer
	err := writeCombatNotifyAudit(&output, gameFrame{}, protoField{number: 22, wire: 2, data: malformed}, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("expected malformed nested payload to fail")
	}
}
