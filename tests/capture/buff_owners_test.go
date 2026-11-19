package capture

import (
	"database/sql"
	"testing"
)

func TestBuffOwnersComeOnlyFromExplicitReferenceTablesAndKnownBuffs(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE buff(id TEXT PRIMARY KEY)",
		"INSERT INTO buff VALUES('70001'),('70002'),('70003')",
		"CREATE TABLE weaponreson_effect(owner_id TEXT, ordinal INTEGER, value INTEGER)",
		"INSERT INTO weaponreson_effect VALUES('410',0,70001),('411',0,70001),('412',0,70001),('413',0,70001),('414',0,70001),('415',0,70001),('410',1,99999)",
		"CREATE TABLE weaponreson_locale(id TEXT, locale TEXT, name TEXT, text_key TEXT)",
		"INSERT INTO weaponreson_locale VALUES('410','zh-TW','定解',NULL),('411','zh-TW','定解',NULL),('412','zh-TW','定解',NULL),('413','zh-TW','定解',NULL),('414','zh-TW','定解',NULL),('415','zh-TW','定解',NULL),('410','en-US','Resolve',NULL)",
		"CREATE TABLE resonantchain_buff_ids(owner_id TEXT, ordinal INTEGER, value TEXT)",
		"INSERT INTO resonantchain_buff_ids VALUES('367',0,'70001'),('368',0,'70002'),('369',0,'70003')",
		"CREATE TABLE resonantchain_locale(id TEXT, locale TEXT, name TEXT, text_key TEXT)",
		"INSERT INTO resonantchain_locale VALUES('367','zh-TW','身入淒雨',NULL),('368','zh-TW','',NULL)",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}

	owners := ResolveBuffOwners(master, 70001)
	if owners == nil || owners.Status != ResolutionCorrelated || owners.OwnerCount != 2 || len(owners.Owners) != 2 {
		t.Fatalf("two named owner kinds expected: %+v", owners)
	}
	if owners.Owners[0].Kind != "resonantchain" || owners.Owners[0].Name != "身入淒雨" || owners.Owners[1].Kind != "weaponreson" || owners.Owners[1].Name != "定解" {
		t.Fatalf("owners must be ordered by kind and carry the locale name: %+v", owners.Owners)
	}
	if got := owners.Owners[1].OwnerIDs; len(got) != 6 || got[0] != 410 || got[5] != 415 {
		t.Fatalf("owner rows with the same name must be merged and sorted: %v", got)
	}
	if ResolveBuffOwners(master, 70002) != nil {
		t.Fatal("an owner whose locale name is empty must not produce text")
	}
	if ResolveBuffOwners(master, 99999) != nil {
		t.Fatal("a reference to an id missing from the buff table must be ignored")
	}
	if ResolveBuffOwners(master, 12345) != nil {
		t.Fatal("an unreferenced Buff must have no owners")
	}
}

func TestBuffOwnerListIsBoundedButKeepsTheCount(t *testing.T) {
	master := &GameMaster{buffOwners: map[uint64][]BuffOwnerText{}}
	for i := 0; i < 9; i++ {
		master.buffOwners[7] = append(master.buffOwners[7], BuffOwnerText{Kind: "weaponreson", Name: string(rune('A' + i)), OwnerIDs: []uint64{uint64(i)}})
	}
	owners := ResolveBuffOwners(master, 7)
	if owners.OwnerCount != 9 || len(owners.Owners) != maxBuffOwnerNames {
		t.Fatalf("owner list must be capped at %d but report 9: %+v", maxBuffOwnerNames, owners)
	}
}

func TestLifecycleRowsTakeOwnersFromTheLinkedBuffId(t *testing.T) {
	p := NewProcessor(nil, nil)
	p.Masters = &GameMaster{
		BuffDefinitions: map[uint64]struct{}{70001: {}, 70002: {}},
		buffOwners:      map[uint64][]BuffOwnerText{70001: {{Kind: "weaponreson", Name: "定解", OwnerIDs: []uint64{410}}}, 70002: {{Kind: "resonantchain", Name: "身入淒雨", OwnerIDs: []uint64{367}}}},
	}
	p.SessionNumber = 1

	apply := &BuffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY", ID: u64(70001), HandleID: u64(5), TargetEntityID: u64(500)}
	p.linkBuffLifecycleGenericAndHandle(apply)
	p.linkBuffLifecycleOwners(apply)
	if apply.BuffOwners == nil || apply.BuffOwners.Owners[0].Name != "定解" {
		t.Fatalf("an apply Id with a buff row must carry its owners: %+v", apply.BuffOwners)
	}
	duration := &BuffLifecycleAuditRow{EventKind: "BUFF_DURATION_CHANGE", HandleID: u64(5), CombatCommonEntityID: u64(500)}
	p.linkBuffLifecycleGenericAndHandle(duration)
	p.linkBuffLifecycleOwners(duration)
	if duration.BuffOwners == nil || duration.BuffOwners.Owners[0].Name != "定解" {
		t.Fatalf("a handle event must inherit the owners of the earlier apply: %+v", duration.BuffOwners)
	}
	typed := &BuffLifecycleAuditRow{EventKind: "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY", BuffID: u64(70002)}
	p.linkBuffLifecycleDefinition(typed)
	p.linkBuffLifecycleOwners(typed)
	if typed.BuffOwners == nil || typed.BuffOwners.Owners[0].Kind != "resonantchain" {
		t.Fatalf("a typed BuffId must carry its owners: %+v", typed.BuffOwners)
	}
	unlinked := &BuffLifecycleAuditRow{EventKind: "BUFF_DURATION_CHANGE", HandleID: u64(99), CombatCommonEntityID: u64(500)}
	p.linkBuffLifecycleGenericAndHandle(unlinked)
	p.linkBuffLifecycleOwners(unlinked)
	if unlinked.BuffOwners != nil {
		t.Fatalf("an unlinked handle must not get owners: %+v", unlinked.BuffOwners)
	}
}

func TestRoleBuffObservationNamesTheRoleEvenWithoutABuffDefinition(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE buff(id TEXT PRIMARY KEY)",
		"INSERT INTO buff VALUES('70001')",
		"CREATE TABLE role_buff_observation(role_id TEXT, buff_id TEXT, observation_kind TEXT, evidence_status TEXT)",
		"INSERT INTO role_buff_observation VALUES('9001','1312010090','active_self_instigated_duration_minus_one','CORRELATED'),('9001','70001','active_self_instigated_duration_minus_one','CORRELATED'),('9002','70001','active_self_instigated_duration_minus_one','CORRELATED')",
		"INSERT INTO roleinfo_locale VALUES('9001','zh-TW','角色甲',NULL),('9002','zh-TW','',NULL),('9002','en-US','Role B',NULL)",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	owners := ResolveBuffOwners(master, 1312010090)
	if owners == nil || owners.Status != ResolutionCorrelated || len(owners.Owners) != 1 || owners.Owners[0].Kind != "role_observation" || owners.Owners[0].Name != "角色甲" || owners.Owners[0].OwnerIDs[0] != 9001 {
		t.Fatalf("an observed role must name a Buff that has no static definition: %+v", owners)
	}
	if got := ResolveBuffOwners(master, 70001); got == nil || got.OwnerCount != 1 {
		t.Fatalf("a role whose locale name is empty must not produce text: %+v", got)
	}
}
