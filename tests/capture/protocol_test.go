package capture

import (
	"bytes"
	"strings"
	"testing"
)

func appendVarintField(dst []byte, number, value uint64) []byte {
	dst = appendTestVarint(dst, number<<3)
	return appendTestVarint(dst, value)
}

func appendBytesField(dst []byte, number uint64, value []byte) []byte {
	dst = appendTestVarint(dst, number<<3|2)
	dst = appendTestVarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendTestVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func TestDamageRequestUsesSchemaEntityAndSkillFields(t *testing.T) {
	p := NewProcessor(nil, &bytes.Buffer{})
	u3n := appendVarintField(nil, FieldDamageExecuteRequestDamageID, 70010001)     // DamageId
	u3n = appendVarintField(u3n, FieldDamageExecuteRequestTargetEntityID, 902)     // TargetEntityId
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestSkillLevel, 2)           // SkillLevel
	u3n = appendVarintField(u3n, FieldDamageExecuteRequestAttackerEntityID, 25320) // AttackerEntityId
	context := appendVarintField(nil, FieldDamageContextSourceType, 0)             // SourceType: FromBullet
	context = appendVarintField(context, FieldDamageContextBulletID, 9001)         // BulletId
	context = appendVarintField(context, FieldDamageContextSkillID, 1312011)       // SkillId
	context = appendVarintField(context, FieldDamageContextSkillMessageID, 77)     // SkillMessageId
	u3n = appendBytesField(u3n, fieldDamageExecuteRequestDamageContext, context)   // DamageContext
	request := appendVarintField(nil, FieldCombatRequestDataRequestID, 42)         // RPC ID
	request = appendBytesField(request, FieldCombatRequestDataDamageExecuteRequest, u3n)
	variant := appendBytesField(nil, FieldCombatSendDataRequest, request)
	outer := appendBytesField(nil, FieldCombatSendPackRequestData, variant)

	p.HandleDamageRequests(GameFrame{Direction: "out", CommandID: CmdCombatSendPackRequest}, outer)

	got, ok := p.Pending[42]
	if !ok {
		t.Fatal("damage request was not retained")
	}
	if !got.HasAttackerEntityID || got.AttackerEntityID != 25320 {
		t.Fatalf("attacker entity ID = %d (present=%t), want 25320", got.AttackerEntityID, got.HasAttackerEntityID)
	}
	if !got.HasTargetEntityID || got.TargetEntityID != 902 {
		t.Fatalf("target entity ID = %d (present=%t), want 902", got.TargetEntityID, got.HasTargetEntityID)
	}
	if got.skillLevel != 2 || got.SkillID != 1312011 || got.SkillMessageID != 77 || got.DamageID != 70010001 || !got.HasDamageSourceType || got.DamageSourceType != 0 || !got.HasBulletID || got.BulletID != 9001 {
		t.Fatalf("request context = %+v, want skill, damage, source type and bullet IDs to match schema fields", got)
	}
}

func TestDamageCandidateResultHasSeparateSemanticBucket(t *testing.T) {
	var diagnostic bytes.Buffer
	p := NewProcessor(nil, &diagnostic)
	p.Masters = &GameMaster{Damage: map[uint64]DamageMasterRecord{
		70010001: {ID: 70010001, CalculateType: 0, SourceHash: strings.Repeat("a", 64), SourceTable: "damage"},
	}}
	p.Pending[10] = PendingDamageRequest{DamageID: 70010001, HasDamageID: true}
	response := appendVarintField(nil, FieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, FieldDamageExecuteResponseImmuneType, 0)
	result := int32(-42)
	response = appendVarintField(response, FieldDamageExecuteResponseDamage, uint64(uint32(result)))

	p.HandleOneDamageResult(10, response)

	if p.Stats.MatchedCombatResultAbs != 42 || p.Stats.DecodedCombatResultAbs != 42 || p.Stats.IncludedUnresolvedRelations != 1 || p.Stats.DamageCandidateEvents != 1 || p.Stats.DamageCandidateResult != 42 || p.Stats.HealingCandidateResult != 0 || p.Stats.OtherCandidateResult != 0 || p.Stats.UnclassifiedResult != 0 {
		t.Fatalf("damage candidate was not isolated from other result buckets: %+v", p.Stats)
	}
}

func TestDamageResponseWithoutSourceUsesUnknown(t *testing.T) {
	var diagnostic bytes.Buffer
	p := NewProcessor(nil, &diagnostic)
	p.Pending[7] = PendingDamageRequest{}
	response := appendVarintField(nil, FieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, FieldDamageExecuteResponseImmuneType, 0)
	response = appendVarintField(response, FieldDamageExecuteResponseDamage, 33)

	p.HandleOneDamageResult(7, response)

	// The result counts as visible (the Development console line is tested with the Development module).
	if p.Stats.IncludedUnresolvedRelations != 1 || p.Stats.SuppressedUnrelatedEvents != 0 {
		t.Fatalf("damage with missing actor was hidden without proof it was unrelated: stats=%+v", p.Stats)
	}
	if p.Stats.MissingAttackerEntityID != 1 {
		t.Fatalf("missingAttackerEntityID = %d, want 1", p.Stats.MissingAttackerEntityID)
	}
}

