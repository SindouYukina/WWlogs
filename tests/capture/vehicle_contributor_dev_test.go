//go:build wwdev

package capture

import (
	"encoding/json"
	"testing"
)

func TestVehicleContributorCandidateJSONKeepsRawAttacker(t *testing.T) {
	resolver, master, event := vehicleDamageContributorFixture()
	event.DamageContributorCandidate = ResolveVehicleDamageContributorCandidate(resolver, master, event)
	view := jsonlView(event, master)
	if view.SchemaVersion != 5 {
		t.Fatalf("schema version = %d, want 5", view.SchemaVersion)
	}
	if view.DamageContributorCandidate == nil || view.DamageContributorCandidate.Status != ResolutionCorrelated {
		t.Fatalf("vehicle contributor candidate = %+v", view.DamageContributorCandidate)
	}
	if view.Raw.AttackerEntityID == nil || *view.Raw.AttackerEntityID != 10 {
		t.Fatalf("raw attacker was rewritten: %+v", view.Raw)
	}
	if view.Credit.CharacterID != nil || view.Credit.Status != ResolutionNotApplicable {
		t.Fatalf("a vehicle contributor must have no character credit and must not be a character-resolution failure: %+v", view.Credit)
	}
	if view.Actors.ActualSource.ActorType == "Vehicle" {
		t.Fatalf("the candidate rewrote the observed actual source: %+v", view.Actors.ActualSource)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["damageContributorCandidate"]; !ok {
		t.Fatalf("candidate field missing from JSON: %s", encoded)
	}
}
