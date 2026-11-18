package capture

const VehicleKindMotorcycle = "motorcycle"

// DamageContributorCandidate records a correlated non-character source while preserving the raw attacker.
type DamageContributorCandidate struct {
	Kind             string           `json:"kind"`
	ActorType        string           `json:"actorType"`
	EntityType       string           `json:"entityType"`
	FollowerConfigID uint64           `json:"followerSummonConfigId"`
	BlueprintType    string           `json:"blueprintType"`
	ActorBlueprint   string           `json:"actorBlueprint"`
	SummonSkillID    uint64           `json:"summonSkillId"`
	Status           ResolutionStatus `json:"status"`
	// Name is the developer note of the vehicle's own template (a TemplateConfig row); no official localized vehicle name is in the combat master.
	Name     *CombatEventLabelTextJSONL `json:"name,omitempty"`
	Evidence string                     `json:"evidence"`
}

func ResolveVehicleDamageContributorCandidate(resolver *EntityResolver, masters *GameMaster, event CombatEvent) *DamageContributorCandidate {
	if resolver == nil || masters == nil || event.EventTypeCandidate != "DAMAGE_CANDIDATE" || !event.HasSourceEntity || !event.HasBulletID || event.BulletID <= 0 {
		return nil
	}
	if event.Source.RuntimeStatus != ResolutionVerified || event.Source.Runtime.EntityID != event.SourceEntityID {
		return nil
	}
	source := event.Source.Runtime
	if !source.HasFollowerSummonConfigID || !source.HasFollowEntityID || source.FollowEntityID == 0 {
		return nil
	}
	if event.SkillMessageID == 0 {
		return nil
	}
	// The outbound UseSkillRequest is decoded only by the research schema decoder (-protocol-schema) and is not core
	// evidence: the candidate is decided the same way with and without that research input, as in a public build.

	motorcycle, status := resolver.Resolve(source.FollowEntityID)
	if status != ResolutionVerified || !motorcycle.HasSummonerID || motorcycle.SummonerID != event.SourceEntityID ||
		!motorcycle.HasSummonType || motorcycle.SummonType != SummonTypeConcomitantMotorcycle ||
		!motorcycle.HasSummonSkillID || !masters.hasVision(motorcycle.SummonSkillID) {
		return nil
	}

	vehicle, ok := masters.vehicleActorForFollowerConfig(source.FollowerSummonConfigID, uint64(event.SkillID), uint64(event.BulletID))
	if !ok {
		return nil
	}
	var name *CombatEventLabelTextJSONL
	if source.HasConfigID && source.HasConfigType && source.ConfigType == 4 {
		if template, ok := masters.templateByID(source.ConfigID); ok {
			name = template.DevLabel
		}
	}
	requestEvidence := "DamageContext.SkillMessageId is present, but no matching outbound UseSkillRequest was resolved; the candidate is correlated from the decoded SkillId and BulletId, verified runtime follower link, and static vehicle actor chain"
	return &DamageContributorCandidate{
		Name:             name,
		Kind:             VehicleKindMotorcycle,
		ActorType:        "Vehicle",
		EntityType:       vehicle.EntityType,
		FollowerConfigID: vehicle.ConfigID,
		BlueprintType:    vehicle.BlueprintType,
		ActorBlueprint:   vehicle.ActorBlueprint,
		SummonSkillID:    motorcycle.SummonSkillID,
		Status:           ResolutionCorrelated,
		Evidence:         requestEvidence + "; PlayerFollowerSelfComponentPb.FollowerSummonConfigId selects a SummonCfg/BlueprintConfig chain with EntityType=BattleVehicle, and that actor's EntitySkillPreload and BulletPreload list this SkillId and BulletId; the source FollowEntityId resolves in the same session to a Type 5 ConcomitantMotorcycle entity whose SummonerId is the raw source entity. The server-reported AttackerEntityId remains the player entity, so vehicle contribution is CORRELATED and does not replace raw source or character credit",
	}
}
