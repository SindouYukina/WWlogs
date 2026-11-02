package capture

// damageContributorCandidate records a correlated non-character source that can
// be displayed separately while preserving the server-reported attacker entity.
type damageContributorCandidate struct {
	ActorType        string           `json:"actorType"`
	EntityType       string           `json:"entityType"`
	FollowerConfigID uint64           `json:"followerSummonConfigId"`
	BlueprintType    string           `json:"blueprintType"`
	ActorBlueprint   string           `json:"actorBlueprint"`
	SummonSkillID    uint64           `json:"summonSkillId"`
	Status           resolutionStatus `json:"status"`
	// Name is the developer note of the vehicle's own template (a TemplateConfig row); no official localized vehicle name is in the combat master.
	Name     *combatEventLabelTextJSONL `json:"name,omitempty"`
	Evidence string                     `json:"evidence"`
}

func resolveVehicleDamageContributorCandidate(resolver *entityResolver, masters *gameMaster, event combatEvent) *damageContributorCandidate {
	if resolver == nil || masters == nil || event.EventTypeCandidate != "DAMAGE_CANDIDATE" || !event.HasSourceEntity || !event.HasBulletID || event.BulletID <= 0 {
		return nil
	}
	if event.Source.RuntimeStatus != resolutionVerified || event.Source.Runtime.entityID != event.SourceEntityID {
		return nil
	}
	source := event.Source.Runtime
	if !source.hasFollowerSummonConfigID || !source.hasFollowEntityID || source.followEntityID == 0 {
		return nil
	}
	if event.SkillMessageID == 0 {
		return nil
	}
	requestMatched := false
	if request := event.SkillRequest; request != nil {
		if request.SessionNumber != event.SessionNumber || request.MessageID != event.SkillMessageID ||
			request.EntityID != event.SourceEntityID || request.SkillID != event.SkillID || request.RequestType != "UseSkillRequest" {
			return nil
		}
		requestMatched = true
	}

	motorcycle, status := resolver.resolve(source.followEntityID)
	if status != resolutionVerified || !motorcycle.hasSummonerID || motorcycle.summonerID != event.SourceEntityID ||
		!motorcycle.hasSummonType || motorcycle.summonType != summonTypeConcomitantMotorcycle ||
		!motorcycle.hasSummonSkillID || !masters.hasVision(motorcycle.summonSkillID) {
		return nil
	}

	vehicle, ok := masters.vehicleActorForFollowerConfig(source.followerSummonConfigID, uint64(event.SkillID), uint64(event.BulletID))
	if !ok {
		return nil
	}
	var name *combatEventLabelTextJSONL
	if source.hasConfigID && source.hasConfigType && source.configType == 4 {
		if template, ok := masters.templateByID(source.configID); ok {
			name = template.DevLabel
		}
	}
	requestEvidence := "DamageContext.SkillMessageId is present, but no matching outbound UseSkillRequest was resolved; the candidate is correlated from the decoded SkillId and BulletId, verified runtime follower link, and static vehicle actor chain"
	if requestMatched {
		requestEvidence = "the outbound UseSkillRequest exactly matches DamageContext.SkillMessageId and the raw source entity"
	}
	return &damageContributorCandidate{
		Name:             name,
		ActorType:        "Vehicle",
		EntityType:       vehicle.EntityType,
		FollowerConfigID: vehicle.ConfigID,
		BlueprintType:    vehicle.BlueprintType,
		ActorBlueprint:   vehicle.ActorBlueprint,
		SummonSkillID:    motorcycle.summonSkillID,
		Status:           resolutionCorrelated,
		Evidence:         requestEvidence + "; PlayerFollowerSelfComponentPb.FollowerSummonConfigId selects a SummonCfg/BlueprintConfig chain with EntityType=BattleVehicle, and that actor's EntitySkillPreload and BulletPreload list this SkillId and BulletId; the source FollowEntityId resolves in the same session to a Type 5 ConcomitantMotorcycle entity whose SummonerId is the raw source entity. The server-reported AttackerEntityId remains the player entity, so vehicle contribution is CORRELATED and does not replace raw source or character credit",
	}
}