func TestEntityAddCapturesRawIdentityWithoutPromotingSkinToCharacter(t *testing.T) {
	resolver := NewEntityResolver()
	monsterCapture := appendVarintField(nil, FieldMonsterCaptureComponentPbTemplateID, 310000010)          // TemplateId
	monsterCapture = appendVarintField(monsterCapture, FieldMonsterCaptureComponentPbMonsterID, 310000020) // MonsterId
	component := appendBytesField(nil, FieldEntityComponentPbMonsterCaptureComponent, monsterCapture)      // EntityComponentPb.MonsterCaptureComponent
	entity := appendVarintField(nil, FieldEntityPbID, 4201)                                                // EntityPb.Id
	entity = appendVarintField(entity, FieldEntityPbConfigID, 692750288)                                   // ConfigId
	entity = appendVarintField(entity, FieldEntityPbConfigType, 1)                                         // ConfigType
	entity = appendVarintField(entity, fieldEntityPbEntityType, 2)                                         // EntityType
	entity = appendVarintField(entity, fieldEntityPbPlayerID, 77)                                          // PlayerId
	entity = appendBytesField(entity, FieldEntityPbComponentPbs, component)                                // ComponentPbs
	entity = appendVarintField(entity, fieldEntityPbRoleSkinID, 901)                                       // RoleSkinId, not CharacterId
	notify := appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1)                                         // IsAdd
	notify = appendBytesField(notify, FieldEntityAddNotifyEntityPbs, entity)

	count, err := resolver.ApplyEntityAddNotify(notify)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("added entities = %d, want 1", count)
	}
	got, status := resolver.Resolve(4201)
	if status != ResolutionVerified || !got.HasMonsterID || got.MonsterID != 310000020 || !got.HasTemplateID || got.TemplateID != 310000010 {
		t.Fatalf("monster identity was not preserved from its component: %+v status=%s", got, status)
	}
	if proof := entityProof(got); proof != "EntityAddNotify.EntityPb.Id" {
		t.Fatalf("entity-add evidence should be explicit, got %q", proof)
	}
	if got.HasRoleSkinID != true || got.RoleSkinID != 901 {
		t.Fatalf("RoleSkinId was not retained as its own field: %+v", got)
	}
}

func TestMonsterCaptureIdentityRemainsCorrelatedUntilMasterCompatibilityVerified(t *testing.T) {
	resolver := NewEntityResolver()
	capture := appendVarintField(nil, FieldMonsterCaptureComponentPbTemplateID, 702)   // MonsterCaptureComponentPb.TemplateId
	capture = appendVarintField(capture, FieldMonsterCaptureComponentPbMonsterID, 701) // MonsterCaptureComponentPb.MonsterId
	component := appendBytesField(nil, FieldEntityComponentPbMonsterCaptureComponent, capture)
	entity := appendVarintField(nil, FieldEntityPbID, 500) // EntityPb.Id
	entity = appendBytesField(entity, FieldEntityPbComponentPbs, component)
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}

	masters := &GameMaster{Monsters: map[uint64]MasterRecord{
		701: {ID: 701, Name: "Monster", Status: string(ResolutionVerified)},
		702: {ID: 702, Name: "Template must not resolve as MonsterId", Status: string(ResolutionVerified)},
	}}
	identity := ResolveEntityIdentity(resolver, masters, 500)
	if !identity.HasMonster || identity.Monster.ID != 701 {
		t.Fatalf("MonsterCapture MonsterId did not resolve directly: %+v", identity)
	}
	if identity.MonsterStatus != ResolutionCorrelated {
		t.Fatalf("unverified master compatibility promoted a versioned Monster join: status=%s identity=%+v", identity.MonsterStatus, identity)
	}
	if !strings.Contains(identity.MonsterProof, "MonsterCaptureComponentPb.MonsterId") || !strings.Contains(identity.MonsterProof, "master compatibility unverified") {
		t.Fatalf("Monster proof omitted schema/build limitation: %q", identity.MonsterProof)
	}
}

