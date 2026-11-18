package devcapture

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestCombatNotifyAuditDecodesWhitelistedDamageExecuteFieldsOnly(t *testing.T) {
	context := appendVarintField(nil, capture.FieldDamageContextSourceType, 2)
	context = appendVarintField(context, capture.FieldDamageContextBulletID, 1312014001)
	context = appendVarintField(context, capture.FieldDamageContextSkillID, 1312014)
	context = appendVarintField(context, capture.FieldDamageContextSkillMessageID, 987654321)
	context = appendVarintField(context, 99, 445566)
	body := appendVarintField(nil, capture.FieldDamageExecuteNotifyDamageID, 1312014001)    // DamageId
	body = appendVarintField(body, capture.FieldDamageExecuteNotifyAttackerEntityID, 35186) // AttackerEntityId
	body = appendVarintField(body, capture.FieldDamageExecuteNotifyTargetEntityID, 27472)   // TargetEntityId
	body = appendVarintField(body, capture.FieldDamageExecuteNotifyDamage, 42626)           // Damage
	body = appendBytesField(body, capture.FieldDamageExecuteNotifyDamageContext, context)   // DamageContext
	body = appendVarintField(body, 99, 887766)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, capture.GameFrame{CommandID: capture.CmdCombatReceivePackNotify}, capture.ProtoField{Number: capture.FieldCombatNotifyDataDamageExecuteNotify, Wire: 2, Data: body}, nil, nil, nil, 1); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	encoded := output.String()
	for _, forbidden := range []string{"445566", "887766"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("audit emitted non-allowlisted value %s", forbidden)
		}
	}
	var row combatNotifyAuditRow
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	if row.SchemaVersion != 2 || row.InnerFieldNumber != capture.FieldCombatNotifyDataDamageExecuteNotify || row.InnerMessageName != "DamageExecuteNotify" || row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" {
		t.Fatalf("unexpected audit header: %+v", row)
	}
	if len(row.NestedRecords) != 2 || row.NestedRecords[0].SchemaName != "DamageExecuteNotify" || row.NestedRecords[1].SchemaName != "DamageContext" {
		t.Fatalf("unexpected damage records: %+v", row.NestedRecords)
	}
	if len(row.NestedRecords[0].Values) != 4 || *row.NestedRecords[0].Values[0].Unsigned != 1312014001 || *row.NestedRecords[0].Values[1].Unsigned != 35186 || *row.NestedRecords[0].Values[2].Unsigned != 27472 || *row.NestedRecords[0].Values[3].Unsigned != 42626 {
		t.Fatalf("unexpected damage values: %+v", row.NestedRecords[0].Values)
	}
	if len(row.NestedRecords[1].Values) != 4 || *row.NestedRecords[1].Values[0].Unsigned != 2 || *row.NestedRecords[1].Values[1].Unsigned != 1312014001 || *row.NestedRecords[1].Values[2].Unsigned != 1312014 || *row.NestedRecords[1].Values[3].Unsigned != 987654321 {
		t.Fatalf("unexpected DamageContext values: %+v", row.NestedRecords[1].Values)
	}
}

func TestCombatNotifyAuditDecodesWhitelistedChangeStateFieldsOnly(t *testing.T) {
	body := appendVarintField(nil, capture.FieldChangeStateNotifyFsmID, 3)     // FsmId
	body = appendVarintField(body, capture.FieldChangeStateNotifyFromState, 8) // FromState
	body = appendVarintField(body, capture.FieldChangeStateNotifyToState, 9)   // ToState
	body = appendVarintField(body, 99, 123456)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, capture.GameFrame{CommandID: capture.CmdCombatReceivePackNotify}, capture.ProtoField{Number: capture.FieldCombatNotifyDataChangeStateNotify, Wire: 2, Data: body}, nil, nil, nil, 1); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	if strings.Contains(output.String(), "123456") {
		t.Fatal("audit emitted a non-allowlisted field value")
	}
	var row combatNotifyAuditRow
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	if row.SchemaVersion != 2 || row.InnerFieldNumber != capture.FieldCombatNotifyDataChangeStateNotify || row.InnerMessageName != "ChangeStateNotify" || row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" {
		t.Fatalf("unexpected audit header: %+v", row)
	}
	if len(row.NestedRecords) != 1 || len(row.NestedRecords[0].Values) != 3 {
		t.Fatalf("unexpected state values: %+v", row.NestedRecords)
	}
	values := row.NestedRecords[0].Values
	if *values[0].Unsigned != 3 || *values[1].Unsigned != 8 || *values[2].Unsigned != 9 {
		t.Fatalf("unexpected ChangeStateNotify values: %+v", values)
	}
}

