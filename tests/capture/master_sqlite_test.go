package capture

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
	if character, ok := traditional.characterBySkin(9001); !ok || character.ID != 1312 || character.SkinID != 9001 {
		t.Fatalf("unique RoleInfo.SkinId relation = %+v, ok=%t", character, ok)
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

	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if !master.hasBuffDefinition(70001) || !master.hasBuffDefinition(18446744073709551614) {
		t.Fatal("normalized Buff definition keys were not loaded exactly")
	}
	if master.hasBuffDefinition(1312010090) {
		t.Fatal("missing Buff ID unexpectedly resolved")
	}
}

func TestLoadGameMasterDatabaseLoadsNormalizedShieldDefinitionKeys(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE shield(id TEXT PRIMARY KEY)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO shield VALUES('1409404003'),('1508990003'),('18446744073709551614')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1409404003, 1508990003, 18446744073709551614} {
		if !master.hasShieldDefinition(id) {
			t.Fatalf("normalized shield definition %d was not loaded", id)
		}
	}
	if master.hasShieldDefinition(1312010090) {
		t.Fatal("missing Shield ID unexpectedly resolved")
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

	master, err := loadGameMasterDatabase(path, "zh-TW")
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

	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	common, ok := master.commonSkill(2000001)
	if !ok || common.SourceTable != "CommonSkillPreload.Id" || common.NameStatus != string(resolutionUnknown) {
		t.Fatalf("CommonSkillPreload fallback = %+v, ok=%t", common, ok)
	}
	if _, ok := master.damageSkill(2000001); ok {
		t.Fatal("CommonSkillPreload ID must not be treated as a SkillDescription ID")
	}
	if common, ok := master.commonSkill(1312); !ok || common.SourceTable != "CommonSkillPreload.Id" {
		t.Fatalf("same numeric ID in CommonSkillPreload namespace = %+v, ok=%t", common, ok)
	}
	if skill, ok := master.damageSkill(1312); !ok || skill.Name != "測試技能" {
		t.Fatalf("damage SkillId must resolve through SkillDescription despite numeric collision: %+v, ok=%t", skill, ok)
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

	master, err := loadGameMasterDatabase(path, "zh-TW")
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

	agree := resolveRoleSkin(master, runtimeEntity{roleSkinID: 81011312, hasRoleSkinID: true}, 1312)
	if agree == nil || agree.Status != resolutionCorrelated || agree.MasterRoleID != 1312 {
		t.Fatalf("matching RoleId should correlate the skin: %+v", agree)
	}
	conflict := resolveRoleSkin(master, runtimeEntity{roleSkinID: 81011312, hasRoleSkinID: true}, 1409)
	if conflict == nil || conflict.Status != resolutionUnknown || conflict.MasterRoleID != 1312 {
		t.Fatalf("a different RoleId must not be treated as agreement: %+v", conflict)
	}
	absent := resolveRoleSkin(master, runtimeEntity{roleSkinID: 81019999, hasRoleSkinID: true}, 1312)
	if absent == nil || absent.Status != resolutionUnknown || absent.MasterRoleID != 0 {
		t.Fatalf("a skin without a row must stay unknown: %+v", absent)
	}
	if resolveRoleSkin(master, runtimeEntity{}, 1312) != nil {
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
	if _, err := loadGameMasterDatabase(path, "zh-TW"); err == nil {
		t.Fatal("a RoleSkin row naming an absent character must fail loading")
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
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	entity := func(id, kind uint64, hasType bool) runtimeEntity {
		return runtimeEntity{summonSkillID: id, hasSummonSkillID: true, summonType: kind, hasSummonType: hasType}
	}
	if r := resolveSummonSkill(master, entity(280026, 1, true)); r == nil || r.Status != resolutionVerified || r.Namespace != "Vision.Id" {
		t.Fatalf("ConcomitantVision with a unique normalized Vision crosswalk must resolve: %+v", r)
	}
	if r := resolveSummonSkill(master, entity(6013, 5, true)); r == nil || r.Status != resolutionCorrelated || r.Namespace != "Vision.Id" {
		t.Fatalf("ConcomitantMotorcycle with a Vision row must correlate: %+v", r)
	}
	if r := resolveSummonSkill(master, entity(280027, 1, true)); r == nil || r.Status != resolutionUnknown || r.Namespace != "" {
		t.Fatalf("a Vision key without a PhantomSkill row must stay unknown for Type 1: %+v", r)
	}
	if r := resolveSummonSkill(master, entity(280028, 1, true)); r == nil || r.Status != resolutionUnknown || r.Namespace != "" {
		t.Fatalf("a duplicated PhantomSkill key must not resolve for Type 1: %+v", r)
	}
	if r := resolveSummonSkill(master, entity(280099, 1, true)); r == nil || r.Status != resolutionUnknown {
		t.Fatalf("a value without a Vision row must stay unknown: %+v", r)
	}
	if r := resolveSummonSkill(master, entity(280026, 4, true)); r == nil || r.Status != resolutionUnknown {
		t.Fatalf("an unmapped summon type must not be translated even when the number matches: %+v", r)
	}
	if r := resolveSummonSkill(master, entity(280026, 0, false)); r == nil || r.Status != resolutionUnknown {
		t.Fatalf("a missing Type must not select a namespace: %+v", r)
	}
	if resolveSummonSkill(master, runtimeEntity{}) != nil {
		t.Fatal("an entity without SummonSkillId must produce no resolution")
	}
}

func TestResolveBulletFollowsSourceChainToExactlyOneActor(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE asset_reference(id TEXT PRIMARY KEY,path TEXT)",
		"INSERT INTO asset_reference VALUES('actorA','/Game/A.A_C'),('actorB','/Game/B.B_C'),('actorV','/Game/V.V_C')",
		"CREATE TABLE modelconfigpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT)",
		"INSERT INTO modelconfigpreload VALUES('10','actorA'),('20','actorB'),('30','actorV')",
		"CREATE TABLE roleskin(id TEXT PRIMARY KEY,role_id TEXT,mesh_id TEXT)",
		"INSERT INTO roleskin VALUES('9001','1312','10')",
		"CREATE TABLE blueprintconfig(id TEXT PRIMARY KEY,blueprint_type TEXT,model_id TEXT)",
		"INSERT INTO blueprintconfig VALUES('1','MonsterB','20'),('2','VisionBP','30')",
		"CREATE TABLE summoncfg(id TEXT PRIMARY KEY,blueprint_type TEXT)",
		"INSERT INTO summoncfg VALUES('280026','VisionBP')",
		"CREATE TABLE vision(id TEXT PRIMARY KEY,birth_cast_skill_id TEXT)",
		"INSERT INTO vision VALUES('280026','280026010')",
		"CREATE TABLE bulletpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT,bullet_id TEXT)",
		"INSERT INTO bulletpreload VALUES('1','actorA','111'),('2','actorB','222'),('3','actorV','280026012'),('4','actorA','333'),('5','actorB','333'),('6','actorA','444')",
		"CREATE TABLE entityskillpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT,skill_id TEXT)",
		"INSERT INTO entityskillpreload VALUES('1','actorA','501'),('2','actorA','502'),('3','actorB','502'),('4','actorB','503')",
		"CREATE TABLE commonbullet(id TEXT PRIMARY KEY,dev_name_zh_cn TEXT)",
		"INSERT INTO commonbullet VALUES('210002005','label-a'),('444','label-shared')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if actor, ok := master.uniqueActorForSkill(501); !ok || actor != "actorA" {
		t.Fatalf("a SkillId listed by exactly one actor asset must resolve in the reverse index: %q %t", actor, ok)
	}
	if _, ok := master.uniqueActorForSkill(502); ok {
		t.Fatal("a SkillId listed by multiple actor assets must remain ambiguous")
	}
	character := bulletSource{RoleSkinID: 9001, HasRoleSkin: true}
	if r := resolveBullet(master, 111, character); r.Status != resolutionCorrelated || r.Namespace != "BulletPreload.ActorBlueprint" || r.ActorBlueprint != "/Game/A.A_C" {
		t.Fatalf("a character bullet held by its own actor must correlate: %+v", r)
	}
	if r := resolveBullet(master, 111, bulletSource{SkillID: 501}); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/A.A_C" || r.Via == "" {
		t.Fatalf("a co-referenced SkillId with one actor owner must resolve its listed bullet: %+v", r)
	}
	if r := resolveBullet(master, 222, bulletSource{SkillID: 502}); r.Status != resolutionUnknown {
		t.Fatalf("a SkillId shared by multiple actors must not select a bullet owner: %+v", r)
	}
	if r := resolveBullet(master, 222, bulletSource{RoleSkinID: 9001, HasRoleSkin: true, SkillID: 503}); r.Status != resolutionUnknown {
		t.Fatalf("a runtime source chain must not be overridden by a conflicting SkillId owner: %+v", r)
	}
	if r := resolveBullet(master, 222, character); r.Status != resolutionUnknown {
		t.Fatalf("a bullet held only by another actor must not resolve for this source: %+v", r)
	}
	monster := bulletSource{BlueprintTypes: []string{"MonsterB"}}
	if r := resolveBullet(master, 222, monster); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/B.B_C" {
		t.Fatalf("a template-derived actor bullet must correlate: %+v", r)
	}
	both := bulletSource{RoleSkinID: 9001, HasRoleSkin: true, BlueprintTypes: []string{"MonsterB"}}
	if r := resolveBullet(master, 333, both); r.Status != resolutionUnknown || r.Namespace != "" {
		t.Fatalf("a key held by two source-chain actors must stay ambiguous: %+v", r)
	}
	echo := bulletSource{RoleSkinID: 9001, HasRoleSkin: true, SkillID: 280026010}
	if r := resolveBullet(master, 280026012, echo); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/V.V_C" {
		t.Fatalf("an echo bullet reached through Vision.BirthCastSkillId must correlate to the echo actor: %+v", r)
	}
	if r := resolveBullet(master, 210002005, character); r.Status != resolutionCorrelated || r.Namespace != "CDT_CommonBulletData.RowKey" {
		t.Fatalf("a common-table-only key must correlate to the common table: %+v", r)
	}
	if r := resolveBullet(master, 444, character); r.Status != resolutionUnknown {
		t.Fatalf("a key held by both the source actor and the common table must stay ambiguous: %+v", r)
	}
	if r := resolveBullet(master, 999, character); r.Status != resolutionUnknown {
		t.Fatalf("an unknown key must stay unknown: %+v", r)
	}
	if resolveBullet(nil, 1, character) != nil {
		t.Fatal("no master means no resolution")
	}
}