func TestJoinSceneSnapshotResolvesBothEntityBranchesAndPreservesOwnerNamespaces(t *testing.T) {
	resolver := NewEntityResolver()
	stale := appendVarintField(nil, FieldEntityPbID, 99)
	if _, err := resolver.ApplyEntityAddNotify(appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, stale)); err != nil {
		t.Fatal(err)
	}

	summonerComponent := appendVarintField(nil, FieldSummonerComponentPbSummonerID, 600) // SummonerComponentPb.SummonerId
	summonerComponent = appendVarintField(summonerComponent, FieldSummonerComponentPbSummonCfgID, 610)
	summonerComponent = appendVarintField(summonerComponent, fieldSummonerComponentPbSummonSkillID, 620)
	summonerComponent = appendVarintField(summonerComponent, fieldSummonerComponentPbPlayerID, 630)
	summonerComponent = appendVarintField(summonerComponent, FieldSummonerComponentPbType, 2)
	monsterComponent := appendVarintField(nil, FieldMonsterCaptureComponentPbTemplateID, 700) // MonsterCaptureComponentPb.TemplateId
	monsterComponent = appendVarintField(monsterComponent, FieldMonsterCaptureComponentPbMonsterID, 701)
	followerComponent := appendVarintField(nil, fieldPlayerFollowerSelfComponentPbPlayerEntityID, 640)
	followerComponent = appendVarintField(followerComponent, fieldPlayerFollowerSelfComponentPbSummonConfigID, 611)
	components := appendBytesField(nil, FieldEntityComponentPbSummonerComponent, summonerComponent)
	components = appendBytesField(components, FieldEntityComponentPbMonsterCaptureComponent, monsterComponent)
	components = appendBytesField(components, fieldEntityComponentPbPlayerFollowerSelfComponentPb, followerComponent)

	summonInfo := appendVarintField(nil, fieldSummonInfoSummonCfgID, 710)            // SummonInfo.SummonCfgId
	summonInfo = appendVarintField(summonInfo, fieldSummonInfoSummonerID, 800)       // SummonInfo.SummonerId
	summonInfo = appendVarintField(summonInfo, fieldSummonInfoSummonSkillID, 720)    // SummonInfo.SummonSkillId
	dynamic := appendVarintField(nil, fieldDynamicEntityInformationID, 500)          // DynamicEntityInformation.Id
	dynamic = appendVarintField(dynamic, fieldDynamicEntityInformationEntityType, 4) // EntityType
	dynamic = appendVarintField(dynamic, fieldDynamicEntityInformationConfigID, 900) // ConfigId
	dynamic = appendVarintField(dynamic, fieldDynamicEntityInformationPlayerID, 77)  // PlayerId
	dynamic = appendVarintField(dynamic, fieldDynamicEntityInformationOwnerID, 88)   // OwnerId
	dynamic = appendBytesField(dynamic, fieldDynamicEntityInformationSummonInfo, summonInfo)
	for _, component := range [][]byte{appendBytesField(nil, FieldEntityComponentPbSummonerComponent, summonerComponent), appendBytesField(nil, FieldEntityComponentPbMonsterCaptureComponent, monsterComponent), appendBytesField(nil, fieldEntityComponentPbPlayerFollowerSelfComponentPb, followerComponent)} {
		dynamic = appendBytesField(dynamic, fieldDynamicEntityInformationComponentPbs, component)
	}

	entityPb := appendVarintField(nil, FieldEntityPbID, 500)             // EntityPb.Id, same entity as DynamicEntityInformation.Id
	entityPb = appendVarintField(entityPb, fieldEntityPbOwnerIncID, 889) // OwnerIncId is distinct from OwnerId
	entityPb = appendBytesField(entityPb, FieldEntityPbComponentPbs, components)
	entityPb = appendVarintField(entityPb, fieldEntityPbRoleSkinID, 901) // RoleSkinId is not CharacterId

	aoi := appendBytesField(nil, fieldPlayerSceneAoiDataDynamicEntityList, dynamic)
	aoi = appendBytesField(aoi, FieldPlayerSceneAoiDataEntities, entityPb)
	otherAoiDynamic := appendVarintField(nil, fieldDynamicEntityInformationID, 501)
	aoi = appendBytesField(aoi, fieldPlayerSceneAoiDataDynamicEntityList, otherAoiDynamic)
	sceneInfo := appendVarintField(nil, fieldSceneInformationSceneID, 42)         // SceneId
	sceneInfo = appendVarintField(sceneInfo, fieldSceneInformationInstanceID, 43) // InstanceId
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationDynamicEntityList, dynamic)
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	joinScene := appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)

	count, err := resolver.applyJoinSceneNotify(joinScene)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("snapshot unique entity count = %d, want 2", count)
	}
	if _, exists := resolver.Entities[99]; exists {
		t.Fatal("scene snapshot retained a stale entity from the prior scene")
	}
	got, status := resolver.Resolve(500)
	if status != ResolutionVerified || got.SourceMessageID != CmdJoinSceneNotify || got.SourceFieldPath != "JoinSceneNotify.SceneInfo.AoiData.Entities.EntityPb.Id" {
		t.Fatalf("snapshot EntityPb proof/status = %q/%s entity=%+v", got.SourceFieldPath, status, got)
	}
	if got.ConfigID != 900 || !got.HasConfigID || got.EntityTypeCode != 4 || got.PlayerID != 77 || got.OwnerID != 88 || got.OwnerIncID != 889 {
		t.Fatalf("runtime/config/owner fields were not kept separately: %+v", got)
	}
	if got.SummonerID != 600 || got.SceneSummonerID != 800 || got.SummonConfigID != 610 || got.SceneSummonConfigID != 710 || got.SummonSkillID != 620 || got.SceneSummonSkillID != 720 {
		t.Fatalf("Summoner component and SummonInfo fields were conflated or lost: %+v", got)
	}
	if got.SummonerPlayerID != 630 || got.PlayerEntityID != 640 || got.FollowerSummonConfigID != 611 || got.MonsterID != 701 || got.TemplateID != 700 || got.RoleSkinID != 901 {
		t.Fatalf("component fields were not preserved independently: %+v", got)
	}
	second, status := resolver.Resolve(501)
	if status != ResolutionVerified || entityProof(second) != "JoinSceneNotify.SceneInfo.AoiData.DynamicEntityList.Id" {
		t.Fatalf("AOI DynamicEntityInformation path not retained: entity=%+v status=%s", second, status)
	}
}

func TestJoinSceneLevelConfigResolvesOnlyUniqueVersionedMonsterCandidate(t *testing.T) {
	resolver := NewEntityResolver()
	masters := &GameMaster{
		Monsters: map[uint64]MasterRecord{
			700: {ID: 700, Name: "candidate monster", Status: string(ResolutionVerified)},
		},
		SceneIdentity: &SceneIdentityMaster{
			InstanceMaps:  map[uint64]uint64{43: 100},
			levelMonsters: map[SceneLevelKey]uint64{{MapID: 100, EntityID: 900}: 700},
		},
	}
	entity := appendVarintField(nil, FieldEntityPbID, 500)         // EntityPb.Id is the runtime CreatureDataId.
	entity = appendVarintField(entity, FieldEntityPbConfigID, 900) // EntityPb.ConfigId / PbDataId.
	entity = appendVarintField(entity, FieldEntityPbConfigType, 1) // Proto_Level; other config namespaces must not use this crosswalk.
	aoi := appendBytesField(nil, FieldPlayerSceneAoiDataEntities, entity)
	sceneInfo := appendVarintField(nil, fieldSceneInformationInstanceID, 43) // InstanceId, resolved through same-version InstanceDungeon data.
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	identity := ResolveEntityIdentity(resolver, masters, 500)
	if !identity.HasMonster || identity.Monster.ID != 700 || identity.MonsterStatus != ResolutionCorrelated {
		t.Fatalf("same-scene unique map-scoped candidate = %+v", identity)
	}
	if !identity.Runtime.HasSceneInstanceID || identity.Runtime.SceneInstanceID != 43 {
		t.Fatalf("runtime audit entity lost its InstanceId scope: %+v", identity.Runtime)
	}
	if !strings.Contains(identity.MonsterProof, "InstanceId=43 -> InstanceDungeon.MapConfigId") {
		t.Fatalf("monster proof omitted scene map derivation: %q", identity.MonsterProof)
	}

	// ConfigType and map scope are required; matching ConfigId alone is insufficient.
	entity = appendVarintField(nil, FieldEntityPbID, 501)
	entity = appendVarintField(entity, FieldEntityPbConfigID, 900)
	entity = appendVarintField(entity, FieldEntityPbConfigType, 2)
	aoi = appendBytesField(nil, FieldPlayerSceneAoiDataEntities, entity)
	sceneInfo = appendVarintField(nil, fieldSceneInformationInstanceID, 43)
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	identity = ResolveEntityIdentity(resolver, masters, 501)
	if identity.HasMonster || identity.MonsterStatus != ResolutionUnknown {
		t.Fatalf("non-Level ConfigType was incorrectly mapped: %+v", identity)
	}
	resolver.reset()
	if resolver.hasSceneInstanceID {
		t.Fatal("scene reset retained InstanceId scope")
	}
}

