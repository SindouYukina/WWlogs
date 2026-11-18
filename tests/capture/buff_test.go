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
	p := NewProcessor(nil, &bytes.Buffer{})
	row := BuffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY"}
	if p.observer != nil {
		p.observer.WriteBuffLifecycleConsole(row)
	}
	if stdout.Len() != 0 {
		t.Fatalf("disabled effect console wrote %q", stdout.String())
	}
}

func TestEntityActiveResponseWithoutSnapshotOutputStillParsesBuff(t *testing.T) {
	p := NewProcessor(nil, &bytes.Buffer{})
	buffRecord := appendVarintField(nil, FieldFightBuffInformationBuffID, 70001)
	response := appendBytesField(nil, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, FieldEntityComponentPbFightBuffComponent, appendBytesField(nil, FieldFightBuffComponentPbFightBuffInfos, buffRecord)))
	if err := p.HandleActiveResponse(GameFrame{CommandID: CmdEntityActiveResponse, Direction: "in", Token: 7}, response); err != nil {
		t.Fatalf("handle ActiveResponse without a snapshot writer: %v", err)
	}
	if p.Stats.BuffSnapshotResponses != 1 || p.Stats.ObservedBuffEvents != 1 {
		t.Fatalf("snapshot responses/events = %d/%d, want 1/1", p.Stats.BuffSnapshotResponses, p.Stats.ObservedBuffEvents)
	}
}

