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
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	u3n := appendVarintField(nil, fieldDamageExecuteRequestDamageID, 70010001)     // DamageId
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestTargetEntityID, 902)     // TargetEntityId
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestSkillLevel, 2)           // SkillLevel
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestAttackerEntityID, 25320) // AttackerEntityId
	context := appendVarintField(nil, fieldDamageContextSourceType, 0)             // SourceType: FromBullet
	context = appendVarintField(context, fieldDamageContextBulletID, 9001)         // BulletId
	context = appendVarintField(context, fieldDamageContextSkillID, 1312011)       // SkillId
	context = appendVarintField(context, fieldDamageContextSkillMessageID, 77)     // SkillMessageId
	u3n = appendBytesField(u3n, fieldDamageExecuteRequestDamageContext, context)   // DamageContext
	request := appendVarintField(nil, fieldCombatRequestDataRequestID, 42)         // RPC ID
	request = appendBytesField(request, fieldCombatRequestDataDamageExecuteRequest, u3n)
	variant := appendBytesField(nil, fieldCombatSendDataRequest, request)
	outer := appendBytesField(nil, fieldCombatSendPackRequestData, variant)

	p.handleDamageRequests(gameFrame{direction: "out", commandID: cmdCombatSendPackRequest}, outer)

	got, ok := p.pending[42]
	if !ok {
		t.Fatal("damage request was not retained")
	}
	if !got.hasAttackerEntityID || got.attackerEntityID != 25320 {
		t.Fatalf("attacker entity ID = %d (present=%t), want 25320", got.attackerEntityID, got.hasAttackerEntityID)
	}
	if !got.hasTargetEntityID || got.targetEntityID != 902 {
		t.Fatalf("target entity ID = %d (present=%t), want 902", got.targetEntityID, got.hasTargetEntityID)
	}
	if got.skillLevel != 2 || got.skillID != 1312011 || got.skillMessageID != 77 || got.damageID != 70010001 || !got.hasDamageSourceType || got.damageSourceType != 0 || !got.hasBulletID || got.bulletID != 9001 {
		t.Fatalf("request context = %+v, want skill, damage, source type and bullet IDs to match schema fields", got)
	}
}

func TestDamageCandidateResultHasSeparateSemanticBucket(t *testing.T) {
	var stdout, diagnostic bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.masters = &gameMaster{damage: map[uint64]damageMasterRecord{
		70010001: {ID: 70010001, CalculateType: 0, SourceHash: strings.Repeat("a", 64), SourceTable: "damage"},
	}}
	p.pending[10] = pendingDamageRequest{damageID: 70010001, hasDamageID: true}
	response := appendVarintField(nil, fieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, fieldDamageExecuteResponseImmuneType, 0)
	result := int32(-42)
	response = appendVarintField(response, fieldDamageExecuteResponseDamage, uint64(uint32(result)))

	p.handleOneDamageResult(10, response)

	if p.stats.matchedCombatResultAbs != 42 || p.stats.decodedCombatResultAbs != 42 || p.stats.includedUnresolvedRelations != 1 || p.stats.damageCandidateEvents != 1 || p.stats.damageCandidateResult != 42 || p.stats.healingCandidateResult != 0 || p.stats.otherCandidateResult != 0 || p.stats.unclassifiedResult != 0 {
		t.Fatalf("damage candidate was not isolated from other result buckets: %+v", p.stats)
	}
}

func TestDamageResponseWithoutSourceUsesUnknown(t *testing.T) {
	var stdout, diagnostic bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.pending[7] = pendingDamageRequest{}
	response := appendVarintField(nil, fieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, fieldDamageExecuteResponseImmuneType, 0)
	response = appendVarintField(response, fieldDamageExecuteResponseDamage, 33)

	p.handleOneDamageResult(7, response)

	// The console line is a development output: the public build writes none, and both count the result as visible.
	if (stdout.Len() != 0) != diagnosticConsole || p.stats.includedUnresolvedRelations != 1 || p.stats.suppressedUnrelatedEvents != 0 {
		t.Fatalf("damage with missing actor was hidden without proof it was unrelated: stdout=%q stats=%+v", stdout.String(), p.stats)
	}
	if p.stats.missingAttackerEntityID != 1 {
		t.Fatalf("missingAttackerEntityID = %d, want 1", p.stats.missingAttackerEntityID)
	}
}

