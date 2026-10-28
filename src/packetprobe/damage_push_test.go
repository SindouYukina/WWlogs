package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDamagePushRecordsPushedDamageWithObservedIdentifiers(t *testing.T) {
	push := decodedField{Number: 4, Name: "DamageExecuteNotify", Kind: "class", Message: &decodedMessage{Type: "DamageExecuteNotify", Fields: []decodedField{
		{Number: 7, Name: "DamageId", Kind: "int64", Value: int64(1001)},
		{Number: 14, Name: "AttackerEntityId", Kind: "int64", Value: int64(51518)},
		{Number: 12, Name: "TargetEntityId", Kind: "int64", Value: int64(52082)},
		{Number: 13, Name: "Damage", Kind: "int32", Value: int32(438)},
		{Number: 10, Name: "ChangeLife", Kind: "int32", Value: int32(438)},
	}}}
	notify := decodedField{Number: 2, Name: "CombatNotifyData", Kind: "class", Message: &decodedMessage{Fields: []decodedField{push}}}
	data := decodedField{Number: 5, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{notify}}}
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
