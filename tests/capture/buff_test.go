package capture

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func appendFixed32TestField(dst []byte, number uint64, value uint32) []byte {
	dst = appendTestVarint(dst, number<<3|5)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	return append(dst, raw[:]...)
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

func TestEntityActiveResponseWithoutSnapshotOutputStillParsesBuff(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	buffRecord := appendVarintField(nil, 2, 70001)
	response := appendBytesField(nil, 11, appendBytesField(nil, 15, appendBytesField(nil, 1, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: 7}, response); err != nil {
		t.Fatalf("handle ActiveResponse without a snapshot writer: %v", err)
	}
	if p.stats.buffSnapshotResponses != 1 || p.stats.observedBuffEvents != 1 {
		t.Fatalf("snapshot responses/events = %d/%d, want 1/1", p.stats.buffSnapshotResponses, p.stats.observedBuffEvents)
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

func TestEntityActiveResponseCorrelatesIdentityComponentsToRequestEntity(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	const token = 55
	const entityID = 8801
	if err := p.handleActiveRequest(token, appendVarintField(nil, 4, entityID)); err != nil {
		t.Fatal(err)
	}

	summoner := appendVarintField(nil, 1, 771)
	summoner = appendVarintField(summoner, 2, 9901)
	summoner = appendVarintField(summoner, 3, 220)
	summoner = appendVarintField(summoner, 4, 772)
	summoner = appendVarintField(summoner, 5, 2)
	monster := appendVarintField(nil, 1, 123456)
	monster = appendVarintField(monster, 2, 654321)
	monster = appendVarintField(monster, 3, 456789)
	follower := appendVarintField(nil, 1, 773)
	follower = appendVarintField(follower, 2, 9902)
	concomitants := appendVarintField(nil, 1, 781)
	concomitants = appendVarintField(concomitants, 2, 782)
	concomitants = appendVarintField(concomitants, 3, 783)
	concomitants = appendVarintField(concomitants, 4, 784)
	playerFollower := appendVarintField(nil, 1, 4)
	playerFollower = appendVarintField(playerFollower, 2, 785)
	playerFollowers := appendBytesField(nil, 1, playerFollower)
	followEntity := appendVarintField(nil, 1, 786)
	var response []byte
	response = appendBytesField(response, 11, appendBytesField(nil, 4, summoner))
	response = appendBytesField(response, 11, appendBytesField(nil, 18, monster))
	response = appendBytesField(response, 11, appendBytesField(nil, 42, follower))
	response = appendBytesField(response, 11, appendBytesField(nil, 25, concomitants))
	response = appendBytesField(response, 11, appendBytesField(nil, 53, playerFollowers))
	response = appendBytesField(response, 11, appendBytesField(nil, 85, followEntity))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: token}, response); err != nil {
		t.Fatal(err)
	}

	entity := p.entities.entities[entityID]
	if !entity.hasMonsterID || entity.monsterID != 456789 || !entity.hasTemplateID || entity.templateID != 123456 || !entity.hasMonsterCaptureEntityID || entity.monsterCaptureEntityID != 654321 {
		t.Fatalf("monster component identity = %+v", entity)
	}
	if !entity.hasSummonerID || entity.summonerID != 771 || !entity.hasSummonerPlayerID || entity.summonerPlayerID != 772 {
		t.Fatalf("summoner component identity = %+v", entity)
	}
	if !entity.hasPlayerEntityID || entity.playerEntityID != 773 || !entity.hasFollowerSummonConfigID || entity.followerSummonConfigID != 9902 {
		t.Fatalf("follower component identity = %+v", entity)
	}
	if !entity.hasPhantomRoleEntityID || entity.phantomRoleEntityID != 783 || len(entity.concomitantVisionIDs) != 1 || entity.concomitantVisionIDs[0] != 781 || len(entity.concomitantCustomIDs) != 1 || entity.concomitantCustomIDs[0] != 782 || !entity.hasWeakVisionEntityID || entity.weakVisionEntityID != 784 {
		t.Fatalf("concomitant component references = %+v", entity)
	}
	if len(entity.playerFollowers) != 1 || !entity.playerFollowers[0].hasType || entity.playerFollowers[0].typeID != 4 || !entity.playerFollowers[0].hasEntity || entity.playerFollowers[0].entityID != 785 || !entity.hasFollowEntityID || entity.followEntityID != 786 {
		t.Fatalf("follower and follow-entity component references = %+v", entity)
	}
	if entity.sourceMessageID != 28481 || entity.componentSourceMessageID != 26702 || !strings.Contains(entity.componentSourceFieldPath, "frame token match + field4 EntityId") {
		t.Fatalf("identity evidence provenance = %+v", entity)
	}
	if status := func() resolutionStatus { _, status := p.entities.resolve(entityID); return status }(); status != resolutionCorrelated {
		t.Fatalf("runtime entity status = %s, want CORRELATED", status)
	}
	if p.stats.activeResponseIdentityComponents != 6 || p.stats.activeResponseSummonerComponents != 1 || p.stats.activeResponseMonsterComponents != 1 || p.stats.activeResponseFollowerComponents != 1 {
		t.Fatalf("identity component counts = %+v", p.stats)
	}

	masters := &gameMaster{monsters: map[uint64]masterRecord{456789: {ID: 456789, Status: string(resolutionVerified)}}}
	identity := resolveEntityIdentity(p.entities, masters, entityID)
	if !identity.HasMonster || identity.Monster.ID != 456789 || identity.MonsterStatus != resolutionCorrelated {
		t.Fatalf("static Monster mapping confidence = %+v", identity)
	}
}

func TestRuntimeIdentityComponentReferencesRejectMalformedAndOversizedLists(t *testing.T) {
	if err := applyEntityComponentPayload(&runtimeEntity{}, 53, appendBytesField(nil, 1, []byte{0x0a, 0x80})); err == nil {
		t.Fatal("malformed PlayerFollowerPb should fail")
	}

	var concomitants []byte
	for i := 0; i <= maxRuntimeComponentEntityRefs; i++ {
		concomitants = appendVarintField(concomitants, 1, uint64(i+1))
	}
	if err := applyEntityComponentPayload(&runtimeEntity{}, 25, concomitants); err == nil {
		t.Fatal("oversized ConcomitantsComponentPb entity list should fail")
	}

	var playerFollowers []byte
	for i := 0; i <= maxRuntimeComponentEntityRefs; i++ {
		playerFollowers = appendBytesField(playerFollowers, 1, appendVarintField(nil, 2, uint64(i+1)))
	}
	if err := applyEntityComponentPayload(&runtimeEntity{}, 53, playerFollowers); err == nil {
		t.Fatal("oversized PlayerFollowersComponentPb list should fail")
	}
}

func TestApplyGameplayEffectNotifyPreservesRawIdentityFields(t *testing.T) {
	body := appendVarintField(nil, 2, 501)    // Handle
	body = appendVarintField(body, 11, 71001) // Id
	body = appendVarintField(body, 5, 2)      // Level
	body = appendVarintField(body, 7, 8801)   // EntityId
	body = appendVarintField(body, 8, 99002)  // InstigatorId
	body = appendVarintField(body, 9, 1)      // ApplyType
	body = appendFixed32TestField(body, 15, mathFloat32bits(12.5))
	body = appendVarintField(body, 6, 1)    // IsActive
	body = appendVarintField(body, 10, 601) // ServerId
	body = appendVarintField(body, 1, 3)    // StackCount
	body = appendFixed32TestField(body, 12, mathFloat32bits(4.25))
	body = appendVarintField(body, 14, 72002) // ConfBuffId
	frame := gameFrame{commandID: 25830, direction: "in", timestamp: time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC)}
	row, err := decodeBuffLifecycleMessage(frame, body)
	if err != nil {
		t.Fatal(err)
	}
	if row.MessageName != "ApplyGameplayEffectNotify" || row.EventKind != "GAMEPLAY_EFFECT_APPLY" || row.EventTypeStatus != resolutionCorrelated {
		t.Fatalf("event classification = %+v", row)
	}
	if row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.TargetStatus != resolutionUnknown || row.InstigatorID == nil || *row.InstigatorID != 99002 || row.InstigatorStatus != resolutionUnknown || !strings.Contains(row.TargetEvidence, "UNKNOWN") || !strings.Contains(row.InstigatorEvidence, "UNKNOWN") {
		t.Fatalf("raw target/source candidates or their UNKNOWN semantics were lost: %+v", row)
	}
	if row.HandleID == nil || *row.HandleID != 501 || row.ID == nil || *row.ID != 71001 || row.ConfBuffID == nil || *row.ConfBuffID != 72002 || row.BuffID != nil {
		t.Fatalf("Handle, Id, ConfBuffId, and BuffId were conflated: %+v", row)
	}
	if row.DurationSeconds == nil || *row.DurationSeconds != 12.5 || row.RemainingDuration == nil || *row.RemainingDuration != 4.25 || row.StackCount == nil || *row.StackCount != 3 || row.IsActive == nil || !*row.IsActive {
		t.Fatalf("duration/stack/active values = %+v", row)
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

func TestCombatNotifyBuffRequestMessagesPreserveSchemaFields(t *testing.T) {
	tests := []struct {
		name      string
		field     uint64
		commandID uint16
		body      []byte
		check     func(*testing.T, buffLifecycleAuditRow)
	}{
		{
			name: "apply buff request notify", field: 53, commandID: 18641,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, 4, 71001), 15, 8801), 11, 2), 1, 1),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.MessageName != "ApplyBuffS2cRequestNotify" || row.EventKind != "BUFF_APPLY_REQUEST_NOTIFY" || row.ID == nil || *row.ID != 71001 || row.InstigatorID == nil || *row.InstigatorID != 8801 || row.StackCount == nil || *row.StackCount != 2 || row.IsIterable == nil || !*row.IsIterable {
					t.Fatalf("apply request fields = %+v", row)
				}
			},
		},
		{
			name: "remove buff request notify", field: 54, commandID: 20022,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 1, 501), 12, 1), 2, 8802),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.MessageName != "RemoveBuffS2cRequestNotify" || row.EventKind != "BUFF_REMOVE_REQUEST_NOTIFY" || row.HandleID == nil || *row.HandleID != 501 || row.StackCount == nil || *row.StackCount != 1 || row.InstigatorID == nil || *row.InstigatorID != 8802 {
					t.Fatalf("remove request fields = %+v", row)
				}
			},
		},
		{
			name: "remove buff by id request notify", field: 59, commandID: 19482,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 4, 71002), 13, 3), 6, 8803),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.MessageName != "RemoveBuffByIdS2cRequestNotify" || row.EventKind != "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY" || row.BuffID == nil || *row.BuffID != 71002 || row.StackCount == nil || *row.StackCount != 3 || row.InstigatorID == nil || *row.InstigatorID != 8803 {
					t.Fatalf("remove by ID fields = %+v", row)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field := test.field
			row, err := decodeBuffLifecycleMessageForCommand(gameFrame{commandID: 25706, direction: "in"}, test.body, test.commandID, &field)
			if err != nil {
				t.Fatalf("decode nested Buff notify: %v", err)
			}
			if row.InnerMessageFieldNumber == nil || *row.InnerMessageFieldNumber != test.field {
				t.Fatalf("inner field provenance = %+v", row.InnerMessageFieldNumber)
			}
			test.check(t, row)
		})
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

