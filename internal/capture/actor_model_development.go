//go:build wwdev

package capture

// actorRoleJSONL is one actor role in a combat event. Roles are kept separate
// because one event can name a different entity for each role: the entity that
// dealt the result, the character credited for it, and the summoner or owner
// that relates to the source entity. A role without evidence stays UNKNOWN
// rather than borrowing another role's entity.
type actorRoleJSONL struct {
	EntityID    *uint64          `json:"entityId,omitempty"`
	CharacterID *uint64          `json:"characterId,omitempty"`
	ActorType   string           `json:"actorType"`
	Status      resolutionStatus `json:"status"`
	Evidence    string           `json:"evidence,omitempty"`
}

// actorsJSONL groups the roles of one combat event. Instigator has no field
// defined by the decoded message, so it is NOT_APPLICABLE. Owner has no verified
// character-level link yet, so an observed runtime owner id is reported with
// status UNKNOWN. A role the source cannot have, such as the owner of a direct
// character or monster, is NOT_APPLICABLE; an observed role that cannot be
// resolved stays UNKNOWN.
type actorsJSONL struct {
	ActualSource      actorRoleJSONL `json:"actualSource"`
	Instigator        actorRoleJSONL `json:"instigator"`
	Owner             actorRoleJSONL `json:"owner"`
	Summoner          actorRoleJSONL `json:"summoner"`
	CreditedCharacter actorRoleJSONL `json:"creditedCharacter"`
	Target            actorRoleJSONL `json:"target"`
}

// projectActors builds the actor roles for one damage or healing event from the
// identities already resolved for it. It adds no new inference: every field is
// copied from an existing resolver result or left UNKNOWN.
func projectActors(event combatEvent) actorsJSONL {
	return actorsJSONL{
		ActualSource:      actualSourceRole(event),
		Instigator:        actorRoleJSONL{ActorType: "NotApplicable", Status: resolutionNotApplicable, Evidence: "the decoded damage or healing message defines no instigator field"},
		Owner:             ownerRole(event),
		Summoner:          summonerRole(event),
		CreditedCharacter: creditedCharacterRole(event),
		Target:            targetRole(event),
	}
}

func actualSourceRole(event combatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.Source.RuntimeStatus, Evidence: event.Source.RuntimeProof}
	if event.Source.Runtime.entityID != 0 {
		id := event.Source.Runtime.entityID
		role.EntityID = &id
	}
	switch {
	case event.Source.HasCharacter && event.Source.CharacterStatus == resolutionVerified:
		role.ActorType = "Character"
	case event.Source.Runtime.hasSummonerID:
		role.ActorType = "Summon"
	case event.Source.HasMonster, isExplicitMonsterEntity(event.Source.Runtime):
		role.ActorType = "Monster"
	case hasPlayerSceneRoleMapping(event.Source.Runtime):
		role.ActorType = "Character"
	}
	return role
}

// isDirectActor reports a source that is itself a character or monster and carries no
// owner or summoner link, so owner and summoner roles do not exist for it.
func isDirectActor(event combatEvent) bool {
	runtime := event.Source.Runtime
	if hasOwnerOrSummonerLink(runtime) || event.SummonerCharacter.HasCharacterID || len(event.SummonerCharacter.EntityIDs) > 0 {
		return false
	}
	return event.Source.HasMonster || isExplicitMonsterEntity(runtime) || hasPlayerSceneRoleMapping(runtime) ||
		(event.Source.HasCharacter && event.Source.CharacterStatus == resolutionVerified)
}

// hasOwnerOrSummonerLink reports any runtime field that links the entity to an owner,
// summoner, follower source or other controlling entity. PlayerId alone is not a link.
func hasOwnerOrSummonerLink(entity runtimeEntity) bool {
	return (entity.hasOwnerID && entity.ownerID != 0) ||
		(entity.hasOwnerIncID && entity.ownerIncID != 0) ||
		(entity.hasSummonerID && entity.summonerID != 0) ||
		(entity.hasSummonerPlayerID && entity.summonerPlayerID != 0) ||
		(entity.hasSceneSummonerID && entity.sceneSummonerID != 0) ||
		(entity.hasSceneSummonConfigID && entity.sceneSummonConfigID != 0) ||
		(entity.hasSceneSummonSkillID && entity.sceneSummonSkillID != 0) ||
		(entity.hasPlayerEntityID && entity.playerEntityID != 0) ||
		(entity.hasPhantomRoleEntityID && entity.phantomRoleEntityID != 0) ||
		(entity.hasWeakVisionEntityID && entity.weakVisionEntityID != 0) ||
		(entity.hasSummonConfigID && entity.summonConfigID != 0) ||
		(entity.hasSummonSkillID && entity.summonSkillID != 0) ||
		(entity.hasSummonType && entity.summonType != 0) ||
		(entity.hasFollowerSummonConfigID && entity.followerSummonConfigID != 0) ||
		(entity.hasFollowEntityID && entity.followEntityID != 0) ||
		len(entity.concomitantVisionIDs) > 0 || len(entity.concomitantCustomIDs) > 0 || len(entity.playerFollowers) > 0
}