func TestEntityActiveResponseCorrelatesIdentityComponentsToRequestEntity(t *testing.T) {
	p := NewProcessor(nil, &bytes.Buffer{})
	const token = 55
	const entityID = 8801
	if err := p.HandleActiveRequest(token, appendVarintField(nil, FieldEntityActiveRequestEntityID, entityID)); err != nil {
		t.Fatal(err)
	}

	summoner := appendVarintField(nil, FieldSummonerComponentPbSummonerID, 771)
	summoner = appendVarintField(summoner, FieldSummonerComponentPbSummonCfgID, 9901)
	summoner = appendVarintField(summoner, fieldSummonerComponentPbSummonSkillID, 220)
	summoner = appendVarintField(summoner, fieldSummonerComponentPbPlayerID, 772)
	summoner = appendVarintField(summoner, FieldSummonerComponentPbType, 2)
	monster := appendVarintField(nil, FieldMonsterCaptureComponentPbTemplateID, 123456)
	monster = appendVarintField(monster, fieldMonsterCaptureComponentPbEntityID, 654321)
	monster = appendVarintField(monster, FieldMonsterCaptureComponentPbMonsterID, 456789)
	follower := appendVarintField(nil, fieldPlayerFollowerSelfComponentPbPlayerEntityID, 773)
	follower = appendVarintField(follower, fieldPlayerFollowerSelfComponentPbSummonConfigID, 9902)
	concomitants := appendVarintField(nil, fieldConcomitantsComponentPbVisionEntityIDs, 781)
	concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbCustomEntityIDs, 782)
	concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbPhantomRoleEid, 783)
	concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbWeakVisionEid, 784)
	playerFollower := appendVarintField(nil, FieldPlayerFollowerPbType, 4)
	playerFollower = appendVarintField(playerFollower, FieldPlayerFollowerPbEntityID, 785)
	playerFollowers := appendBytesField(nil, fieldPlayerFollowersComponentPbListPlayerFollowerPb, playerFollower)
	followEntity := appendVarintField(nil, fieldFollowEntityComponentPbEntityID, 786)
	var response []byte
	response = appendBytesField(response, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, FieldEntityComponentPbSummonerComponent, summoner))
	response = appendBytesField(response, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, FieldEntityComponentPbMonsterCaptureComponent, monster))
	response = appendBytesField(response, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbPlayerFollowerSelfComponentPb, follower))
	response = appendBytesField(response, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbConcomitantsComponentPb, concomitants))
	response = appendBytesField(response, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbPlayerFollowersComponentPb, playerFollowers))
	response = appendBytesField(response, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbFollowEntityComponentPb, followEntity))
	if err := p.HandleActiveResponse(GameFrame{CommandID: CmdEntityActiveResponse, Direction: "in", Token: token}, response); err != nil {
		t.Fatal(err)
	}

	entity := p.Entities.Entities[entityID]
	if !entity.HasMonsterID || entity.MonsterID != 456789 || !entity.HasTemplateID || entity.TemplateID != 123456 || !entity.HasMonsterCaptureEntityID || entity.MonsterCaptureEntityID != 654321 {
		t.Fatalf("monster component identity = %+v", entity)
	}
	if !entity.HasSummonerID || entity.SummonerID != 771 || !entity.HasSummonerPlayerID || entity.SummonerPlayerID != 772 {
		t.Fatalf("summoner component identity = %+v", entity)
	}
	if !entity.HasPlayerEntityID || entity.PlayerEntityID != 773 || !entity.HasFollowerSummonConfigID || entity.FollowerSummonConfigID != 9902 {
		t.Fatalf("follower component identity = %+v", entity)
	}
	if !entity.HasPhantomRoleEntityID || entity.PhantomRoleEntityID != 783 || len(entity.ConcomitantVisionIDs) != 1 || entity.ConcomitantVisionIDs[0] != 781 || len(entity.ConcomitantCustomIDs) != 1 || entity.ConcomitantCustomIDs[0] != 782 || !entity.HasWeakVisionEntityID || entity.WeakVisionEntityID != 784 {
		t.Fatalf("concomitant component references = %+v", entity)
	}
	if len(entity.PlayerFollowers) != 1 || !entity.PlayerFollowers[0].HasType || entity.PlayerFollowers[0].TypeID != 4 || !entity.PlayerFollowers[0].HasEntity || entity.PlayerFollowers[0].EntityID != 785 || !entity.HasFollowEntityID || entity.FollowEntityID != 786 {
		t.Fatalf("follower and follow-entity component references = %+v", entity)
	}
	if entity.SourceMessageID != CmdEntityActiveRequest || entity.ComponentSourceMessageID != CmdEntityActiveResponse || entity.ComponentSourceFieldPath != activeResponseComponentPath {
		t.Fatalf("identity evidence provenance = %+v", entity)
	}
	if status := func() ResolutionStatus { _, status := p.Entities.Resolve(entityID); return status }(); status != ResolutionCorrelated {
		t.Fatalf("runtime entity status = %s, want CORRELATED", status)
	}
	if p.Stats.ActiveResponseIdentityComponents != 6 || p.Stats.ActiveResponseSummonerComponents != 1 || p.Stats.ActiveResponseMonsterComponents != 1 || p.Stats.ActiveResponseFollowerComponents != 1 {
		t.Fatalf("identity component counts = %+v", p.Stats)
	}

	masters := &GameMaster{Monsters: map[uint64]MasterRecord{456789: {ID: 456789, Status: string(ResolutionVerified)}}}
	identity := ResolveEntityIdentity(p.Entities, masters, entityID)
	if !identity.HasMonster || identity.Monster.ID != 456789 || identity.MonsterStatus != ResolutionCorrelated {
		t.Fatalf("static Monster mapping confidence = %+v", identity)
	}
}

func TestRuntimeIdentityComponentReferencesRejectMalformedAndOversizedLists(t *testing.T) {
	if err := applyEntityComponentPayload(&RuntimeEntity{}, fieldEntityComponentPbPlayerFollowersComponentPb, appendBytesField(nil, fieldPlayerFollowersComponentPbListPlayerFollowerPb, []byte{0x0a, 0x80})); err == nil {
		t.Fatal("malformed PlayerFollowerPb should fail")
	}

	var concomitants []byte
	for i := 0; i <= maxRuntimeComponentEntityRefs; i++ {
		concomitants = appendVarintField(concomitants, fieldConcomitantsComponentPbVisionEntityIDs, uint64(i+1))
	}
	if err := applyEntityComponentPayload(&RuntimeEntity{}, 25, concomitants); err == nil {
		t.Fatal("oversized ConcomitantsComponentPb entity list should fail")
	}

	var playerFollowers []byte
	for i := 0; i <= maxRuntimeComponentEntityRefs; i++ {
		playerFollowers = appendBytesField(playerFollowers, fieldPlayerFollowersComponentPbListPlayerFollowerPb, appendVarintField(nil, FieldPlayerFollowerPbEntityID, uint64(i+1)))
	}
	if err := applyEntityComponentPayload(&RuntimeEntity{}, 53, playerFollowers); err == nil {
		t.Fatal("oversized PlayerFollowersComponentPb list should fail")
	}
}