func TestCombatCommonContextIsKeptSeparateFromBuffTarget(t *testing.T) {
	body := appendVarintField(appendVarintField(appendVarintField(nil, 2, 101), 3, 202), 5, 303)
	entityID, originatorID, messageID, err := decodeCombatCommonContext(body)
	if err != nil {
		t.Fatalf("decode CombatCommon context: %v", err)
	}
	if entityID == nil || *entityID != 303 || originatorID == nil || *originatorID != 202 || messageID == nil || *messageID != 101 {
		t.Fatalf("CombatCommon raw context = entity:%v originator:%v message:%v", entityID, originatorID, messageID)
	}
	row, err := decodeBuffLifecycleMessageForCommand(gameFrame{commandID: 25706, direction: "in"}, nil, 18641, nil)
	if err != nil {
		t.Fatalf("decode empty nested request: %v", err)
	}
	if row.TargetEntityID != nil {
		t.Fatalf("CombatCommon.EntityId must not be silently asserted as target: %+v", row)
	}
	if _, _, _, err := decodeCombatCommonContext([]byte{0x0a, 0x02, 0x01}); err == nil {
		t.Fatal("malformed CombatCommon payload must remain observable")
	}
}

func TestBuffLifecycleMessagesPreserveCommandSpecificFields(t *testing.T) {
	tests := []struct {
		name      string
		commandID uint16
		body      []byte
		check     func(*testing.T, buffLifecycleAuditRow)
	}{
		{
			name: "remove gameplay effect notify", commandID: 18345,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 13, 501), 5, 8801), 14, 99002),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.EventKind != "GAMEPLAY_EFFECT_REMOVE" || row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.InstigatorID == nil || *row.InstigatorID != 99002 || row.HandleID == nil || *row.HandleID != 501 {
					t.Fatalf("remove notify fields = %+v", row)
				}
			},
		},
		{
			name: "remove gameplay effect push", commandID: 20358,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, 14, 502), 15, 8802), 10, 1), 4, 99003),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8802 || row.HandleID == nil || *row.HandleID != 502 || row.IsPrematureRemoval == nil || !*row.IsPrematureRemoval || row.InstigatorID == nil || *row.InstigatorID != 99003 {
					t.Fatalf("remove push fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff add", commandID: 17893,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 12, 8803), 1, 1), 3, 70001),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8803 || row.BuffID == nil || *row.BuffID != 70001 || row.IsBuffAdd == nil || !*row.IsBuffAdd || row.InstigatorStatus != resolutionUnknown {
					t.Fatalf("simple buff fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff layer count", commandID: 22102,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 6, 8804), 7, 4), 2, 70002),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8804 || row.BuffID == nil || *row.BuffID != 70002 || row.StackCount == nil || *row.StackCount != 4 {
					t.Fatalf("layer count fields = %+v", row)
				}
			},
		},
		{
			name: "stack count by handle", commandID: 29363,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, 9, 503), 4, 5), 6, 1), 1, 1),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.HandleID == nil || *row.HandleID != 503 || row.StackCount == nil || *row.StackCount != 5 || row.TargetEntityID != nil || row.NotRefreshDuration == nil || !*row.NotRefreshDuration || row.NotRefreshPeriod == nil || !*row.NotRefreshPeriod {
					t.Fatalf("handle-based stack fields were conflated with target ID: %+v", row)
				}
			},
		},
		{
			name: "duration by handle", commandID: 16974,
			body: appendFixed32TestField(appendFixed32TestField(appendVarintField(nil, 12, 504), 2, mathFloat32bits(9.5)), 5, mathFloat32bits(2.25)),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.HandleID == nil || *row.HandleID != 504 || row.DurationSeconds == nil || *row.DurationSeconds != 9.5 || row.RemainingDuration == nil || *row.RemainingDuration != 2.25 || row.TargetEntityID != nil {
					t.Fatalf("handle-based duration fields were conflated with target ID: %+v", row)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row, err := decodeBuffLifecycleMessage(gameFrame{commandID: test.commandID, direction: "in"}, test.body)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, row)
		})
	}
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

