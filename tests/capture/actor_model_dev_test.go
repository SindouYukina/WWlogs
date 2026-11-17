//go:build wwdev

package capture

import (
	"testing"
)

func TestProjectActorsRetainsPlayerSceneRoleWithoutMasterName(t *testing.T) {
	event := unresolvedEvent()
	event.SourceEntityID, event.HasSourceEntity = 41001, true
	event.Source = EntityIdentity{Runtime: playerRoleRuntime(41001, 1508), RuntimeStatus: ResolutionVerified}

	view := jsonlView(event, nil)
	if view.Credit.CharacterID == nil || *view.Credit.CharacterID != 1508 || view.Credit.Status != ResolutionCorrelated {
		t.Fatalf("FightRoleInfos role was disconnected from the credit block: %+v", view.Credit)
	}
	if view.Credit.NameStatus != ResolutionUnknown || view.Credit.Name != "" {
		t.Fatalf("an unresolved name was presented as resolved: %+v", view.Credit)
	}
	actors := view.Actors
	if actors.ActualSource.ActorType != "Character" || actors.CreditedCharacter.CharacterID == nil ||
		*actors.CreditedCharacter.CharacterID != 1508 || actors.CreditedCharacter.Status != ResolutionCorrelated {
		t.Fatalf("role actor projection = %+v", actors)
	}
	if actors.Owner.Status != ResolutionNotApplicable || actors.Summoner.Status != ResolutionNotApplicable {
		t.Fatalf("direct character owner/summoner = %+v/%+v, want NOT_APPLICABLE", actors.Owner, actors.Summoner)
	}
}

func TestCreditFallbackNeverOverwritesMasterBackedCredit(t *testing.T) {
	event := CombatEvent{
		CharacterID: 1409, CharacterName: "Resolved", CharacterStatus: ResolutionVerified, CharacterNameStatus: ResolutionVerified,
		Source: EntityIdentity{Runtime: playerRoleRuntime(41001, 1508), RuntimeStatus: ResolutionVerified},
	}
	view := jsonlView(event, nil)
	if view.Credit.CharacterID == nil || *view.Credit.CharacterID != 1409 || view.Credit.Name != "Resolved" || view.Credit.Status != ResolutionVerified {
		t.Fatalf("Master-backed credit was replaced by the scene role: %+v", view.Credit)
	}
}

func TestProjectActorsKeepsUnresolvedRolesUnknown(t *testing.T) {
	// An entity with no type, no role mapping and no Master identity gives no basis for any claim.
	event := unresolvedEvent()
	event.Source = EntityIdentity{Runtime: RuntimeEntity{EntityID: 50001}, RuntimeStatus: ResolutionUnknown}
	event.Target = EntityIdentity{Runtime: RuntimeEntity{EntityID: 50002}, RuntimeStatus: ResolutionUnknown}

	view := jsonlView(event, nil)
	actors := view.Actors
	if actors.ActualSource.ActorType != "Unknown" || actors.Target.ActorType != "Unknown" ||
		actors.Owner.Status != ResolutionUnknown || actors.Summoner.Status != ResolutionUnknown || actors.CreditedCharacter.Status != ResolutionUnknown {
		t.Fatalf("an unresolved entity was classified: %+v", actors)
	}
	if view.Credit.CharacterID != nil || view.Credit.Status == ResolutionNotApplicable {
		t.Fatalf("credit = %+v", view.Credit)
	}
}
