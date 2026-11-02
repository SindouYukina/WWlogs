//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFormatBuffLifecycleConsoleUsesRawEffectEvidence(t *testing.T) {
	timestamp := time.Date(2026, 11, 2, 2, 37, 28, 33000000, time.UTC)
	buffID, handleID, targetID, stack := uint64(70001), uint64(111), uint64(8801), uint64(3)
	duration, remaining := float32(12.5), float32(4.25)
	line := formatBuffLifecycleConsole(buffLifecycleAuditRow{
		Timestamp: timestamp, EventKind: "GAMEPLAY_EFFECT_APPLY",
		BuffID: &buffID, HandleID: &handleID, TargetEntityID: &targetID, StackCount: &stack,
		DurationSeconds: &duration, RemainingDuration: &remaining,
		BuffDefinitionStatus: resolutionUnknown, TargetStatus: resolutionUnknown,
	})
	for _, want := range []string{"BuffEvent", "kind=GAMEPLAY_EFFECT_APPLY", "buffId=70001", "handle=111", "target=8801", "targetStatus=UNKNOWN", "stack=3", "duration_s=12.500", "remaining_s=4.250", "buffIdLookup=UNKNOWN"} {
		if !strings.Contains(line, want) {
			t.Fatalf("effect console line %q is missing %q", line, want)
		}
	}
	if strings.Contains(strings.ToLower(line), "debuff") || strings.Contains(line, "name=") {
		t.Fatalf("effect console output invented a name or polarity: %q", line)
	}
}

func TestFormatBuffLifecycleConsoleShowsCorrelatedStackHolder(t *testing.T) {
	effectID, handleID, entityID, stack := uint64(10061000), uint64(177), uint64(29106), uint64(6)
	line := formatBuffLifecycleConsole(buffLifecycleAuditRow{
		EventKind: "BUFF_STACK_COUNT_CHANGE", ID: &effectID, HandleID: &handleID,
		HandleLinkedID: &effectID, HandleLinkedStatus: resolutionCorrelated,
		CombatCommonEntityID: &entityID, StackCount: &stack,
		TargetStatus: resolutionUnknown,
	})
	for _, want := range []string{"BuffEvent", "kind=BUFF_STACK_COUNT_CHANGE", "id=10061000", "handle=177", "handleLinkedId=10061000", "holder=29106", "holderStatus=CORRELATED", "handleLinkedStatus=CORRELATED", "stack=6"} {
		if !strings.Contains(line, want) {
			t.Fatalf("correlated stack event %q is missing %q", line, want)
		}
	}
	if strings.Contains(line, "targetStatus=UNKNOWN") {
		t.Fatalf("handle-linked stack update was mislabeled as an unknown target: %q", line)
	}
}

func TestFormatBuffLifecycleConsoleKeepsUnlinkedHolderUnconfirmed(t *testing.T) {
	entityID, handleID, stack := uint64(29106), uint64(177), uint64(6)
	line := formatBuffLifecycleConsole(buffLifecycleAuditRow{
		EventKind: "BUFF_STACK_COUNT_CHANGE", HandleID: &handleID,
		HandleLinkedStatus: resolutionUnknown, CombatCommonEntityID: &entityID,
		StackCount: &stack,
	})
	for _, want := range []string{"holderCandidate=29106", "holderStatus=UNKNOWN", "stack=6"} {
		if !strings.Contains(line, want) {
			t.Fatalf("unlinked stack event %q is missing %q", line, want)
		}
	}
	if strings.Contains(line, "holder=29106") {
		t.Fatalf("unlinked stack event was presented as a confirmed holder: %q", line)
	}
}

func TestFormatBuffLifecycleConsoleDoesNotTreatRequestContextAsTarget(t *testing.T) {
	id, entityID := uint64(10061000), uint64(90)
	line := formatBuffLifecycleConsole(buffLifecycleAuditRow{
		EventKind: "BUFF_APPLY_REQUEST_NOTIFY", ID: &id, CombatCommonEntityID: &entityID,
		GenericIDDefinitionStatus: resolutionCorrelated, TargetStatus: resolutionUnknown,
	})
	for _, want := range []string{"kind=BUFF_APPLY_REQUEST_NOTIFY", "id=10061000", "commonEntity=90", "idReferenceStatus=CORRELATED"} {
		if !strings.Contains(line, want) {
			t.Fatalf("request event %q is missing %q", line, want)
		}
	}
	if strings.Contains(line, "targetStatus=UNKNOWN") {
		t.Fatalf("request context was mislabeled as an unknown target: %q", line)
	}
}

