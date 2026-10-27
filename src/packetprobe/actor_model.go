package main

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
// that this decoder observes, so it is always UNKNOWN. Owner has no verified
// character-level link yet, so it is reported only as the runtime owner id
// with status UNKNOWN.
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
		Instigator:        actorRoleJSONL{ActorType: "Unknown", Status: resolutionUnknown, Evidence: "no observed instigator field in the decoded damage or healing message"},
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
	case event.Source.HasMonster:
		role.ActorType = "Monster"
	}
	return role
}

func ownerRole(event combatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: resolutionUnknown, Evidence: "runtime owner id is observed but its character-level meaning is not verified"}
	if event.Source.Runtime.hasOwnerID {
		id := event.Source.Runtime.ownerID
		role.EntityID = &id
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
	return role
}

func creditedCharacterRole(event combatEvent) actorRoleJSONL {
	role := actorRoleJSONL{ActorType: "Unknown", Status: event.CharacterStatus, Evidence: event.CharacterProof}
	if event.CharacterID != 0 {
		id := event.CharacterID
		role.CharacterID = &id
		role.ActorType = "Character"
	}
	return role
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
	case event.Target.HasMonster:
		role.ActorType = "Monster"
	}
	return role
}
