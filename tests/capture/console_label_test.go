//go:build wwdev

package capture

import (
	"strings"
	"testing"
)

func TestConsoleLineShowsDeveloperLabelAndSourceCharacter(t *testing.T) {
	event := combatEvent{
		SourceEntityID: 46742, HasSourceEntity: true, TargetEntityID: 46880, HasTargetEntity: true,
		SkillID: 1409253, SkillDevLabel: "Skill 1", SkillDevLabelKind: "unofficial-translation",
		DamageID: 140925101, HasDamageID: true, Damage: 10616, EventTypeCandidate: "DAMAGE_CANDIDATE",
		Source: entityIdentity{HasCharacter: true, CharacterStatus: resolutionVerified, Character: masterRecord{ID: 1409, Name: "Cartethyia", NameStatus: string(resolutionVerified)}},
	}
	line := event.formatNormal()
	if !strings.Contains(line, "[internal] Skill 1 [SkillId:1409253]") {
		t.Fatalf("a skill without an official name must show its developer label: %s", line)
	}
	if !strings.Contains(line, "Cartethyia [Char:1409] [Entity:46742]") {
		t.Fatalf("a character source must be named: %s", line)
	}

	named := event
	named.SkillName, named.SkillNameStatus, named.SkillStatus = "Official Name", resolutionVerified, resolutionVerified
	if line := named.formatNormal(); strings.Contains(line, "[internal]") || !strings.Contains(line, "Official Name [SkillId:1409253, VERIFIED]") {
		t.Fatalf("an official name must win over the developer label: %s", line)
	}
	bare := event
	bare.SkillDevLabel = ""
	if line := bare.formatNormal(); !strings.Contains(line, "SkillId:1409253  [DamageId") || strings.Contains(line, "[internal]") {
		t.Fatalf("without a label the line keeps the plain SkillId: %s", line)
	}
}
