package capture

import (
	"bytes"
	"encoding/binary"
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

func TestWriteBuffLifecycleConsoleCanBeDisabled(t *testing.T) {
	var stdout bytes.Buffer
	p := newProcessor(nil, &stdout, &bytes.Buffer{}, nil)
	row := buffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY"}
	p.writeBuffLifecycleConsole(row)
	if stdout.Len() != 0 {
		t.Fatalf("disabled effect console wrote %q", stdout.String())
	}
}

func TestEntityActiveResponseWithoutSnapshotOutputStillParsesBuff(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	buffRecord := appendVarintField(nil, fieldFightBuffInformationBuffID, 70001)
	response := appendBytesField(nil, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbFightBuffComponent, appendBytesField(nil, fieldFightBuffComponentPbFightBuffInfos, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: cmdEntityActiveResponse, direction: "in", token: 7}, response); err != nil {
		t.Fatalf("handle ActiveResponse without a snapshot writer: %v", err)
	}
	if p.stats.buffSnapshotResponses != 1 || p.stats.observedBuffEvents != 1 {
		t.Fatalf("snapshot responses/events = %d/%d, want 1/1", p.stats.buffSnapshotResponses, p.stats.observedBuffEvents)
	}
}

func TestEntityActiveResponseCorrelatesIdentityComponentsToRequestEntity(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	const token = 55
	const entityID = 8801
	if err := p.handleActiveRequest(token, appendVarintField(nil, fieldEntityActiveRequestEntityID, entityID)); err != nil {
		t.Fatal(err)
	}

	summoner := appendVarintField(nil, fieldSummonerComponentPbSummonerID, 771)
	summoner = appendVarintField(summoner, fieldSummonerComponentPbSummonCfgID, 9901)
	summoner = appendVarintField(summoner, fieldSummonerComponentPbSummonSkillID, 220)
	summoner = appendVarintField(summoner, fieldSummonerComponentPbPlayerID, 772)
	summoner = appendVarintField(summoner, fieldSummonerComponentPbType, 2)
	monster := appendVarintField(nil, fieldMonsterCaptureComponentPbTemplateID, 123456)
	monster = appendVarintField(monster, fieldMonsterCaptureComponentPbEntityID, 654321)
	monster = appendVarintField(monster, fieldMonsterCaptureComponentPbMonsterID, 456789)
	follower := appendVarintField(nil, fieldPlayerFollowerSelfComponentPbPlayerEntityID, 773)
	follower = appendVarintField(follower, fieldPlayerFollowerSelfComponentPbSummonConfigID, 9902)
	concomitants := appendVarintField(nil, fieldConcomitantsComponentPbVisionEntityIDs, 781)
	concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbCustomEntityIDs, 782)
	concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbPhantomRoleEid, 783)
	concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbWeakVisionEid, 784)
	playerFollower := appendVarintField(nil, fieldPlayerFollowerPbType, 4)
	playerFollower = appendVarintField(playerFollower, fieldPlayerFollowerPbEntityID, 785)
	playerFollowers := appendBytesField(nil, fieldPlayerFollowersComponentPbListPlayerFollowerPb, playerFollower)
	followEntity := appendVarintField(nil, fieldFollowEntityComponentPbEntityID, 786)
	var response []byte
	response = appendBytesField(response, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbSummonerComponent, summoner))
	response = appendBytesField(response, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbMonsterCaptureComponent, monster))
	response = appendBytesField(response, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbPlayerFollowerSelfComponentPb, follower))
	response = appendBytesField(response, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbConcomitantsComponentPb, concomitants))
	response = appendBytesField(response, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbPlayerFollowersComponentPb, playerFollowers))
	response = appendBytesField(response, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbFollowEntityComponentPb, followEntity))
	if err := p.handleActiveResponse(gameFrame{commandID: cmdEntityActiveResponse, direction: "in", token: token}, response); err != nil {
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
	if entity.sourceMessageID != cmdEntityActiveRequest || entity.componentSourceMessageID != cmdEntityActiveResponse || entity.componentSourceFieldPath != activeResponseComponentPath {
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
	if err := applyEntityComponentPayload(&runtimeEntity{}, fieldEntityComponentPbPlayerFollowersComponentPb, appendBytesField(nil, fieldPlayerFollowersComponentPbListPlayerFollowerPb, []byte{0x0a, 0x80})); err == nil {
		t.Fatal("malformed PlayerFollowerPb should fail")
	}

	var concomitants []byte
	for i := 0; i <= maxRuntimeComponentEntityRefs; i++ {
		concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbVisionEntityIDs, uint64(i+1))
	}
	if err := applyEntityComponentPayload(&runtimeEntity{}, 25, concomitants); err == nil {
		t.Fatal("oversized ConcomitantsComponentPb entity list should fail")
	}

	var playerFollowers []byte
	for i := 0; i <= maxRuntimeComponentEntityRefs; i++ {
		playerFollowers = appendBytesField(playerFollowers, fieldPlayerFollowersComponentPbListPlayerFollowerPb, appendVarintField(nil, fieldPlayerFollowerPbEntityID, uint64(i+1)))
	}
	if err := applyEntityComponentPayload(&runtimeEntity{}, 53, playerFollowers); err == nil {
		t.Fatal("oversized PlayerFollowersComponentPb list should fail")
	}
}

