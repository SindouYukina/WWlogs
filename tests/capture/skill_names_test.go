package capture

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

// writeTestSkillHit writes one paired damage result whose request carried DamageContext.SkillId and DamageId.
func writeTestSkillHit(p *processor, rpcID, skillID, damageID uint64, amount int64) {
	event := testDamageEvent(p, 100, 200)
	event.EventTypeCandidate, event.Damage, event.RPCID, event.SkillID = "DAMAGE_CANDIDATE", amount, rpcID, skillID
	event.DamageID, event.HasDamageID = damageID, damageID != 0
	p.writeDamageEvent(event)
}

func TestSkillRowKeepsDistinctSortedDamageIDsWithoutChangingTotals(t *testing.T) {
	p := newPairingTestProcessor()
	writeTestSkillHit(p, 1, 1312050, 1312050202, 100)
	writeTestSkillHit(p, 2, 1312050, 1312050201, 150)
	writeTestSkillHit(p, 3, 1312050, 1312050202, 50)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	want := SkillDamageSnapshot{SkillID: 1312050, DamageDealt: 300, OutgoingEvents: 3, DamageIDs: []uint64{1312050201, 1312050202}}
	if skill := skillOf(t, roleOf(t, got, 1409).Skills, 1312050); !reflect.DeepEqual(skill, want) {
		t.Fatalf("skill row = %+v, want %+v", skill, want)
	}
}

func TestSkillRowMarksDamageIDsIncompleteForMissingOrTooManyIDs(t *testing.T) {
	var missing skillAggregate
	missing.add(11, 501, 10)
	missing.add(11, 0, 10)
	var many skillAggregate
	for i := uint64(0); i <= maxDamageIDsPerSkill; i++ {
		many.add(22, 1000+i, 1)
	}
	missingRows, _ := missing.snapshot()
	manyRows, _ := many.snapshot()
	if row := missingRows[0]; !row.DamageIDsIncomplete || !reflect.DeepEqual(row.DamageIDs, []uint64{501}) || row.OutgoingEvents != 2 || row.DamageDealt != 20 {
		t.Fatalf("hit without a DamageId = %+v", row)
	}
	if row := manyRows[0]; !row.DamageIDsIncomplete || len(row.DamageIDs) != maxDamageIDsPerSkill || row.OutgoingEvents != maxDamageIDsPerSkill+1 {
		t.Fatalf("bounded DamageIds = %d incomplete=%v events=%d", len(row.DamageIDs), row.DamageIDsIncomplete, row.OutgoingEvents)
	}
	// A snapshot is an independent copy: later hits must not show up in an earlier snapshot.
	before, _ := missing.snapshot()
	missing.add(11, 400, 1)
	if !reflect.DeepEqual(before[0].DamageIDs, []uint64{501}) {
		t.Fatalf("snapshot shares its DamageIds with the aggregate: %v", before[0].DamageIDs)
	}
}

// testSkillNameMaster has role 1312 with UI skill 1006203 (damage 9001, 9002), UI skill 1006201 (damage 9003), a DamageId
// 9004 that two UI skills list, and an Echo skill 280026010 named by PhantomItem 60001062.
func testSkillNameMaster() *gameMaster {
	names := newSkillNameMaster()
	names.damageListSkills = map[uint64]uint64{9001: 1006203, 9002: 1006203, 9003: 1006201, 9005: 1004207}
	names.uiSkillRoles = map[uint64]map[uint64]struct{}{1006203: {1312: {}}, 1006201: {1312: {}}, 1004207: {1508: {}}}
	names.uiSkillNames = map[uint64]string{1006203: "暝傘形·重鎖獄瘴", 1006201: "攏傘形", 1004207: "解弦視界·萬理歸塵"}
	names.echoItems = map[uint64]uint64{280026010: 60001062}
	names.echoNames = map[uint64]string{60001062: "共鳴迴響·芙露德莉斯"}
	return &gameMaster{skills: map[uint64]masterRecord{
		1312011: {ID: 1312011, Name: "攏傘形", NameStatus: string(resolutionVerified)},
		1312050: {ID: 1312050, NameStatus: string(resolutionUnknown)},
	}, skillNames: names}
}

