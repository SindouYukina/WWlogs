//go:build wwdev

package capture

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDamagePushRecordsPushedDamageWithObservedIdentifiers(t *testing.T) {
	push := decodedField{Number: FieldCombatNotifyDataDamageExecuteNotify, Name: "DamageExecuteNotify", Kind: "class", Message: &decodedMessage{Type: "DamageExecuteNotify", Fields: []decodedField{
		{Number: FieldDamageExecuteNotifyDamageID, Name: "DamageId", Kind: "int64", Value: int64(1001)},
		{Number: FieldDamageExecuteNotifyAttackerEntityID, Name: "AttackerEntityId", Kind: "int64", Value: int64(51518)},
		{Number: FieldDamageExecuteNotifyTargetEntityID, Name: "TargetEntityId", Kind: "int64", Value: int64(52082)},
		{Number: FieldDamageExecuteNotifyDamage, Name: "Damage", Kind: "int32", Value: int32(438)},
		{Number: FieldDamageExecuteNotifyChangeLife, Name: "ChangeLife", Kind: "int32", Value: int32(438)},
	}}}
	notify := decodedField{Number: FieldCombatReceiveDataCombatNotifyData, Name: "CombatNotifyData", Kind: "class", Message: &decodedMessage{Fields: []decodedField{push}}}
	data := decodedField{Number: FieldCombatReceivePackNotifyData, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{notify}}}
	decoded := &decodedMessage{Type: "CombatReceivePackNotify", Fields: []decodedField{data}}

	var out strings.Builder
	p := &Processor{SessionNumber: 1, Dev: processorDiagnostics{damagePushOut: &out, frameOrdinal: 4}}
	p.archiveDamagePushes(GameFrame{}, decoded)

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
