//go:build wwdev

package capture

import (
	"encoding/json"
	"testing"
)

func TestProjectActorsKeepsRolesSeparate(t *testing.T) {
	event := CombatEvent{
		CharacterID:     1501,
		CharacterStatus: ResolutionVerified,
		CharacterProof:  "runtime-resolved character",
		SummonerCharacter: SummonerCharacterIdentity{
			EntityIDs:      []uint64{9001},
			CharacterID:    1502,
			HasCharacterID: true,
			Status:         ResolutionCorrelated,
			Proof:          "player-level follower list",
		},
	}
	event.Source.Runtime.EntityID = 28002
	event.Source.Runtime.HasSummonerID = true
	event.Source.Runtime.SummonerID = 9001
	event.Source.RuntimeStatus = ResolutionVerified
	event.Target.Runtime.EntityID = 28530
	event.Target.RuntimeStatus = ResolutionVerified
	event.Target.HasMonster = true

	actors := projectActors(event)
	if actors.ActualSource.ActorType != "Summon" || *actors.ActualSource.EntityID != 28002 {
		t.Fatalf("actual source = %+v, want Summon 28002", actors.ActualSource)
	}
	if actors.CreditedCharacter.ActorType != "Character" || *actors.CreditedCharacter.CharacterID != 1501 {
		t.Fatalf("credited character = %+v", actors.CreditedCharacter)
	}
	if actors.Summoner.Status != ResolutionCorrelated || *actors.Summoner.CharacterID != 1502 || *actors.Summoner.EntityID != 9001 {
		t.Fatalf("summoner = %+v", actors.Summoner)
	}
	if actors.Target.ActorType != "Monster" || *actors.Target.EntityID != 28530 {
		t.Fatalf("target = %+v", actors.Target)
	}
	if actors.Instigator.Status != ResolutionNotApplicable || actors.Instigator.EntityID != nil {
		t.Fatalf("instigator has no decoded protocol field and must be not applicable, got %+v", actors.Instigator)
	}
	if actors.Owner.EntityID != nil || actors.Owner.Status != ResolutionUnknown {
		t.Fatalf("owner without runtime owner id = %+v", actors.Owner)
	}
}

func TestProjectActorsDoesNotBorrowSourceEntityAsOwner(t *testing.T) {
	var event CombatEvent
	event.Source.Runtime.EntityID = 40001
	event.Source.Runtime.HasOwnerID = true
	event.Source.Runtime.OwnerID = 40002

	actors := projectActors(event)
	if actors.Owner.EntityID == nil || *actors.Owner.EntityID != 40002 {
		t.Fatalf("owner = %+v, want runtime owner 40002", actors.Owner)
	}
	if actors.ActualSource.EntityID == nil || *actors.ActualSource.EntityID != 40001 {
		t.Fatalf("actual source = %+v, want 40001", actors.ActualSource)
	}
	if actors.Owner.Status != ResolutionUnknown {
		t.Fatalf("owner status = %s, want UNKNOWN", actors.Owner.Status)
	}
}

func TestActorsSerializeWithStableRoleNames(t *testing.T) {
	line, err := json.Marshal(projectActors(CombatEvent{}))
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

// unresolvedEvent mirrors the defaults of buildDamageEvent: nothing about credit or summoner is resolved yet.
func unresolvedEvent() CombatEvent {
	return CombatEvent{
		CharacterStatus: ResolutionUnknown, CharacterNameStatus: ResolutionUnknown,
		SummonerCharacter: SummonerCharacterIdentity{Status: ResolutionUnknown},
	}
}

// monsterRuntime is an explicit EntityType=2 entity with no Master monster row.
func monsterRuntime(entityID uint64) RuntimeEntity {
	return RuntimeEntity{EntityID: entityID, EntityTypeCode: EntityTypeMonster, HasEntityType: true}
}

// playerRoleRuntime is a role listed in the local PlayerSceneGroup.
func playerRoleRuntime(entityID, roleID uint64) RuntimeEntity {
	return RuntimeEntity{
		EntityID: entityID, PlayerID: 77, HasPlayerID: true, RoleID: roleID, HasRoleID: true,
		RoleSourceFieldPath: "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId",
	}
}

func TestProjectActorsUsesRuntimeMonsterTypeWithoutMasterIdentity(t *testing.T) {
	event := unresolvedEvent()
	event.Source = EntityIdentity{Runtime: monsterRuntime(40001), RuntimeStatus: ResolutionVerified}
	event.Target = EntityIdentity{Runtime: monsterRuntime(40002), RuntimeStatus: ResolutionVerified}

	actors := projectActors(event)
	if actors.ActualSource.ActorType != "Monster" || actors.Target.ActorType != "Monster" {
		t.Fatalf("explicit runtime Monster types were lost: source=%+v target=%+v", actors.ActualSource, actors.Target)
	}
	for name, role := range map[string]actorRoleJSONL{"owner": actors.Owner, "summoner": actors.Summoner, "credit": actors.CreditedCharacter} {
		if role.Status != ResolutionNotApplicable {
			t.Errorf("%s of a direct monster = %+v, want NOT_APPLICABLE", name, role)
		}
	}
}

func TestProjectActorsKeepsMonsterWithOwnerLinkUnresolved(t *testing.T) {
	// A monster that carries an owner field (as in the 2026-11-02 LIVE capture) has an owner
	// that is observed but unresolved; it must not be reported as having none.
	event := unresolvedEvent()
	runtime := monsterRuntime(40001)
	runtime.OwnerIncID, runtime.HasOwnerIncID = 1125899906870022, true
	event.Source = EntityIdentity{Runtime: runtime, RuntimeStatus: ResolutionVerified}

	actors := projectActors(event)
	if actors.ActualSource.ActorType != "Monster" {
		t.Fatalf("actual source = %+v", actors.ActualSource)
	}
	if actors.Owner.Status != ResolutionUnknown || actors.Summoner.Status != ResolutionUnknown || actors.CreditedCharacter.Status == ResolutionNotApplicable {
		t.Fatalf("an observed owner link was hidden: owner=%+v summoner=%+v credit=%+v", actors.Owner, actors.Summoner, actors.CreditedCharacter)
	}
}

func TestProjectActorsKeepsSummonOwnerUnresolved(t *testing.T) {
	event := unresolvedEvent()
	runtime := monsterRuntime(40001)
	runtime.SummonerID, runtime.HasSummonerID = 9001, true
	event.Source = EntityIdentity{Runtime: runtime, RuntimeStatus: ResolutionVerified}

	actors := projectActors(event)
	if actors.ActualSource.ActorType != "Summon" || actors.Owner.Status != ResolutionUnknown || actors.Summoner.Status == ResolutionNotApplicable {
		t.Fatalf("summon roles = %+v", actors)
	}
}
