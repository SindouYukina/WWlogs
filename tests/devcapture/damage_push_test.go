package devcapture

import (
	"encoding/json"
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

func TestDamagePushRecordsPushedDamageWithObservedIdentifiers(t *testing.T) {
	push := decodedField{Number: capture.FieldCombatNotifyDataDamageExecuteNotify, Name: "DamageExecuteNotify", Kind: "class", Message: &decodedMessage{Type: "DamageExecuteNotify", Fields: []decodedField{
		{Number: capture.FieldDamageExecuteNotifyDamageID, Name: "DamageId", Kind: "int64", Value: int64(1001)},
		{Number: capture.FieldDamageExecuteNotifyAttackerEntityID, Name: "AttackerEntityId", Kind: "int64", Value: int64(51518)},
		{Number: capture.FieldDamageExecuteNotifyTargetEntityID, Name: "TargetEntityId", Kind: "int64", Value: int64(52082)},
		{Number: capture.FieldDamageExecuteNotifyDamage, Name: "Damage", Kind: "int32", Value: int32(438)},
		{Number: capture.FieldDamageExecuteNotifyChangeLife, Name: "ChangeLife", Kind: "int32", Value: int32(438)},
	}}}
	notify := decodedField{Number: capture.FieldCombatReceiveDataCombatNotifyData, Name: "CombatNotifyData", Kind: "class", Message: &decodedMessage{Fields: []decodedField{push}}}
	data := decodedField{Number: capture.FieldCombatReceivePackNotifyData, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{notify}}}
	decoded := &decodedMessage{Type: "CombatReceivePackNotify", Fields: []decodedField{data}}

	var out strings.Builder
	p := observed(&processor{Processor: &capture.Processor{SessionNumber: 1}, dev: processorDiagnostics{damagePushOut: &out, frameOrdinal: 4}})
	p.archiveDamagePushes(capture.GameFrame{}, decoded)

	var record damagePushRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &record); err != nil {
		t.Fatalf("record is not JSON: %v (%q)", err, out.String())
	}
	if record.AttackerID != 51518 || record.TargetID != 52082 || record.Damage != 438 || record.DamageID != 1001 || record.Stream != "DAMAGE_EXECUTE_NOTIFY_PUSH" {
		t.Fatalf("record = %+v", record)
	}
	if record.FrameOrdinal != 3 {
		t.Fatalf("frame ordinal = %d, want 3", record.FrameOrdinal)
	}
}