func TestEntityAddCapturesRawIdentityWithoutPromotingSkinToCharacter(t *testing.T) {
	resolver := newEntityResolver()
	monsterCapture := appendVarintField(nil, fieldMonsterCaptureComponentPbTemplateID, 310000010)          // TemplateId
	monsterCapture = appendVarintField(monsterCapture, fieldMonsterCaptureComponentPbMonsterID, 310000020) // MonsterId
	component := appendBytesField(nil, fieldEntityComponentPbMonsterCaptureComponent, monsterCapture)      // EntityComponentPb.MonsterCaptureComponent
	entity := appendVarintField(nil, fieldEntityPbID, 4201)                                                // EntityPb.Id
	entity = appendVarintField(entity, fieldEntityPbConfigID, 692750288)                                   // ConfigId
	entity = appendVarintField(entity, fieldEntityPbConfigType, 1)                                         // ConfigType
	entity = appendVarintField(entity, fieldEntityPbEntityType, 2)                                         // EntityType
	entity = appendVarintField(entity, fieldEntityPbPlayerID, 77)                                          // PlayerId
	entity = appendBytesField(entity, fieldEntityPbComponentPbs, component)                                // ComponentPbs
	entity = appendVarintField(entity, fieldEntityPbRoleSkinID, 901)                                       // RoleSkinId, not CharacterId
	notify := appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1)                                         // IsAdd
	notify = appendBytesField(notify, fieldEntityAddNotifyEntityPbs, entity)

	count, err := resolver.applyEntityAddNotify(notify)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("added entities = %d, want 1", count)
	}
	got, status := resolver.resolve(4201)
	if status != resolutionVerified || !got.hasMonsterID || got.monsterID != 310000020 || !got.hasTemplateID || got.templateID != 310000010 {
		t.Fatalf("monster identity was not preserved from its component: %+v status=%s", got, status)
	}
	if proof := entityProof(got); proof != "EntityAddNotify.EntityPb.Id" {
		t.Fatalf("entity-add evidence should be explicit, got %q", proof)
	}
	if got.hasRoleSkinID != true || got.roleSkinID != 901 {
		t.Fatalf("RoleSkinId was not retained as its own field: %+v", got)
	}
}

func TestMonsterCaptureIdentityRemainsCorrelatedUntilMasterCompatibilityVerified(t *testing.T) {
	resolver := newEntityResolver()
	capture := appendVarintField(nil, fieldMonsterCaptureComponentPbTemplateID, 702)   // MonsterCaptureComponentPb.TemplateId
	capture = appendVarintField(capture, fieldMonsterCaptureComponentPbMonsterID, 701) // MonsterCaptureComponentPb.MonsterId
	component := appendBytesField(nil, fieldEntityComponentPbMonsterCaptureComponent, capture)
	entity := appendVarintField(nil, fieldEntityPbID, 500) // EntityPb.Id
	entity = appendBytesField(entity, fieldEntityPbComponentPbs, component)
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}

	masters := &gameMaster{monsters: map[uint64]masterRecord{
		701: {ID: 701, Name: "Monster", Status: string(resolutionVerified)},
		702: {ID: 702, Name: "Template must not resolve as MonsterId", Status: string(resolutionVerified)},
	}}
	identity := resolveEntityIdentity(resolver, masters, 500)
	if !identity.HasMonster || identity.Monster.ID != 701 {
		t.Fatalf("MonsterCapture MonsterId did not resolve directly: %+v", identity)
	}
	if identity.MonsterStatus != resolutionCorrelated {
		t.Fatalf("unverified master compatibility promoted a versioned Monster join: status=%s identity=%+v", identity.MonsterStatus, identity)
	}
	if !strings.Contains(identity.MonsterProof, "MonsterCaptureComponentPb.MonsterId") || !strings.Contains(identity.MonsterProof, "master compatibility unverified") {
		t.Fatalf("Monster proof omitted schema/build limitation: %q", identity.MonsterProof)
	}
}

