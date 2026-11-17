package devcapture

import "wwlogs/internal/capture"

// actorRoleJSONL is one actor role in a combat event. Roles are kept separate
// because one event can name a different entity for each role: the entity that
// dealt the result, the character credited for it, and the summoner or owner
// that relates to the source entity. A role without evidence stays UNKNOWN
// rather than borrowing another role's entity.
type actorRoleJSONL struct {
	EntityID    *uint64                  `json:"entityId,omitempty"`
	CharacterID *uint64                  `json:"characterId,omitempty"`
	ActorType   string                   `json:"actorType"`
	Status      capture.ResolutionStatus `json:"status"`
	Evidence    string                   `json:"evidence,omitempty"`
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
func projectActors(event capture.CombatEvent) actorsJSONL {
	return actorsJSONL{
		ActualSource:      actualSourceRole(event),
		Instigator:        actorRoleJSONL{ActorType: "NotApplicable", Status: capture.ResolutionNotApplicable, Evidence: "the decoded damage or healing message defines no instigator field"},
		Owner:             ownerRole(event),
		Summoner:          summonerRole(event),
		CreditedCharacter: creditedCharacterRole(event),
		Target:            targetRole(event),
	}
}

func actualSourceRole(event capture.CombatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.Source.RuntimeStatus, Evidence: event.Source.RuntimeProof}
	if event.Source.Runtime.EntityID != 0 {
		id := event.Source.Runtime.EntityID
		role.EntityID = &id
	}
	switch {
	case event.Source.HasCharacter && event.Source.CharacterStatus == capture.ResolutionVerified:
		role.ActorType = "Character"
	case event.Source.Runtime.HasSummonerID:
		role.ActorType = "Summon"
	case event.Source.HasMonster, capture.IsExplicitMonsterEntity(event.Source.Runtime):
		role.ActorType = "Monster"
	case capture.HasPlayerSceneRoleMapping(event.Source.Runtime):
		role.ActorType = "Character"
	}
	return role
}

// isDirectActor reports a source that is itself a character or monster and carries no
// owner or summoner link, so owner and summoner roles do not exist for it.
func isDirectActor(event capture.CombatEvent) bool {
	runtime := event.Source.Runtime
	if hasOwnerOrSummonerLink(runtime) || event.SummonerCharacter.HasCharacterID || len(event.SummonerCharacter.EntityIDs) > 0 {
		return false
	}
	return event.Source.HasMonster || capture.IsExplicitMonsterEntity(runtime) || capture.HasPlayerSceneRoleMapping(runtime) ||
		(event.Source.HasCharacter && event.Source.CharacterStatus == capture.ResolutionVerified)
}

// hasOwnerOrSummonerLink reports any runtime field that links the entity to an owner,
// summoner, follower source or other controlling entity. PlayerId alone is not a link.
func hasOwnerOrSummonerLink(entity capture.RuntimeEntity) bool {
	return (entity.HasOwnerID && entity.OwnerID != 0) ||
		(entity.HasOwnerIncID && entity.OwnerIncID != 0) ||
		(entity.HasSummonerID && entity.SummonerID != 0) ||
		(entity.HasSummonerPlayerID && entity.SummonerPlayerID != 0) ||
		(entity.HasSceneSummonerID && entity.SceneSummonerID != 0) ||
		(entity.HasSceneSummonConfigID && entity.SceneSummonConfigID != 0) ||
		(entity.HasSceneSummonSkillID && entity.SceneSummonSkillID != 0) ||
		(entity.HasPlayerEntityID && entity.PlayerEntityID != 0) ||
		(entity.HasPhantomRoleEntityID && entity.PhantomRoleEntityID != 0) ||
		(entity.HasWeakVisionEntityID && entity.WeakVisionEntityID != 0) ||
		(entity.HasSummonConfigID && entity.SummonConfigID != 0) ||
		(entity.HasSummonSkillID && entity.SummonSkillID != 0) ||
		(entity.HasSummonType && entity.SummonType != 0) ||
		(entity.HasFollowerSummonConfigID && entity.FollowerSummonConfigID != 0) ||
		(entity.HasFollowEntityID && entity.FollowEntityID != 0) ||
		len(entity.ConcomitantVisionIDs) > 0 || len(entity.ConcomitantCustomIDs) > 0 || len(entity.PlayerFollowers) > 0
}

