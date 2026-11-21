package capture

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// A trial character plays its parent role: names, Outro and skill rows come from the parent, statistics keep its RoleId.
func TestTrialRoleReadsItsParentRole(t *testing.T) {
	master := testSkillNameMaster()
	master.TrialRoleParents = map[uint64]uint64{13127004: 1312}
	master.Characters = map[uint64]MasterRecord{1312: {ID: 1312, Name: "鎖暝", NameStatus: string(ResolutionVerified)}}
	if master.StaticRoleID(13127004) != 1312 || master.StaticRoleID(1312) != 1312 || master.StaticRoleID(0) != 0 {
		t.Fatal("staticRoleID does not follow TrialRoleInfo.ParentId")
	}
	var none *GameMaster
	if none.StaticRoleID(13127004) != 13127004 {
		t.Fatal("without a Master a RoleId must stay as it is")
	}
	skills := []SkillDamageSnapshot{{SkillID: 1312050, DamageDealt: 80, OutgoingEvents: 2, DamageIDs: []uint64{9001, 9002}}}
	view, _ := projectSkills(master, 13127004, skills, UnassignedSkillTotals{}, 80)
	if view[0].Name != "暝傘形·重鎖獄瘴" || view[0].NameStatus != "DERIVED" || view[0].NameSource != "damage_list" || view[0].DamageDealt != 80 {
		t.Fatalf("trial role skill row = %+v, want the parent role's UI skill", view[0])
	}
	if name, status := roleDisplayName(master, 13127004); name != "鎖暝" || status != string(ResolutionVerified) {
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
	completeMasterFixture(t, path)
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
		if element, err := resolver.RoleElement(ctx, id); err != nil || element != 5 {
			t.Fatalf("role %d element = %d %v, want the parent's 5", id, element, err)
		}
		// The character art follows the same relation: a trial character shows its parent's avatar and portrait.
		if static, err := resolver.StaticRole(ctx, id); err != nil || static != 1512 {
			t.Fatalf("role %d static role = %d %v, want 1512", id, static, err)
		}
	}
	if name, err := resolver.Resolve(ctx, "role", 99999999, "zh-TW"); err != nil || name.Status != "UNKNOWN" {
		t.Fatalf("an unknown role was named: %+v %v", name, err)
	}
	if static, err := resolver.StaticRole(ctx, 99999999); err != nil || static != 99999999 {
		t.Fatalf("a role that is not a trial must keep its own art, got %d %v", static, err)
	}
	// The trial relation is a role relation only: no other name kind follows it.
	if name, _ := resolver.Resolve(ctx, "monster", 15127004, "zh-TW"); name.Status != "UNKNOWN" {
		t.Fatalf("a non-role lookup followed the trial relation: %+v", name)
	}
}