func TestJoinSceneSnapshotResolvesBothEntityBranchesAndPreservesOwnerNamespaces(t *testing.T) {
	resolver := newEntityResolver()
	stale := appendVarintField(nil, fieldEntityPbID, 99)
	if _, err := resolver.applyEntityAddNotify(appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, stale)); err != nil {
		t.Fatal(err)
	}

	summonerComponent := appendVarintField(nil, fieldSummonerComponentPbSummonerID, 600) // SummonerComponentPb.SummonerId
	summonerComponent = appendVarintField(summonerComponent, fieldSummonerComponentPbSummonCfgID, 610)
	summonerComponent = appendVarintField(summonerComponent, fieldSummonerComponentPbSummonSkillID, 620)
	summonerComponent = appendVarintField(summonerComponent, fieldSummonerComponentPbPlayerID, 630)
	summonerComponent = appendVarintField(summonerComponent, fieldSummonerComponentPbType, 2)
	monsterComponent := appendVarintField(nil, fieldMonsterCaptureComponentPbTemplateID, 700) // MonsterCaptureComponentPb.TemplateId
	monsterComponent = appendVarintField(monsterComponent, fieldMonsterCaptureComponentPbMonsterID, 701)
	followerComponent := appendVarintField(nil, fieldPlayerFollowerSelfComponentPbPlayerEntityID, 640)
	followerComponent = appendVarintField(followerComponent, fieldPlayerFollowerSelfComponentPbSummonConfigID, 611)
	components := appendBytesField(nil, fieldEntityComponentPbSummonerComponent, summonerComponent)
	components = appendBytesField(components, fieldEntityComponentPbMonsterCaptureComponent, monsterComponent)
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
	for _, component := range [][]byte{appendBytesField(nil, fieldEntityComponentPbSummonerComponent, summonerComponent), appendBytesField(nil, fieldEntityComponentPbMonsterCaptureComponent, monsterComponent), appendBytesField(nil, fieldEntityComponentPbPlayerFollowerSelfComponentPb, followerComponent)} {
		dynamic = appendBytesField(dynamic, fieldDynamicEntityInformationComponentPbs, component)
	}

	entityPb := appendVarintField(nil, fieldEntityPbID, 500)             // EntityPb.Id, same entity as DynamicEntityInformation.Id
	entityPb = appendVarintField(entityPb, fieldEntityPbOwnerIncID, 889) // OwnerIncId is distinct from OwnerId
	entityPb = appendBytesField(entityPb, fieldEntityPbComponentPbs, components)
	entityPb = appendVarintField(entityPb, fieldEntityPbRoleSkinID, 901) // RoleSkinId is not CharacterId

	aoi := appendBytesField(nil, fieldPlayerSceneAoiDataDynamicEntityList, dynamic)
	aoi = appendBytesField(aoi, fieldPlayerSceneAoiDataEntities, entityPb)
	otherAoiDynamic := appendVarintField(nil, fieldDynamicEntityInformationID, 501)
	aoi = appendBytesField(aoi, fieldPlayerSceneAoiDataDynamicEntityList, otherAoiDynamic)
	sceneInfo := appendVarintField(nil, fieldSceneInformationSceneID, 42)         // SceneId
	sceneInfo = appendVarintField(sceneInfo, fieldSceneInformationInstanceID, 43) // InstanceId
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationDynamicEntityList, dynamic)
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	joinScene := appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)

	count, err := resolver.applyJoinSceneNotify(joinScene)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("snapshot unique entity count = %d, want 2", count)
	}
	if _, exists := resolver.entities[99]; exists {
		t.Fatal("scene snapshot retained a stale entity from the prior scene")
	}
	got, status := resolver.resolve(500)
	if status != resolutionVerified || got.sourceMessageID != cmdJoinSceneNotify || got.sourceFieldPath != "JoinSceneNotify.SceneInfo.AoiData.Entities.EntityPb.Id" {
		t.Fatalf("snapshot EntityPb proof/status = %q/%s entity=%+v", got.sourceFieldPath, status, got)
	}
	if got.configID != 900 || !got.hasConfigID || got.entityTypeCode != 4 || got.playerID != 77 || got.ownerID != 88 || got.ownerIncID != 889 {
		t.Fatalf("runtime/config/owner fields were not kept separately: %+v", got)
	}
	if got.summonerID != 600 || got.sceneSummonerID != 800 || got.summonConfigID != 610 || got.sceneSummonConfigID != 710 || got.summonSkillID != 620 || got.sceneSummonSkillID != 720 {
		t.Fatalf("Summoner component and SummonInfo fields were conflated or lost: %+v", got)
	}
	if got.summonerPlayerID != 630 || got.playerEntityID != 640 || got.followerSummonConfigID != 611 || got.monsterID != 701 || got.templateID != 700 || got.roleSkinID != 901 {
		t.Fatalf("component fields were not preserved independently: %+v", got)
	}
	second, status := resolver.resolve(501)
	if status != resolutionVerified || entityProof(second) != "JoinSceneNotify.SceneInfo.AoiData.DynamicEntityList.Id" {
		t.Fatalf("AOI DynamicEntityInformation path not retained: entity=%+v status=%s", second, status)
	}
}

func TestJoinSceneLevelConfigResolvesOnlyUniqueVersionedMonsterCandidate(t *testing.T) {
	resolver := newEntityResolver()
	masters := &gameMaster{
		monsters: map[uint64]masterRecord{
			700: {ID: 700, Name: "candidate monster", Status: string(resolutionVerified)},
		},
		sceneIdentity: &sceneIdentityMaster{
			instanceMaps:  map[uint64]uint64{43: 100},
			levelMonsters: map[sceneLevelKey]uint64{{mapID: 100, entityID: 900}: 700},
		},
	}
	entity := appendVarintField(nil, fieldEntityPbID, 500)         // EntityPb.Id is the runtime CreatureDataId.
	entity = appendVarintField(entity, fieldEntityPbConfigID, 900) // EntityPb.ConfigId / PbDataId.
	entity = appendVarintField(entity, fieldEntityPbConfigType, 1) // Proto_Level; other config namespaces must not use this crosswalk.
	aoi := appendBytesField(nil, fieldPlayerSceneAoiDataEntities, entity)
	sceneInfo := appendVarintField(nil, fieldSceneInformationInstanceID, 43) // InstanceId, resolved through same-version InstanceDungeon data.
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	identity := resolveEntityIdentity(resolver, masters, 500)
	if !identity.HasMonster || identity.Monster.ID != 700 || identity.MonsterStatus != resolutionCorrelated {
		t.Fatalf("same-scene unique map-scoped candidate = %+v", identity)
	}
	if !identity.Runtime.hasSceneInstanceID || identity.Runtime.sceneInstanceID != 43 {
		t.Fatalf("runtime audit entity lost its InstanceId scope: %+v", identity.Runtime)
	}
	if !strings.Contains(identity.MonsterProof, "InstanceId=43 -> InstanceDungeon.MapConfigId") {
		t.Fatalf("monster proof omitted scene map derivation: %q", identity.MonsterProof)
	}

	// ConfigType and map scope are required; matching ConfigId alone is insufficient.
	entity = appendVarintField(nil, fieldEntityPbID, 501)
	entity = appendVarintField(entity, fieldEntityPbConfigID, 900)
	entity = appendVarintField(entity, fieldEntityPbConfigType, 2)
	aoi = appendBytesField(nil, fieldPlayerSceneAoiDataEntities, entity)
	sceneInfo = appendVarintField(nil, fieldSceneInformationInstanceID, 43)
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	identity = resolveEntityIdentity(resolver, masters, 501)
	if identity.HasMonster || identity.MonsterStatus != resolutionUnknown {
		t.Fatalf("non-Level ConfigType was incorrectly mapped: %+v", identity)
	}
	resolver.reset()
	if resolver.hasSceneInstanceID {
		t.Fatal("scene reset retained InstanceId scope")
	}
}