func TestEntityActiveResponseDoesNotAttachComponentsWithoutLiveRequestEntity(t *testing.T) {
	t.Run("unmatched token", func(t *testing.T) {
		p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
		monster := appendVarintField(nil, 3, 456789)
		response := appendBytesField(nil, 11, appendBytesField(nil, 18, monster))
		if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: 77}, response); err != nil {
			t.Fatal(err)
		}
		if len(p.entities.entities) != 0 {
			t.Fatalf("unmatched response created runtime entities: %+v", p.entities.entities)
		}
	})

	t.Run("request entity removed before response", func(t *testing.T) {
		p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
		const token = 78
		const entityID = 8802
		if err := p.handleActiveRequest(token, appendVarintField(nil, 4, entityID)); err != nil {
			t.Fatal(err)
		}
		remove := appendVarintField(nil, 4, 1)
		remove = appendBytesField(remove, 5, appendVarintField(nil, 1, entityID))
		if _, _, err := p.entities.applyEntityRemoveNotify(remove); err != nil {
			t.Fatal(err)
		}
		monster := appendVarintField(nil, 3, 456789)
		response := appendBytesField(nil, 11, appendBytesField(nil, 18, monster))
		if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: token}, response); err != nil {
			t.Fatal(err)
		}
		if _, ok := p.entities.entities[entityID]; ok {
			t.Fatal("late response resurrected a removed entity")
		}
	})
}

func mathFloat32bits(value float32) uint32 {
	return math.Float32bits(value)
}
