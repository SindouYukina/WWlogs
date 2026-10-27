package main

import (
	"encoding/json"
	"testing"
)

func TestProjectActorsKeepsRolesSeparate(t *testing.T) {
	event := combatEvent{
		CharacterID:     1501,
		CharacterStatus: resolutionVerified,
		CharacterProof:  "runtime-resolved character",
		SummonerCharacter: summonerCharacterIdentity{
			EntityIDs:      []uint64{9001},
			CharacterID:    1502,
			HasCharacterID: true,
			Status:         resolutionCorrelated,
			Proof:          "player-level follower list",
		},
	}
	event.Source.Runtime.entityID = 28002
	event.Source.Runtime.hasSummonerID = true
	event.Source.Runtime.summonerID = 9001
	event.Source.RuntimeStatus = resolutionVerified
	event.Target.Runtime.entityID = 28530
	event.Target.RuntimeStatus = resolutionVerified
	event.Target.HasMonster = true

	actors := projectActors(event)
	if actors.ActualSource.ActorType != "Summon" || *actors.ActualSource.EntityID != 28002 {
		t.Fatalf("actual source = %+v, want Summon 28002", actors.ActualSource)
	}
	if actors.CreditedCharacter.ActorType != "Character" || *actors.CreditedCharacter.CharacterID != 1501 {
		t.Fatalf("credited character = %+v", actors.CreditedCharacter)
	}
	if actors.Summoner.Status != resolutionCorrelated || *actors.Summoner.CharacterID != 1502 || *actors.Summoner.EntityID != 9001 {
		t.Fatalf("summoner = %+v", actors.Summoner)
	}
	if actors.Target.ActorType != "Monster" || *actors.Target.EntityID != 28530 {
		t.Fatalf("target = %+v", actors.Target)
	}
	if actors.Instigator.Status != resolutionUnknown || actors.Instigator.EntityID != nil {
		t.Fatalf("instigator must stay unknown, got %+v", actors.Instigator)
	}
	if actors.Owner.EntityID != nil || actors.Owner.Status != resolutionUnknown {
		t.Fatalf("owner without runtime owner id = %+v", actors.Owner)
	}
}

func TestProjectActorsDoesNotBorrowSourceEntityAsOwner(t *testing.T) {
	var event combatEvent
	event.Source.Runtime.entityID = 40001
	event.Source.Runtime.hasOwnerID = true
	event.Source.Runtime.ownerID = 40002

	actors := projectActors(event)
	if actors.Owner.EntityID == nil || *actors.Owner.EntityID != 40002 {
		t.Fatalf("owner = %+v, want runtime owner 40002", actors.Owner)
	}
	if actors.ActualSource.EntityID == nil || *actors.ActualSource.EntityID != 40001 {
		t.Fatalf("actual source = %+v, want 40001", actors.ActualSource)
	}
	if actors.Owner.Status != resolutionUnknown {
		t.Fatalf("owner status = %s, want UNKNOWN", actors.Owner.Status)
	}
}

func TestActorsSerializeWithStableRoleNames(t *testing.T) {
	line, err := json.Marshal(projectActors(combatEvent{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(line, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, role := range []string{"actualSource", "instigator", "owner", "summoner", "creditedCharacter", "target"} {
		if _, ok := decoded[role]; !ok {
			t.Fatalf("missing role %q in %s", role, line)
		}
	}
}