func TestFormatBuffLifecycleConsoleOmitsMaxStackSentinelFromRemoveRequest(t *testing.T) {
	buffID, entityID, stack := uint64(600100116), uint64(30116), ^uint64(0)
	line := formatBuffLifecycleConsole(buffLifecycleAuditRow{
		EventKind: "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY", BuffID: &buffID,
		CombatCommonEntityID: &entityID, StackCount: &stack,
		BuffDefinitionStatus: resolutionCorrelated,
	})
	if !strings.Contains(line, "buffId=600100116") || !strings.Contains(line, "commonEntity=30116") {
		t.Fatalf("remove request context was lost: %q", line)
	}
	if !strings.Contains(line, "effectState=UNCONFIRMED") || !strings.Contains(line, "buffIdLookup=CORRELATED") {
		t.Fatalf("request evidence was not labeled precisely: %q", line)
	}
	if strings.Contains(line, "stack=") {
		t.Fatalf("raw max-value stack sentinel was presented as a real count: %q", line)
	}
}

func TestFormatBuffLifecycleConsoleMarksApplyRequestUnconfirmed(t *testing.T) {
	id, instigator := uint64(3000020003), uint64(30116)
	line := formatBuffLifecycleConsole(buffLifecycleAuditRow{
		EventKind: "BUFF_APPLY_REQUEST_NOTIFY", ID: &id, InstigatorID: &instigator,
		GenericIDDefinitionStatus: resolutionCorrelated,
	})
	if !strings.Contains(line, "effectState=UNCONFIRMED") || !strings.Contains(line, "idReferenceStatus=CORRELATED") {
		t.Fatalf("apply request evidence was not labeled precisely: %q", line)
	}
}