func TestJoinSceneSnapshotParseFailureKeepsPriorResolverState(t *testing.T) {
	resolver := newEntityResolver()
	entity := appendVarintField(nil, fieldEntityPbID, 99)
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	malformedDynamic := []byte{0x0a, 0x80} // truncated varint in DynamicEntityInformation
	sceneInfo := appendBytesField(nil, fieldSceneInformationDynamicEntityList, malformedDynamic)
	joinScene := appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)
	if _, err := resolver.applyJoinSceneNotify(joinScene); err == nil {
		t.Fatal("malformed scene entity should fail decoding")
	}
	if _, exists := resolver.entities[99]; !exists {
		t.Fatal("failed scene snapshot replaced the prior resolver state")
	}
}

func TestJoinScenePlayerFormationMapsCreatureDataIDToRoleID(t *testing.T) {
	resolver := newEntityResolver()
	role := appendVarintField(nil, fieldSceneRoleInformationRoleID, 1312)           // SceneRoleInformation.RoleId
	role = appendVarintField(role, fieldSceneRoleInformationEntityID, 502)          // SceneRoleInformation.EntityId
	role = appendVarintField(role, fieldSceneRoleInformationOnStageOutOfControl, 1) // OnStageOutOfControl
	group := appendBytesField(nil, fieldPlayerSceneGroupDataFightRoleInfos, role)   // PlayerSceneGroupData.FightRoleInfos[]
	player := appendVarintField(nil, fieldScenePlayerInformationPlayerID, 77)       // ScenePlayerInformation.PlayerId
	player = appendBytesField(player, fieldScenePlayerInformationPlayerSceneGroupData, group)
	sceneInfo := appendBytesField(nil, fieldSceneInformationPlayerInfos, player) // SceneInformation.PlayerInfos[]
	joinScene := appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)

	count, err := resolver.applyJoinSceneNotify(joinScene)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("snapshot unique entity count = %d, want 1", count)
	}
	entity, status := resolver.resolve(502)
	if status != resolutionVerified || !entity.hasRoleID || entity.roleID != 1312 || !entity.hasPlayerID || entity.playerID != 77 {
		t.Fatalf("scene role identity = %+v status=%s", entity, status)
	}
	if entity.hasOwnerID || entity.hasOwnerIncID || entity.hasSummonerID {
		t.Fatalf("player formation membership was conflated with owner/summoner: %+v", entity)
	}
	if entity.roleSourceFieldPath != "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId" {
		t.Fatalf("role mapping provenance = %q", entity.roleSourceFieldPath)
	}

	masters := &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(resolutionVerified), Status: string(resolutionVerified)},
	}}
	identity := resolveEntityIdentity(resolver, masters, 502)
	if !identity.HasCharacter || identity.Character.ID != 1312 || identity.CharacterStatus != resolutionVerified {
		t.Fatalf("direct scene RoleId did not resolve CharacterId: %+v", identity)
	}
}

func TestUpdateSceneGroupDataMapsEntityIDToRoleID(t *testing.T) {
	resolver := newEntityResolver()
	role := appendVarintField(nil, fieldSceneRoleInformationRoleID, 1312)           // SceneRoleInformation.RoleId
	role = appendVarintField(role, fieldSceneRoleInformationEntityID, 502)          // SceneRoleInformation.EntityId
	role = appendVarintField(role, fieldSceneRoleInformationOnStageOutOfControl, 1) // SceneRoleInformation.OnStageOutOfControl
	group := appendVarintField(nil, fieldPlayerSceneGroupDataGroup, 1)              // PlayerSceneGroupData.Group
	group = appendBytesField(group, fieldPlayerSceneGroupDataFightRoleInfos, role)
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
	entity, status := resolver.resolve(502)
	if status != resolutionVerified || !entity.hasRoleID || entity.roleID != 1312 || !entity.hasPlayerID || entity.playerID != 77 {
		t.Fatalf("scene-group identity = %+v status=%s", entity, status)
	}
	if got := entityProof(entity); got != "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].EntityId" {
		t.Fatalf("runtime entity proof = %q", got)
	}
	identity := resolveEntityIdentity(resolver, &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(resolutionVerified), Status: string(resolutionVerified)},
	}}, 502)
	if !identity.HasCharacter || identity.Character.ID != 1312 || identity.CharacterStatus != resolutionVerified {
		t.Fatalf("direct SceneRoleInformation mapping did not resolve the character: %+v", identity)
	}
}

