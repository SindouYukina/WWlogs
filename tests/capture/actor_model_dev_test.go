//go:build wwdev

package capture

import (
	"testing"
)

func TestProjectActorsRetainsPlayerSceneRoleWithoutMasterName(t *testing.T) {
	event := unresolvedEvent()
	event.SourceEntityID, event.HasSourceEntity = 41001, true
	event.Source = entityIdentity{Runtime: playerRoleRuntime(41001, 1508), RuntimeStatus: resolutionVerified}

	view := event.jsonlView(nil)
	if view.Credit.CharacterID == nil || *view.Credit.CharacterID != 1508 || view.Credit.Status != resolutionCorrelated {
		t.Fatalf("FightRoleInfos role was disconnected from the credit block: %+v", view.Credit)
	}
	if view.Credit.NameStatus != resolutionUnknown || view.Credit.Name != "" {
		t.Fatalf("an unresolved name was presented as resolved: %+v", view.Credit)
	}
	actors := view.Actors
	if actors.ActualSource.ActorType != "Character" || actors.CreditedCharacter.CharacterID == nil ||
		*actors.CreditedCharacter.CharacterID != 1508 || actors.CreditedCharacter.Status != resolutionCorrelated {
		t.Fatalf("role actor projection = %+v", actors)
	}
	if actors.Owner.Status != resolutionNotApplicable || actors.Summoner.Status != resolutionNotApplicable {
		t.Fatalf("direct character owner/summoner = %+v/%+v, want NOT_APPLICABLE", actors.Owner, actors.Summoner)
	}
}

func TestCreditFallbackNeverOverwritesMasterBackedCredit(t *testing.T) {
	event := combatEvent{
		CharacterID: 1409, CharacterName: "Resolved", CharacterStatus: resolutionVerified, CharacterNameStatus: resolutionVerified,
		Source: entityIdentity{Runtime: playerRoleRuntime(41001, 1508), RuntimeStatus: resolutionVerified},
	}
	view := event.jsonlView(nil)
	if view.Credit.CharacterID == nil || *view.Credit.CharacterID != 1409 || view.Credit.Name != "Resolved" || view.Credit.Status != resolutionVerified {
		t.Fatalf("Master-backed credit was replaced by the scene role: %+v", view.Credit)
	}
}

func TestProjectActorsKeepsUnresolvedRolesUnknown(t *testing.T) {
	// An entity with no type, no role mapping and no Master identity gives no basis for any claim.
	event := unresolvedEvent()
	event.Source = entityIdentity{Runtime: runtimeEntity{entityID: 50001}, RuntimeStatus: resolutionUnknown}
	event.Target = entityIdentity{Runtime: runtimeEntity{entityID: 50002}, RuntimeStatus: resolutionUnknown}

	view := event.jsonlView(nil)
	actors := view.Actors
	if actors.ActualSource.ActorType != "Unknown" || actors.Target.ActorType != "Unknown" ||
		actors.Owner.Status != resolutionUnknown || actors.Summoner.Status != resolutionUnknown || actors.CreditedCharacter.Status != resolutionUnknown {
		t.Fatalf("an unresolved entity was classified: %+v", actors)
	}
	if view.Credit.CharacterID != nil || view.Credit.Status == resolutionNotApplicable {
		t.Fatalf("credit = %+v", view.Credit)
	}
}