func TestEntityActiveResponseDecodesBuffSnapshotWithCorrelatedEntity(t *testing.T) {
	var output, eventStream bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffSnapshots = &output
	p.combatEventsJSONL = &eventStream
	p.masters = &gameMaster{buffDefinitions: map[uint64]struct{}{70001: {}}}
	p.sessionNumber = 5
	const token = 42
	if err := p.handleActiveRequest(token, appendVarintField(nil, 4, 8801)); err != nil {
		t.Fatal(err)
	}

	var buffRecord []byte
	buffRecord = appendVarintField(buffRecord, 1, 111)
	buffRecord = appendVarintField(buffRecord, 2, 70001)
	buffRecord = appendVarintField(buffRecord, 3, 2)
	buffRecord = appendVarintField(buffRecord, 4, 3)
	buffRecord = appendVarintField(buffRecord, 5, 99002)
	buffRecord = appendVarintField(buffRecord, 6, 8801)
	buffRecord = appendVarintField(buffRecord, 7, 1)
	buffRecord = appendFixed32TestField(buffRecord, 8, mathFloat32bits(12.5))
	buffRecord = appendFixed32TestField(buffRecord, 9, mathFloat32bits(4.25))
	buffRecord = appendBytesField(buffRecord, 10, appendVarintField(nil, 1, 7))
	buffRecord = appendVarintField(buffRecord, 11, 1)
	buffRecord = appendVarintField(buffRecord, 14, 70002)
	buffRecord = appendVarintField(buffRecord, 30, 9)

	xAs := appendBytesField(nil, 1, buffRecord)
	componentPb := appendBytesField(nil, 15, xAs)
	response := appendBytesField(nil, 11, componentPb)
	frame := gameFrame{commandID: 26702, direction: "in", token: token, timestamp: time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC)}
	if err := p.handleActiveResponse(frame, response); err != nil {
		t.Fatal(err)
	}
	if p.stats.buffSnapshotResponses != 1 {
		t.Fatalf("snapshot responses = %d, want 1", p.stats.buffSnapshotResponses)
	}
	if p.stats.observedBuffEvents != 1 {
		t.Fatalf("observed buff records = %d, want 1", p.stats.observedBuffEvents)
	}
	var row buffSnapshotAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.EntityID == nil || *row.EntityID != 8801 || row.EntityIDStatus != resolutionCorrelated {
		t.Fatalf("entity association = %v/%s", row.EntityID, row.EntityIDStatus)
	}
	if row.Buff.ID != 70001 || row.Buff.Level != 2 || row.Buff.Stack != 3 || row.Buff.InstigatorID != 99002 || !row.Buff.HasActive || !row.Buff.Active {
		t.Fatalf("decoded Buff snapshot = %+v", row.Buff)
	}
	if row.BuffIDStatus != resolutionCorrelated || row.BuffDefinitionStatus != resolutionCorrelated || row.BuffDefinitionTable != "buff" || row.BuffDefinitionID == nil || *row.BuffDefinitionID != 70001 || row.BuffSemanticStatus != resolutionUnknown || row.InstigatorSemantics != resolutionCorrelated || !strings.Contains(row.WindowsSchemaVerification, "client/master compatibility UNKNOWN") {
		t.Fatalf("evidence levels were overstated: %+v", row)
	}
	if !strings.Contains(row.BuffDefinitionEvidence, "exact normalized buff.id row found") || !strings.Contains(row.BuffSemanticEvidence, "does not establish application or uptime") || !row.Buff.HasConfBuffID || row.Buff.ConfBuffID == row.Buff.ID {
		t.Fatalf("BuffId semantics or ConfBuffId separation = %+v", row)
	}
	if !strings.Contains(row.InstigatorEvidence, "CreatureModel.GetEntity") || !strings.Contains(row.InstigatorEvidence, "client/master compatibility UNKNOWN") {
		t.Fatalf("snapshot instigator evidence basis = %q", row.InstigatorEvidence)
	}
	if row.ComponentPath != "EntityActiveResponse.field11(ComponentPbs[])->EntityComponentPb.field15(FightBuffComponent)->FightBuffComponentPb.field1(FightBuffInfos[])->FightBuffInformation" {
		t.Fatalf("component schema path = %q", row.ComponentPath)
	}
	if row.Buff.HandleID != 111 || !row.Buff.HasHandleID || row.Buff.EntityID != 8801 || !row.Buff.HasEntityID || row.Buff.ConfBuffID != 70002 || !row.Buff.HasConfBuffID {
		t.Fatalf("additional Windows schema fields = %+v", row.Buff)
	}
	if len(row.Buff.Context) != 1 || !bytes.Equal(row.Buff.Context[0], appendVarintField(nil, 1, 7)) {
		t.Fatalf("opaque Buff context = %v", row.Buff.Context)
	}
	if len(row.Buff.UnknownFieldNums) != 1 || row.Buff.UnknownFieldNums[0] != 30 {
		t.Fatalf("unknown fields = %v, want [30]", row.Buff.UnknownFieldNums)
	}
	var streamRow map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(eventStream.Bytes()), &streamRow); err != nil {
		t.Fatalf("decode unified event row: %v", err)
	}
	if streamRow["schemaVersion"] != float64(buffSnapshotSchemaVersion) || streamRow["eventType"] != "BUFF_SNAPSHOT" || streamRow["sessionNumber"] != float64(5) || streamRow["messageId"] != float64(26702) || streamRow["buffSemanticStatus"] != string(resolutionUnknown) {
		t.Fatalf("snapshot missing from unified event stream: %#v", streamRow)
	}
}