func TestUpdateSceneGroupDataPreservesLifecycleAndRejectsRoleConflicts(t *testing.T) {
	resolver := newEntityResolver()
	entityPb := appendVarintField(nil, fieldEntityPbID, 502) // EntityPb.Id
	entityPb = appendVarintField(entityPb, fieldEntityPbConfigID, 1312)
	entityPb = appendVarintField(entityPb, fieldEntityPbConfigType, 3)
	if _, err := resolver.applyEntityAddNotify(appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entityPb)); err != nil {
		t.Fatal(err)
	}
	makeUpdate := func(roleID uint64) []byte {
		role := appendVarintField(nil, fieldSceneRoleInformationRoleID, roleID)
		role = appendVarintField(role, fieldSceneRoleInformationEntityID, 502)
		group := appendBytesField(nil, fieldPlayerSceneGroupDataFightRoleInfos, role)
		player := appendBytesField(appendVarintField(nil, fieldPlayerSceneGroupDataListPlayerID, 77), fieldPlayerSceneGroupDataListPlayerSceneGroupData, group)
		return appendBytesField(nil, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, player)
	}
	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeUpdate(1312)); err != nil {
		t.Fatal(err)
	}
	if entity, status := resolver.resolve(502); status != resolutionVerified || entity.sourceMessageID != cmdEntityAddNotify || entity.roleID != 1312 {
		t.Fatalf("team update downgraded EntityAdd lifecycle evidence: %+v status=%s", entity, status)
	}
	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeUpdate(1313)); err != nil {
		t.Fatal(err)
	}
	entity, _ := resolver.resolve(502)
	if !entity.roleConflict || entity.hasRoleID {
		t.Fatalf("conflicting role evidence was not retained as unresolved: %+v", entity)
	}
}

func TestMalformedSceneGroupUpdateDoesNotPartiallyMutateResolver(t *testing.T) {
	resolver := newEntityResolver()
	validRole := appendVarintField(nil, fieldSceneRoleInformationRoleID, 1312)
	validRole = appendVarintField(validRole, fieldSceneRoleInformationEntityID, 502)
	validGroup := appendBytesField(nil, fieldPlayerSceneGroupDataFightRoleInfos, validRole)
	validPlayer := appendBytesField(appendVarintField(nil, fieldPlayerSceneGroupDataListPlayerID, 77), fieldPlayerSceneGroupDataListPlayerSceneGroupData, validGroup)
	notify := appendBytesField(nil, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, validPlayer)
	notify = appendBytesField(notify, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, []byte{0x0a, 0x80}) // truncated PlayerId varint
	if _, err := resolver.applyUpdateSceneGroupDataNotify(notify); err == nil {
		t.Fatal("malformed nested player record should fail")
	}
	if len(resolver.entities) != 0 {
		t.Fatalf("malformed update partially changed runtime identity map: %+v", resolver.entities)
	}
}

func TestEntityPbCharacterConfigIDResolvesRoleWithUnknownCaptureVersion(t *testing.T) {
	resolver := newEntityResolver()
	entity := appendVarintField(nil, fieldEntityPbID, 502)          // EntityPb.Id
	entity = appendVarintField(entity, fieldEntityPbConfigID, 1312) // EntityPb.ConfigId / PbDataId
	entity = appendVarintField(entity, fieldEntityPbConfigType, 3)  // EntityConfigType.Proto_Character
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	masters := &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(resolutionVerified), Status: string(resolutionVerified)},
	}}
	identity := resolveEntityIdentity(resolver, masters, 502)
	if !identity.HasCharacter || identity.Character.ID != 1312 || identity.CharacterStatus != resolutionCorrelated {
		t.Fatalf("EntityPb character config should remain correlated until capture version is known: %+v", identity)
	}
	if !strings.Contains(identity.CharacterProof, "ConfigId(field 2)") || !strings.Contains(identity.CharacterProof, "Proto_Character(3)") {
		t.Fatalf("direct character config provenance missing: %q", identity.CharacterProof)
	}
}

func TestEntityPbNonCharacterConfigIDDoesNotResolveCharacter(t *testing.T) {
	resolver := newEntityResolver()
	entity := appendVarintField(nil, fieldEntityPbID, 502)
	entity = appendVarintField(entity, fieldEntityPbConfigID, 1312)
	entity = appendVarintField(entity, fieldEntityPbConfigType, 1) // ConfigType is not Proto_Character.
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	identity := resolveEntityIdentity(resolver, &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Name: "Test Role", Status: string(resolutionVerified)},
	}}, 502)
	if identity.HasCharacter || identity.CharacterStatus != resolutionUnknown {
		t.Fatalf("non-character PbDataId was treated as CharacterId: %+v", identity)
	}
}

func TestRoleSkinCharacterJoinRemainsCorrelatedUntilMasterCompatibilityVerified(t *testing.T) {
	resolver := newEntityResolver()
	entity := appendVarintField(nil, fieldEntityPbID, 503)           // EntityPb.Id
	entity = appendVarintField(entity, fieldEntityPbRoleSkinID, 901) // EntityPb.RoleSkinId
	unknownEntity := appendVarintField(nil, fieldEntityPbID, 504)
	unknownEntity = appendVarintField(unknownEntity, fieldEntityPbRoleSkinID, 902)
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	notify = appendBytesField(notify, fieldEntityAddNotifyEntityPbs, unknownEntity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}

	masters := &gameMaster{charactersSkin: map[uint64]masterRecord{
		901: {ID: 1312, SkinID: 901, Name: "Test Role", Status: string(resolutionVerified)},
		902: {ID: 1313, SkinID: 902, Status: string(resolutionUnknown)},
	}}
	identity := resolveEntityIdentity(resolver, masters, 503)
	if !identity.HasCharacter || identity.Character.ID != 1312 {
		t.Fatalf("RoleSkinId did not resolve its character master row: %+v", identity)
	}
	if identity.CharacterStatus != resolutionCorrelated {
		t.Fatalf("unverified master compatibility promoted a versioned skin-to-character join: status=%s identity=%+v", identity.CharacterStatus, identity)
	}
	if !strings.Contains(identity.CharacterProof, "RoleSkinId(23)") || !strings.Contains(identity.CharacterProof, "master compatibility unverified") {
		t.Fatalf("skin proof omitted schema/build limitation: %q", identity.CharacterProof)
	}
	unknown := resolveEntityIdentity(resolver, masters, 504)
	if !unknown.HasCharacter || unknown.CharacterStatus != resolutionUnknown {
		t.Fatalf("skin join upgraded an unverified character master: %+v", unknown)
	}
}