func TestApplyGameplayEffectNotifyPreservesRawIdentityFields(t *testing.T) {
	body := appendVarintField(nil, FieldApplyGameplayEffectNotifyHandle, 501)         // Handle
	body = appendVarintField(body, FieldApplyGameplayEffectNotifyID, 71001)           // Id
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyLevel, 2)            // Level
	body = appendVarintField(body, FieldApplyGameplayEffectNotifyEntityID, 8801)      // EntityId
	body = appendVarintField(body, FieldApplyGameplayEffectNotifyInstigatorID, 99002) // InstigatorId
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyApplyType, 1)        // ApplyType
	body = appendFixed32TestField(body, FieldApplyGameplayEffectNotifyDuration, mathFloat32bits(12.5))
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyIsActive, 1)   // IsActive
	body = appendVarintField(body, fieldApplyGameplayEffectNotifyServerID, 601) // ServerId
	body = appendVarintField(body, FieldApplyGameplayEffectNotifyStackCount, 3) // StackCount
	body = appendFixed32TestField(body, FieldApplyGameplayEffectNotifyLeftDuration, mathFloat32bits(4.25))
	body = appendVarintField(body, FieldApplyGameplayEffectNotifyConfBuffID, 72002) // ConfBuffId
	frame := GameFrame{CommandID: CmdApplyGameplayEffectNotify, Direction: "in", Timestamp: time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC)}
	row, err := DecodeBuffLifecycleMessage(frame, body)
	if err != nil {
		t.Fatal(err)
	}
	if row.MessageName != "ApplyGameplayEffectNotify" || row.EventKind != "GAMEPLAY_EFFECT_APPLY" || row.EventTypeStatus != ResolutionCorrelated {
		t.Fatalf("event classification = %+v", row)
	}
	if row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.TargetStatus != ResolutionUnknown || row.InstigatorID == nil || *row.InstigatorID != 99002 || row.InstigatorStatus != ResolutionUnknown || !strings.Contains(row.TargetEvidence, "UNKNOWN") || !strings.Contains(row.InstigatorEvidence, "UNKNOWN") {
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
		check     func(*testing.T, BuffLifecycleAuditRow)
	}{
		{
			name: "apply buff request notify", field: FieldCombatNotifyDataApplyBuffS2CRequestNotify, commandID: CmdApplyBuffS2cRequestNotify,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, FieldApplyBuffS2cRequestNotifyID, 71001), FieldApplyBuffS2cRequestNotifyInstigatorID, 8801), fieldApplyBuffS2cRequestNotifyStackCount, 2), fieldApplyBuffS2cRequestNotifyIsIterable, 1),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.MessageName != "ApplyBuffS2cRequestNotify" || row.EventKind != "BUFF_APPLY_REQUEST_NOTIFY" || row.ID == nil || *row.ID != 71001 || row.InstigatorID == nil || *row.InstigatorID != 8801 || row.StackCount == nil || *row.StackCount != 2 || row.IsIterable == nil || !*row.IsIterable {
					t.Fatalf("apply request fields = %+v", row)
				}
			},
		},
		{
			name: "remove buff request notify", field: fieldCombatNotifyDataRemoveBuffS2CRequestNotify, commandID: CmdRemoveBuffS2cRequestNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, fieldRemoveBuffS2cRequestNotifyHandle, 501), fieldRemoveBuffS2cRequestNotifyStackCount, 1), fieldRemoveBuffS2cRequestNotifyInstigatorID, 8802),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.MessageName != "RemoveBuffS2cRequestNotify" || row.EventKind != "BUFF_REMOVE_REQUEST_NOTIFY" || row.HandleID == nil || *row.HandleID != 501 || row.StackCount == nil || *row.StackCount != 1 || row.InstigatorID == nil || *row.InstigatorID != 8802 {
					t.Fatalf("remove request fields = %+v", row)
				}
			},
		},
		{
			name: "remove buff by id request notify", field: FieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify, commandID: CmdRemoveBuffByIDS2cRequestNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, FieldRemoveBuffByIDS2cRequestNotifyBuffID, 71002), fieldRemoveBuffByIDS2cRequestNotifyStackCount, 3), FieldRemoveBuffByIDS2cRequestNotifyInstigatorID, 8803),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.MessageName != "RemoveBuffByIdS2cRequestNotify" || row.EventKind != "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY" || row.BuffID == nil || *row.BuffID != 71002 || row.StackCount == nil || *row.StackCount != 3 || row.InstigatorID == nil || *row.InstigatorID != 8803 {
					t.Fatalf("remove by ID fields = %+v", row)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field := test.field
			row, err := decodeBuffLifecycleMessageForCommand(GameFrame{CommandID: CmdCombatReceivePackNotify, Direction: "in"}, test.body, test.commandID, &field)
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
	body := appendVarintField(appendVarintField(appendVarintField(nil, FieldCombatCommonMessageID, 101), fieldCombatCommonOriginator, 202), FieldCombatCommonEntityID, 303)
	entityID, originatorID, messageID, err := decodeCombatCommonContext(body)
	if err != nil {
		t.Fatalf("decode CombatCommon context: %v", err)
	}
	if entityID == nil || *entityID != 303 || originatorID == nil || *originatorID != 202 || messageID == nil || *messageID != 101 {
		t.Fatalf("CombatCommon raw context = entity:%v originator:%v message:%v", entityID, originatorID, messageID)
	}
	row, err := decodeBuffLifecycleMessageForCommand(GameFrame{CommandID: CmdCombatReceivePackNotify, Direction: "in"}, nil, CmdApplyBuffS2cRequestNotify, nil)
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
		check     func(*testing.T, BuffLifecycleAuditRow)
	}{
		{
			name: "remove gameplay effect notify", commandID: CmdRemoveGameplayEffectNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, FieldRemoveGameplayEffectNotifyHandle, 501), FieldRemoveGameplayEffectNotifyEntityID, 8801), FieldRemoveGameplayEffectNotifyInstigatorID, 99002),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.EventKind != "GAMEPLAY_EFFECT_REMOVE" || row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.InstigatorID == nil || *row.InstigatorID != 99002 || row.HandleID == nil || *row.HandleID != 501 {
					t.Fatalf("remove notify fields = %+v", row)
				}
			},
		},
		{
			name: "remove gameplay effect push", commandID: CmdRemoveGameplayEffectPush,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, FieldRemoveGameplayEffectPushHandle, 502), FieldRemoveGameplayEffectPushEntityID, 8802), FieldRemoveGameplayEffectPushIsPrematureRemoval, 1), fieldRemoveGameplayEffectPushInstigatorID, 99003),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8802 || row.HandleID == nil || *row.HandleID != 502 || row.IsPrematureRemoval == nil || !*row.IsPrematureRemoval || row.InstigatorID == nil || *row.InstigatorID != 99003 {
					t.Fatalf("remove push fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff add", commandID: CmdSimpleCombatEntityBuffUpdateNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, FieldSimpleCombatEntityBuffUpdateNotifyEntityID, 8803), fieldSimpleCombatEntityBuffUpdateNotifyIsBuffAdd, 1), fieldSimpleCombatEntityBuffUpdateNotifyBuffID, 70001),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8803 || row.BuffID == nil || *row.BuffID != 70001 || row.IsBuffAdd == nil || !*row.IsBuffAdd || row.InstigatorStatus != ResolutionUnknown {
					t.Fatalf("simple buff fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff layer count", commandID: CmdSimpleCombatEntityBuffLayerCountNotify,
			body: appendVarintField(appendVarintField(appendVarintField(nil, fieldSimpleCombatEntityBuffLayerCountNotifyEntityID, 8804), fieldSimpleCombatEntityBuffLayerCountNotifyLayerCount, 4), fieldSimpleCombatEntityBuffLayerCountNotifyBuffID, 70002),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8804 || row.BuffID == nil || *row.BuffID != 70002 || row.StackCount == nil || *row.StackCount != 4 {
					t.Fatalf("layer count fields = %+v", row)
				}
			},
		},
		{
			name: "stack count by handle", commandID: CmdBuffStackCountNotify,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, FieldBuffStackCountNotifyHandleID, 503), FieldBuffStackCountNotifyNewStackCount, 5), fieldBuffStackCountNotifyNotRefreshDuration, 1), fieldBuffStackCountNotifyNotRefreshPeriod, 1),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.HandleID == nil || *row.HandleID != 503 || row.StackCount == nil || *row.StackCount != 5 || row.TargetEntityID != nil || row.NotRefreshDuration == nil || !*row.NotRefreshDuration || row.NotRefreshPeriod == nil || !*row.NotRefreshPeriod {
					t.Fatalf("handle-based stack fields were conflated with target ID: %+v", row)
				}
			},
		},
		{
			name: "duration by handle", commandID: CmdBuffDurationNotify,
			body: appendFixed32TestField(appendFixed32TestField(appendVarintField(nil, FieldBuffDurationNotifyHandleID, 504), FieldBuffDurationNotifyDuration, mathFloat32bits(9.5)), FieldBuffDurationNotifyLeftDuration, mathFloat32bits(2.25)),
			check: func(t *testing.T, row BuffLifecycleAuditRow) {
				if row.HandleID == nil || *row.HandleID != 504 || row.DurationSeconds == nil || *row.DurationSeconds != 9.5 || row.RemainingDuration == nil || *row.RemainingDuration != 2.25 || row.TargetEntityID != nil {
					t.Fatalf("handle-based duration fields were conflated with target ID: %+v", row)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row, err := DecodeBuffLifecycleMessage(GameFrame{CommandID: test.commandID, Direction: "in"}, test.body)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, row)
		})
	}
}