func TestEntityActiveResponseDoesNotSubstituteConfBuffIDForMissingDefinition(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffSnapshots = &output
	p.masters = &gameMaster{buffDefinitions: map[uint64]struct{}{70002: {}}}
	buffRecord := appendVarintField(nil, 2, 1312010090)
	buffRecord = appendVarintField(buffRecord, 14, 70002)
	response := appendBytesField(nil, 11, appendBytesField(nil, 15, appendBytesField(nil, 1, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: 7}, response); err != nil {
		t.Fatal(err)
	}
	var row buffSnapshotAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.Buff.ID != 1312010090 || !row.Buff.HasID || row.Buff.ConfBuffID != 70002 || !row.Buff.HasConfBuffID {
		t.Fatalf("raw Buff and ConfBuff IDs were not kept separate: %+v", row.Buff)
	}
	if row.BuffDefinitionStatus != resolutionUnknown || row.BuffDefinitionID != nil || row.BuffDefinitionTable != "" {
		t.Fatalf("missing BuffId definition was resolved through ConfBuffId: %+v", row)
	}
	if row.BuffSemanticStatus != resolutionUnknown {
		t.Fatalf("unresolved runtime semantics changed: %s", row.BuffSemanticStatus)
	}
}

func TestEntityActiveResponseWithoutMatchingRequestKeepsEntityUnknown(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffSnapshots = &output
	buffRecord := appendVarintField(nil, 2, 70001)
	response := appendBytesField(nil, 11, appendBytesField(nil, 15, appendBytesField(nil, 1, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: 7}, response); err != nil {
		t.Fatal(err)
	}
	var row buffSnapshotAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.EntityID != nil || row.EntityIDStatus != resolutionUnknown {
		t.Fatalf("unmatched response entity = %v/%s", row.EntityID, row.EntityIDStatus)
	}
	if strings.Contains(output.String(), "entityId\":0") {
		t.Fatal("missing entity ID was serialized as a fabricated zero value")
	}
}

func TestEntityActiveResponseAddsBuffObservationToEncounterSummary(t *testing.T) {
	var summary bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &summary
	p.sessionNumber = 3
	const token = 17
	if err := p.handleActiveRequest(token, appendVarintField(nil, 4, 8801)); err != nil {
		t.Fatal(err)
	}
	buffRecord := appendVarintField(nil, 2, 70001)
	response := appendBytesField(nil, 11, appendBytesField(nil, 15, appendBytesField(nil, 1, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: token}, response); err != nil {
		t.Fatal(err)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"BuffId 70001 / EntityId 8801: 1 snapshot observation(s)", "do not establish apply, refresh, removal"} {
		if !strings.Contains(summary.String(), want) {
			t.Errorf("encounter summary missing %q:\n%s", want, summary.String())
		}
	}
}

func TestCombatReceivePackNotifyDecodesInnerBuffApply(t *testing.T) {
	var lifecycle bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffLifecycle = &lifecycle
	p.census = newCommandCensus()
	p.sessionNumber = 4

	apply := appendVarintField(nil, 2, 501)     // Handle
	apply = appendVarintField(apply, 11, 71001) // Id
	apply = appendVarintField(apply, 7, 8801)   // EntityId
	apply = appendVarintField(apply, 8, 99002)  // InstigatorId
	notify := appendBytesField(nil, 5, apply)   // CombatNotifyData.ApplyGameplayEffectNotify
	data := appendBytesField(nil, 2, notify)    // CombatReceiveData.CombatNotifyData
	body := appendBytesField(nil, 5, data)      // CombatReceivePackNotify.Data[]
	frame := gameFrame{commandID: 25706, direction: "in", timestamp: time.Date(2026, 10, 27, 5, 0, 0, 0, time.UTC)}

	if err := p.handleCombatReceivePackNotify(frame, body); err != nil {
		t.Fatalf("handle CombatReceivePackNotify: %v", err)
	}
	var row buffLifecycleAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(lifecycle.Bytes()), &row); err != nil {
		t.Fatalf("decode lifecycle JSONL: %v", err)
	}
	if row.CommandID != 25706 || row.MessageID != 25706 || row.InnerMessageFieldNumber == nil || *row.InnerMessageFieldNumber != 5 {
		t.Fatalf("outer and inner message provenance = %+v", row)
	}
	if row.MessageName != "ApplyGameplayEffectNotify" || row.EventKind != "GAMEPLAY_EFFECT_APPLY" || row.SessionNumber != 4 {
		t.Fatalf("inner Buff classification = %+v", row)
	}
	if row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.InstigatorID == nil || *row.InstigatorID != 99002 {
		t.Fatalf("inner Buff target/provider raw fields = %+v", row)
	}
	if p.census.combatNotifyFields[5] != 1 {
		t.Fatalf("CombatNotifyData field 5 observations = %d, want 1", p.census.combatNotifyFields[5])
	}
}

func TestCombatSendPackResponseProcessesNestedBuffApply(t *testing.T) {
	var lifecycle bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffLifecycle = &lifecycle
	p.census = newCommandCensus()
	p.sessionNumber = 4

	apply := appendVarintField(nil, 2, 501)     // Handle
	apply = appendVarintField(apply, 11, 71001) // Id
	apply = appendVarintField(apply, 7, 8801)   // EntityId
	apply = appendVarintField(apply, 8, 99002)  // InstigatorId
	notify := appendBytesField(nil, 5, apply)   // CombatNotifyData.ApplyGameplayEffectNotify
	data := appendBytesField(nil, 2, notify)    // CombatReceiveData.CombatNotifyData
	receive := appendBytesField(nil, 5, data)   // CombatReceivePackNotify.Data[]
	body := appendBytesField(nil, 14, receive)  // CombatSendPackResponse.ReceivePackNotify
	frame := gameFrame{commandID: 20332, direction: "in", timestamp: time.Date(2026, 10, 27, 5, 0, 0, 0, time.UTC)}

	if err := p.handleEmbeddedCombatReceivePackNotify(frame, body); err != nil {
		t.Fatalf("handle embedded CombatReceivePackNotify: %v", err)
	}
	var row buffLifecycleAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(lifecycle.Bytes()), &row); err != nil {
		t.Fatalf("decode lifecycle JSONL: %v", err)
	}
	if row.CommandID != 20332 || row.MessageID != 20332 || row.InnerMessageFieldNumber == nil || *row.InnerMessageFieldNumber != 5 {
		t.Fatalf("outer and inner message provenance = %+v", row)
	}
	if row.MessageName != "ApplyGameplayEffectNotify" || row.EventKind != "GAMEPLAY_EFFECT_APPLY" || row.SessionNumber != 4 {
		t.Fatalf("inner Buff classification = %+v", row)
	}
	if row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.InstigatorID == nil || *row.InstigatorID != 99002 {
		t.Fatalf("inner Buff target/provider raw fields = %+v", row)
	}
	if p.census.combatNotifyFields[5] != 1 {
		t.Fatalf("CombatNotifyData field 5 observations = %d, want 1", p.census.combatNotifyFields[5])
	}
}

func TestCombatReceivePackNotifyCarriesCombatCommonContextWithBuffRequest(t *testing.T) {
	var lifecycle bytes.Buffer
	p := &processor{
		sessionNumber: 5,
		buffLifecycle: &lifecycle,
		census:        newCommandCensus(),
		entities: &entityResolver{entities: map[uint64]runtimeEntity{
			8801: {
				entityID: 8801, roleID: 771, hasRoleID: true,
				sourceMessageID: 17286, sourceFieldPath: "scene role map",
			},
		}},
		masters: &gameMaster{characters: map[uint64]masterRecord{
			771: {ID: 771, Name: "Candidate", Status: string(resolutionVerified), NameStatus: string(resolutionVerified)},
		}},
	}
	request := appendVarintField(appendVarintField(nil, 4, 71001), 15, 8801)
	notify := appendBytesField(nil, 1, appendVarintField(appendVarintField(nil, 2, 17), 5, 303))
	notify = appendBytesField(notify, 53, request)
	data := appendBytesField(nil, 2, notify)
	body := appendBytesField(nil, 5, data)
	if err := p.handleCombatReceivePackNotify(gameFrame{commandID: 25706, direction: "in"}, body); err != nil {
		t.Fatalf("handle nested Buff request: %v", err)
	}
	var row buffLifecycleAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(lifecycle.Bytes()), &row); err != nil {
		t.Fatalf("decode nested Buff request: %v", err)
	}
	if row.InnerMessageFieldNumber == nil || *row.InnerMessageFieldNumber != 53 || row.CombatCommonMessageID == nil || *row.CombatCommonMessageID != 17 || row.CombatCommonEntityID == nil || *row.CombatCommonEntityID != 303 {
		t.Fatalf("nested Buff request lost envelope provenance: %+v", row)
	}
	if row.TargetEntityID != nil || row.TargetStatus != resolutionUnknown || row.CombatCommonEntityID == nil || *row.CombatCommonEntityID != 303 {
		t.Fatalf("CombatCommon context must remain raw and must not be asserted as a target: %+v", row)
	}
	if row.ProviderStatus != resolutionUnknown || row.ProviderNameStatus != resolutionUnknown || row.InstigatorStatus != resolutionUnknown {
		t.Fatalf("provider semantics must remain UNKNOWN despite a character candidate: %+v", row)
	}
	if row.InstigatorCharacterCandidateID == nil || *row.InstigatorCharacterCandidateID != 771 || row.InstigatorCharacterCandidateName != "Candidate" {
		t.Fatalf("resolved character must be retained as a candidate: %+v", row)
	}
	if row.InstigatorCharacterCandidateStatus != resolutionVerified {
		t.Fatalf("candidate identity status = %s, want VERIFIED", row.InstigatorCharacterCandidateStatus)
	}
	if p.census.combatNotifyFields[53] != 1 {
		t.Fatalf("inner request census count = %d, want 1", p.census.combatNotifyFields[53])
	}
	lifecycle.Reset()
	zeroContext := appendBytesField(nil, 1, appendVarintField(nil, 5, 0))
	zeroNotify := appendBytesField(zeroContext, 53, request)
	if err := p.handleCombatReceivePackNotify(gameFrame{commandID: 25706, direction: "in"}, appendBytesField(nil, 5, appendBytesField(nil, 2, zeroNotify))); err != nil {
		t.Fatalf("handle Buff request with zero CombatCommon.EntityId: %v", err)
	}
	row = buffLifecycleAuditRow{}
	if err := json.Unmarshal(bytes.TrimSpace(lifecycle.Bytes()), &row); err != nil {
		t.Fatalf("decode Buff request with zero CombatCommon.EntityId: %v", err)
	}
	if row.CombatCommonEntityID == nil || *row.CombatCommonEntityID != 0 || row.TargetEntityID != nil {
		t.Fatalf("zero raw entity ID must remain raw and not resolve as a target: %+v", row)
	}
}