func TestEntityPbAndSceneRoleCharacterConflictStaysUnknown(t *testing.T) {
	resolver := newEntityResolver()
	role := appendVarintField(nil, fieldSceneRoleInformationRoleID, 1312)
	role = appendVarintField(role, fieldSceneRoleInformationEntityID, 502)
	group := appendBytesField(nil, fieldPlayerSceneGroupDataFightRoleInfos, role)
	player := appendVarintField(nil, fieldScenePlayerInformationPlayerID, 77)
	player = appendBytesField(player, fieldScenePlayerInformationPlayerSceneGroupData, group)
	entity := appendVarintField(nil, fieldEntityPbID, 502)
	entity = appendVarintField(entity, fieldEntityPbConfigID, 1313)
	entity = appendVarintField(entity, fieldEntityPbConfigType, 3)
	aoi := appendBytesField(nil, fieldPlayerSceneAoiDataEntities, entity) // PlayerSceneAoiData.Entities[]
	sceneInfo := appendBytesField(nil, fieldSceneInformationPlayerInfos, player)
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationAoiData, aoi)
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	identity := resolveEntityIdentity(resolver, &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Status: string(resolutionVerified)},
		1313: {ID: 1313, Status: string(resolutionVerified)},
	}}, 502)
	if identity.HasCharacter || identity.CharacterStatus != resolutionUnknown || !strings.Contains(identity.CharacterProof, "conflicting character IDs") {
		t.Fatalf("conflicting character initialization paths should remain unknown: %+v", identity)
	}
}

func TestJoinSceneConflictingRoleMappingsStayUnknown(t *testing.T) {
	resolver := newEntityResolver()
	makePlayer := func(playerID, roleID uint64) []byte {
		role := appendVarintField(nil, fieldSceneRoleInformationRoleID, roleID)
		role = appendVarintField(role, fieldSceneRoleInformationEntityID, 502)
		group := appendBytesField(nil, fieldPlayerSceneGroupDataFightRoleInfos, role)
		player := appendVarintField(nil, fieldScenePlayerInformationPlayerID, playerID)
		return appendBytesField(player, fieldScenePlayerInformationPlayerSceneGroupData, group)
	}
	sceneInfo := appendBytesField(nil, fieldSceneInformationPlayerInfos, makePlayer(77, 1312))
	sceneInfo = appendBytesField(sceneInfo, fieldSceneInformationPlayerInfos, makePlayer(88, 1313))
	if _, err := resolver.applyJoinSceneNotify(appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	entity, _ := resolver.resolve(502)
	if !entity.roleConflict || entity.hasRoleID || entity.roleID != 0 {
		t.Fatalf("conflicting role identities were not retained as ambiguous: %+v", entity)
	}
	identity := resolveEntityIdentity(resolver, &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Status: string(resolutionVerified)},
		1313: {ID: 1313, Status: string(resolutionVerified)},
	}}, 502)
	if identity.HasCharacter || identity.CharacterStatus != resolutionUnknown {
		t.Fatalf("conflicting RoleIds resolved to a character: %+v", identity)
	}
}

