package capture

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A trial character plays its parent role: names, Outro and skill rows come from the parent, statistics keep its RoleId.
func TestTrialRoleReadsItsParentRole(t *testing.T) {
	master := testSkillNameMaster()
	master.trialRoleParents = map[uint64]uint64{13127004: 1312}
	master.characters = map[uint64]masterRecord{1312: {ID: 1312, Name: "鎖暝", NameStatus: string(resolutionVerified)}}
	if master.staticRoleID(13127004) != 1312 || master.staticRoleID(1312) != 1312 || master.staticRoleID(0) != 0 {
		t.Fatal("staticRoleID does not follow TrialRoleInfo.ParentId")
	}
	var none *gameMaster
	if none.staticRoleID(13127004) != 13127004 {
		t.Fatal("without a Master a RoleId must stay as it is")
	}
	skills := []SkillDamageSnapshot{{SkillID: 1312050, DamageDealt: 80, OutgoingEvents: 2, DamageIDs: []uint64{9001, 9002}}}
	view, _ := projectSkills(master, 13127004, skills, UnassignedSkillTotals{}, 80)
	if view[0].Name != "暝傘形·重鎖獄瘴" || view[0].NameStatus != "DERIVED" || view[0].NameSource != "damage_list" || view[0].DamageDealt != 80 {
		t.Fatalf("trial role skill row = %+v, want the parent role's UI skill", view[0])
	}
	if name, status := roleDisplayName(master, 13127004); name != "鎖暝" || status != string(resolutionVerified) {
		t.Fatalf("trial role name = %q %s", name, status)
	}
	// A role that is not a trial keeps its own lookups: another role's UI skill still never names its rows.
	view, _ = projectSkills(master, 1508, skills, UnassignedSkillTotals{}, 80)
	if view[0].NameStatus != "UNKNOWN" {
		t.Fatalf("a non-trial role was named through another role: %+v", view[0])
	}
}

func TestDisplayResolverNamesATrialRoleByItsParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE roleinfo (id TEXT PRIMARY KEY, element_id TEXT)",
		"CREATE TABLE roleinfo_locale (id TEXT, locale TEXT, name TEXT, PRIMARY KEY(id, locale))",
		"CREATE TABLE trialroleinfo (id TEXT PRIMARY KEY, group_id TEXT, type INTEGER, parent_role_id TEXT)",
		"INSERT INTO roleinfo VALUES ('1512', '5')",
		"INSERT INTO roleinfo_locale VALUES ('1512', 'zh-TW', '莉莉'), ('1512', 'en-US', 'Lily')",
		"INSERT INTO trialroleinfo VALUES ('15127004', '15127004', 1, '1512')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	resolver, err := OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	ctx := context.Background()
	for _, id := range []uint64{1512, 15127004} {
		name, err := resolver.Resolve(ctx, "role", id, "zh-TW")
		if err != nil || name.Name != "莉莉" || name.Status != "VERIFIED" {
			t.Fatalf("role %d name = %+v %v", id, name, err)
		}
		if element := resolver.RoleElement(ctx, id); element != 5 {
			t.Fatalf("role %d element = %d, want the parent's 5", id, element)
		}
		// The character art follows the same relation: a trial character shows its parent's avatar and portrait.
		if static := resolver.StaticRole(ctx, id); static != 1512 {
			t.Fatalf("role %d static role = %d, want 1512", id, static)
		}
	}
	if name, _ := resolver.Resolve(ctx, "role", 99999999, "zh-TW"); name.Status != "UNKNOWN" {
		t.Fatalf("an unknown role was named: %+v", name)
	}
	if static := resolver.StaticRole(ctx, 99999999); static != 99999999 {
		t.Fatalf("a role that is not a trial must keep its own art, got %d", static)
	}
	// The trial relation is a role relation only: no other name kind follows it.
	if name, _ := resolver.Resolve(ctx, "monster", 15127004, "zh-TW"); name.Status != "UNKNOWN" {
		t.Fatalf("a non-role lookup followed the trial relation: %+v", name)
	}
}