func TestSkillRowNameSources(t *testing.T) {
	master := testSkillNameMaster()
	cases := []struct {
		name     string
		roleID   uint64
		skill    SkillDamageSnapshot
		wantName string
		status   string
		source   string
		sourceID uint64
	}{
		{"official name wins over a damage list", 1312, SkillDamageSnapshot{SkillID: 1312011, DamageIDs: []uint64{9001}}, "攏傘形", "VERIFIED", "official", 0},
		{"every DamageId listed by one UI skill of the role", 1312, SkillDamageSnapshot{SkillID: 1312050, DamageIDs: []uint64{9001, 9002}}, "暝傘形·重鎖獄瘴", "DERIVED", "damage_list", 1006203},
		{"DamageIds split across UI skills", 1312, SkillDamageSnapshot{SkillID: 1312050, DamageIDs: []uint64{9001, 9003}}, "", "UNKNOWN", "", 0},
		{"a DamageId no single UI skill lists", 1312, SkillDamageSnapshot{SkillID: 1312050, DamageIDs: []uint64{9001, 9004}}, "", "UNKNOWN", "", 0},
		{"a hit without a DamageId", 1312, SkillDamageSnapshot{SkillID: 1312050, DamageIDs: []uint64{9001}, DamageIDsIncomplete: true}, "", "UNKNOWN", "", 0},
		{"the UI skill belongs to another role", 1312, SkillDamageSnapshot{SkillID: 1508404, DamageIDs: []uint64{9005}}, "", "UNKNOWN", "", 0},
		{"a vehicle has no skill group", 0, SkillDamageSnapshot{SkillID: 1312050, DamageIDs: []uint64{9001}}, "", "UNKNOWN", "", 0},
		{"no DamageIds at all", 1312, SkillDamageSnapshot{SkillID: 1312050}, "", "UNKNOWN", "", 0},
		{"an Echo skill with one shared Echo name", 1409, SkillDamageSnapshot{SkillID: 280026010, DamageIDs: []uint64{28002601}}, "共鳴迴響·芙露德莉斯", "DERIVED", "echo", 60001062},
	}
	for _, c := range cases {
		name, status, source, sourceID := master.skillRowName(c.roleID, c.skill)
		if name != c.wantName || status != c.status || source != c.source || sourceID != c.sourceID {
			t.Errorf("%s: got (%q, %s, %q, %d), want (%q, %s, %q, %d)", c.name, name, status, source, sourceID, c.wantName, c.status, c.source, c.sourceID)
		}
	}
	var none *gameMaster
	if name, status, source, _ := none.skillRowName(1312, SkillDamageSnapshot{SkillID: 1}); name != "" || status != "UNKNOWN" || source != "" {
		t.Fatalf("no Master = (%q, %s, %q)", name, status, source)
	}
}

func TestViewSkillsCarryTheNameSource(t *testing.T) {
	p := newPairingTestProcessor()
	p.masters = testSkillNameMaster()
	skills := []SkillDamageSnapshot{
		{SkillID: 1312050, DamageDealt: 80, OutgoingEvents: 2, DamageIDs: []uint64{9001, 9002}},
		{SkillID: 1312011, DamageDealt: 20, OutgoingEvents: 1, DamageIDs: []uint64{9003}},
	}
	view, _ := p.viewSkills(1312, skills, UnassignedSkillTotals{}, 100)
	if view[0].Name != "暝傘形·重鎖獄瘴" || view[0].NameStatus != "DERIVED" || view[0].NameSource != "damage_list" || view[0].SourceID != 1006203 || view[0].DamageDealt != 80 {
		t.Fatalf("derived row = %+v", view[0])
	}
	if view[1].Name != "攏傘形" || view[1].NameStatus != "VERIFIED" || view[1].NameSource != "official" || view[1].SourceID != 0 {
		t.Fatalf("official row = %+v", view[1])
	}
	vehicle, _ := p.viewSkills(0, skills[:1], UnassignedSkillTotals{}, 80)
	if vehicle[0].Name != "" || vehicle[0].NameStatus != "UNKNOWN" || vehicle[0].NameSource != "" {
		t.Fatalf("vehicle row was named through a role skill group: %+v", vehicle[0])
	}
}