func TestApplyGameplayEffectNotifyPreservesRawIdentityFields(t *testing.T) {
	body := appendVarintField(nil, fieldApplyGameplayEffectNotifyHandle, 501)         // Handle
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyID, 71001)           // Id
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyLevel, 2)            // Level
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyEntityID, 8801)      // EntityId
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyInstigatorID, 99002) // InstigatorId
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyApplyType, 1)        // ApplyType
	body = appendFixed32TestField(body, fieldApplyGameplayEffectNotifyDuration, mathFloat32bits(12.5))
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyIsActive, 1)   // IsActive
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyServerID, 601) // ServerId
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyStackCount, 3) // StackCount
	body = appendFixed32TestField(body, fieldApplyGameplayEffectNotifyLeftDuration, mathFloat32bits(4.25))
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyConfBuffID, 72002) // ConfBuffId
	frame := gameFrame{commandID: cmdApplyGameplayEffectNotify, direction: "in", timestamp: time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC)}
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

func TestCombatNotifyBuffRequestMessagesPreserveSchemaFields(t *testing.T) {
	tests := []struct {
		name      string
		field     uint64
		commandID uint16
		body      []byte
		check     func(*testing.T, buffLifecycleAuditRow)
	}{
		{
			name: "apply buff request notify", field: fieldCombatNotifyDataApplyBuffS2CRequestNotify, commandID: cmdApplyBuffS2cRequestNotify,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, fieldApplyBuffS2cRequestNotifyID, 71001), fieldApplyBuffS2cRequestNotifyInstigatorID, 8801), fieldApplyBuffS2cRequestNotifyStackCount, 2), fieldApplyBuffS2cRequestNotifyIsIterable, 1),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.MessageName != "ApplyBuffS2cRequestNotify" || row.EventKind != "BUFF_APPLY_REQUEST_NOTIFY" || row.ID == nil || *row.ID != 71001 || row.InstigatorID == nil || *row.InstigatorID != 8801 || row.StackCount == nil || *row.StackCount != 2 || row.IsIterable == nil || !*row.IsIterable {
					t.Fatalf("apply request fields = %+v", row)
				}
			},
		},
		{
			name: "remove buff request notify", field: fieldCombatNotifyDataRemoveBuffS2CRequestNotify, commandID: cmdRemoveBuffS2cRequestNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, fieldRemoveBuffS2cRequestNotifyHandle, 501), fieldRemoveBuffS2cRequestNotifyStackCount, 1), fieldRemoveBuffS2cRequestNotifyInstigatorID, 8802),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.MessageName != "RemoveBuffS2cRequestNotify" || row.EventKind != "BUFF_REMOVE_REQUEST_NOTIFY" || row.HandleID == nil || *row.HandleID != 501 || row.StackCount == nil || *row.StackCount != 1 || row.InstigatorID == nil || *row.InstigatorID != 8802 {
					t.Fatalf("remove request fields = %+v", row)
				}
			},
		},
		{
			name: "remove buff by id request notify", field: fieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify, commandID: cmdRemoveBuffByIDS2cRequestNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, fieldRemoveBuffByIDS2cRequestNotifyBuffID, 71002), fieldRemoveBuffByIDS2cRequestNotifyStackCount, 3), fieldRemoveBuffByIDS2cRequestNotifyInstigatorID, 8803),
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
			row, err := decodeBuffLifecycleMessageForCommand(gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in"}, test.body, test.commandID, &field)
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

func TestCombatCommonContextIsKeptSeparateFromBuffTarget(t *testing.T) {
	body := appendVarintField(appendVarintField(appendVarintField(nil, fieldCombatCommonMessageID, 101), fieldCombatCommonOriginator, 202), fieldCombatCommonEntityID, 303)
	entityID, originatorID, messageID, err := decodeCombatCommonContext(body)
	if err != nil {
		t.Fatalf("decode CombatCommon context: %v", err)
	}
	if entityID == nil || *entityID != 303 || originatorID == nil || *originatorID != 202 || messageID == nil || *messageID != 101 {
		t.Fatalf("CombatCommon raw context = entity:%v originator:%v message:%v", entityID, originatorID, messageID)
	}
	row, err := decodeBuffLifecycleMessageForCommand(gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in"}, nil, cmdApplyBuffS2cRequestNotify, nil)
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
			name: "remove gameplay effect notify", commandID: cmdRemoveGameplayEffectNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, fieldRemoveGameplayEffectNotifyHandle, 501), fieldRemoveGameplayEffectNotifyEntityID, 8801), fieldRemoveGameplayEffectNotifyInstigatorID, 99002),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.EventKind != "GAMEPLAY_EFFECT_REMOVE" || row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.InstigatorID == nil || *row.InstigatorID != 99002 || row.HandleID == nil || *row.HandleID != 501 {
					t.Fatalf("remove notify fields = %+v", row)
				}
			},
		},
		{
			name: "remove gameplay effect push", commandID: cmdRemoveGameplayEffectPush,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, fieldRemoveGameplayEffectPushHandle, 502), fieldRemoveGameplayEffectPushEntityID, 8802), fieldRemoveGameplayEffectPushIsPrematureRemoval, 1), fieldRemoveGameplayEffectPushInstigatorID, 99003),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8802 || row.HandleID == nil || *row.HandleID != 502 || row.IsPrematureRemoval == nil || !*row.IsPrematureRemoval || row.InstigatorID == nil || *row.InstigatorID != 99003 {
					t.Fatalf("remove push fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff add", commandID: cmdSimpleCombatEntityBuffUpdateNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, fieldSimpleCombatEntityBuffUpdateNotifyEntityID, 8803), fieldSimpleCombatEntityBuffUpdateNotifyIsBuffAdd, 1), fieldSimpleCombatEntityBuffUpdateNotifyBuffID, 70001),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8803 || row.BuffID == nil || *row.BuffID != 70001 || row.IsBuffAdd == nil || !*row.IsBuffAdd || row.InstigatorStatus != resolutionUnknown {
					t.Fatalf("simple buff fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff layer count", commandID: cmdSimpleCombatEntityBuffLayerCountNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, fieldSimpleCombatEntityBuffLayerCountNotifyEntityID, 8804), fieldSimpleCombatEntityBuffLayerCountNotifyLayerCount, 4), fieldSimpleCombatEntityBuffLayerCountNotifyBuffID, 70002),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8804 || row.BuffID == nil || *row.BuffID != 70002 || row.StackCount == nil || *row.StackCount != 4 {
					t.Fatalf("layer count fields = %+v", row)
				}
			},
		},
		{
			name: "stack count by handle", commandID: cmdBuffStackCountNotify,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, fieldBuffStackCountNotifyHandleID, 503), fieldBuffStackCountNotifyNewStackCount, 5), fieldBuffStackCountNotifyNotRefreshDuration, 1), fieldBuffStackCountNotifyNotRefreshPeriod, 1),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.HandleID == nil || *row.HandleID != 503 || row.StackCount == nil || *row.StackCount != 5 || row.TargetEntityID != nil || row.NotRefreshDuration == nil || !*row.NotRefreshDuration || row.NotRefreshPeriod == nil || !*row.NotRefreshPeriod {
					t.Fatalf("handle-based stack fields were conflated with target ID: %+v", row)
				}
			},
		},
		{
			name: "duration by handle", commandID: cmdBuffDurationNotify,
			body: appendFixed32TestField(appendFixed32TestField(appendVarintField(nil, fieldBuffDurationNotifyHandleID, 504), fieldBuffDurationNotifyDuration, mathFloat32bits(9.5)), fieldBuffDurationNotifyLeftDuration, mathFloat32bits(2.25)),
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

func TestEntityActiveResponseDoesNotAttachComponentsWithoutLiveRequestEntity(t *testing.T) {
	t.Run("unmatched token", func(t *testing.T) {
		p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
		monster := appendVarintField(nil, fieldMonsterCaptureComponentPbMonsterID, 456789)
		response := appendBytesField(nil, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbMonsterCaptureComponent, monster))
		if err := p.handleActiveResponse(gameFrame{commandID: cmdEntityActiveResponse, direction: "in", token: 77}, response); err != nil {
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
		if err := p.handleActiveRequest(token, appendVarintField(nil, fieldEntityActiveRequestEntityID, entityID)); err != nil {
			t.Fatal(err)
		}
		remove := appendVarintField(nil, fieldEntityRemoveNotifyIsRemove, 1)
		remove = appendBytesField(remove, fieldEntityRemoveNotifyRemoveInfos, appendVarintField(nil, fieldEntityRemoveInfoEntityID, entityID))
		if _, _, err := p.entities.applyEntityRemoveNotify(remove); err != nil {
			t.Fatal(err)
		}
		monster := appendVarintField(nil, fieldMonsterCaptureComponentPbMonsterID, 456789)
		response := appendBytesField(nil, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbMonsterCaptureComponent, monster))
		if err := p.handleActiveResponse(gameFrame{commandID: cmdEntityActiveResponse, direction: "in", token: token}, response); err != nil {
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
