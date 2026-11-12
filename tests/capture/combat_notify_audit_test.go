//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCombatNotifyAuditDecodesWhitelistedDamageExecuteFieldsOnly(t *testing.T) {
	context := appendVarintField(nil, fieldDamageContextSourceType, 2)
	context = appendVarintField(context, fieldDamageContextBulletID, 1312014001)
	context = appendVarintField(context, fieldDamageContextSkillID, 1312014)
	context = appendVarintField(context, fieldDamageContextSkillMessageID, 987654321)
	context = appendVarintField(context, 99, 445566)
	body := appendVarintField(nil, fieldDamageExecuteNotifyDamageID, 1312014001)    // DamageId
	body = appendVarintField(body, fieldDamageExecuteNotifyAttackerEntityID, 35186) // AttackerEntityId
	body = appendVarintField(body, fieldDamageExecuteNotifyTargetEntityID, 27472)   // TargetEntityId
	body = appendVarintField(body, fieldDamageExecuteNotifyDamage, 42626)           // Damage
	body = appendBytesField(body, fieldDamageExecuteNotifyDamageContext, context)   // DamageContext
	body = appendVarintField(body, 99, 887766)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, gameFrame{commandID: cmdCombatReceivePackNotify}, protoField{number: fieldCombatNotifyDataDamageExecuteNotify, wire: 2, data: body}, nil, nil, nil, 1); err != nil {
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
	if row.SchemaVersion != 2 || row.InnerFieldNumber != fieldCombatNotifyDataDamageExecuteNotify || row.InnerMessageName != "DamageExecuteNotify" || row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" {
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
	body := appendVarintField(nil, fieldChangeStateNotifyFsmID, 3)     // FsmId
	body = appendVarintField(body, fieldChangeStateNotifyFromState, 8) // FromState
	body = appendVarintField(body, fieldChangeStateNotifyToState, 9)   // ToState
	body = appendVarintField(body, 99, 123456)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, gameFrame{commandID: cmdCombatReceivePackNotify}, protoField{number: fieldCombatNotifyDataChangeStateNotify, wire: 2, data: body}, nil, nil, nil, 1); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	if strings.Contains(output.String(), "123456") {
		t.Fatal("audit emitted a non-allowlisted field value")
	}
	var row combatNotifyAuditRow
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	if row.SchemaVersion != 2 || row.InnerFieldNumber != fieldCombatNotifyDataChangeStateNotify || row.InnerMessageName != "ChangeStateNotify" || row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" {
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
	malformed := appendBytesField(nil, fieldDamageExecuteNotifyDamageContext, []byte{0x08, 0x80})
	var output bytes.Buffer
	err := writeCombatNotifyAudit(&output, gameFrame{}, protoField{number: fieldCombatNotifyDataDamageExecuteNotify, wire: 2, data: malformed}, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("expected malformed DamageContext payload to fail")
	}
}

func TestCombatNotifyAuditDecodesWhitelistedActivateBuffFieldsOnly(t *testing.T) {
	body := appendVarintField(nil, fieldActivateBuffNotifyOn, 1)      // On
	body = appendVarintField(body, fieldActivateBuffNotifyHandle, 68) // Handle
	body = appendVarintField(body, 99, 987654321)                     // not an ActivateBuffNotify field
	var output bytes.Buffer
	entityID, originatorID, messageID := uint64(27472), uint64(1001), uint64(78)
	frame := gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in", timestamp: time.Date(2026, 10, 27, 15, 36, 18, 0, time.FixedZone("JST", 9*60*60))}
	if err := writeCombatNotifyAudit(&output, frame, protoField{number: fieldCombatNotifyDataActivateBuffNotify, wire: 2, data: body}, &entityID, &originatorID, &messageID, 1); err != nil {
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
	if row.InnerFieldNumber != fieldCombatNotifyDataActivateBuffNotify || row.InnerMessageName != "ActivateBuffNotify" || row.DecodeStatus != "CANDIDATE_SCHEMA_FIELDS_DECODED" {
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
	attribute := appendFixed32TestField(nil, fieldGameplayAttributeDataBaseValue, math.Float32bits(42.5))
	attribute = appendFixed32TestField(attribute, fieldGameplayAttributeDataCurrentValue, math.Float32bits(37.25))
	attribute = appendVarintField(attribute, fieldGameplayAttributeDataAttributeType, 17)
	body := appendBytesField(nil, fieldAttributeChangedNotifyAttributes, attribute)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, gameFrame{commandID: cmdCombatReceivePackNotify}, protoField{number: fieldCombatNotifyDataAttributeChangedNotify, wire: 2, data: body}, nil, nil, nil, 1); err != nil {
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
	attribute := appendVarintField(nil, fieldRecoverAttrAttrID, 6)
	attribute = appendFixed32TestField(attribute, fieldRecoverAttrRatio, math.Float32bits(0.5))
	attribute = appendFixed32TestField(attribute, fieldRecoverAttrMaxValue, math.Float32bits(100))
	attribute = appendFixed32TestField(attribute, fieldRecoverAttrCurrentValue, math.Float32bits(45))
	body := appendBytesField(nil, fieldRecoverPropChangedNotifyAttributes, attribute)
	var output bytes.Buffer
	if err := writeCombatNotifyAudit(&output, gameFrame{commandID: cmdCombatReceivePackNotify}, protoField{number: fieldCombatNotifyDataRecoverPropChangedNotify, wire: 2, data: body}, nil, nil, nil, 1); err != nil {
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
	malformed := append(appendTestVarint(nil, fieldAttributeChangedNotifyAttributes<<3|2), 0x05, 0x01) // Attributes declares 5 bytes, carries 1
	var output bytes.Buffer
	err := writeCombatNotifyAudit(&output, gameFrame{}, protoField{number: fieldCombatNotifyDataAttributeChangedNotify, wire: 2, data: malformed}, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("expected malformed nested payload to fail")
	}
}
