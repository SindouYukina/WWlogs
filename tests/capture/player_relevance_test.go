package capture

func addTestRuntimeEntity(p *processor, entityID uint64, update func(*runtimeEntity)) {
	entity := runtimeEntity{entityID: entityID, sourceMessageID: 25185, sourceFieldPath: "EntityAddNotify.EntityPb.Id"}
	if update != nil {
		update(&entity)
	}
	p.entities.entities[entityID] = entity
	p.entities.observeLifecycleAdd(entity)
}

func addTestPlayerRoleGroupEntity(p *processor, entityID, playerID, roleID uint64, update ...bool) {
	roleSourceFieldPath := "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
	sourceMessageID := uint16(18559)
	if len(update) > 0 && update[0] {
		roleSourceFieldPath = "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
		sourceMessageID = 17286
	}
	entity := runtimeEntity{
		entityID: entityID, playerID: playerID, hasPlayerID: true,
		roleID: roleID, hasRoleID: true,
		roleSourceFieldPath: roleSourceFieldPath,
		sourceMessageID:     sourceMessageID,
	}
	p.entities.entities[entityID] = entity
	p.entities.observeLifecycleAdd(entity)
}

func testDamageEvent(p *processor, sourceID, targetID uint64) combatEvent {
	source := resolveEntityIdentity(p.entities, p.masters, sourceID)
	target := resolveEntityIdentity(p.entities, p.masters, targetID)
	return combatEvent{
		SourceEntityID: sourceID, HasSourceEntity: sourceID != 0, Source: source,
		TargetEntityID: targetID, HasTargetEntity: targetID != 0, Target: target,
		Damage: 10, CharacterStatus: resolutionUnknown, SkillStatus: resolutionUnknown,
		EventTypeStatus: resolutionUnknown,
	}
}
