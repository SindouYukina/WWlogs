//go:build wwdev

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVehicleDamageIsItsOwnSummaryRowNotACharacter(t *testing.T) {
	summary := &encounterSummary{characters: make(map[uint64]*characterSummary)}
	event := combatEvent{
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 5248,
		DamageContributorCandidate: &damageContributorCandidate{
			ActorType: "Vehicle", FollowerConfigID: 290101, Status: resolutionCorrelated,
			Name: &combatEventLabelTextJSONL{Text: "Vehicle: Motorcycle (Skill Use)", Kind: "unofficial-translation"},
		},
	}
	addVehicleContributorEvent(summary, event)
	addVehicleContributorEvent(summary, event)
	if len(summary.characters) != 0 || summary.unknownCredit != 0 {
		t.Fatalf("a vehicle row must not create a character or an uncredited event: %+v", summary)
	}
	var out bytes.Buffer
	if err := writeVehicleContributors(&out, summary); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "[internal] Vehicle: Motorcycle (Skill Use)") || !strings.Contains(text, "2 event(s), CombatResult magnitude 10496") || !strings.Contains(text, "CORRELATED") {
		t.Fatalf("vehicle row text = %q", text)
	}
}
