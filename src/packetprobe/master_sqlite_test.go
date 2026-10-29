package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGameMasterDatabaseUsesLocaleAndKeepsIDNamespacesSeparate(t *testing.T) {
	databasePath := buildMasterDatabaseFixture(t)

	traditional, err := loadGameMasterDatabase(databasePath, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if traditional.characters[1312].Name != "測試角色" || traditional.monsters[700].Name != "測試怪物" {
		t.Fatalf("zh-TW names were not selected: role=%+v monster=%+v", traditional.characters[1312], traditional.monsters[700])
	}
	if traditional.characters[1312].ID != traditional.skills[1312].ID || traditional.characters[1312].Name == traditional.skills[1312].Name {
		t.Fatalf("same numeric ID in RoleInfo and SkillDescription was conflated: role=%+v skill=%+v", traditional.characters[1312], traditional.skills[1312])
	}
	if traditional.skills[1312].RoleID != 1312 || traditional.skills[1312].RoleStatus != string(resolutionCorrelated) {
		t.Fatalf("role skill group association should retain correlated evidence: %+v", traditional.skills[1312])
	}
	if traditional.damage[18446744073709551614].ID != 18446744073709551614 {
		t.Fatalf("large text ID lost precision: %+v", traditional.damage)
	}
	if len(traditional.manifest.SourceHash) != 64 || traditional.locale != "zh-TW" {
		t.Fatalf("runtime provenance missing database hash or locale: %+v", traditional.manifest)
	}

	if _, exists := traditional.characters[999999]; exists {
		t.Fatal("unknown ID unexpectedly resolved")
	}
}

func TestLoadGameMasterDatabaseResolvesOnlyUniqueSceneMonsterAndTemplateReferences(t *testing.T) {
	master, err := loadGameMasterDatabase(buildMasterDatabaseFixture(t), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := master.sceneMonster(50, 900); !ok || id != 700 {
		t.Fatalf("unique map-scoped monster relation = %d, ok=%t", id, ok)
	}
	if _, ok := master.sceneMonster(50, 901); ok {
		t.Fatal("ambiguous monster relation must remain unresolved")
	}
	if _, ok := master.sceneMonster(51, 900); ok {
		t.Fatal("unmapped instance must not resolve by map or entity ID alone")
	}
	candidates, ok := master.sceneTemplates(50, 500)
	if !ok || len(candidates) != 1 || candidates[0].TemplateID != 18446744073709551613 || candidates[0].BlueprintType != "Monster_Test" {
		t.Fatalf("map-scoped TemplateConfig reference = %+v, ok=%t", candidates, ok)
	}
	if _, ok := master.sceneTemplates(51, 500); ok {
		t.Fatal("template relation must remain unresolved without matching map")
	}
}

func TestLoadGameMasterDatabaseRejectsMissingFileAndUnsupportedLocale(t *testing.T) {
	if _, err := loadGameMasterDatabase(filepath.Join(t.TempDir(), "missing.sqlite"), "zh-TW"); err == nil {
		t.Fatal("missing master database must fail")
	}
	if _, err := loadGameMasterDatabase(buildMasterDatabaseFixture(t), "fr-FR"); err == nil {
		t.Fatal("unsupported locale must fail")
	}
}

func TestLoadGameMasterDatabaseUsesDefaultTraditionalChineseLocaleAgainstLocalCombatMaster(t *testing.T) {
	path := os.Getenv("WWLOGS_MASTER_DB")
	if path == "" {
		t.Skip("WWLOGS_MASTER_DB is not set")
	}
	before, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if master.locale != "zh-TW" {
		t.Fatalf("runtime locale = %q, want zh-TW", master.locale)
	}
	for label, records := range map[string]map[uint64]masterRecord{
		"role": master.characters, "monster": master.monsters, "skill": master.skills,
	} {
		if len(records) == 0 {
			t.Fatalf("zh-TW has no %s records", label)
		}
	}
	if _, ok := master.characters[1409]; !ok {
		t.Fatal("zh-TW does not resolve known RoleInfo ID 1409")
	}
	if _, ok := master.monsters[310000010]; !ok {
		t.Fatal("zh-TW does not resolve known MonsterInfo ID 310000010")
	}
	if len(master.sceneIdentity.templateCatalog) == 0 || len(master.sceneIdentity.levelTemplates) == 0 {
		t.Fatal("zh-TW did not load scene TemplateConfig definitions and relations")
	}
	after, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("read-only master load changed database content: %s != %s", before, after)
	}
}

func buildMasterDatabaseFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master_data.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		"CREATE TABLE roleinfo(id TEXT PRIMARY KEY,skill_id TEXT)",
		"CREATE TABLE roleinfo_locale(id TEXT,locale TEXT,name TEXT,text_key TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE monsterinfo(id TEXT PRIMARY KEY)",
		"CREATE TABLE monsterinfo_locale(id TEXT,locale TEXT,name TEXT,text_key TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE skill(id TEXT PRIMARY KEY,skill_group_id TEXT,skill_level_group_id TEXT)",
		"CREATE TABLE skilldescription(id TEXT PRIMARY KEY,skill_level_group_id TEXT)",
		"CREATE TABLE skilldescription_locale(id TEXT,locale TEXT,name TEXT,text_key TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE role_skill_group_ref(role_id TEXT,skill_group_id TEXT,PRIMARY KEY(role_id,skill_group_id))",
		"CREATE TABLE damage(id TEXT PRIMARY KEY,calculate_type INTEGER)",
		"CREATE TABLE map_instance(instance_id TEXT PRIMARY KEY,map_id TEXT)",
		"CREATE TABLE map_entity_monster(map_id TEXT,entity_id TEXT,monster_id TEXT,PRIMARY KEY(map_id,entity_id,monster_id))",
		"CREATE TABLE templateconfig(id TEXT PRIMARY KEY,blueprint_type TEXT,raw_name TEXT)",
		"CREATE TABLE map_entity_template_ref(map_id TEXT,entity_id TEXT,ordinal INTEGER,template_id TEXT,PRIMARY KEY(map_id,entity_id,ordinal))",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	rows := []string{
		"INSERT INTO roleinfo VALUES('1312','77')",
		"INSERT INTO roleinfo_locale VALUES('1312','zh-TW','測試角色','Role.Name')",
		"INSERT INTO roleinfo_locale VALUES('1312','ko-KR','시험 캐릭터','Role.Name')",
		"INSERT INTO roleinfo_locale VALUES('1312','en-US','Test Character','Role.Name')",
		"INSERT INTO monsterinfo VALUES('700')",
		"INSERT INTO monsterinfo VALUES('701')",
		"INSERT INTO monsterinfo_locale VALUES('700','zh-TW','測試怪物','Monster.Name')",
		"INSERT INTO monsterinfo_locale VALUES('700','en-US','Test Monster','Monster.Name')",
		"INSERT INTO skill VALUES('1312','77','88')",
		"INSERT INTO skilldescription VALUES('1312','88')",
		"INSERT INTO skilldescription_locale VALUES('1312','zh-TW','測試技能','Skill.Name')",
		"INSERT INTO skilldescription_locale VALUES('1312','ko-KR','시험 기술','Skill.Name')",
		"INSERT INTO role_skill_group_ref VALUES('1312','77')",
		"INSERT INTO damage VALUES('18446744073709551614',1)",
		"INSERT INTO map_instance VALUES('50','80')",
		"INSERT INTO map_entity_monster VALUES('80','900','700')",
		"INSERT INTO map_entity_monster VALUES('80','901','700')",
		"INSERT INTO map_entity_monster VALUES('80','901','701')",
		"INSERT INTO templateconfig VALUES('18446744073709551613','Monster_Test','')",
		"INSERT INTO map_entity_template_ref VALUES('80','500',0,'18446744073709551613')",
	}
	for _, statement := range rows {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRuntimeSkillDefinitionsRetainMultipleActorBindingsWithoutInferringOwner(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"CREATE TABLE entityskillpreload(id TEXT PRIMARY KEY,skill_id TEXT)",
		"CREATE INDEX idx_preload_skill ON entityskillpreload(skill_id)",
		"INSERT INTO skilldescription VALUES('9001',NULL),('9002',NULL)",
		"INSERT INTO entityskillpreload VALUES('1','9001'),('2','9001'),('3','9002')",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(master.skills) != 3 {
		t.Fatalf("duplicate actor bindings multiplied skill definitions: %d", len(master.skills))
	}
	for _, id := range []uint64{9001, 9002} {
		record := master.skills[id]
		if record.Status != string(resolutionVerified) || record.NameStatus != string(resolutionUnknown) || record.Name != "" || record.RoleID != 0 || record.SourceTable != "EntitySkillPreload.SkillId -> skilldescription (group UNKNOWN)" {
			t.Fatalf("runtime definition fabricated a name or owner: %+v", record)
		}
	}
	if master.skills[1312].Name != "測試技能" {
		t.Fatal("existing description label was overwritten")
	}
}

func TestTemplateNamesUseRequestedLocaleAndNeverFallBackToRawLabels(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"ALTER TABLE templateconfig ADD COLUMN tid_name TEXT",
		"UPDATE templateconfig SET tid_name='Entity.Name'",
		"CREATE TABLE templateconfig_locale(id TEXT,locale TEXT,name TEXT,name_text_key TEXT,subtitle TEXT,PRIMARY KEY(id,locale))",
		"INSERT INTO templateconfig_locale VALUES('18446744073709551613','zh-TW','測試王','Entity.Name','測試稱號')",
		"INSERT INTO templateconfig_locale VALUES('18446744073709551613','en-US','Test Boss','Entity.Name',NULL)",
		"INSERT INTO templateconfig_locale VALUES('18446744073709551613','ja-JP','dnt/internal','Entity.Name',NULL)",
		"INSERT INTO templateconfig_locale VALUES('18446744073709551613','zh-CN','Wrong name','Other.Key',NULL)",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for _, item := range []struct{ locale, name string }{{"zh-TW", "測試王"}, {"en-US", "Test Boss"}, {"ko-KR", ""}, {"ja-JP", ""}, {"zh-CN", ""}} {
		master, err := loadGameMasterDatabase(path, item.locale)
		if err != nil {
			t.Fatal(err)
		}
		candidates, ok := master.sceneTemplates(50, 500)
		if !ok || len(candidates) != 1 || candidates[0].Name != item.name {
			t.Fatalf("locale %s resolved %+v", item.locale, candidates)
		}
		expected := string(resolutionVerified)
		if item.name == "" {
			expected = string(resolutionUnknown)
		}
		if candidates[0].NameStatus != expected {
			t.Fatalf("locale %s status %+v", item.locale, candidates)
		}
	}
}

func TestMalformedOptionalTemplateLocaleFailsObservably(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE templateconfig_locale(id TEXT,locale TEXT,name TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := loadGameMasterDatabase(path, "zh-TW"); err == nil {
		t.Fatal("malformed locale schema was silently accepted")
	}
}