func TestEntityActiveResponseDoesNotAttachComponentsWithoutLiveRequestEntity(t *testing.T) {
	t.Run("unmatched token", func(t *testing.T) {
		p := NewProcessor(nil, &bytes.Buffer{})
		monster := appendVarintField(nil, FieldMonsterCaptureComponentPbMonsterID, 456789)
		response := appendBytesField(nil, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, FieldEntityComponentPbMonsterCaptureComponent, monster))
		if err := p.HandleActiveResponse(GameFrame{CommandID: CmdEntityActiveResponse, Direction: "in", Token: 77}, response); err != nil {
			t.Fatal(err)
		}
		if len(p.Entities.Entities) != 0 {
			t.Fatalf("unmatched response created runtime entities: %+v", p.Entities.Entities)
		}
	})

	t.Run("request entity removed before response", func(t *testing.T) {
		p := NewProcessor(nil, &bytes.Buffer{})
		const token = 78
		const entityID = 8802
		if err := p.HandleActiveRequest(token, appendVarintField(nil, FieldEntityActiveRequestEntityID, entityID)); err != nil {
			t.Fatal(err)
		}
		remove := appendVarintField(nil, FieldEntityRemoveNotifyIsRemove, 1)
		remove = appendBytesField(remove, FieldEntityRemoveNotifyRemoveInfos, appendVarintField(nil, FieldEntityRemoveInfoEntityID, entityID))
		if _, _, err := p.Entities.ApplyEntityRemoveNotify(remove); err != nil {
			t.Fatal(err)
		}
		monster := appendVarintField(nil, FieldMonsterCaptureComponentPbMonsterID, 456789)
		response := appendBytesField(nil, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, FieldEntityComponentPbMonsterCaptureComponent, monster))
		if err := p.HandleActiveResponse(GameFrame{CommandID: CmdEntityActiveResponse, Direction: "in", Token: token}, response); err != nil {
			t.Fatal(err)
		}
		if _, ok := p.Entities.Entities[entityID]; ok {
			t.Fatal("late response resurrected a removed entity")
		}
	})
}

func mathFloat32bits(value float32) uint32 {
	return math.Float32bits(value)
}