func TestGameplayEffectApplyCorrelatesProviderOnlyWithMatchingGroundTruthContext(t *testing.T) {
	newProcessorWithIdentity := func() (*processor, *bytes.Buffer) {
		var lifecycle bytes.Buffer
		p := &processor{
			sessionNumber: 7,
			buffLifecycle: &lifecycle,
			entities: &entityResolver{entities: map[uint64]runtimeEntity{
				8801: {entityID: 8801, roleID: 771, hasRoleID: true, sourceMessageID: 17286, sourceFieldPath: "scene role map"},
			}},
			masters: &gameMaster{characters: map[uint64]masterRecord{
				771: {ID: 771, Name: "Cartethyia", Status: string(resolutionVerified), NameStatus: string(resolutionVerified)},
			}},
		}
		return p, &lifecycle
	}

	t.Run("correlates provider and target", func(t *testing.T) {
		p, output := newProcessorWithIdentity()
		target, originator, message := uint64(303), uint64(771), uint64(17)
		body := appendVarintField(nil, 2, 1234)
		body = appendVarintField(body, 7, target)
		body = appendVarintField(body, 8, 8801)
		if err := p.handleInnerBuffLifecycleMessage(gameFrame{commandID: 25706, direction: "in"}, 53, 25830, body, &target, &originator, &message); err != nil {
			t.Fatal(err)
		}
		var row buffLifecycleAuditRow
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
			t.Fatal(err)
		}
		if row.SchemaVersion != buffLifecycleSchemaVersion || row.TargetStatus != resolutionCorrelated || row.InstigatorStatus != resolutionCorrelated || row.ProviderStatus != resolutionCorrelated || row.ProviderNameStatus != resolutionCorrelated {
			t.Fatalf("expected schema v4 correlated gameplay-effect attribution: %+v", row)
		}
		if row.ProviderCharacterID == nil || *row.ProviderCharacterID != 771 || row.ProviderName != "Cartethyia" {
			t.Fatalf("provider identity = (%v, %q)", row.ProviderCharacterID, row.ProviderName)
		}
	})

	t.Run("leaves mismatched target unknown", func(t *testing.T) {
		p, output := newProcessorWithIdentity()
		commonTarget, originator, message := uint64(303), uint64(771), uint64(17)
		body := appendVarintField(nil, 2, 1234)
		body = appendVarintField(body, 7, 304)
		body = appendVarintField(body, 8, 8801)
		if err := p.handleInnerBuffLifecycleMessage(gameFrame{commandID: 25706, direction: "in"}, 53, 25830, body, &commonTarget, &originator, &message); err != nil {
			t.Fatal(err)
		}
		var row buffLifecycleAuditRow
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
			t.Fatal(err)
		}
		if row.TargetStatus != resolutionUnknown || row.InstigatorStatus != resolutionUnknown || row.ProviderStatus != resolutionUnknown || row.ProviderCharacterID != nil {
			t.Fatalf("mismatched target must not produce provider attribution: %+v", row)
		}
	})

	t.Run("does not claim an unverified provider name", func(t *testing.T) {
		p, output := newProcessorWithIdentity()
		p.masters.characters[771] = masterRecord{ID: 771, Status: string(resolutionVerified), NameStatus: string(resolutionUnknown)}
		target, originator, message := uint64(303), uint64(771), uint64(17)
		body := appendVarintField(nil, 2, 1234)
		body = appendVarintField(body, 7, target)
		body = appendVarintField(body, 8, 8801)
		if err := p.handleInnerBuffLifecycleMessage(gameFrame{commandID: 25706, direction: "in"}, 53, 25830, body, &target, &originator, &message); err != nil {
			t.Fatal(err)
		}
		var row buffLifecycleAuditRow
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
			t.Fatal(err)
		}
		if row.ProviderStatus != resolutionCorrelated || row.ProviderNameStatus != resolutionUnknown || row.ProviderName != "" {
			t.Fatalf("unverified master name must stay unknown: %+v", row)
		}
	})

	t.Run("does not promote non-gameplay Buff target semantics", func(t *testing.T) {
		p, output := newProcessorWithIdentity()
		target, originator, message := uint64(303), uint64(771), uint64(17)
		body := appendVarintField(nil, 12, target)
		if err := p.handleInnerBuffLifecycleMessage(gameFrame{commandID: 25706, direction: "in"}, 53, 17893, body, &target, &originator, &message); err != nil {
			t.Fatal(err)
		}
		var row buffLifecycleAuditRow
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
			t.Fatal(err)
		}
		if row.TargetStatus != resolutionUnknown || row.ProviderStatus != resolutionUnknown {
			t.Fatalf("CombatCommon match must not promote unrelated Buff target semantics: %+v", row)
		}
	})
}