func TestSummonerEntityChainRejectsCyclesAndCharacterConflicts(t *testing.T) {
	makeResolver := func(ids ...uint64) *entityResolver {
		resolver := newEntityResolver()
		for i, id := range ids {
			next := ids[(i+1)%len(ids)]
			summoner := appendVarintField(nil, fieldSummonerComponentPbSummonerID, next)
			entity := appendVarintField(nil, fieldEntityPbID, id)
			entity = appendBytesField(entity, fieldEntityPbComponentPbs, appendBytesField(nil, fieldEntityComponentPbSummonerComponent, summoner))
			notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
			if _, err := resolver.applyEntityAddNotify(notify); err != nil {
				t.Fatal(err)
			}
		}
		return resolver
	}
	cycle := makeResolver(200, 201)
	entity, _ := cycle.resolve(200)
	if got := resolveSummonerCharacter(cycle, nil, entity); got.Status != resolutionUnknown || !strings.Contains(got.Proof, "cycle") {
		t.Fatalf("summoner cycle was not retained as unknown: %+v", got)
	}

	conflicted := newEntityResolver()
	role := appendVarintField(nil, fieldSceneRoleInformationRoleID, 1312)
	role = appendVarintField(role, fieldSceneRoleInformationEntityID, 100)
	group := appendBytesField(nil, fieldPlayerSceneGroupDataFightRoleInfos, role)
	player := appendBytesField(appendVarintField(nil, fieldScenePlayerInformationPlayerID, 77), fieldScenePlayerInformationPlayerSceneGroupData, group)
	pbEntity := appendVarintField(nil, fieldEntityPbID, 100)
	pbEntity = appendVarintField(pbEntity, fieldEntityPbConfigID, 1313)
	pbEntity = appendVarintField(pbEntity, fieldEntityPbConfigType, 3)
	aoi := appendBytesField(nil, fieldPlayerSceneAoiDataEntities, pbEntity)
	sceneInfo := appendBytesField(appendBytesField(nil, fieldSceneInformationPlayerInfos, player), fieldSceneInformationAoiData, aoi)
	if _, err := conflicted.applyJoinSceneNotify(appendBytesField(nil, fieldJoinSceneNotifySceneInfo, sceneInfo)); err != nil {
		t.Fatal(err)
	}
	summoner := appendVarintField(nil, fieldSummonerComponentPbSummonerID, 100)
	sourceEntity := appendVarintField(nil, fieldEntityPbID, 200)
	sourceEntity = appendBytesField(sourceEntity, fieldEntityPbComponentPbs, appendBytesField(nil, fieldEntityComponentPbSummonerComponent, summoner))
	if _, err := conflicted.applyEntityAddNotify(appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, sourceEntity)); err != nil {
		t.Fatal(err)
	}
	source, _ := conflicted.resolve(200)
	got := resolveSummonerCharacter(conflicted, &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Status: string(resolutionVerified)},
		1313: {ID: 1313, Status: string(resolutionVerified)},
	}}, source)
	if got.HasCharacterID || got.Status != resolutionUnknown || !strings.Contains(got.Proof, "conflicting character initialization") {
		t.Fatalf("conflicting parent identity was not kept unknown: %+v", got)
	}
}

func TestEntityRemoveClearsMappingAndActiveRequestKeepsUnknownEntity(t *testing.T) {
	resolver := newEntityResolver()
	if err := resolver.observeActiveRequest(appendVarintField(nil, fieldEntityActiveRequestEntityID, 90)); err != nil {
		t.Fatal(err)
	}
	if got, status := resolver.resolve(90); status != resolutionCorrelated || got.entityID != 90 || got.hasMonsterID {
		t.Fatalf("active request should correlate the raw entity without resolving a character or monster: %+v status=%s", got, status)
	}
	if proof := entityProof(resolver.entities[90]); proof != "EntityActiveRequest.EntityId; observed only" {
		t.Fatalf("active-request evidence should remain explicit, got %q", proof)
	}
	info := appendVarintField(nil, fieldEntityRemoveInfoEntityID, 90)    // EntityRemoveInfo.EntityId
	remove := appendVarintField(nil, fieldEntityRemoveNotifyIsRemove, 1) // EntityRemoveNotify.IsRemove
	remove = appendBytesField(remove, fieldEntityRemoveNotifyRemoveInfos, info)
	removed, removedIDs, err := resolver.applyEntityRemoveNotify(remove)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || len(resolver.entities) != 0 || len(removedIDs) != 1 || removedIDs[0] != 90 {
		t.Fatalf("remove should clear the runtime mapping and report its ID: removed=%d IDs=%v entities=%d", removed, removedIDs, len(resolver.entities))
	}
}

func TestEntityIDReuseAfterRemoveStartsFreshCharacterMapping(t *testing.T) {
	resolver := newEntityResolver()
	makeTeamUpdate := func(roleID uint64) []byte {
		role := appendVarintField(nil, fieldSceneRoleInformationRoleID, roleID) // SceneRoleInformation.RoleId
		role = appendVarintField(role, fieldSceneRoleInformationEntityID, 90)   // SceneRoleInformation.EntityId
		group := appendBytesField(nil, fieldPlayerSceneGroupDataFightRoleInfos, role)
		player := appendBytesField(appendVarintField(nil, fieldPlayerSceneGroupDataListPlayerID, 77), fieldPlayerSceneGroupDataListPlayerSceneGroupData, group)
		return appendBytesField(nil, fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList, player)
	}
	addEntity := func() []byte {
		entity := appendVarintField(nil, fieldEntityPbID, 90) // EntityPb.Id
		return appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	}
	removeEntity := func() []byte {
		info := appendVarintField(nil, fieldEntityRemoveInfoEntityID, 90)    // EntityRemoveInfo.EntityId
		remove := appendVarintField(nil, fieldEntityRemoveNotifyIsRemove, 1) // EntityRemoveNotify.IsRemove
		return appendBytesField(remove, fieldEntityRemoveNotifyRemoveInfos, info)
	}
	masters := &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Name: "Old Role", Status: string(resolutionVerified)},
		1313: {ID: 1313, Name: "New Role", Status: string(resolutionVerified)},
	}}

	if _, err := resolver.applyEntityAddNotify(addEntity()); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeTeamUpdate(1312)); err != nil {
		t.Fatal(err)
	}
	oldIdentity := resolveEntityIdentity(resolver, masters, 90)
	if !oldIdentity.HasCharacter || oldIdentity.Character.ID != 1312 {
		t.Fatalf("initial entity identity = %+v, want CharacterId 1312", oldIdentity)
	}

	if _, _, err := resolver.applyEntityRemoveNotify(removeEntity()); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.applyEntityAddNotify(addEntity()); err != nil {
		t.Fatal(err)
	}
	newEntity, status := resolver.resolve(90)
	if status != resolutionVerified || newEntity.hasRoleID || newEntity.roleConflict {
		t.Fatalf("reused EntityId retained its prior role evidence: %+v status=%s", newEntity, status)
	}
	newIdentity := resolveEntityIdentity(resolver, masters, 90)
	if newIdentity.HasCharacter {
		t.Fatalf("reused EntityId resolved to stale CharacterId %d before new mapping", newIdentity.Character.ID)
	}

	if _, err := resolver.applyUpdateSceneGroupDataNotify(makeTeamUpdate(1313)); err != nil {
		t.Fatal(err)
	}
	newIdentity = resolveEntityIdentity(resolver, masters, 90)
	if !newIdentity.HasCharacter || newIdentity.Character.ID != 1313 || newIdentity.Character.ID == oldIdentity.Character.ID {
		t.Fatalf("reused EntityId did not accept the new lifecycle mapping: %+v", newIdentity)
	}
}