func TestJoinSceneSnapshotParseFailureKeepsPriorResolverState(t *testing.T) {
	resolver := NewEntityResolver()
	entity := appendVarintField(nil, FieldEntityPbID, 99)
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	malformedDynamic := []byte{0x0a, 0x80} // truncated varint in DynamicEntityInformation
	sceneInfo := appendBytesField(nil, fieldSceneInformationDynamicEntityList, malformedDynamic)
	joinScene := appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)
	if _, err := resolver.applyJoinSceneNotify(joinScene); err == nil {
		t.Fatal("malformed scene entity should fail decoding")
	}
	if _, exists := resolver.Entities[99]; !exists {
		t.Fatal("failed scene snapshot replaced the prior resolver state")
	}
}

func TestJoinScenePlayerFormationMapsCreatureDataIDToRoleID(t *testing.T) {
	resolver := NewEntityResolver()
	role := appendVarintField(nil, FieldSceneRoleInformationRoleID, 1312)           // SceneRoleInformation.RoleId
	role = appendVarintField(role, FieldSceneRoleInformationEntityID, 502)          // SceneRoleInformation.EntityId
	role = appendVarintField(role, fieldSceneRoleInformationOnStageOutOfControl, 1) // OnStageOutOfControl
	group := appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, role)   // PlayerSceneGroupData.FightRoleInfos[]
	player := appendVarintField(nil, FieldScenePlayerInformationPlayerID, 77)       // ScenePlayerInformation.PlayerId
	player = appendBytesField(player, FieldScenePlayerInformationPlayerSceneGroupData, group)
	sceneInfo := appendBytesField(nil, FieldSceneInformationPlayerInfos, player) // SceneInformation.PlayerInfos[]
	joinScene := appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)

	count, err := resolver.applyJoinSceneNotify(joinScene)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("snapshot unique entity count = %d, want 1", count)
	}
	entity, status := resolver.Resolve(502)
	if status != ResolutionVerified || !entity.HasRoleID || entity.RoleID != 1312 || !entity.HasPlayerID || entity.PlayerID != 77 {
		t.Fatalf("scene role identity = %+v status=%s", entity, status)
	}
	if entity.HasOwnerID || entity.HasOwnerIncID || entity.HasSummonerID {
		t.Fatalf("player formation membership was conflated with owner/summoner: %+v", entity)
	}
	if entity.RoleSourceFieldPath != "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId" {
		t.Fatalf("role mapping provenance = %q", entity.RoleSourceFieldPath)
	}

	masters := &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(ResolutionVerified), Status: string(ResolutionVerified)},
	}}
	identity := ResolveEntityIdentity(resolver, masters, 502)
	if !identity.HasCharacter || identity.Character.ID != 1312 || identity.CharacterStatus != ResolutionVerified {
		t.Fatalf("direct scene RoleId did not resolve CharacterId: %+v", identity)
	}
}

func TestUpdateSceneGroupDataMapsEntityIDToRoleID(t *testing.T) {
	resolver := NewEntityResolver()
	role := appendVarintField(nil, FieldSceneRoleInformationRoleID, 1312)           // SceneRoleInformation.RoleId
	role = appendVarintField(role, FieldSceneRoleInformationEntityID, 502)          // SceneRoleInformation.EntityId
	role = appendVarintField(role, fieldSceneRoleInformationOnStageOutOfControl, 1) // SceneRoleInformation.OnStageOutOfControl
	group := appendVarintField(nil, fieldPlayerSceneGroupDataGroup, 1)              // PlayerSceneGroupData.Group
	group = appendBytesField(group, FieldPlayerSceneGroupDataFightRoleInfos, role)
	group = appendVarintField(group, fieldPlayerSceneGroupDataCurRole, 1312)    // PlayerSceneGroupData.CurRole
	player := appendVarintField(nil, fieldPlayerSceneGroupDataListPlayerID, 77) // PlayerSceneGroupDataList.PlayerId
	player = appendBytesField(player, fieldPlayerSceneGroupDataListPlayerSceneGroupData, group)
	player = appendVarintField(player, fieldPlayerSceneGroupDataListCurrentGroup, 1)                 // PlayerSceneGroupDataList.CurrentGroup
	notify := appendBytesField(nil, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, player) // UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[]

	count, err := resolver.applyUpdateSceneGroupDataNotify(notify)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("mapped role count = %d, want 1", count)
	}
	entity, status := resolver.Resolve(502)
	if status != ResolutionVerified || !entity.HasRoleID || entity.RoleID != 1312 || !entity.HasPlayerID || entity.PlayerID != 77 {
		t.Fatalf("scene-group identity = %+v status=%s", entity, status)
	}
	if got := entityProof(entity); got != "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].EntityId" {
		t.Fatalf("runtime entity proof = %q", got)
	}
	identity := ResolveEntityIdentity(resolver, &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(ResolutionVerified), Status: string(ResolutionVerified)},
	}}, 502)
	if !identity.HasCharacter || identity.Character.ID != 1312 || identity.CharacterStatus != ResolutionVerified {
		t.Fatalf("direct SceneRoleInformation mapping did not resolve the character: %+v", identity)
	}
}

