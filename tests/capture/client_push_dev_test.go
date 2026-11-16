//go:build wwdev

package capture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestClientPushRecordsRoleSwitchAndActiveRoleOnSkill(t *testing.T) {
	var out strings.Builder
	p := &processor{clientPushOut: &out, frameOrdinal: 1, sessionNumber: 1}
	frame := gameFrame{commandID: cmdCombatSendPackRequest, timestamp: time.Date(2026, 10, 28, 0, 36, 5, 0, time.UTC)}

	role := decodedField{Number: fieldCombatPushDataClientCurrentRoleReportPush, Name: "ClientCurrentRoleReportPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: fieldClientCurrentRoleReportPushCurrentRoleID, Name: "CurrentRoleId", Kind: "int32", Value: int32(1409)},
		{Number: fieldClientCurrentRoleReportPushCurrentEntityID, Name: "CurrentEntityId", Kind: "int64", Value: int64(51522)},
	}}}
	p.archiveClientPushes(frame, pushWrapper(role))

	skill := decodedField{Number: fieldCombatPushDataEndSkillPush, Name: "EndSkillPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: fieldEndSkillPushUseSkillInfo, Name: "UseSkillInfo", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
			{Number: fieldUseSkillInformationSkillID, Name: "SkillId", Kind: "int32", Value: int32(80025056)},
			{Number: fieldUseSkillInformationTimeStamp, Name: "TimeStamp", Kind: "float", Value: float64(54.5)},
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
	if skillRow.SkillRoleKey == nil || *skillRow.SkillRoleKey != 80025 {
		t.Fatalf("skill role prefix = %+v", skillRow)
	}
}

func TestClientPushSkillBeforeAnyRoleReportHasNoActiveRole(t *testing.T) {
	var out strings.Builder
	p := &processor{clientPushOut: &out, frameOrdinal: 1}
	skill := decodedField{Number: fieldCombatPushDataEndSkillPush, Name: "EndSkillPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: fieldEndSkillPushUseSkillInfo, Name: "UseSkillInfo", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
			{Number: fieldUseSkillInformationSkillID, Name: "SkillId", Kind: "int32", Value: int32(100001)},
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

func TestCombatActionRequestCapturedWithoutClientPushOutput(t *testing.T) {
	p := &processor{combatActionRequests: make(map[combatActionRequestKey]combatActionRequest)}
	info := &decodedMessage{Fields: []decodedField{
		{Name: "SkillId", Value: int64(280026010)},
		{Name: "TargetId", Value: int64(28288)},
	}}
	request := &decodedMessage{Fields: []decodedField{
		{Name: "CombatCommon", Message: &decodedMessage{Fields: []decodedField{
			{Name: "MessageId", Value: int64(1152921504606850898)},
			{Name: "EntityId", Value: int64(27736)},
		}}},
		{Name: "UseSkillRequest", Message: &decodedMessage{Fields: []decodedField{{Name: "UseSkillInfo", Message: info}}}},
	}}
	p.observeCombatActionRequest(gameFrame{timestamp: time.Date(2026, 10, 28, 4, 58, 26, 0, time.UTC)}, request)

	got, ok := p.combatActionRequests[combatActionRequestKey{sessionNumber: 0, messageID: 1152921504606850898}]
	if !ok || got.SkillID != 280026010 || got.EntityID != 27736 || got.TargetID != 28288 || got.RequestType != "UseSkillRequest" {
		t.Fatalf("captured action = %+v, present=%v", got, ok)
	}

	pending := pendingDamageRequest{skillMessageID: got.MessageID}
	event := (&processor{combatActionRequests: p.combatActionRequests, entities: newEntityResolver()}).buildDamageEvent(1, pending, 0, 0, false, 1)
	if event.SkillRequest == nil || event.SkillRequest.SkillID != got.SkillID {
		t.Fatalf("damage request did not link exact outbound action: %+v", event.SkillRequest)
	}
}

func TestSkillRequestIsArchivedEvenWhenNoDamageIsObserved(t *testing.T) {
	var out strings.Builder
	p := &processor{clientPushOut: &out, combatActionRequests: make(map[combatActionRequestKey]combatActionRequest), frameOrdinal: 1, sessionNumber: 1}
	info := &decodedMessage{Fields: []decodedField{
		{Name: "SkillId", Value: int64(7001001)},
		{Name: "TargetId", Value: int64(28288)},
	}}
	request := &decodedMessage{Fields: []decodedField{
		{Name: "CombatCommon", Message: &decodedMessage{Fields: []decodedField{
			{Name: "MessageId", Value: int64(9001)},
			{Name: "EntityId", Value: int64(27734)},
		}}},
		{Name: "UseSkillRequest", Message: &decodedMessage{Fields: []decodedField{{Name: "UseSkillInfo", Message: info}}}},
	}}
	data := &decodedMessage{Fields: []decodedField{{Name: "Request", Message: request}}}
	decoded := &decodedMessage{Fields: []decodedField{{Name: "Data", Message: data}}}
	p.archiveClientPushes(gameFrame{commandID: cmdCombatSendPackRequest, timestamp: time.Date(2026, 10, 28, 4, 58, 26, 0, time.UTC)}, decoded)

	var row clientPushRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &row); err != nil {
		t.Fatalf("request row: %v", err)
	}
	if row.PushName != "SkillActionRequest" || row.MessageID == nil || *row.MessageID != 9001 || row.TargetEntityID == nil || *row.TargetEntityID != 28288 {
		t.Fatalf("request row = %+v", row)
	}
	if !strings.Contains(row.Evidence, "do not establish a miss") {
		t.Fatalf("request evidence = %q", row.Evidence)
	}
	key := combatActionRequestKey{sessionNumber: 1, messageID: 9001}
	action := p.combatActionRequests[key]
	action.LinkedDamageEvents = 1
	p.combatActionRequests[key] = action
	p.observeCombatActionRequest(gameFrame{}, request)
	if p.combatActionRequests[key].LinkedDamageEvents != 1 || strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
		t.Fatalf("duplicate request changed the action link count or emitted a duplicate row: %+v", p.combatActionRequests[key])
	}
}

