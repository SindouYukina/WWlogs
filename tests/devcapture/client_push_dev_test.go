package devcapture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func pushWrapper(child decodedField) *decodedMessage {
	push := decodedField{Number: capture.FieldCombatSendDataPush, Name: "Push", Kind: "class", Message: &decodedMessage{Fields: []decodedField{child}}}
	return &decodedMessage{Type: "CombatSendPackRequest", Fields: []decodedField{
		{Number: capture.FieldCombatSendPackRequestData, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{push}}},
	}}
}

func TestClientPushRecordsRoleSwitchAndActiveRoleOnSkill(t *testing.T) {
	var out strings.Builder
	p := observed(&processor{Processor: &capture.Processor{SessionNumber: 1}, dev: processorDiagnostics{clientPushOut: &out, frameOrdinal: 1}})
	frame := capture.GameFrame{CommandID: capture.CmdCombatSendPackRequest, Timestamp: time.Date(2026, 10, 28, 0, 36, 5, 0, time.UTC)}

	role := decodedField{Number: capture.FieldCombatPushDataClientCurrentRoleReportPush, Name: "ClientCurrentRoleReportPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: capture.FieldClientCurrentRoleReportPushCurrentRoleID, Name: "CurrentRoleId", Kind: "int32", Value: int32(1409)},
		{Number: capture.FieldClientCurrentRoleReportPushCurrentEntityID, Name: "CurrentEntityId", Kind: "int64", Value: int64(51522)},
	}}}
	p.archiveClientPushes(frame, pushWrapper(role))

	skill := decodedField{Number: capture.FieldCombatPushDataEndSkillPush, Name: "EndSkillPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: capture.FieldEndSkillPushUseSkillInfo, Name: "UseSkillInfo", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
			{Number: capture.FieldUseSkillInformationSkillID, Name: "SkillId", Kind: "int32", Value: int32(80025056)},
			{Number: capture.FieldUseSkillInformationTimeStamp, Name: "TimeStamp", Kind: "float", Value: float64(54.5)},
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
	p := observed(&processor{Processor: &capture.Processor{}, dev: processorDiagnostics{clientPushOut: &out, frameOrdinal: 1}})
	skill := decodedField{Number: capture.FieldCombatPushDataEndSkillPush, Name: "EndSkillPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: capture.FieldEndSkillPushUseSkillInfo, Name: "UseSkillInfo", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
			{Number: capture.FieldUseSkillInformationSkillID, Name: "SkillId", Kind: "int32", Value: int32(100001)},
		}}},
	}}}
	p.archiveClientPushes(capture.GameFrame{}, pushWrapper(skill))
	var row clientPushRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &row); err != nil {
		t.Fatalf("row: %v", err)
	}
	if row.ActiveRoleID != nil {
		t.Fatalf("active role = %v, want absent before any report", *row.ActiveRoleID)
	}
}

func TestCombatActionRequestCapturedWithoutClientPushOutput(t *testing.T) {
	p := observed(&processor{Processor: &capture.Processor{}, dev: processorDiagnostics{combatActionRequests: make(map[combatActionRequestKey]combatActionRequest)}})
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
	p.observeCombatActionRequest(capture.GameFrame{Timestamp: time.Date(2026, 10, 28, 4, 58, 26, 0, time.UTC)}, request)

	got, ok := p.dev.combatActionRequests[combatActionRequestKey{sessionNumber: 0, messageID: 1152921504606850898}]
	if !ok || got.SkillID != 280026010 || got.EntityID != 27736 || got.TargetID != 28288 || got.RequestType != "UseSkillRequest" {
		t.Fatalf("captured action = %+v, present=%v", got, ok)
	}

	// A damage result with this SkillMessageId links the exact outbound action (writeDamageEventOutputs).
	link := (observed(&processor{Processor: &capture.Processor{}, dev: processorDiagnostics{combatActionRequests: p.dev.combatActionRequests}})).linkSkillRequest(got.MessageID)
	if link == nil || link.SkillID != got.SkillID || link.LinkedDamageEvents != 1 {
		t.Fatalf("damage request did not link exact outbound action: %+v", link)
	}
}