func TestUpdateSceneGroupDataPreservesLifecycleAndRejectsRoleConflicts(t *testing.T) {
	resolver := NewEntityResolver()
	entityPb := appendVarintField(nil, FieldEntityPbID, 502) // EntityPb.Id
	entityPb = appendVarintField(entityPb, FieldEntityPbConfigID, 1312)
	entityPb = appendVarintField(entityPb, FieldEntityPbConfigType, 3)
	if _, err := resolver.ApplyEntityAddNotify(appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entityPb)); err != nil {
		t.Fatal(err)
	}
	makeUpdate := func(roleID uint64) []byte {
		role := appendVarintField(nil, FieldSceneRoleInformationRoleID, roleID)
		role = appendVarintField(role, FieldSceneRoleInformationEntityID, 502)
		group := appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, role)
		player := appendBytesField(appendVarintField(nil, fieldPlayerSceneGroupDataListPlayerID, 77), fieldPlayerSceneGroupDataListPlayerSceneGroupData, group)
		return appendBytesField(nil, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, player)
	}
	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeUpdate(1312)); err != nil {
		t.Fatal(err)
	}
	if entity, status := resolver.Resolve(502); status != ResolutionVerified || entity.SourceMessageID != CmdEntityAddNotify || entity.RoleID != 1312 {
		t.Fatalf("team update downgraded EntityAdd lifecycle evidence: %+v status=%s", entity, status)
	}
	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeUpdate(1313)); err != nil {
		t.Fatal(err)
	}
	entity, _ := resolver.Resolve(502)
	if !entity.RoleConflict || entity.HasRoleID {
		t.Fatalf("conflicting role evidence was not retained as unresolved: %+v", entity)
	}
}

func TestMalformedSceneGroupUpdateDoesNotPartiallyMutateResolver(t *testing.T) {
	resolver := NewEntityResolver()
	validRole := appendVarintField(nil, FieldSceneRoleInformationRoleID, 1312)
	validRole = appendVarintField(validRole, FieldSceneRoleInformationEntityID, 502)
	validGroup := appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, validRole)
	validPlayer := appendBytesField(appendVarintField(nil, fieldPlayerSceneGroupDataListPlayerID, 77), fieldPlayerSceneGroupDataListPlayerSceneGroupData, validGroup)
	notify := appendBytesField(nil, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, validPlayer)
	notify = appendBytesField(notify, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, []byte{0x0a, 0x80}) // truncated PlayerId varint
	if _, err := resolver.applyUpdateSceneGroupDataNotify(notify); err == nil {
		t.Fatal("malformed nested player record should fail")
	}
	if len(resolver.Entities) != 0 {
		t.Fatalf("malformed update partially changed runtime identity map: %+v", resolver.Entities)
	}
}

func TestEntityPbCharacterConfigIDResolvesRoleWithUnknownCaptureVersion(t *testing.T) {
	resolver := NewEntityResolver()
	entity := appendVarintField(nil, FieldEntityPbID, 502)          // EntityPb.Id
	entity = appendVarintField(entity, FieldEntityPbConfigID, 1312) // EntityPb.ConfigId / PbDataId
	entity = appendVarintField(entity, FieldEntityPbConfigType, 3)  // EntityConfigType.Proto_Character
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	masters := &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(ResolutionVerified), Status: string(ResolutionVerified)},
	}}
	identity := ResolveEntityIdentity(resolver, masters, 502)
	if !identity.HasCharacter || identity.Character.ID != 1312 || identity.CharacterStatus != ResolutionCorrelated {
		t.Fatalf("EntityPb character config should remain correlated until capture version is known: %+v", identity)
	}
	if !strings.Contains(identity.CharacterProof, "ConfigId(field 2)") || !strings.Contains(identity.CharacterProof, "Proto_Character(3)") {
		t.Fatalf("direct character config provenance missing: %q", identity.CharacterProof)
	}
}

func TestEntityPbNonCharacterConfigIDDoesNotResolveCharacter(t *testing.T) {
	resolver := NewEntityResolver()
	entity := appendVarintField(nil, FieldEntityPbID, 502)
	entity = appendVarintField(entity, FieldEntityPbConfigID, 1312)
	entity = appendVarintField(entity, FieldEntityPbConfigType, 1) // ConfigType is not Proto_Character.
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	identity := ResolveEntityIdentity(resolver, &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Name: "Test Role", Status: string(ResolutionVerified)},
	}}, 502)
	if identity.HasCharacter || identity.CharacterStatus != ResolutionUnknown {
		t.Fatalf("non-character PbDataId was treated as CharacterId: %+v", identity)
	}
}

