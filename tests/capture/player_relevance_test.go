package capture

func addTestRuntimeEntity(p *Processor, entityID uint64, update func(*RuntimeEntity)) {
	entity := RuntimeEntity{EntityID: entityID, SourceMessageID: CmdEntityAddNotify, SourceFieldPath: "EntityAddNotify.EntityPb.Id"}
	if update != nil {
		update(&entity)
	}
	p.Entities.Entities[entityID] = entity
	p.Entities.ObserveLifecycleAdd(entity)
}

func addTestPlayerRoleGroupEntity(p *Processor, entityID, playerID, roleID uint64, update ...bool) {
	roleSourceFieldPath := "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
	sourceMessageID := uint16(CmdJoinSceneNotify)
	if len(update) > 0 && update[0] {
		roleSourceFieldPath = "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
		sourceMessageID = CmdUpdateSceneGroupDataNotify
	}
	entity := RuntimeEntity{
		EntityID: entityID, PlayerID: playerID, HasPlayerID: true,
		RoleID: roleID, HasRoleID: true,
		RoleSourceFieldPath: roleSourceFieldPath,
		SourceMessageID:     sourceMessageID,
	}
	p.Entities.Entities[entityID] = entity
	p.Entities.ObserveLifecycleAdd(entity)
}

func testDamageEvent(p *Processor, sourceID, targetID uint64) CombatEvent {
	source := ResolveEntityIdentity(p.Entities, p.Masters, sourceID)
	target := ResolveEntityIdentity(p.Entities, p.Masters, targetID)
	return CombatEvent{
		SourceEntityID: sourceID, HasSourceEntity: sourceID != 0, Source: source,
		TargetEntityID: targetID, HasTargetEntity: targetID != 0, Target: target,
		Damage: 10, CharacterStatus: ResolutionUnknown, SkillStatus: ResolutionUnknown,
		EventTypeStatus: ResolutionUnknown,
	}
}