func TestSkillNameSourcesLoaderExcludesAmbiguousRelations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE skill(id TEXT PRIMARY KEY,skill_group_id TEXT)",
		"CREATE TABLE skill_locale(id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE role_skill_group_ref(role_id TEXT,skill_group_id TEXT,PRIMARY KEY(role_id,skill_group_id))",
		"CREATE TABLE skill_damage_ref(skill_id TEXT,ordinal INTEGER,damage_id TEXT,PRIMARY KEY(skill_id,ordinal))",
		"CREATE TABLE vision(id TEXT PRIMARY KEY,skill_id TEXT,birth_cast_skill_id TEXT)",
		"CREATE TABLE phantomitem(id TEXT PRIMARY KEY,skill_id TEXT)",
		"CREATE TABLE phantomitem_locale(id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))",
		"INSERT INTO skill VALUES('1006203','1312'),('1006201','1312')",
		"INSERT INTO skill_locale VALUES('1006203','en-US','Liberation'),('1006203','zh-TW','解放'),('1006201','en-US','Basic')",
		"INSERT INTO role_skill_group_ref VALUES('1312','1312')",
		// 9001 belongs to one UI skill; 9004 is listed by two and must not name anything.
		"INSERT INTO skill_damage_ref VALUES('1006203',0,'9001'),('1006203',1,'9004'),('1006201',0,'9004')",
		// Vision 280026: one Echo name in both locales. Vision 280013 and 281013 share skill 280013010 with different names.
		// Vision 280099 has items whose names agree in zh-TW but differ in en-US. Vision.SkillId 2000003 is shared by two
		// differently named Echoes, as in the real Master.
		// Visions 280045 and 281045 cast the same skill and name one Echo: the row belongs to the item the name came from.
		"INSERT INTO vision VALUES('280026','2000003','280026010'),('280027','2000003',NULL),('280013','280013010',NULL),('281013','280013010',NULL),('280099','280099010',NULL),('280045','28004502',NULL),('281045','28004502',NULL)",
		"INSERT INTO phantomitem VALUES('60001062','280026'),('60001063','280026'),('60001072','280027'),('60000602','280013'),('60200103','281013'),('60009901','280099'),('60009902','280099'),('60002252','280045'),('60102255','281045')",
		"INSERT INTO phantomitem_locale VALUES('60002252','en-US','Echo'),('60002252','zh-TW','迴響'),('60102255','en-US','Echo'),('60102255','zh-TW','迴響')",
		"INSERT INTO phantomitem_locale VALUES('60001062','en-US','Reminiscence: Fleurdelys'),('60001062','zh-TW','共鳴迴響·芙露德莉斯'),('60001063','en-US','Reminiscence: Fleurdelys'),('60001063','zh-TW','共鳴迴響·芙露德莉斯')",
		"INSERT INTO phantomitem_locale VALUES('60001072','en-US','Other Echo'),('60000602','en-US','Fallacy of No Return'),('60200103','en-US','Heterized Armament')",
		"INSERT INTO phantomitem_locale VALUES('60009901','en-US','A'),('60009901','zh-TW','同'),('60009902','en-US','B'),('60009902','zh-TW','同')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	master := &gameMaster{}
	if err := loadSQLiteSkillNameSources(db, "zh-TW", master); err != nil {
		t.Fatal(err)
	}
	db.Close()
	names := master.skillNames
	if !reflect.DeepEqual(names.damageListSkills, map[uint64]uint64{9001: 1006203}) {
		t.Fatalf("damage list = %v, want only the unambiguous DamageId", names.damageListSkills)
	}
	if _, ok := names.uiSkillRoles[1006203][1312]; !ok {
		t.Fatalf("UI skill roles = %v", names.uiSkillRoles)
	}
	if names.uiSkillNames[1006203] != "解放" || names.uiSkillNames[1006201] != "Basic" {
		t.Fatalf("UI skill names = %v, want zh-TW with en-US fallback", names.uiSkillNames)
	}
	if !reflect.DeepEqual(names.echoItems, map[uint64]uint64{280026010: 60001062, 28004502: 60002252}) {
		t.Fatalf("echo items = %v, want only the Echo skills whose candidates share one name per locale", names.echoItems)
	}
	// An icon comes from the Vision of the item that names the row; a skill two different Echoes share has neither.
	if !reflect.DeepEqual(names.echoVisions, map[uint64]uint64{280026010: 280026, 28004502: 280045}) {
		t.Fatalf("echo visions = %v", names.echoVisions)
	}
	if names.echoNames[60001062] != "共鳴迴響·芙露德莉斯" {
		t.Fatalf("echo names = %v", names.echoNames)
	}
}