func TestSkillRequestIsArchivedEvenWhenNoDamageIsObserved(t *testing.T) {
	var out strings.Builder
	p := observed(&processor{Processor: &capture.Processor{SessionNumber: 1}, dev: processorDiagnostics{
		clientPushOut:        &out,
		combatActionRequests: make(map[combatActionRequestKey]combatActionRequest),
		frameOrdinal:         1,
	}})
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
	p.archiveClientPushes(capture.GameFrame{CommandID: capture.CmdCombatSendPackRequest, Timestamp: time.Date(2026, 10, 28, 4, 58, 26, 0, time.UTC)}, decoded)

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
	action := p.dev.combatActionRequests[key]
	action.LinkedDamageEvents = 1
	p.dev.combatActionRequests[key] = action
	p.observeCombatActionRequest(capture.GameFrame{}, request)
	if p.dev.combatActionRequests[key].LinkedDamageEvents != 1 || strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
		t.Fatalf("duplicate request changed the action link count or emitted a duplicate row: %+v", p.dev.combatActionRequests[key])
	}
}

// The research schema decode of a role report writes its research record only; the local role entity is the core
// decoder's (observeLocalRoleReports), so a research input can never set or change it (Phase 3A R03).
func TestResearchRoleReportDoesNotChangeTheLocalRoleEntity(t *testing.T) {
	var out strings.Builder
	p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
	p.dev.clientPushOut, p.dev.frameOrdinal = &out, 1
	role := decodedField{Number: capture.FieldCombatPushDataClientCurrentRoleReportPush, Name: "ClientCurrentRoleReportPush", Kind: "class", Message: &decodedMessage{Fields: []decodedField{
		{Number: capture.FieldClientCurrentRoleReportPushCurrentRoleID, Name: "CurrentRoleId", Kind: "int32", Value: int32(1409)},
		{Number: capture.FieldClientCurrentRoleReportPushCurrentEntityID, Name: "CurrentEntityId", Kind: "int64", Value: int64(51522)},
	}}}
	p.archiveClientPushes(capture.GameFrame{}, pushWrapper(role))
	if p.PlayerRelevance.IsRoleEntity(51522) {
		t.Fatal("the research decode set the core local role entity")
	}
	if p.Stats.PlayerRoleReports != 0 {
		t.Fatalf("the research decode counted a core role report: %+v", p.Stats)
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
	p.dev.clientPushOut = &out
	p.dev.protocolSchema = &protocolSchema{}
	p.ObserveClientPushFrame(capture.GameFrame{CommandID: capture.CmdCombatSendPackRequest, Direction: "out"}, localRoleReportBody(1409, 51522))
	if !p.PlayerRelevance.IsRoleEntity(51522) || p.Stats.PlayerRoleReports != 1 {
		t.Fatalf("core role decoder did not run with a research schema: role=%v stats=%+v", p.PlayerRelevance.IsRoleEntity(51522), p.Stats)
	}
}

// A research protocol schema must not replace the core role decoder (Phase 3A R03): the role report is read, counted
// and conflict-checked the same way with and without one.
func TestProtocolSchemaKeepsTheCoreRoleDecoder(t *testing.T) {
	for _, test := range []struct {
		name      string
		entityIDs []uint64
		wantRole  bool
	}{
		{"one reported entity", []uint64{51522}, true},
		{"conflicting reported entities", []uint64{51522, 51523}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := func(schema *protocolSchema) *processor {
				p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
				p.dev.protocolSchema = schema
				p.ObserveClientPushFrame(capture.GameFrame{CommandID: capture.CmdCombatSendPackRequest, Direction: "out"}, localRoleReportBody(1409, test.entityIDs...))
				return p
			}
			plain, research := run(nil), run(&protocolSchema{})
			for name, p := range map[string]*processor{"without schema": plain, "with schema": research} {
				if got := p.PlayerRelevance.IsRoleEntity(51522); got != test.wantRole {
					t.Fatalf("%s: local role entity = %v, want %v", name, got, test.wantRole)
				}
			}
			if plain.Stats != research.Stats {
				t.Fatalf("core statistics differ with a research schema:\nwithout %+v\nwith    %+v", plain.Stats, research.Stats)
			}
		})
	}
}
