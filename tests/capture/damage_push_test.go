//go:build wwdev

package capture

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDamagePushRecordsPushedDamageWithObservedIdentifiers(t *testing.T) {
	push := decodedField{Number: fieldCombatNotifyDataDamageExecuteNotify, Name: "DamageExecuteNotify", Kind: "class", Message: &decodedMessage{Type: "DamageExecuteNotify", Fields: []decodedField{
		{Number: fieldDamageExecuteNotifyDamageID, Name: "DamageId", Kind: "int64", Value: int64(1001)},
		{Number: fieldDamageExecuteNotifyAttackerEntityID, Name: "AttackerEntityId", Kind: "int64", Value: int64(51518)},
		{Number: fieldDamageExecuteNotifyTargetEntityID, Name: "TargetEntityId", Kind: "int64", Value: int64(52082)},
		{Number: fieldDamageExecuteNotifyDamage, Name: "Damage", Kind: "int32", Value: int32(438)},
		{Number: fieldDamageExecuteNotifyChangeLife, Name: "ChangeLife", Kind: "int32", Value: int32(438)},
	}}}
	notify := decodedField{Number: fieldCombatReceiveDataCombatNotifyData, Name: "CombatNotifyData", Kind: "class", Message: &decodedMessage{Fields: []decodedField{push}}}
	data := decodedField{Number: fieldCombatReceivePackNotifyData, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{notify}}}
	decoded := &decodedMessage{Type: "CombatReceivePackNotify", Fields: []decodedField{data}}

	var out strings.Builder
	p := &processor{damagePushOut: &out, frameOrdinal: 4, sessionNumber: 1}
	p.archiveDamagePushes(gameFrame{}, decoded)

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