func ownerRole(event capture.CombatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: capture.ResolutionUnknown, Evidence: "the source ownership relation is not resolved"}
	if event.Source.Runtime.HasOwnerID {
		id := event.Source.Runtime.OwnerID
		role.EntityID = &id
		role.Evidence = "runtime owner entity id is observed; its character-level meaning is not verified"
		return role
	}
	if isDirectActor(event) {
		role.ActorType, role.Status = "NotApplicable", capture.ResolutionNotApplicable
		role.Evidence = "the source is a direct character or monster with no owner field"
	}
	return role
}

func summonerRole(event capture.CombatEvent) actorRoleJSONL {
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
		role.ActorType, role.Status = "NotApplicable", capture.ResolutionNotApplicable
		role.Evidence = "the source is a direct character or monster, not a summon"
	}
	return role
}

func creditedCharacterRole(event capture.CombatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.CharacterStatus, Evidence: event.CharacterProof}
	if event.CharacterID != 0 {
		id := event.CharacterID
		role.CharacterID = &id
		role.ActorType = "Character"
		return role
	}
	// The scene role group names the character even when Master has no row or name for it.
	// This never replaces a character that the Master-backed path already resolved.
	if runtime := event.Source.Runtime; capture.HasPlayerSceneRoleMapping(runtime) && runtime.RoleID != 0 && !runtime.RoleConflict {
		id := runtime.RoleID
		role.CharacterID = &id
		role.ActorType = "Character"
		role.Status = capture.ResolutionCorrelated
		role.Evidence = "PlayerSceneGroupData.FightRoleInfos.RoleId -> EntityId; the role is identified without a Master name"
		return role
	}
	if isVehicleContributor(event) || ((event.Source.HasMonster || capture.IsExplicitMonsterEntity(event.Source.Runtime)) && !hasOwnerOrSummonerLink(event.Source.Runtime)) {
		role.ActorType, role.Status = "NotApplicable", capture.ResolutionNotApplicable
		role.Evidence = "the source is an independent vehicle contributor or a non-player monster, which has no character credit"
	}
	return role
}

// isVehicleContributor reports a confirmed or correlated Vehicle contributor candidate.
// It does not change the actual source of the event.
func isVehicleContributor(event capture.CombatEvent) bool {
	candidate := event.DamageContributorCandidate
	return candidate != nil && candidate.ActorType == "Vehicle" &&
		(candidate.Status == capture.ResolutionVerified || candidate.Status == capture.ResolutionCorrelated)
}

// creditJSONLFallback fills the credit block for events whose Master-backed character
// is absent. It runs after the Master-backed credit is copied, so it cannot overwrite it.
func creditJSONLFallback(credit *combatEventCreditJSONL, event capture.CombatEvent) {
	if event.CharacterID != 0 {
		return
	}
	role := creditedCharacterRole(event)
	switch {
	case role.Status == capture.ResolutionNotApplicable:
		credit.Status, credit.NameStatus, credit.Evidence = capture.ResolutionNotApplicable, capture.ResolutionNotApplicable, role.Evidence
	case role.CharacterID != nil:
		credit.CharacterID, credit.Status, credit.Evidence = role.CharacterID, role.Status, role.Evidence
	}
}

func targetRole(event capture.CombatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.Target.RuntimeStatus, Evidence: event.Target.RuntimeProof}
	if event.Target.Runtime.EntityID != 0 {
		id := event.Target.Runtime.EntityID
		role.EntityID = &id
	}
	switch {
	case event.Target.HasCharacter && event.Target.CharacterStatus == capture.ResolutionVerified:
		role.ActorType = "Character"
	case event.Target.HasMonster, capture.IsExplicitMonsterEntity(event.Target.Runtime):
		role.ActorType = "Monster"
	case capture.HasPlayerSceneRoleMapping(event.Target.Runtime):
		role.ActorType = "Character"
	}
	return role
}
