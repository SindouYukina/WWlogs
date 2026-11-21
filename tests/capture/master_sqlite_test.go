package capture

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadGameMasterDatabaseUsesLocaleAndKeepsIDNamespacesSeparate(t *testing.T) {
	databasePath := buildMasterDatabaseFixture(t)

	completeMasterFixture(t, databasePath)
	traditional, err := LoadGameMasterDatabase(databasePath, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if traditional.Characters[1312].Name != "測試角色" || traditional.Monsters[700].Name != "測試怪物" {
		t.Fatalf("zh-TW names were not selected: role=%+v monster=%+v", traditional.Characters[1312], traditional.Monsters[700])
	}
	if traditional.Characters[1312].ID != traditional.Skills[1312].ID || traditional.Characters[1312].Name == traditional.Skills[1312].Name {
		t.Fatalf("same numeric ID in RoleInfo and SkillDescription was conflated: role=%+v skill=%+v", traditional.Characters[1312], traditional.Skills[1312])
	}
	if traditional.Skills[1312].RoleID != 1312 || traditional.Skills[1312].RoleStatus != string(ResolutionCorrelated) {
		t.Fatalf("role skill group association should retain correlated evidence: %+v", traditional.Skills[1312])
	}
	if traditional.Damage[18446744073709551614].ID != 18446744073709551614 {
		t.Fatalf("large text ID lost precision: %+v", traditional.Damage)
	}
	if len(traditional.Manifest.SourceHash) != 64 || traditional.Locale != "zh-TW" {
		t.Fatalf("runtime provenance missing database hash or locale: %+v", traditional.Manifest)
	}

	if _, exists := traditional.Characters[999999]; exists {
		t.Fatal("unknown ID unexpectedly resolved")
	}
	if character, ok := traditional.characterBySkin(9001); !ok || character.ID != 1312 || character.SkinID != 9001 {
		t.Fatalf("unique RoleInfo.SkinId relation = %+v, ok=%t", character, ok)
	}
}

// Role and monster names follow the rule of DisplayResolver.Resolve: the locale's official text, else the official en-US
// text, else no name. A placeholder is no text, en-US itself falls back to nothing, and Language is the locale the name is.
func TestLoadGameMasterDatabaseFallsBackToEnglishRoleAndMonsterNames(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"INSERT INTO roleinfo_locale VALUES('1312','zh-CN','dnt/测试角色','Role.Name')",
		"INSERT INTO monsterinfo_locale VALUES('701','en-US','dnt/placeholder','Monster.Name'),('701','ja-JP',' DNT/仮','Monster.Name')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	db.Close()
	completeMasterFixture(t, path)
	for _, c := range []struct {
		locale, role, roleLanguage, monster, monsterLanguage string
	}{
		{"zh-TW", "測試角色", "zh-TW", "測試怪物", "zh-TW"},
		{"ko-KR", "시험 캐릭터", "ko-KR", "Test Monster", "en-US"},
		{"ja-JP", "Test Character", "en-US", "Test Monster", "en-US"},
		{"zh-CN", "Test Character", "en-US", "Test Monster", "en-US"},
		{"en-US", "Test Character", "en-US", "Test Monster", "en-US"},
	} {
		master, err := LoadGameMasterDatabase(path, c.locale)
		if err != nil {
			t.Fatal(err)
		}
		role, monster := master.Characters[1312], master.Monsters[700]
		if role.Name != c.role || role.Language != c.roleLanguage || role.NameStatus != string(ResolutionVerified) {
			t.Fatalf("%s role = %+v, want %q from %s", c.locale, role, c.role, c.roleLanguage)
		}
		if monster.Name != c.monster || monster.Language != c.monsterLanguage || monster.NameStatus != string(ResolutionVerified) {
			t.Fatalf("%s monster = %+v, want %q from %s", c.locale, monster, c.monster, c.monsterLanguage)
		}
		// 701 has only placeholders: no name in any locale, and the record keeps the requested locale.
		if unnamed := master.Monsters[701]; unnamed.Name != "" || unnamed.NameStatus != string(ResolutionUnknown) || unnamed.Language != c.locale {
			t.Fatalf("%s placeholder-only monster = %+v, want no name", c.locale, unnamed)
		}
	}
}

func TestLoadGameMasterDatabaseLoadsNormalizedBuffDefinitionKeys(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE buff(id TEXT PRIMARY KEY)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO buff VALUES('70001'),('18446744073709551614')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if !master.HasBuffDefinition(70001) || !master.HasBuffDefinition(18446744073709551614) {
		t.Fatal("normalized Buff definition keys were not loaded exactly")
	}
	if master.HasBuffDefinition(1312010090) {
		t.Fatal("missing Buff ID unexpectedly resolved")
	}
}

func TestLoadGameMasterDatabaseRejectsAmbiguousRoleSkinMappings(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO roleinfo(id,skill_id,skin_id) VALUES('1313','78','9001')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO roleinfo_locale(id,locale,name,text_key) VALUES('1313','zh-TW','Test Role','Role.Name')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := master.characterBySkin(9001); ok {
		t.Fatal("ambiguous skin ID must not resolve to either character")
	}
	if _, ok := master.ambiguousSkins[9001]; !ok {
		t.Fatal("ambiguous skin ID was not retained as an explicit ambiguity")
	}
}

func TestSkillNamespacesRemainSeparateForDamageAndCommonRequests(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE commonskillpreload(id TEXT PRIMARY KEY)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO commonskillpreload VALUES('2000001'),('1312')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	common, ok := master.commonSkill(2000001)
	if !ok || common.SourceTable != "CommonSkillPreload.Id" || common.NameStatus != string(ResolutionUnknown) {
		t.Fatalf("CommonSkillPreload fallback = %+v, ok=%t", common, ok)
	}
	if _, ok := master.DamageSkill(2000001); ok {
		t.Fatal("CommonSkillPreload ID must not be treated as a SkillDescription ID")
	}
	if common, ok := master.commonSkill(1312); !ok || common.SourceTable != "CommonSkillPreload.Id" {
		t.Fatalf("same numeric ID in CommonSkillPreload namespace = %+v, ok=%t", common, ok)
	}
	if skill, ok := master.DamageSkill(1312); !ok || skill.Name != "測試技能" {
		t.Fatalf("damage SkillId must resolve through SkillDescription despite numeric collision: %+v, ok=%t", skill, ok)
	}
}

func TestLoadGameMasterDatabaseResolvesOnlyUniqueSceneMonsterAndTemplateReferences(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
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
	if _, err := LoadGameMasterDatabase(filepath.Join(t.TempDir(), "missing.sqlite"), "zh-TW"); err == nil {
		t.Fatal("missing master database must fail")
	}
	if _, err := LoadGameMasterDatabase(buildMasterDatabaseFixture(t), "fr-FR"); err == nil {
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
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if master.Locale != "zh-TW" {
		t.Fatalf("runtime locale = %q, want zh-TW", master.Locale)
	}
	for label, records := range map[string]map[uint64]MasterRecord{
		"role": master.Characters, "monster": master.Monsters, "skill": master.Skills,
	} {
		if len(records) == 0 {
			t.Fatalf("zh-TW has no %s records", label)
		}
	}
	if _, ok := master.Characters[1409]; !ok {
		t.Fatal("zh-TW does not resolve known RoleInfo ID 1409")
	}
	if _, ok := master.Monsters[310000010]; !ok {
		t.Fatal("zh-TW does not resolve known MonsterInfo ID 310000010")
	}
	if len(master.SceneIdentity.TemplateCatalog) == 0 || len(master.SceneIdentity.LevelTemplates) == 0 {
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
		"CREATE TABLE roleinfo(id TEXT PRIMARY KEY,skill_id TEXT,skin_id TEXT)",
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
		"INSERT INTO roleinfo VALUES('1312','77','9001')",
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

// TestDamageOwnersKeepTheStorageOrderOfASkillGroupsRoles pins the order the Development damageOwners output shows: the
// roles of one skill group come in roleinfo's storage order, not in id order, whatever plan SQLite picks. The index over
// (skill_id, id) would hand the join the roles in id order.
func TestDamageOwnersKeepTheStorageOrderOfASkillGroupsRoles(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	execMasterSQL(t, path, []string{
		"INSERT INTO roleinfo VALUES('5010','77','0')",
		"INSERT INTO roleinfo VALUES('5008','77','0')",
		"INSERT INTO roleinfo VALUES('5009','77','0')",
		"CREATE INDEX roleinfo_by_skill_and_id ON roleinfo(skill_id, id)",
		"CREATE TABLE skill_damage_ref(skill_id TEXT NOT NULL, ordinal INTEGER NOT NULL, damage_id TEXT NOT NULL, PRIMARY KEY(skill_id, ordinal)) WITHOUT ROWID",
		"INSERT INTO skill_damage_ref VALUES('1312', 0, '18446744073709551614')",
	})
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	var roles []uint64
	for _, owner := range master.DamageOwners[18446744073709551614] {
		if owner.Kind != "skill" || owner.ID != 1312 {
			t.Fatalf("unexpected owner %+v", owner)
		}
		roles = append(roles, owner.RoleID)
	}
	if want := []uint64{1312, 5010, 5008, 5009}; !slices.Equal(roles, want) {
		t.Fatalf("roles of skill group 77 = %v, want roleinfo's storage order %v", roles, want)
	}
}

func TestRuntimeSkillDefinitionsRetainMultipleActorBindingsWithoutInferringOwner(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"CREATE TABLE entityskillpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT,skill_id TEXT)",
		"CREATE INDEX idx_preload_skill ON entityskillpreload(skill_id)",
		"INSERT INTO skilldescription VALUES('9001',NULL),('9002',NULL)",
		"INSERT INTO entityskillpreload VALUES('1','actor-a','9001'),('2','actor-b','9001'),('3','actor-a','9002')",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(master.Skills) != 3 {
		t.Fatalf("duplicate actor bindings multiplied skill definitions: %d", len(master.Skills))
	}
	for _, id := range []uint64{9001, 9002} {
		record := master.Skills[id]
		if record.Status != string(ResolutionVerified) || record.NameStatus != string(ResolutionUnknown) || record.Name != "" || record.RoleID != 0 || record.SourceTable != "EntitySkillPreload.SkillId -> skilldescription (group UNKNOWN)" {
			t.Fatalf("runtime definition fabricated a name or owner: %+v", record)
		}
	}
	if master.Skills[1312].Name != "測試技能" {
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
		completeMasterFixture(t, path)
		master, err := LoadGameMasterDatabase(path, item.locale)
		if err != nil {
			t.Fatal(err)
		}
		candidates, ok := master.sceneTemplates(50, 500)
		if !ok || len(candidates) != 1 || candidates[0].Name != item.name {
			t.Fatalf("locale %s resolved %+v", item.locale, candidates)
		}
		expected := string(ResolutionVerified)
		if item.name == "" {
			expected = string(ResolutionUnknown)
		}
		if candidates[0].NameStatus != expected {
			t.Fatalf("locale %s status %+v", item.locale, candidates)
		}
	}
}

// A template locale table without the key and subtitle columns the loader reads is refused by name, never loaded with
// template names silently missing.
func TestMalformedTemplateLocaleFailsObservably(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	completeMasterFixture(t, path)
	execMasterSQL(t, path, []string{"DROP TABLE templateconfig_locale", "CREATE TABLE templateconfig_locale(id TEXT,locale TEXT,name TEXT)"})
	_, err := LoadGameMasterDatabase(path, "zh-TW")
	if !errors.Is(err, errMasterRuntimeSchema) || !strings.Contains(err.Error(), "missing column templateconfig_locale.name_text_key") || !strings.Contains(err.Error(), "missing column templateconfig_locale.subtitle") {
		t.Fatalf("a malformed template locale table: %v", err)
	}
}

func TestLoadGameMasterDatabaseTranslatesRoleSkinThroughNormalizedTable(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE roleskin(id TEXT PRIMARY KEY,role_id TEXT NOT NULL REFERENCES roleinfo(id))",
		"INSERT INTO roleskin(id,role_id) VALUES('81011312','1312')",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
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
	if roleID, ok := master.roleSkinRole(81011312); !ok || roleID != 1312 {
		t.Fatalf("RoleSkin 81011312 -> RoleId = %d, ok=%t", roleID, ok)
	}
	if _, ok := master.roleSkinRole(81019999); ok {
		t.Fatal("unknown RoleSkin id unexpectedly resolved")
	}
	if character, ok := master.characterBySkin(81011312); !ok || character.ID != 1312 {
		t.Fatalf("characterBySkin through roleskin = %+v, ok=%t", character, ok)
	}

	agree := resolveRoleSkin(master, RuntimeEntity{RoleSkinID: 81011312, HasRoleSkinID: true}, 1312)
	if agree == nil || agree.Status != ResolutionCorrelated || agree.MasterRoleID != 1312 {
		t.Fatalf("matching RoleId should correlate the skin: %+v", agree)
	}
	conflict := resolveRoleSkin(master, RuntimeEntity{RoleSkinID: 81011312, HasRoleSkinID: true}, 1409)
	if conflict == nil || conflict.Status != ResolutionUnknown || conflict.MasterRoleID != 1312 {
		t.Fatalf("a different RoleId must not be treated as agreement: %+v", conflict)
	}
	absent := resolveRoleSkin(master, RuntimeEntity{RoleSkinID: 81019999, HasRoleSkinID: true}, 1312)
	if absent == nil || absent.Status != ResolutionUnknown || absent.MasterRoleID != 0 {
		t.Fatalf("a skin without a row must stay unknown: %+v", absent)
	}
	if resolveRoleSkin(master, RuntimeEntity{}, 1312) != nil {
		t.Fatal("an entity without RoleSkinId must produce no skin resolution")
	}
}

func TestLoadGameMasterDatabaseRejectsRoleSkinWithMissingCharacter(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE roleskin(id TEXT PRIMARY KEY,role_id TEXT NOT NULL)",
		"INSERT INTO roleskin(id,role_id) VALUES('81019998','424242')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	completeMasterFixture(t, path)
	if _, err := LoadGameMasterDatabase(path, "zh-TW"); err == nil || errors.Is(err, errMasterRuntimeSchema) {
		t.Fatalf("a RoleSkin row naming an absent character must fail loading as a broken relation: %v", err)
	}
}

func TestResolveSummonSkillMapsVisionTypesToDTVisionKeys(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE phantomskill(id TEXT PRIMARY KEY,phantom_skill_id TEXT)",
		"INSERT INTO phantomskill(id,phantom_skill_id) VALUES('280026','280026'),('280028a','280028'),('280028b','280028')",
		"CREATE TABLE vision(id TEXT PRIMARY KEY)",
		"INSERT INTO vision(id) VALUES('280026'),('6013'),('280027'),('280028')",
		`CREATE VIEW summon_skill_vision_ref AS
			WITH unique_phantom_skill AS (
				SELECT CAST(phantom_skill_id AS TEXT) AS phantom_skill_id
				FROM phantomskill
				WHERE phantom_skill_id IS NOT NULL AND CAST(phantom_skill_id AS TEXT) <> ''
				GROUP BY CAST(phantom_skill_id AS TEXT)
				HAVING COUNT(*) = 1
			)
			SELECT 1 AS summon_type, p.phantom_skill_id AS summon_skill_id,
				CAST(v.id AS TEXT) AS vision_id, p.phantom_skill_id AS phantom_skill_id
			FROM unique_phantom_skill AS p
			JOIN vision AS v ON CAST(v.id AS TEXT) = p.phantom_skill_id`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
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
	entity := func(id, kind uint64, hasType bool) RuntimeEntity {
		return RuntimeEntity{SummonSkillID: id, HasSummonSkillID: true, SummonType: kind, HasSummonType: hasType}
	}
	if r := ResolveSummonSkill(master, entity(280026, 1, true)); r == nil || r.Status != ResolutionVerified || r.Namespace != "Vision.Id" {
		t.Fatalf("ConcomitantVision with a unique normalized Vision crosswalk must resolve: %+v", r)
	}
	if r := ResolveSummonSkill(master, entity(6013, 5, true)); r == nil || r.Status != ResolutionCorrelated || r.Namespace != "Vision.Id" {
		t.Fatalf("ConcomitantMotorcycle with a Vision row must correlate: %+v", r)
	}
	if r := ResolveSummonSkill(master, entity(280027, 1, true)); r == nil || r.Status != ResolutionUnknown || r.Namespace != "" {
		t.Fatalf("a Vision key without a PhantomSkill row must stay unknown for Type 1: %+v", r)
	}
	if r := ResolveSummonSkill(master, entity(280028, 1, true)); r == nil || r.Status != ResolutionUnknown || r.Namespace != "" {
		t.Fatalf("a duplicated PhantomSkill key must not resolve for Type 1: %+v", r)
	}
	if r := ResolveSummonSkill(master, entity(280099, 1, true)); r == nil || r.Status != ResolutionUnknown {
		t.Fatalf("a value without a Vision row must stay unknown: %+v", r)
	}
	if r := ResolveSummonSkill(master, entity(280026, 4, true)); r == nil || r.Status != ResolutionUnknown {
		t.Fatalf("an unmapped summon type must not be translated even when the number matches: %+v", r)
	}
	if r := ResolveSummonSkill(master, entity(280026, 0, false)); r == nil || r.Status != ResolutionUnknown {
		t.Fatalf("a missing Type must not select a namespace: %+v", r)
	}
	if ResolveSummonSkill(master, RuntimeEntity{}) != nil {
		t.Fatal("an entity without SummonSkillId must produce no resolution")
	}
}

func TestResolveSkillLabelNeverFillsOfficialNameAndRejectsConflicts(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE skillinfo(asset_id TEXT,skill_id TEXT,dev_name_zh_cn TEXT,PRIMARY KEY(asset_id,skill_id))",
		"INSERT INTO skillinfo VALUES('a1','5001','label-one'),('a2','5001','label-one'),('a1','5002','label-x'),('a2','5002','label-y')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
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
	if l := ResolveSkillLabel(master, 5001); l == nil || l.Status != ResolutionCorrelated || l.Label != "label-one" {
		t.Fatalf("a label identical in every table must correlate: %+v", l)
	}
	if l := ResolveSkillLabel(master, 5002); l == nil || l.Status != ResolutionUnknown || l.Label != "" {
		t.Fatalf("a key with conflicting labels must stay unknown: %+v", l)
	}
	if ResolveSkillLabel(master, 9999) != nil || ResolveSkillLabel(master, 0) != nil || ResolveSkillLabel(master, ^uint64(0)) != nil || ResolveSkillLabel(nil, 5001) != nil {
		t.Fatal("absent, zero, sentinel or master-less skills must produce no label")
	}
}

func TestLoadGameMasterDatabaseLoadsBuffStaticParametersWithoutNames(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE buff(id TEXT PRIMARY KEY, duration_policy INTEGER, stack_limit_count INTEGER)",
		"INSERT INTO buff VALUES('10041000',2,3),('10040002',2,1),('70000045',1,0),('5',0,0)",
		"CREATE TABLE buff_duration_magnitude(owner_id TEXT, ordinal INTEGER, value REAL)",
		// 10041000 has one magnitude; 10040002 lists two, which would need a rule to choose; 999 has no buff row.
		"INSERT INTO buff_duration_magnitude VALUES('10041000',0,14.8),('10040002',0,2.0),('10040002',1,3.0),('999',0,1.0)",
		"CREATE TABLE buff_granted_tags(owner_id TEXT, ordinal INTEGER, value TEXT)",
		"INSERT INTO buff_granted_tags VALUES('10041000',0,'tag.path.a'),('10041000',1,'tag.path.b'),('999',0,'orphan')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
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
	aero, ok := master.buffStatic(10041000)
	if !ok || !aero.HasDuration || aero.DurationSeconds != 14.8 || !aero.HasStackLimit || aero.StackLimit != 3 || !aero.HasDurationPolicy || aero.DurationPolicy != 2 {
		t.Fatalf("static parameters = %+v, ok=%t", aero, ok)
	}
	if len(aero.Tags) != 2 || aero.Tags[0] != "tag.path.a" || aero.Tags[1] != "tag.path.b" {
		t.Fatalf("granted tags = %v", aero.Tags)
	}
	if tick, _ := master.buffStatic(10040002); tick.HasDuration {
		t.Fatalf("an ambiguous multi-value duration magnitude was used: %+v", tick)
	}
	if _, ok := master.buffStatic(999); ok {
		t.Fatal("a magnitude or tag row without a buff row created a definition")
	}
	if _, ok := master.buffStatic(1312010090); ok {
		t.Fatal("a missing buff id has static parameters")
	}
	var nilMaster *GameMaster
	if _, ok := nilMaster.buffStatic(10041000); ok {
		t.Fatal("a nil master has static parameters")
	}
}

// A Master whose Buff static tables are missing is refused by name; with the tables present and empty, a buff row keeps
// its definition and gains no static parameter it does not have.
func TestLoadGameMasterDatabaseRefusesAMasterWithoutBuffStaticTables(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	execMasterSQL(t, path, []string{"CREATE TABLE buff(id TEXT PRIMARY KEY)", "INSERT INTO buff VALUES('70001')"})
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if !master.HasBuffDefinition(70001) {
		t.Fatal("buff definition lost")
	}
	if static, ok := master.buffStatic(70001); ok && (static.HasDuration || static.HasStackLimit || len(static.Tags) > 0) {
		t.Fatalf("static parameters appeared without rows: %+v", static)
	}
	execMasterSQL(t, path, []string{"DROP TABLE buff_duration_magnitude", "DROP TABLE buff_granted_tags", "ALTER TABLE buff DROP COLUMN stack_limit_count"})
	_, err = LoadGameMasterDatabase(path, "zh-TW")
	if !errors.Is(err, errMasterRuntimeSchema) {
		t.Fatalf("a Master without the Buff static tables: %v", err)
	}
	for _, part := range []string{"missing table buff_duration_magnitude", "missing table buff_granted_tags", "missing column buff.stack_limit_count"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("the refusal does not name %q: %v", part, err)
		}
	}
}

func TestLoadGameMasterDatabaseLoadsLocalizedBuffTermsAsCorrelatedNames(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE buff(id TEXT PRIMARY KEY)",
		"INSERT INTO buff VALUES('10041000'),('10061000')",
		"CREATE TABLE term(id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL)",
		"INSERT INTO term VALUES('850009','Term850009_Title','Term850009_Desc'),('850012','Term850012_Title','Term850012_Desc')",
		"CREATE TABLE term_locale(id TEXT NOT NULL, locale TEXT NOT NULL, name TEXT NOT NULL, name_text_key TEXT NOT NULL, description TEXT NOT NULL, description_text_key TEXT NOT NULL, PRIMARY KEY(id, locale))",
		"INSERT INTO term_locale VALUES('850009','zh-TW','風蝕效應','Term850009_Title','持續時造成<color=Wind>氣動傷害</color>','Term850009_Desc')," +
			"('850009','en-US','Aero Erosion','Term850009_Title','deals <color=Wind>Aero damage</color>','Term850009_Desc')," +
			"('850012','zh-TW','虛湮效應','Term850012_Title','降低防禦','Term850012_Desc')",
		"CREATE TABLE buff_term_ref(buff_id TEXT PRIMARY KEY, term_id TEXT NOT NULL, evidence_status TEXT NOT NULL)",
		// 10061000 is only HYPOTHESIS and 70001 has no buff row: neither may become a name.
		"INSERT INTO buff_term_ref VALUES('10041000','850009','CORRELATED'),('10061000','850012','HYPOTHESIS'),('70001','850009','CORRELATED')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	completeMasterFixture(t, path)
	traditional, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	term, ok := traditional.buffTerm(10041000)
	if !ok || term.Name != "風蝕效應" || term.TermID != 850009 || term.Status != ResolutionCorrelated {
		t.Fatalf("zh-TW term = %+v, ok=%t", term, ok)
	}
	if term.Description != "持續時造成氣動傷害" {
		t.Fatalf("rich-text markup was not removed from the description: %q", term.Description)
	}
	if _, ok := traditional.buffTerm(10061000); ok {
		t.Fatal("a HYPOTHESIS relation became a name")
	}
	if _, ok := traditional.buffTerm(70001); ok {
		t.Fatal("a relation without a buff row became a name")
	}
	english, err := LoadGameMasterDatabase(path, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if term, _ := english.buffTerm(10041000); term.Name != "Aero Erosion" {
		t.Fatalf("en-US term = %+v", term)
	}
	var nilMaster *GameMaster
	if _, ok := nilMaster.buffTerm(10041000); ok {
		t.Fatal("a nil master has terms")
	}
}