func TestBuffLifecycleOutputRetainsUnknownFieldsAndRejectsWireMismatch(t *testing.T) {
	var output, eventStream bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffLifecycle = &output
	p.combatEventsJSONL = &eventStream
	p.sessionNumber = 2
	body := appendVarintField(nil, 5, 8801)
	body = appendVarintField(body, 99, 1)
	frame := gameFrame{commandID: 18345, direction: "in"}
	if err := p.handleBuffLifecycleMessage(frame, body); err != nil {
		t.Fatal(err)
	}
	var row buffLifecycleAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.TargetEntityID == nil || *row.TargetEntityID != 8801 || len(row.UnknownFieldNumbers) != 1 || row.UnknownFieldNumbers[0] != 99 {
		t.Fatalf("unexpected fields were not preserved as unknown: %+v", row)
	}
	var streamRow map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(eventStream.Bytes()), &streamRow); err != nil {
		t.Fatalf("decode unified event row: %v", err)
	}
	if streamRow["schemaVersion"] != float64(buffLifecycleSchemaVersion) || streamRow["eventType"] != "BUFF_LIFECYCLE" || streamRow["sessionNumber"] != float64(2) || streamRow["messageId"] != float64(18345) || streamRow["direction"] != "in" {
		t.Fatalf("Buff lifecycle missing from unified event stream: %#v", streamRow)
	}
	badWire := appendBytesField(nil, 5, []byte{1})
	if _, err := decodeBuffLifecycleMessage(gameFrame{commandID: 18345}, badWire); err == nil {
		t.Fatal("recognized field with a schema-incompatible wire type should fail")
	}
}