func TestRoleSkinCharacterJoinRemainsCorrelatedUntilMasterCompatibilityVerified(t *testing.T) {
	resolver := NewEntityResolver()
	entity := appendVarintField(nil, FieldEntityPbID, 503)           // EntityPb.Id
	entity = appendVarintField(entity, fieldEntityPbRoleSkinID, 901) // EntityPb.RoleSkinId
	unknownEntity := appendVarintField(nil, FieldEntityPbID, 504)
	unknownEntity = appendVarintField(unknownEntity, fieldEntityPbRoleSkinID, 902)
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	notify = appendBytesField(notify, FieldEntityAddNotifyEntityPbs, unknownEntity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}

	masters := &GameMaster{charactersSkin: map[uint64]MasterRecord{
		901: {ID: 1312, SkinID: 901, Name: "Test Role", Status: string(ResolutionVerified)},
		902: {ID: 1313, SkinID: 902, Status: string(ResolutionUnknown)},
	}}
	identity := ResolveEntityIdentity(resolver, masters, 503)
	if !identity.HasCharacter || identity.Character.ID != 1312 {
		t.Fatalf("RoleSkinId did not resolve its character master row: %+v", identity)
	}
	if identity.CharacterStatus != ResolutionCorrelated {
		t.Fatalf("unverified master compatibility promoted a versioned skin-to-character join: status=%s identity=%+v", identity.CharacterStatus, identity)
	}
	if !strings.Contains(identity.CharacterProof, "RoleSkinId(23)") || !strings.Contains(identity.CharacterProof, "master compatibility unverified") {
		t.Fatalf("skin proof omitted schema/build limitation: %q", identity.CharacterProof)
	}
	unknown := ResolveEntityIdentity(resolver, masters, 504)
	if !unknown.HasCharacter || unknown.CharacterStatus != ResolutionUnknown {
		t.Fatalf("skin join upgraded an unverified character master: %+v", unknown)
	}
}

func TestEntityPbAndSceneRoleCharacterConflictStaysUnknown(t *testing.T) {
	resolver := NewEntityResolver()
	role := appendVarintField(nil, FieldSceneRoleInformationRoleID, 1312)
	role = appendVarintField(role, FieldSceneRoleInformationEntityID, 502)
	group := appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, role)
	player := appendVarintField(nil, FieldScenePlayerInformationPlayerID, 77)
	player = appendBytesField(player, FieldScenePlayerInformationPlayerSceneGroupData, group)
	entity := appendVarintField(nil, FieldEntityPbID, 502)
	entity = appendVarintField(entity, FieldEntityPbConfigID, 1313)
	entity = appendVarintField(entity, FieldEntityPbConfigType, 3)
	aoi := appendBytesField(nil, FieldPlayerSceneAoiDataEntities, entity) // PlayerSceneAoiData.Entities[]
	sceneInfo := appendBytesField(nil, FieldSceneInformationPlayerInfos, player)
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	identity := ResolveEntityIdentity(resolver, &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Status: string(ResolutionVerified)},
		1313: {ID: 1313, Status: string(ResolutionVerified)},
	}}, 502)
	if identity.HasCharacter || identity.CharacterStatus != ResolutionUnknown || !strings.Contains(identity.CharacterProof, "conflicting character IDs") {
		t.Fatalf("conflicting character initialization paths should remain unknown: %+v", identity)
	}
}

func TestJoinSceneConflictingRoleMappingsStayUnknown(t *testing.T) {
	resolver := NewEntityResolver()
	makePlayer := func(playerID, roleID uint64) []byte {
		role := appendVarintField(nil, FieldSceneRoleInformationRoleID, roleID)
		role = appendVarintField(role, FieldSceneRoleInformationEntityID, 502)
		group := appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, role)
		player := appendVarintField(nil, FieldScenePlayerInformationPlayerID, playerID)
		return appendBytesField(player, FieldScenePlayerInformationPlayerSceneGroupData, group)
	}
	sceneInfo := appendBytesField(nil, FieldSceneInformationPlayerInfos, makePlayer(77, 1312))
	sceneInfo = appendBytesField(sceneInfo, FieldSceneInformationPlayerInfos, makePlayer(88, 1313))
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	entity, _ := resolver.Resolve(502)
	if !entity.RoleConflict || entity.HasRoleID || entity.RoleID != 0 {
		t.Fatalf("conflicting role identities were not retained as ambiguous: %+v", entity)
	}
	identity := ResolveEntityIdentity(resolver, &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Status: string(ResolutionVerified)},
		1313: {ID: 1313, Status: string(ResolutionVerified)},
	}}, 502)
	if identity.HasCharacter || identity.CharacterStatus != ResolutionUnknown {
		t.Fatalf("conflicting RoleIds resolved to a character: %+v", identity)
	}
}