func TestSkillNameSourcesLoaderToleratesAnOlderMaster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE skill(id TEXT PRIMARY KEY,skill_group_id TEXT)"); err != nil {
		t.Fatal(err)
	}
	master := &gameMaster{}
	if err := loadSQLiteSkillNameSources(db, "zh-TW", master); err != nil {
		t.Fatal(err)
	}
	if master.skillNames == nil || len(master.skillNames.damageListSkills) != 0 || len(master.skillNames.echoItems) != 0 {
		t.Fatalf("a Master without the relations named rows: %+v", master.skillNames)
	}
}

// TestTrackedMasterDerivesObservedSkillNames pins derivations observed in archived captures to the tracked Master: the
// DamageIds are the ones the paired requests of those captures carried.
func TestTrackedMasterDerivesObservedSkillNames(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	master, err := loadMasterDataFromDatabase(db, "embedded", "test", "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		roleID   uint64
		skill    SkillDamageSnapshot
		source   string
		sourceID uint64
		wantName string
	}{
		{1312, SkillDamageSnapshot{SkillID: 1312050, DamageIDs: []uint64{1312050201, 1312050202}}, "damage_list", 1006203, "暝傘形·重鎖獄瘴"},
		{1508, SkillDamageSnapshot{SkillID: 1508404, DamageIDs: []uint64{15084040013, 15084040023}}, "damage_list", 1004207, "解弦視界·萬理歸塵"},
		{1209, SkillDamageSnapshot{SkillID: 1209200, DamageIDs: []uint64{120920002}}, "damage_list", 1004403, "臨界協議"},
		{1409, SkillDamageSnapshot{SkillID: 280026010, DamageIDs: []uint64{28002601, 28002602}}, "echo", 60001062, "共鳴迴響·芙露德莉斯"},
		// A row reaches one Echo through a variant family, a developer label, or the Echo's own actor blueprint.
		{1209, SkillDamageSnapshot{SkillID: 280034001, DamageIDs: []uint64{28003401}}, "echo", 60001902, "爐芯機骸"},
		{1508, SkillDamageSnapshot{SkillID: 230006, DamageIDs: []uint64{28003301}}, "echo", 60001672, "共鳴迴響·鳴式·利維亞坦"},
		{1312, SkillDamageSnapshot{SkillID: 28004502, DamageIDs: []uint64{280045012, 280045013}}, "echo", 60002252, "共鳴迴響·天演溯心"},
		// Tune Break: the genre, the damage type and the role's one Tune Break skill.
		{1508, SkillDamageSnapshot{SkillID: 1508991, DamageIDs: []uint64{1000009901, 1000009902, 1000009903}}, "tune_break", 1004210, "諧度破壞·長刃"},
		{1209, SkillDamageSnapshot{SkillID: 1209991, DamageIDs: []uint64{1000009901, 1000009903}}, "tune_break", 1004410, "解耦"},
		// The same genre on a role whose Tune Break skill is another role's stays unnamed through damage it does not own.
		{1312, SkillDamageSnapshot{SkillID: 1508991, DamageIDs: []uint64{28003401}}, "", 0, ""},
		// Mixed UI skills and an unlisted DamageId stay unnamed.
		{1102, SkillDamageSnapshot{SkillID: 1102030, DamageIDs: []uint64{1102030002, 1102030010}}, "", 0, ""},
		// A role must own the UI skill: Chisa's DamageIds never name a Lockdawn row.
		{1312, SkillDamageSnapshot{SkillID: 1508404, DamageIDs: []uint64{15084040013}}, "", 0, ""},
	}
	for _, c := range cases {
		name, status, source, sourceID := master.skillRowName(c.roleID, c.skill)
		wantStatus := "DERIVED"
		if c.source == "" {
			wantStatus = "UNKNOWN"
		}
		if name != c.wantName || source != c.source || sourceID != c.sourceID || status != wantStatus {
			t.Errorf("role %d skill %d: got (%q, %s, %q, %d), want (%q, %s, %q, %d)", c.roleID, c.skill.SkillID, name, status, source, sourceID, c.wantName, wantStatus, c.source, c.sourceID)
		}
	}
	if name, status, source, _ := master.skillRowName(1312, SkillDamageSnapshot{SkillID: 1312011, DamageIDs: []uint64{1312011001}}); name != "攏傘形" || status != "VERIFIED" || source != "official" {
		t.Fatalf("an official name changed: (%q, %s, %q)", name, status, source)
	}
	// Hits without a SkillId are named only when every DamageId belongs to one Echo's common bullets.
	unassigned := UnassignedSkillTotals{DamageDealt: 10, OutgoingEvents: 1, MissingSkillEvents: 1, DamageIDs: []uint64{28003302}}
	if name, item, vision, ok := master.unassignedSkillEcho(unassigned); !ok || name != "共鳴迴響·鳴式·利維亞坦" || item != 60001672 || vision != 280033 {
		t.Fatalf("unassigned Echo = (%q, %d, %d, %v)", name, item, vision, ok)
	}
	if _, _, _, ok := master.unassignedSkillEcho(UnassignedSkillTotals{DamageIDs: []uint64{28003302, 1409011001}}); ok {
		t.Fatal("a DamageId of no Echo must not name the row")
	}
	if _, _, _, ok := master.unassignedSkillEcho(UnassignedSkillTotals{DamageIDs: []uint64{28003302}, DamageIDsIncomplete: true}); ok {
		t.Fatal("an incomplete list must not name the row")
	}
	if kind, id := master.skillRowIcon(SkillDamageSnapshot{SkillID: 280026010}, skillNameSourceEcho, 60001062); kind != skillIconKindEchoSkill || id != 280026 {
		t.Fatalf("Echo icon = %s/%d", kind, id)
	}
	// A skill several Visions cast takes the icon of the base Echo that names it, for any role that uses that Echo.
	for _, c := range []struct{ skill, vision uint64 }{{230006, 280033}, {280034001, 280034}, {28004502, 280045}, {28004501, 280045}, {280010010, 280010}} {
		if kind, id := master.skillRowIcon(SkillDamageSnapshot{SkillID: c.skill}, skillNameSourceEcho, 0); kind != skillIconKindEchoSkill || id != c.vision {
			t.Errorf("skill %d Echo icon = %s/%d, want %d", c.skill, kind, id, c.vision)
		}
	}
	if kind, id := master.skillRowIcon(SkillDamageSnapshot{SkillID: 1409011}, skillNameSourceEcho, 0); kind != "" || id != 0 {
		t.Fatalf("a skill of no Echo got an Echo icon: %s/%d", kind, id)
	}
	if kind, id := master.skillRowIcon(SkillDamageSnapshot{SkillID: 1508991}, skillNameSourceTuneBreak, 1004210); kind != skillIconKindSkillConfig || id != 1004210 {
		t.Fatalf("Tune Break icon = %s/%d", kind, id)
	}
	if kind, id := master.skillRowIcon(SkillDamageSnapshot{SkillID: 1312050}, "", 0); kind != skillIconKindSkill || id != 1312050 {
		t.Fatalf("unnamed row icon = %s/%d", kind, id)
	}
}