func TestBuffLifecycleDefinitionLookupUsesOnlyTypedBuffID(t *testing.T) {
	master := &gameMaster{buffDefinitions: map[uint64]struct{}{70001: {}}}
	decodeOutput := func(commandID uint16, body []byte) (buffLifecycleAuditRow, map[string]any) {
		t.Helper()
		var lifecycle, eventStream bytes.Buffer
		p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
		p.masters = master
		p.buffLifecycle = &lifecycle
		p.combatEventsJSONL = &eventStream
		if err := p.handleBuffLifecycleMessage(gameFrame{commandID: commandID, direction: "in"}, body); err != nil {
			t.Fatal(err)
		}
		var row buffLifecycleAuditRow
		if err := json.Unmarshal(bytes.TrimSpace(lifecycle.Bytes()), &row); err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(eventStream.Bytes()), &event); err != nil {
			t.Fatal(err)
		}
		if event["schemaVersion"] != float64(buffLifecycleSchemaVersion) {
			t.Fatalf("unified lifecycle event schema version = %v", event["schemaVersion"])
		}
		return row, event
	}

	matched, matchedEvent := decodeOutput(19482, appendVarintField(nil, 4, 70001))
	if matched.BuffID == nil || *matched.BuffID != 70001 || matched.BuffDefinitionStatus != resolutionCorrelated || matched.BuffDefinitionTable != "buff" || matched.BuffDefinitionID == nil || *matched.BuffDefinitionID != 70001 {
		t.Fatalf("typed BuffId did not link to its exact table row: %+v", matched)
	}
	if matched.BuffDefinitionEvidence == "" || matched.SchemaVersion != buffLifecycleSchemaVersion {
		t.Fatalf("static link provenance/schema version missing: %+v", matched)
	}
	if matchedEvent["buffDefinitionStatus"] != string(resolutionCorrelated) || matchedEvent["buffDefinitionTable"] != "buff" || matchedEvent["buffDefinitionId"] != float64(70001) {
		t.Fatalf("unified event did not expose the typed static link: %#v", matchedEvent)
	}

	unmatched, unmatchedEvent := decodeOutput(19482, appendVarintField(nil, 4, 1312010090))
	if unmatched.BuffID == nil || *unmatched.BuffID != 1312010090 || unmatched.BuffDefinitionStatus != resolutionUnknown || unmatched.BuffDefinitionTable != "" || unmatched.BuffDefinitionID != nil {
		t.Fatalf("unmatched typed BuffId was resolved: %+v", unmatched)
	}
	if unmatchedEvent["buffDefinitionStatus"] != string(resolutionUnknown) || unmatchedEvent["buffDefinitionId"] != nil {
		t.Fatalf("unmatched unified event was misclassified: %#v", unmatchedEvent)
	}

	apply := appendVarintField(nil, 11, 70001)  // ApplyGameplayEffectNotify.Id
	apply = appendVarintField(apply, 14, 70001) // ConfBuffId
	nonBuffID, nonBuffIDEvent := decodeOutput(25830, apply)
	if nonBuffID.BuffID != nil || nonBuffID.BuffDefinitionStatus != "" || nonBuffID.BuffDefinitionID != nil {
		t.Fatalf("generic Id or ConfBuffId was substituted for typed BuffId: %+v", nonBuffID)
	}
	if _, exists := nonBuffIDEvent["buffDefinitionStatus"]; exists {
		t.Fatalf("untyped ID was assigned a static Buff link: %#v", nonBuffIDEvent)
	}
}

func TestWriteBuffLifecycleConsoleEmitsBuffEvent(t *testing.T) {
	var stdout, diagnostic bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.showEffectEvents = true
	buffID := uint64(70001)
	p.writeBuffLifecycleConsole(buffLifecycleAuditRow{
		EventKind: "GAMEPLAY_EFFECT_REMOVE", BuffID: &buffID, BuffDefinitionStatus: resolutionUnknown,
		TargetStatus: resolutionUnknown,
	})
	if !strings.Contains(stdout.String(), "BuffEvent") || !strings.Contains(stdout.String(), "kind=GAMEPLAY_EFFECT_REMOVE") || !strings.Contains(stdout.String(), "buffId=70001") {
		t.Fatalf("effect lifecycle was not printed: %q", stdout.String())
	}
	if diagnostic.Len() != 0 {
		t.Fatalf("effect lifecycle output had an unexpected diagnostic: %q", diagnostic.String())
	}
}