// The research schema decode of a role report writes its research record only; the local role entity is the core
// decoder's (observeLocalRoleReports), so a research input can never set or change it (Phase 3A R03).
func TestResearchRoleReportDoesNotChangeTheLocalRoleEntity(t *testing.T) {
	var out strings.Builder
	p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
	p.clientPushOut, p.frameOrdinal = &out, 1
	role := decodedField{Number: fieldCombatPushDataClientCurrentRoleReportPush, Name: "ClientCurrentRoleReportPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: fieldClientCurrentRoleReportPushCurrentRoleID, Name: "CurrentRoleId", Kind: "int32", Value: int32(1409)},
		{Number: fieldClientCurrentRoleReportPushCurrentEntityID, Name: "CurrentEntityId", Kind: "int64", Value: int64(51522)},
	}}}
	p.archiveClientPushes(gameFrame{}, pushWrapper(role))
	if p.playerRelevance.isRoleEntity(51522) {
		t.Fatal("the research decode set the core local role entity")
	}
	if p.stats.playerRoleReports != 0 {
		t.Fatalf("the research decode counted a core role report: %+v", p.stats)
	}
	var row clientPushRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &row); err != nil || row.EntityID == nil || *row.EntityID != 51522 {
		t.Fatalf("research role record = %q (%v)", out.String(), err)
	}
}

// With a research schema the same outbound request is decoded twice: once by the core (state, statistics), once for
// research outputs. The core result equals the run without a schema.
func TestRoleReportWithResearchSchemaKeepsTheCoreResult(t *testing.T) {
	var out strings.Builder
	p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
	p.clientPushOut = &out
	p.protocolSchema = &protocolSchema{}
	p.archiveClientPushFrame(gameFrame{commandID: cmdCombatSendPackRequest, direction: "out"}, localRoleReportBody(1409, 51522))
	if !p.playerRelevance.isRoleEntity(51522) || p.stats.playerRoleReports != 1 {
		t.Fatalf("core role decoder did not run with a research schema: role=%v stats=%+v", p.playerRelevance.isRoleEntity(51522), p.stats)
	}
}