func TestResolveBulletUsesLiveVisionSummonOfTheSourceEntity(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE asset_reference(id TEXT PRIMARY KEY,path TEXT)",
		"INSERT INTO asset_reference VALUES('actorV','/Game/V.V_C')",
		"CREATE TABLE modelconfigpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT)",
		"INSERT INTO modelconfigpreload VALUES('30','actorV')",
		"CREATE TABLE blueprintconfig(id TEXT PRIMARY KEY,blueprint_type TEXT,model_id TEXT)",
		"INSERT INTO blueprintconfig VALUES('2','VisionBP','30')",
		"CREATE TABLE summoncfg(id TEXT PRIMARY KEY,blueprint_type TEXT)",
		"INSERT INTO summoncfg VALUES('280045','VisionBP')",
		"CREATE TABLE bulletpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT,bullet_id TEXT)",
		"INSERT INTO bulletpreload VALUES('1','actorV','280045101')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	with := bulletSource{VisionSummonCfgs: []uint64{280045}}
	if r := resolveBullet(master, 280045101, with); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/V.V_C" {
		t.Fatalf("a live vision summon of the source entity must lead to the echo actor: %+v", r)
	}
	own := bulletSource{OwnSummonCfgID: 280045, HasOwnSummonCfgID: true}
	if r := resolveBullet(master, 280045101, own); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/V.V_C" {
		t.Fatalf("a summon firing its own bullet must resolve through its own SummonCfg: %+v", r)
	}
	if r := resolveBullet(master, 280045101, bulletSource{}); r.Status != resolutionUnknown {
		t.Fatalf("without a vision summon the bullet must stay a numeric match: %+v", r)
	}

	resolver := &entityResolver{entities: map[uint64]runtimeEntity{
		92: {entityID: 92, summonerID: 90, hasSummonerID: true, summonType: 1, hasSummonType: true, summonConfigID: 280045, hasSummonConfigID: true},
		93: {entityID: 93, summonerID: 90, hasSummonerID: true, summonType: 5, hasSummonType: true, summonConfigID: 6013, hasSummonConfigID: true},
		94: {entityID: 94, summonerID: 77, hasSummonerID: true, summonType: 1, hasSummonType: true, summonConfigID: 280026, hasSummonConfigID: true},
	}}
	if got := resolver.visionSummonConfigs(90); len(got) != 1 || got[0] != 280045 {
		t.Fatalf("only ConcomitantVision summons of the named summoner count: %v", got)
	}
	if resolver.visionSummonConfigs(0) != nil || (*entityResolver)(nil).visionSummonConfigs(90) != nil {
		t.Fatal("no summoner or no resolver must yield nothing")
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
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if l := resolveSkillLabel(master, 5001); l == nil || l.Status != resolutionCorrelated || l.Label != "label-one" {
		t.Fatalf("a label identical in every table must correlate: %+v", l)
	}
	if l := resolveSkillLabel(master, 5002); l == nil || l.Status != resolutionUnknown || l.Label != "" {
		t.Fatalf("a key with conflicting labels must stay unknown: %+v", l)
	}
	if resolveSkillLabel(master, 9999) != nil || resolveSkillLabel(master, 0) != nil || resolveSkillLabel(master, ^uint64(0)) != nil || resolveSkillLabel(nil, 5001) != nil {
		t.Fatal("absent, zero, sentinel or master-less skills must produce no label")
	}
}