func TestSummonerEntityChainRejectsCyclesAndCharacterConflicts(t *testing.T) {
	makeResolver := func(ids ...uint64) *EntityResolver {
		resolver := NewEntityResolver()
		for i, id := range ids {
			next := ids[(i+1)%len(ids)]
			summoner := appendVarintField(nil, FieldSummonerComponentPbSummonerID, next)
			entity := appendVarintField(nil, FieldEntityPbID, id)
			entity = appendBytesField(entity, FieldEntityPbComponentPbs, appendBytesField(nil, FieldEntityComponentPbSummonerComponent, summoner))
			notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
			if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
				t.Fatal(err)
			}
		}
		return resolver
	}
	cycle := makeResolver(200, 201)
	entity, _ := cycle.Resolve(200)
	if got := ResolveSummonerCharacter(cycle, nil, entity); got.Status != ResolutionUnknown || !strings.Contains(got.Proof, "cycle") {
		t.Fatalf("summoner cycle was not retained as unknown: %+v", got)
	}

	conflicted := NewEntityResolver()
	role := appendVarintField(nil, FieldSceneRoleInformationRoleID, 1312)
	role = appendVarintField(role, FieldSceneRoleInformationEntityID, 100)
	group := appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, role)
	player := appendBytesField(appendVarintField(nil, FieldScenePlayerInformationPlayerID, 77), FieldScenePlayerInformationPlayerSceneGroupData, group)
	pbEntity := appendVarintField(nil, FieldEntityPbID, 100)
	pbEntity = appendVarintField(pbEntity, FieldEntityPbConfigID, 1313)
	pbEntity = appendVarintField(pbEntity, FieldEntityPbConfigType, 3)
	aoi := appendBytesField(nil, FieldPlayerSceneAoiDataEntities, pbEntity)
	sceneInfo := appendBytesField(appendBytesField(nil, FieldSceneInformationPlayerInfos, player), fieldSceneInformationAoiData, aoi)
	if _, err := conflicted.applyJoinSceneNotify(appendBytesField(nil, FieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	summoner := appendVarintField(nil, FieldSummonerComponentPbSummonerID, 100)
	sourceEntity := appendVarintField(nil, FieldEntityPbID, 200)
	sourceEntity = appendBytesField(sourceEntity, FieldEntityPbComponentPbs, appendBytesField(nil, FieldEntityComponentPbSummonerComponent, summoner))
	if _, err := conflicted.ApplyEntityAddNotify(appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, sourceEntity)); err != nil {
		t.Fatal(err)
	}
	source, _ := conflicted.Resolve(200)
	got := ResolveSummonerCharacter(conflicted, &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Status: string(ResolutionVerified)},
		1313: {ID: 1313, Status: string(ResolutionVerified)},
	}}, source)
	if got.HasCharacterID || got.Status != ResolutionUnknown || !strings.Contains(got.Proof, "conflicting character initialization") {
		t.Fatalf("conflicting parent identity was not kept unknown: %+v", got)
	}
}

func TestEntityRemoveClearsMappingAndActiveRequestKeepsUnknownEntity(t *testing.T) {
	resolver := NewEntityResolver()
	if err := resolver.observeActiveRequest(appendVarintField(nil, FieldEntityActiveRequestEntityID, 90)); err != nil {
		t.Fatal(err)
	}
	if got, status := resolver.Resolve(90); status != ResolutionCorrelated || got.EntityID != 90 || got.HasMonsterID {
		t.Fatalf("active request should correlate the raw entity without resolving a character or monster: %+v status=%s", got, status)
	}
	if proof := entityProof(resolver.Entities[90]); proof != "EntityActiveRequest.EntityId; observed only" {
		t.Fatalf("active-request evidence should remain explicit, got %q", proof)
	}
	info := appendVarintField(nil, FieldEntityRemoveInfoEntityID, 90)    // EntityRemoveInfo.EntityId
	remove := appendVarintField(nil, FieldEntityRemoveNotifyIsRemove, 1) // EntityRemoveNotify.IsRemove
	remove = appendBytesField(remove, FieldEntityRemoveNotifyRemoveInfos, info)
	removed, removedIDs, err := resolver.ApplyEntityRemoveNotify(remove)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || len(resolver.Entities) != 0 || len(removedIDs) != 1 || removedIDs[0] != 90 {
		t.Fatalf("remove should clear the runtime mapping and report its ID: removed=%d IDs=%v entities=%d", removed, removedIDs, len(resolver.Entities))
	}
}

func TestEntityIDReuseAfterRemoveStartsFreshCharacterMapping(t *testing.T) {
	resolver := NewEntityResolver()
	makeTeamUpdate := func(roleID uint64) []byte {
		role := appendVarintField(nil, FieldSceneRoleInformationRoleID, roleID) // SceneRoleInformation.RoleId
		role = appendVarintField(role, FieldSceneRoleInformationEntityID, 90)   // SceneRoleInformation.EntityId
		group := appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, role)
		player := appendBytesField(appendVarintField(nil, fieldPlayerSceneGroupDataListPlayerID, 77), fieldPlayerSceneGroupDataListPlayerSceneGroupData, group)
		return appendBytesField(nil, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, player)
	}
	addEntity := func() []byte {
		entity := appendVarintField(nil, FieldEntityPbID, 90) // EntityPb.Id
		return appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	}
	removeEntity := func() []byte {
		info := appendVarintField(nil, FieldEntityRemoveInfoEntityID, 90)    // EntityRemoveInfo.EntityId
		remove := appendVarintField(nil, FieldEntityRemoveNotifyIsRemove, 1) // EntityRemoveNotify.IsRemove
		return appendBytesField(remove, FieldEntityRemoveNotifyRemoveInfos, info)
	}
	masters := &GameMaster{Characters: map[uint64]MasterRecord{
		1312: {ID: 1312, Name: "Old Role", Status: string(ResolutionVerified)},
		1313: {ID: 1313, Name: "New Role", Status: string(ResolutionVerified)},
	}}

	if _, err := resolver.ApplyEntityAddNotify(addEntity()); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeTeamUpdate(1312)); err != nil {
		t.Fatal(err)
	}
	oldIdentity := ResolveEntityIdentity(resolver, masters, 90)
	if !oldIdentity.HasCharacter || oldIdentity.Character.ID != 1312 {
		t.Fatalf("initial entity identity = %+v, want CharacterId 1312", oldIdentity)
	}

	if _, _, err := resolver.ApplyEntityRemoveNotify(removeEntity()); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ApplyEntityAddNotify(addEntity()); err != nil {
		t.Fatal(err)
	}
	newEntity, status := resolver.Resolve(90)
	if status != ResolutionVerified || newEntity.HasRoleID || newEntity.RoleConflict {
		t.Fatalf("reused EntityId retained its prior role evidence: %+v status=%s", newEntity, status)
	}
	newIdentity := ResolveEntityIdentity(resolver, masters, 90)
	if newIdentity.HasCharacter {
		t.Fatalf("reused EntityId resolved to stale CharacterId %d before new mapping", newIdentity.Character.ID)
	}

	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeTeamUpdate(1313)); err != nil {
		t.Fatal(err)
	}
	newIdentity = ResolveEntityIdentity(resolver, masters, 90)
	if !newIdentity.HasCharacter || newIdentity.Character.ID != 1313 || newIdentity.Character.ID == oldIdentity.Character.ID {
		t.Fatalf("reused EntityId did not accept the new lifecycle mapping: %+v", newIdentity)
	}
}