// A role lookup separates what the Master does not list (no trial row, no roleinfo row, a role without an element: no
// error) from what it cannot answer (a failed read, or a parent or element that is not an ID: an error). Neither error is
// cached: a lookup that failed because its context ended answers correctly once asked again.
func TestDisplayResolverRoleLookupsReportFailuresWithoutCachingThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE roleinfo (id TEXT PRIMARY KEY, element_id TEXT)",
		"CREATE TABLE roleinfo_locale (id TEXT, locale TEXT, name TEXT, PRIMARY KEY(id, locale))",
		"CREATE TABLE trialroleinfo (id TEXT PRIMARY KEY, group_id TEXT, type INTEGER, parent_role_id TEXT)",
		"INSERT INTO roleinfo VALUES ('1512', '5'), ('1513', NULL), ('1514', ''), ('1515', 'fire')",
		"INSERT INTO roleinfo_locale VALUES ('1512', 'zh-TW', '莉莉')",
		"INSERT INTO trialroleinfo VALUES ('15127004', '15127004', 1, '1512'), ('15127005', '15127005', 1, NULL), ('15127006', '15127006', 1, 'x'), ('15127007', '15127007', 1, '0')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	completeMasterFixture(t, path)
	resolver, err := OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	ctx := context.Background()

	// A context that has ended is a failed read, and the next lookup reads the tables again (before, the trial was
	// cached as a role of its own: its own art, no element and no name for the rest of the session).
	ended, cancel := context.WithCancel(ctx)
	cancel()
	if static, err := resolver.StaticRole(ended, 15127004); !errors.Is(err, errRoleTables) || static != 15127004 {
		t.Fatalf("ended context: static role %d %v, want the RoleId itself with errRoleTables", static, err)
	}
	if element, err := resolver.RoleElement(ended, 15127004); !errors.Is(err, errRoleTables) || element != 0 {
		t.Fatalf("ended context: element %d %v, want 0 with errRoleTables", element, err)
	}
	if name, err := resolver.Resolve(ended, "role", 15127004, "zh-TW"); !errors.Is(err, errRoleTables) || name.Name != "" || name.Status != "UNKNOWN" {
		t.Fatalf("ended context: name %+v %v, want no name with errRoleTables", name, err)
	}
	if static, err := resolver.StaticRole(ctx, 15127004); err != nil || static != 1512 {
		t.Fatalf("after a failed read: static role %d %v, want 1512", static, err)
	}
	if element, err := resolver.RoleElement(ctx, 15127004); err != nil || element != 5 {
		t.Fatalf("after a failed read: element %d %v, want 5", element, err)
	}
	if name, err := resolver.Resolve(ctx, "role", 15127004, "zh-TW"); err != nil || name.Name != "莉莉" {
		t.Fatalf("after a failed read: name %+v %v, want the parent's name", name, err)
	}

	// What the Master does not list is no error: the role is not a trial, or has no element (the neutral color).
	for _, c := range []struct {
		role, static uint64
	}{{99999999, 99999999}, {1513, 1513}, {1514, 1514}} {
		if static, err := resolver.StaticRole(ctx, c.role); err != nil || static != c.static {
			t.Fatalf("role %d: static role %d %v, want %d", c.role, static, err, c.static)
		}
		if element, err := resolver.RoleElement(ctx, c.role); err != nil || element != 0 {
			t.Fatalf("role %d: element %d %v, want 0 without an error", c.role, element, err)
		}
	}

	// A value that is not an ID is a broken Master, never a missing row: a NULL, text or zero parent (the loader refuses
	// such a trial row) and an element that is not a number.
	for _, trial := range []uint64{15127005, 15127006, 15127007} {
		if static, err := resolver.StaticRole(ctx, trial); !errors.Is(err, errRoleValue) || static != trial {
			t.Fatalf("trial %d: static role %d %v, want the RoleId with errRoleValue", trial, static, err)
		}
		if _, err := resolver.RoleElement(ctx, trial); !errors.Is(err, errRoleValue) {
			t.Fatalf("trial %d: element error %v, want errRoleValue", trial, err)
		}
		if name, err := resolver.Resolve(ctx, "role", trial, "zh-TW"); !errors.Is(err, errRoleValue) || name.Status != "UNKNOWN" {
			t.Fatalf("trial %d: name %+v %v, want errRoleValue", trial, name, err)
		}
		// Asked again, the broken row is read again: the error was not cached as an answer.
		if _, err := resolver.StaticRole(ctx, trial); !errors.Is(err, errRoleValue) {
			t.Fatalf("trial %d: second lookup %v, want errRoleValue again", trial, err)
		}
	}
	if element, err := resolver.RoleElement(ctx, 1515); !errors.Is(err, errRoleValue) || element != 0 {
		t.Fatalf("element 'fire': %d %v, want 0 with errRoleValue", element, err)
	}

	// A failed SQLite read is errRoleTables; answers read before it stay cached.
	resolver.db.Close()
	if _, err := resolver.StaticRole(ctx, 1409); !errors.Is(err, errRoleTables) {
		t.Fatalf("closed database: static role error %v, want errRoleTables", err)
	}
	if _, err := resolver.RoleElement(ctx, 1409); !errors.Is(err, errRoleTables) {
		t.Fatalf("closed database: element error %v, want errRoleTables", err)
	}
	if static, err := resolver.StaticRole(ctx, 15127004); err != nil || static != 1512 {
		t.Fatalf("closed database: cached static role %d %v, want 1512", static, err)
	}
}
