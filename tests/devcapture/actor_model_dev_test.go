package devcapture

import (
	"testing"

	"wwlogs/internal/capture"
)

func TestProjectActorsRetainsPlayerSceneRoleWithoutMasterName(t *testing.T) {
	event := unresolvedEvent()
	event.SourceEntityID, event.HasSourceEntity = 41001, true
	event.Source = capture.EntityIdentity{Runtime: playerRoleRuntime(41001, 1508), RuntimeStatus: capture.ResolutionVerified}

	view := jsonlView(event, nil)
	if view.Credit.CharacterID == nil || *view.Credit.CharacterID != 1508 || view.Credit.Status != capture.ResolutionCorrelated {
		t.Fatalf("FightRoleInfos role was disconnected from the credit block: %+v", view.Credit)
	}
	if view.Credit.NameStatus != capture.ResolutionUnknown || view.Credit.Name != "" {
		t.Fatalf("an unresolved name was presented as resolved: %+v", view.Credit)
	}
	actors := view.Actors
	if actors.ActualSource.ActorType != "Character" || actors.CreditedCharacter.CharacterID == nil ||
		*actors.CreditedCharacter.CharacterID != 1508 || actors.CreditedCharacter.Status != capture.ResolutionCorrelated {
		t.Fatalf("role actor projection = %+v", actors)
	}
	if actors.Owner.Status != capture.ResolutionNotApplicable || actors.Summoner.Status != capture.ResolutionNotApplicable {
		t.Fatalf("direct character owner/summoner = %+v/%+v, want NOT_APPLICABLE", actors.Owner, actors.Summoner)
	}
}

func TestCreditFallbackNeverOverwritesMasterBackedCredit(t *testing.T) {
	event := capture.CombatEvent{
		CharacterID: 1409, CharacterName: "Resolved", CharacterStatus: capture.ResolutionVerified, CharacterNameStatus: capture.ResolutionVerified,
		Source: capture.EntityIdentity{Runtime: playerRoleRuntime(41001, 1508), RuntimeStatus: capture.ResolutionVerified},
	}
	view := jsonlView(event, nil)
	if view.Credit.CharacterID == nil || *view.Credit.CharacterID != 1409 || view.Credit.Name != "Resolved" || view.Credit.Status != capture.ResolutionVerified {
		t.Fatalf("Master-backed credit was replaced by the scene role: %+v", view.Credit)
	}
}

func TestProjectActorsKeepsUnresolvedRolesUnknown(t *testing.T) {
	// An entity with no type, no role mapping and no Master identity gives no basis for any claim.
	event := unresolvedEvent()
	event.Source = capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 50001}, RuntimeStatus: capture.ResolutionUnknown}
	event.Target = capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 50002}, RuntimeStatus: capture.ResolutionUnknown}

	view := jsonlView(event, nil)
	actors := view.Actors
	if actors.ActualSource.ActorType != "Unknown" || actors.Target.ActorType != "Unknown" ||
		actors.Owner.Status != capture.ResolutionUnknown || actors.Summoner.Status != capture.ResolutionUnknown || actors.CreditedCharacter.Status != capture.ResolutionUnknown {
		t.Fatalf("an unresolved entity was classified: %+v", actors)
	}
	if view.Credit.CharacterID != nil || view.Credit.Status == capture.ResolutionNotApplicable {
		t.Fatalf("credit = %+v", view.Credit)
	}
}