func TestActiveRequestDoesNotDowngradeEntityAddEvidence(t *testing.T) {
	resolver := NewEntityResolver()
	entity := appendVarintField(nil, FieldEntityPbID, 90) // EntityPb.Id
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	if err := resolver.observeActiveRequest(appendVarintField(nil, FieldEntityActiveRequestEntityID, 90)); err != nil {
		t.Fatal(err)
	}
	got, status := resolver.Resolve(90)
	if status != ResolutionVerified || entityProof(got) != "EntityAddNotify.EntityPb.Id" {
		t.Fatalf("active-request observation downgraded EntityAdd evidence: entity=%+v status=%s proof=%q", got, status, entityProof(got))
	}
}

func TestLateEntityAddAndSessionResetKeepEntityIdentityScoped(t *testing.T) {
	resolver := NewEntityResolver()
	if err := resolver.observeActiveRequest(appendVarintField(nil, FieldEntityActiveRequestEntityID, 90)); err != nil {
		t.Fatal(err)
	}
	if _, status := resolver.Resolve(90); status != ResolutionCorrelated {
		t.Fatalf("active request should only correlate the entity, got %s", status)
	}

	entity := appendVarintField(nil, FieldEntityPbID, 90) // EntityPb.Id
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	if got, status := resolver.Resolve(90); status != ResolutionVerified || entityProof(got) != "EntityAddNotify.EntityPb.Id" {
		t.Fatalf("later entity-add evidence should upgrade observation: entity=%+v status=%s", got, status)
	}

	resolver.reset()
	if got, status := resolver.Resolve(90); status != ResolutionUnknown || got.EntityID != 90 {
		t.Fatalf("reused entity ID should be unknown after session reset: entity=%+v status=%s", got, status)
	}
}

func TestSourceRoleRequiresTwoDistinctSkillsAndRejectsConflicts(t *testing.T) {
	resolver := newSourceRoleResolver()
	first := MasterRecord{ID: 1001, RoleID: 1312, NameKind: "SkillName", Status: string(ResolutionVerified)}
	second := MasterRecord{ID: 1002, RoleID: 1312, NameKind: "SkillName", Status: string(ResolutionVerified)}
	resolver.Observe(90, first)
	if _, ok := resolver.CorrelatedRole(90); ok {
		t.Fatal("one skill must not establish a character attribution")
	}
	resolver.Observe(90, second)
	if role, ok := resolver.CorrelatedRole(90); !ok || role != 1312 {
		t.Fatalf("two distinct skills should correlate role 1312, got role=%d ok=%t", role, ok)
	}
	resolver.Observe(90, MasterRecord{ID: 2001, RoleID: 7001, NameKind: "SkillName", Status: string(ResolutionVerified)})
	if _, ok := resolver.CorrelatedRole(90); ok {
		t.Fatal("conflicting role evidence must remain unresolved")
	}
}

func TestPendingAndConsumedRPCWindowsStayBounded(t *testing.T) {
	p := NewProcessor(nil, &bytes.Buffer{})
	for rpcID := uint64(1); rpcID <= maxPendingDamageRequests+10; rpcID++ {
		p.rememberPending(rpcID, PendingDamageRequest{})
	}
	if len(p.Pending) != maxPendingDamageRequests || p.Stats.EvictedPending != 10 {
		t.Fatalf("pending map not bounded: len=%d evicted=%d", len(p.Pending), p.Stats.EvictedPending)
	}
	for rpcID := uint64(1); rpcID <= maxConsumedRPCIDs+10; rpcID++ {
		p.rememberConsumed(rpcID, 0)
	}
	if len(p.consumed) != maxConsumedRPCIDs {
		t.Fatalf("consumed RPC dedupe map not bounded: len=%d", len(p.consumed))
	}
	if _, ok := p.consumed[1]; ok {
		t.Fatal("oldest consumed RPC ID should have aged out")
	}
}

func TestSummonerComponentKeepsRawIDSeparateFromSourceEntity(t *testing.T) {
	resolver := NewEntityResolver()
	summoner := appendVarintField(nil, FieldSummonerComponentPbSummonerID, 77) // SummonerComponentPb.SummonerId
	summoner = appendVarintField(summoner, FieldSummonerComponentPbSummonCfgID, 6001)
	component := appendBytesField(nil, FieldEntityComponentPbSummonerComponent, summoner)
	follower := appendVarintField(nil, fieldPlayerFollowerSelfComponentPbPlayerEntityID, 88) // PlayerFollowerSelfComponentPb.PlayerEntityId
	component = appendBytesField(component, fieldEntityComponentPbPlayerFollowerSelfComponentPb, follower)
	entity := appendVarintField(nil, FieldEntityPbID, 500)
	entity = appendBytesField(entity, FieldEntityPbComponentPbs, component)
	notify := appendBytesField(appendVarintField(nil, FieldEntityAddNotifyIsAdd, 1), FieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	got := resolver.Entities[500]
	if !got.HasSummonerID || got.SummonerID != 77 || !got.HasPlayerEntityID || got.PlayerEntityID != 88 || got.EntityID != 500 {
		t.Fatalf("summoner, player entity, and source IDs should remain distinct: %+v", got)
	}
}