func ownerRole(event combatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: resolutionUnknown, Evidence: "the source ownership relation is not resolved"}
	if event.Source.Runtime.hasOwnerID {
		id := event.Source.Runtime.ownerID
		role.EntityID = &id
		role.Evidence = "runtime owner entity id is observed; its character-level meaning is not verified"
		return role
	}
	if isDirectActor(event) {
		role.ActorType, role.Status = "NotApplicable", resolutionNotApplicable
		role.Evidence = "the source is a direct character or monster with no owner field"
	}
	return role
}

func summonerRole(event combatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.SummonerCharacter.Status, Evidence: event.SummonerCharacter.Proof}
	if event.SummonerCharacter.HasCharacterID {
		id := event.SummonerCharacter.CharacterID
		role.CharacterID = &id
		role.ActorType = "Character"
	}
	if len(event.SummonerCharacter.EntityIDs) > 0 {
		id := event.SummonerCharacter.EntityIDs[0]
		role.EntityID = &id
	}
	if isDirectActor(event) {
		role.ActorType, role.Status = "NotApplicable", resolutionNotApplicable
		role.Evidence = "the source is a direct character or monster, not a summon"
	}
	return role
}

func creditedCharacterRole(event combatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.CharacterStatus, Evidence: event.CharacterProof}
	if event.CharacterID != 0 {
		id := event.CharacterID
		role.CharacterID = &id
		role.ActorType = "Character"
		return role
	}
	// The scene role group names the character even when Master has no row or name for it.
	// This never replaces a character that the Master-backed path already resolved.
	if runtime := event.Source.Runtime; hasPlayerSceneRoleMapping(runtime) && runtime.roleID != 0 && !runtime.roleConflict {
		id := runtime.roleID
		role.CharacterID = &id
		role.ActorType = "Character"
		role.Status = resolutionCorrelated
		role.Evidence = "PlayerSceneGroupData.FightRoleInfos.RoleId -> EntityId; the role is identified without a Master name"
		return role
	}
	if isVehicleContributor(event) || ((event.Source.HasMonster || isExplicitMonsterEntity(event.Source.Runtime)) && !hasOwnerOrSummonerLink(event.Source.Runtime)) {
		role.ActorType, role.Status = "NotApplicable", resolutionNotApplicable
		role.Evidence = "the source is an independent vehicle contributor or a non-player monster, which has no character credit"
	}
	return role
}

// isVehicleContributor reports a confirmed or correlated Vehicle contributor candidate.
// It does not change the actual source of the event.
func isVehicleContributor(event combatEvent) bool {
	candidate := event.DamageContributorCandidate
	return candidate != nil && candidate.ActorType == "Vehicle" &&
		(candidate.Status == resolutionVerified || candidate.Status == resolutionCorrelated)
}

// creditJSONLFallback fills the credit block for events whose Master-backed character
// is absent. It runs after the Master-backed credit is copied, so it cannot overwrite it.
func creditJSONLFallback(credit *combatEventCreditJSONL, event combatEvent) {
	if event.CharacterID != 0 {
		return
	}
	role := creditedCharacterRole(event)
	switch {
	case role.Status == resolutionNotApplicable:
		credit.Status, credit.NameStatus, credit.Evidence = resolutionNotApplicable, resolutionNotApplicable, role.Evidence
	case role.CharacterID != nil:
		credit.CharacterID, credit.Status, credit.Evidence = role.CharacterID, role.Status, role.Evidence
	}
}

func targetRole(event combatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.Target.RuntimeStatus, Evidence: event.Target.RuntimeProof}
	if event.Target.Runtime.entityID != 0 {
		id := event.Target.Runtime.entityID
		role.EntityID = &id
	}
	switch {
	case event.Target.HasCharacter && event.Target.CharacterStatus == resolutionVerified:
		role.ActorType = "Character"
	case event.Target.HasMonster, isExplicitMonsterEntity(event.Target.Runtime):
		role.ActorType = "Monster"
	case hasPlayerSceneRoleMapping(event.Target.Runtime):
		role.ActorType = "Character"
	}
	return role
}
