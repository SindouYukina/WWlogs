package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func pushWrapper(child decodedField) *decodedMessage {
	push := decodedField{Number: 2, Name: "Push", Kind: "class", Message: &decodedMessage{Fields: []decodedField{child}}}
	return &decodedMessage{Type: "CombatSendPackRequest", Fields: []decodedField{
		{Number: 14, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{push}}},
	}}
}

func TestClientPushRecordsRoleSwitchAndActiveRoleOnSkill(t *testing.T) {
	var out strings.Builder
	p := &processor{clientPushOut: &out, frameOrdinal: 1, sessionNumber: 1}
	frame := gameFrame{commandID: 19470, timestamp: time.Date(2026, 10, 28, 0, 36, 5, 0, time.UTC)}

	role := decodedField{Number: 1, Name: "ClientCurrentRoleReportPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: 1, Name: "CurrentRoleId", Kind: "int32", Value: int32(1409)},
		{Number: 2, Name: "CurrentEntityId", Kind: "int64", Value: int64(51522)},
	}}}
	p.archiveClientPushes(frame, pushWrapper(role))

	skill := decodedField{Number: 2, Name: "EndSkillPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: 1, Name: "UseSkillInfo", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
			{Number: 1, Name: "SkillId", Kind: "int32", Value: int32(80025056)},
			{Number: 2, Name: "TimeStamp", Kind: "float", Value: float64(54.5)},
		}}},
	}}}
	p.archiveClientPushes(frame, pushWrapper(skill))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("records = %d, want 2", len(lines))
	}
	var roleRow, skillRow clientPushRecord
	if err := json.Unmarshal([]byte(lines[0]), &roleRow); err != nil {
		t.Fatalf("role row: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &skillRow); err != nil {
		t.Fatalf("skill row: %v", err)
	}
	if roleRow.CurrentRoleID == nil || *roleRow.CurrentRoleID != 1409 || roleRow.EntityID == nil || *roleRow.EntityID != 51522 {
		t.Fatalf("role row = %+v", roleRow)
	}
	if skillRow.SkillID == nil || *skillRow.SkillID != 80025056 || skillRow.ActiveRoleID == nil || *skillRow.ActiveRoleID != 1409 {
		t.Fatalf("skill row = %+v", skillRow)
	}
}

func TestClientPushSkillBeforeAnyRoleReportHasNoActiveRole(t *testing.T) {
	var out strings.Builder
	p := &processor{clientPushOut: &out, frameOrdinal: 1}
	skill := decodedField{Number: 2, Name: "EndSkillPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: 1, Name: "UseSkillInfo", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
			{Number: 1, Name: "SkillId", Kind: "int32", Value: int32(100001)},
		}}},
	}}}
	p.archiveClientPushes(gameFrame{}, pushWrapper(skill))
	var row clientPushRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &row); err != nil {
		t.Fatalf("row: %v", err)
	}
	if row.ActiveRoleID != nil {
		t.Fatalf("active role = %v, want absent before any report", *row.ActiveRoleID)
	}
}