func TestActiveRequestDoesNotDowngradeEntityAddEvidence(t *testing.T) {
	resolver := newEntityResolver()
	entity := appendVarintField(nil, fieldEntityPbID, 90) // EntityPb.Id
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	if err := resolver.observeActiveRequest(appendVarintField(nil, fieldEntityActiveRequestEntityID, 90)); err != nil {
		t.Fatal(err)
	}
	got, status := resolver.resolve(90)
	if status != resolutionVerified || entityProof(got) != "EntityAddNotify.EntityPb.Id" {
		t.Fatalf("active-request observation downgraded EntityAdd evidence: entity=%+v status=%s proof=%q", got, status, entityProof(got))
	}
}

func TestLateEntityAddAndSessionResetKeepEntityIdentityScoped(t *testing.T) {
	resolver := newEntityResolver()
	if err := resolver.observeActiveRequest(appendVarintField(nil, fieldEntityActiveRequestEntityID, 90)); err != nil {
		t.Fatal(err)
	}
	if _, status := resolver.resolve(90); status != resolutionCorrelated {
		t.Fatalf("active request should only correlate the entity, got %s", status)
	}

	entity := appendVarintField(nil, fieldEntityPbID, 90) // EntityPb.Id
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	if got, status := resolver.resolve(90); status != resolutionVerified || entityProof(got) != "EntityAddNotify.EntityPb.Id" {
		t.Fatalf("later entity-add evidence should upgrade observation: entity=%+v status=%s", got, status)
	}

	resolver.reset()
	if got, status := resolver.resolve(90); status != resolutionUnknown || got.entityID != 90 {
		t.Fatalf("reused entity ID should be unknown after session reset: entity=%+v status=%s", got, status)
	}
}

func TestSourceRoleRequiresTwoDistinctSkillsAndRejectsConflicts(t *testing.T) {
	resolver := newSourceRoleResolver()
	first := masterRecord{ID: 1001, RoleID: 1312, NameKind: "SkillName", Status: string(resolutionVerified)}
	second := masterRecord{ID: 1002, RoleID: 1312, NameKind: "SkillName", Status: string(resolutionVerified)}
	resolver.observe(90, first)
	if _, ok := resolver.correlatedRole(90); ok {
		t.Fatal("one skill must not establish a character attribution")
	}
	resolver.observe(90, second)
	if role, ok := resolver.correlatedRole(90); !ok || role != 1312 {
		t.Fatalf("two distinct skills should correlate role 1312, got role=%d ok=%t", role, ok)
	}
	resolver.observe(90, masterRecord{ID: 2001, RoleID: 7001, NameKind: "SkillName", Status: string(resolutionVerified)})
	if _, ok := resolver.correlatedRole(90); ok {
		t.Fatal("conflicting role evidence must remain unresolved")
	}
}

func TestPendingAndConsumedRPCWindowsStayBounded(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	for rpcID := uint64(1); rpcID <= maxPendingDamageRequests+10; rpcID++ {
		p.rememberPending(rpcID, pendingDamageRequest{})
	}
	if len(p.pending) != maxPendingDamageRequests || p.stats.evictedPending != 10 {
		t.Fatalf("pending map not bounded: len=%d evicted=%d", len(p.pending), p.stats.evictedPending)
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
	resolver := newEntityResolver()
	summoner := appendVarintField(nil, fieldSummonerComponentPbSummonerID, 77) // SummonerComponentPb.SummonerId
	summoner = appendVarintField(summoner, fieldSummonerComponentPbSummonCfgID, 6001)
	component := appendBytesField(nil, fieldEntityComponentPbSummonerComponent, summoner)
	follower := appendVarintField(nil, fieldPlayerFollowerSelfComponentPbPlayerEntityID, 88) // PlayerFollowerSelfComponentPb.PlayerEntityId
	component = appendBytesField(component, fieldEntityComponentPbPlayerFollowerSelfComponentPb, follower)
	entity := appendVarintField(nil, fieldEntityPbID, 500)
	entity = appendBytesField(entity, fieldEntityPbComponentPbs, component)
	notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
	if _, err := resolver.applyEntityAddNotify(notify); err != nil {
		t.Fatal(err)
	}
	got := resolver.entities[500]
	if !got.hasSummonerID || got.summonerID != 77 || !got.hasPlayerEntityID || got.playerEntityID != 88 || got.entityID != 500 {
		t.Fatalf("summoner, player entity, and source IDs should remain distinct: %+v", got)
	}
}