func TestResolveEchoItems(t *testing.T) {
	item := func(id, monster, parent uint64, zh, en string) echoItem {
		return echoItem{id: id, monster: monster, parent: parent, names: map[string]string{"zh-TW": zh, "en-US": en}}
	}
	cases := []struct {
		name  string
		items []echoItem
		want  uint64
		ok    bool
	}{
		{"one name for every candidate", []echoItem{item(3, 30, 0, "甲", "A"), item(2, 20, 0, "甲", "A")}, 2, true},
		{"a variant names the base", []echoItem{item(60101905, 6010190, 6000190, "異相·乙", "Phantom: B"), item(60001903, 6000190, 0, "乙", "B"), item(60001902, 6000190, 0, "乙", "B")}, 60001902, true},
		{"two different Echoes stay unnamed", []echoItem{item(1, 10, 0, "甲", "A"), item(2, 20, 0, "乙", "B")}, 0, false},
		{"a variant of another monster stays unnamed", []echoItem{item(1, 10, 0, "甲", "A"), item(2, 22, 99, "異", "X")}, 0, false},
		{"only variants have no base to name them", []echoItem{item(2, 22, 10, "異", "X"), item(3, 33, 10, "另", "Y")}, 0, false},
		{"no monster data cannot prove a family", []echoItem{item(1, 0, 0, "甲", "A"), item(2, 0, 0, "乙", "B")}, 0, false},
		{"nothing", nil, 0, false},
	}
	for _, c := range cases {
		if got, ok := resolveEchoItems(c.items); got != c.want || ok != c.ok {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestTuneBreakNeedsTheGenreEveryDamageIdAndOneRoleSkill(t *testing.T) {
	names := newSkillNameMaster()
	names.tuneBreakSkills = map[uint64]struct{}{1508991: {}}
	names.tuneBreakDamage = map[uint64]struct{}{1000009901: {}, 1000009902: {}}
	names.roleTuneBreak = map[uint64]uint64{1508: 1004210}
	cases := []struct {
		name   string
		roleID uint64
		skill  SkillDamageSnapshot
		ok     bool
	}{
		{"every condition holds", 1508, SkillDamageSnapshot{SkillID: 1508991, DamageIDs: []uint64{1000009901, 1000009902}}, true},
		{"not the genre", 1508, SkillDamageSnapshot{SkillID: 1508000, DamageIDs: []uint64{1000009901}}, false},
		{"another damage type among the hits", 1508, SkillDamageSnapshot{SkillID: 1508991, DamageIDs: []uint64{1000009901, 15084040013}}, false},
		{"a hit without a DamageId", 1508, SkillDamageSnapshot{SkillID: 1508991, DamageIDs: []uint64{1000009901}, DamageIDsIncomplete: true}, false},
		{"a role without exactly one Tune Break skill", 1409, SkillDamageSnapshot{SkillID: 1508991, DamageIDs: []uint64{1000009901}}, false},
		{"a vehicle", 0, SkillDamageSnapshot{SkillID: 1508991, DamageIDs: []uint64{1000009901}}, false},
	}
	for _, c := range cases {
		if _, ok := names.tuneBreakSkill(c.roleID, c.skill); ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
		}
	}
}

func TestViewSkillsNameTheEchoOfAnUnassignedRowWithoutMovingIt(t *testing.T) {
	p := newPairingTestProcessor()
	master := testSkillNameMaster()
	master.skillNames.damageVisions = map[uint64]uint64{28003302: 280033}
	master.skillNames.visionItems = map[uint64]uint64{280033: 60001672}
	master.skillNames.echoNames[60001672] = "共鳴迴響·鳴式·利維亞坦"
	p.masters = master
	unassigned := UnassignedSkillTotals{DamageDealt: 42025, OutgoingEvents: 27, MissingSkillEvents: 27, DamageIDs: []uint64{28003302}}
	skills, row := p.viewSkills(1508, nil, unassigned, 42025)
	if len(skills) != 0 || row.Name != "共鳴迴響·鳴式·利維亞坦" || row.NameStatus != "DERIVED" || row.NameSource != "echo" || row.SourceID != 60001672 || row.IconKind != "echo-skill" || row.IconID != 280033 {
		t.Fatalf("unassigned row = %+v, rows %+v", row, skills)
	}
	if row.DamageDealt != 42025 || row.OutgoingEvents != 27 {
		t.Fatalf("naming the row changed its totals: %+v", row)
	}
	_, other := p.viewSkills(1508, nil, UnassignedSkillTotals{DamageDealt: 9, OutgoingEvents: 1, MissingSkillEvents: 1, DamageIDs: []uint64{1}}, 9)
	if other.Name != "" || other.NameStatus != "" || other.IconKind != "" {
		t.Fatalf("an unknown DamageId named the row: %+v", other)
	}
}
