package desktop

import (
	"testing"

	"wwlogs/internal/capture"
)

// A career record names its skill the way the encounter's own skill table does: by the role it shows as, with a derived
// name kept derived, a verified name preferred, and an unnamed row left to the Master's own name or UNKNOWN.
func TestCareerRecordSkillsAreNamedAsTheirEncounterNamesThem(t *testing.T) {
	view := &capture.ViewEncounter{Roles: []capture.ViewRole{
		{RoleID: 15127004, StaticRoleID: 1512, Skills: []capture.ViewSkill{
			{SkillID: 15124104, Name: "Derived row", NameStatus: "DERIVED"},
			{SkillID: 1508500, Name: "Echo hit", NameStatus: "DERIVED"},
			{SkillID: 99, NameStatus: "UNKNOWN"},
		}},
		{RoleID: 1512, StaticRoleID: 1512, Skills: []capture.ViewSkill{{SkillID: 15124104, Name: "Basic", NameStatus: "VERIFIED"}}},
		{RoleID: 1409, Skills: []capture.ViewSkill{{SkillID: 140901, Name: "Skill", NameStatus: "VERIFIED"}}},
	}}
	names := careerSkillNames(view)
	if name := names[careerSkillKey{1512, 15124104}]; name.Name != "Basic" || name.Status != "VERIFIED" {
		t.Fatalf("a verified name must win over a derived one: %+v", name)
	}
	if name := names[careerSkillKey{1512, 1508500}]; name.Name != "Echo hit" || name.Status != "DERIVED" {
		t.Fatalf("a trial role's derived name must stay derived under its parent: %+v", name)
	}
	if _, named := names[careerSkillKey{1512, 99}]; named {
		t.Fatal("an unnamed row was given a name")
	}
	// A role without a static role is keyed by its own RoleId.
	if name := names[careerSkillKey{1409, 140901}]; name.Name != "Skill" {
		t.Fatalf("role 1409 = %+v", name)
	}
}
