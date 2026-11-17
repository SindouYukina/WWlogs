package devcapture

import (
	"testing"

	"wwlogs/internal/capture"
)

func TestDamageOwnersListSkillsAndBuffsAsStaticReferences(t *testing.T) {
	master := &capture.GameMaster{DamageOwners: map[uint64][]capture.DamageOwnerJSONL{
		1311291301: {{Kind: "buff", ID: 2002205305}, {Kind: "skill", ID: 1006106, RoleID: 1311, RoleName: "角色甲"}},
	}}
	owners := resolveDamageOwners(master, 1311291301, true)
	if owners == nil || owners.Status != capture.ResolutionCorrelated || owners.OwnerCount != 2 || owners.Owners[1].RoleName != "角色甲" {
		t.Fatalf("static owners expected: %+v", owners)
	}
	if resolveDamageOwners(master, 9, true) != nil || resolveDamageOwners(master, 1311291301, false) != nil || resolveDamageOwners(nil, 1, true) != nil {
		t.Fatal("an unreferenced or absent DamageId must have no owners")
	}
}