func TestCombatNotifyAuditRejectsMalformedDamageContext(t *testing.T) {
	malformed := appendBytesField(nil, capture.FieldDamageExecuteNotifyDamageContext, []byte{0x08, 0x80})
	var output bytes.Buffer
	err := writeCombatNotifyAudit(&output, capture.GameFrame{}, capture.ProtoField{Number: capture.FieldCombatNotifyDataDamageExecuteNotify, Wire: 2, Data: malformed}, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("expected malformed DamageContext payload to fail")
	}
}

func TestCombatNotifyAuditDecodesWhitelistedActivateBuffFieldsOnly(t *testing.T) {
	body := appendVarintField(nil, capture.FieldActivateBuffNotifyOn, 1)      // On
	body = appendVarintField(body, capture.FieldActivateBuffNotifyHandle, 68) // Handle
	body = appendVarintField(body, 99, 987654321)                             // not an ActivateBuffNotify field
	var output bytes.Buffer
	entityID, originatorID, messageID := uint64(27472), uint64(1001), uint64(78)
	frame := capture.GameFrame{CommandID: capture.CmdCombatReceivePackNotify, Direction: "in", Timestamp: time.Date(2026, 10, 27, 15, 36, 18, 0, time.FixedZone("JST", 9*60*60))}
	if err := writeCombatNotifyAudit(&output, frame, capture.ProtoField{Number: capture.FieldCombatNotifyDataActivateBuffNotify, Wire: 2, Data: body}, &entityID, &originatorID, &messageID, 1); err != nil {
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
	if row.InnerFieldNumber != capture.FieldCombatNotifyDataActivateBuffNotify || row.InnerMessageName != "ActivateBuffNotify" || row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" {
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
	attribute := appendFixed32TestField(nil, capture.FieldGameplayAttributeDataBaseValue, math.Float32bits(42.5))
	attribute = appendFixed32TestField(attribute, capture.FieldGameplayAttributeDataCurrentValue, math.Float32bits(37.25))
	attribute = appendVarintField(attribute, capture.FieldGameplayAttributeDataAttributeType, 17)
	body := appendBytesField(nil, capture.FieldAttributeChangedNotifyAttributes, attribute)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, capture.GameFrame{CommandID: capture.CmdCombatReceivePackNotify}, capture.ProtoField{Number: capture.FieldCombatNotifyDataAttributeChangedNotify, Wire: 2, Data: body}, nil, nil, nil, 1); err != nil {
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
	attribute := appendVarintField(nil, capture.FieldRecoverAttrAttrID, 6)
	attribute = appendFixed32TestField(attribute, capture.FieldRecoverAttrRatio, math.Float32bits(0.5))
	attribute = appendFixed32TestField(attribute, capture.FieldRecoverAttrMaxValue, math.Float32bits(100))
	attribute = appendFixed32TestField(attribute, capture.FieldRecoverAttrCurrentValue, math.Float32bits(45))
	body := appendBytesField(nil, capture.FieldRecoverPropChangedNotifyAttributes, attribute)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, capture.GameFrame{CommandID: capture.CmdCombatReceivePackNotify}, capture.ProtoField{Number: capture.FieldCombatNotifyDataRecoverPropChangedNotify, Wire: 2, Data: body}, nil, nil, nil, 1); err != nil {
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

func TestCombatNotifyAuditRejectsMalformedNestedPayload(t *testing.T) {
	malformed := append(appendTestVarint(nil, capture.FieldAttributeChangedNotifyAttributes<<3|2), 0x05, 0x01) // Attributes declares 5 bytes, carries 1
	var output bytes.Buffer
	err := writeCombatNotifyAudit(&output, capture.GameFrame{}, capture.ProtoField{Number: capture.FieldCombatNotifyDataAttributeChangedNotify, Wire: 2, Data: malformed}, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("expected malformed nested payload to fail")
	}
}

// The CombatNotifyData audit rows name the protocol their fields were read with, the current client's
// (TestEvidenceNamesTheCurrentProtocol checks the evidence of both profiles).
func TestCombatNotifyAuditEvidenceNamesTheCurrentProtocol(t *testing.T) {
	if !strings.Contains(combatNotifyAuditSchemaBasis, capture.CurrentProtocol()) {
		t.Fatalf("combat notify audit %q does not name the current client protocol", combatNotifyAuditSchemaBasis)
	}
}
