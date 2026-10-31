//go:build wwdev

package capture

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

func TestPushDamageBlockListsAttackerLabelsAndStaysOutOfTotals(t *testing.T) {
	summary := &encounterSummary{pushDamage: map[string]*pushDamageSummary{
		"Party character 角色甲 [CharacterId:1]":                             {label: "Party character 角色甲 [CharacterId:1]", status: resolutionCorrelated, events: 2, damage: 300},
		"Scene entity 場景戰鬥實體 [Template:14000169] (no character credited)": {label: "Scene entity 場景戰鬥實體 [Template:14000169] (no character credited)", status: resolutionCorrelated, events: 5, damage: 900},
	}}
	var out bytes.Buffer
	if err := writePushDamage(&out, summary); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "not included in any total") || !strings.Contains(text, "2 event(s), magnitude 300") || !strings.Contains(text, "no character credited") {
		t.Fatalf("push block = %q", text)
	}
	if summary.combatResultAbs != 0 || summary.eventCount != 0 {
		t.Fatal("push rows must never touch the encounter totals")
	}
}
