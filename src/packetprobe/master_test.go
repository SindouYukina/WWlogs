package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGameMasterValidatesMetadataAndBuildsSkinIndex(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{
		GameVersion: "3.7.0", ResourceVersion: "3.7.8", ClientRegion: "Global", Language: "zh-Hant",
		SourceURL: "https://example.invalid/source", SourceCommit: "test-commit",
		Verification: "STATIC_DATA_ONLY", SourceTables: []string{"roles"},
		CharacterCount: 1, LocalizedCharacterNameCount: 1, MonsterCount: 1, LocalizedMonsterNameCount: 1,
		SkillCount: 1, LocalizedSkillNameCount: 1, DamageCount: 1, BuffStatus: "UNAVAILABLE",
	}
	writeFixtureJSON(t, filepath.Join(dir, "manifest.json"), manifest)
	writeFixtureJSON(t, filepath.Join(dir, "characters.json"), []masterRecord{{
		ID: 1312, Name: "測試角色", GameVersion: "3.7.0", ResourceVersion: "3.7.8", SourceCommit: "test-commit",
		Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "roles", SkinID: 81001312,
	}})
	writeFixtureJSON(t, filepath.Join(dir, "monsters.json"), []masterRecord{{
		ID: 310000010, Name: "測試怪物", GameVersion: "3.7.0", ResourceVersion: "3.7.8", SourceCommit: "test-commit",
		Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "monsters",
	}})
	writeFixtureJSON(t, filepath.Join(dir, "skills.json"), []masterRecord{{
		ID: 1312011, Name: "測試技能", GameVersion: "3.7.0", ResourceVersion: "3.7.8", SourceCommit: "test-commit",
		Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "skills", RoleID: 1312, NameKind: "SkillName",
	}})
	writeFixtureJSON(t, filepath.Join(dir, "damage.json"), []damageMasterRecord{{
		ID: 120900505, CalculateType: 1, GameVersion: "3.7.0", ResourceVersion: "3.7.8",
		SourceCommit: "test-commit", SourceTable: "damage",
	}})

	master, err := loadGameMaster(dir)
	if err != nil {
		t.Fatal(err)
	}
	if character, ok := master.characterBySkin(81001312); !ok || character.ID != 1312 {
		t.Fatalf("skin-to-character index = %+v, ok=%t", character, ok)
	}
	if monster, ok := master.monsters[310000010]; !ok || monster.Name != "測試怪物" {
		t.Fatalf("monster lookup = %+v, ok=%t", monster, ok)
	}
	if skill, ok := master.skills[1312011]; !ok || skill.RoleID != 1312 {
		t.Fatalf("skill lookup = %+v, ok=%t", skill, ok)
	}
	if damage, ok := master.damage[120900505]; !ok || damage.CalculateType != 1 {
		t.Fatalf("damage classification lookup = %+v, ok=%t", damage, ok)
	}
}

func TestLoadGameMasterRejectsMetadataDriftAndDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{
		GameVersion: "3.7.0", ResourceVersion: "3.7.8", ClientRegion: "Global", Language: "zh-Hant",
		SourceURL: "https://example.invalid/source", SourceCommit: "test-commit",
		Verification: "STATIC_DATA_ONLY", SourceTables: []string{"roles"}, CharacterCount: 2,
		BuffStatus: "UNAVAILABLE",
	}
	writeFixtureJSON(t, filepath.Join(dir, "manifest.json"), manifest)
	writeFixtureJSON(t, filepath.Join(dir, "characters.json"), []masterRecord{
		{ID: 1, Name: "A", GameVersion: "3.7.0", ResourceVersion: "3.7.8", SourceCommit: "test-commit", Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "roles"},
		{ID: 1, Name: "B", GameVersion: "3.7.0", ResourceVersion: "3.7.8", SourceCommit: "test-commit", Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "roles"},
	})
	writeFixtureJSON(t, filepath.Join(dir, "monsters.json"), []masterRecord{})
	writeFixtureJSON(t, filepath.Join(dir, "skills.json"), []masterRecord{})
	if _, err := loadGameMaster(dir); err == nil {
		t.Fatal("duplicate IDs should be rejected")
	}
	manifest.CharacterCount = 1
	writeFixtureJSON(t, filepath.Join(dir, "manifest.json"), manifest)
	writeFixtureJSON(t, filepath.Join(dir, "characters.json"), []masterRecord{{
		ID: 1, Name: "A", GameVersion: "3.7.1", ResourceVersion: "3.7.8", SourceCommit: "test-commit",
		Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "roles",
	}})
	if _, err := loadGameMaster(dir); err == nil {
		t.Fatal("version metadata drift should be rejected")
	}
}

func TestLoadGameMasterRetainsCharacterIdentityWithoutLocalizedName(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{
		GameVersion: "3.7.0", ResourceVersion: "3.7.8", ClientRegion: "Global", Language: "zh-Hant",
		SourceURL: "https://example.invalid/source", SourceCommit: "test-commit",
		Verification: "STATIC_DATA_ONLY", SourceTables: []string{"roles"}, CharacterCount: 1,
		BuffStatus: "UNAVAILABLE",
	}
	writeFixtureJSON(t, filepath.Join(dir, "manifest.json"), manifest)
	writeFixtureJSON(t, filepath.Join(dir, "characters.json"), []masterRecord{{
		ID: 1313, Name: "", NameStatus: string(resolutionUnknown), GameVersion: "3.7.0", ResourceVersion: "3.7.8",
		SourceCommit: "test-commit", Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "roles", SkinID: 81001313,
	}})
	writeFixtureJSON(t, filepath.Join(dir, "monsters.json"), []masterRecord{})
	writeFixtureJSON(t, filepath.Join(dir, "skills.json"), []masterRecord{})

	master, err := loadGameMaster(dir)
	if err != nil {
		t.Fatal(err)
	}
	character, ok := master.characterBySkin(81001313)
	if !ok || character.ID != 1313 || character.Name != "" || character.NameStatus != string(resolutionUnknown) {
		t.Fatalf("unlocalized character identity was lost or mislabeled: %+v, ok=%t", character, ok)
	}
}

func TestLoadGameMasterSupportsClientSourceHash(t *testing.T) {
	dir := t.TempDir()
	const sourceHash = "a4b6d12c079e3f4b6a8c0d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e"
	manifest := masterManifest{
		GameVersion: "3.7.1", ResourceVersion: "3.7.1", ClientRegion: "Global", Language: "en",
		SourceHash: sourceHash, Verification: "STATIC_DATA_ONLY", SourceTables: []string{"Client/Content/Aki/database"},
		CharacterCount: 1, MonsterCount: 0, SkillCount: 1, DamageCount: 1, BuffStatus: "UNAVAILABLE",
	}
	writeFixtureJSON(t, filepath.Join(dir, "manifest.json"), manifest)
	writeFixtureJSON(t, filepath.Join(dir, "characters.json"), []masterRecord{{
		ID: 1312, Name: "Test Character", GameVersion: "3.7.1", ResourceVersion: "3.7.1",
		SourceHash: sourceHash, Language: "en", Status: string(resolutionVerified), SourceTable: "RoleInfo",
	}})
	writeFixtureJSON(t, filepath.Join(dir, "monsters.json"), []masterRecord{})
	writeFixtureJSON(t, filepath.Join(dir, "skills.json"), []masterRecord{{
		ID: 1312011, Name: "Test Skill", GameVersion: "3.7.1", ResourceVersion: "3.7.1",
		SourceHash: sourceHash, Language: "en", Status: string(resolutionVerified), SourceTable: "SkillDescription -> Skill",
		RoleID: 1312, RoleStatus: string(resolutionVerified), CharacterName: "Test Character",
	}})
	writeFixtureJSON(t, filepath.Join(dir, "damage.json"), []damageMasterRecord{{
		ID: 120900505, CalculateType: 1, GameVersion: "3.7.1", ResourceVersion: "3.7.1",
		SourceHash: sourceHash, SourceTable: "Damage",
	}})

	master, err := loadGameMaster(dir)
	if err != nil {
		t.Fatal(err)
	}
	event := combatEvent{DamageID: 120900505, HasDamageID: true}
	event.resolveEventType(master)
	if event.EventTypeCandidate != "HEALING_CANDIDATE" || event.EventTypeStatus != resolutionCorrelated {
		t.Fatalf("source-hash master did not resolve candidate type: %+v", event)
	}
	if !strings.Contains(event.EventTypeProof, sourceHash) {
		t.Fatalf("classification evidence omitted source hash: %q", event.EventTypeProof)
	}
	if skill, ok := master.skills[1312011]; !ok || skill.RoleID != 1312 || skill.RoleStatus != string(resolutionVerified) {
		t.Fatalf("source-hash master lost static Skill-to-Role mapping: %+v, ok=%t", skill, ok)
	}
}

func TestLoadSceneIdentityKeepsOnlyUniqueMapScopedMonsterLinks(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{GameVersion: "3.7.1", ClientRegion: "Global"}
	source := sceneIdentityFile{
		Manifest: sceneIdentityManifest{
			GameVersion: "3.7.1", ClientRegion: "Global", Platform: "Windows UHD",
			SourceHash: strings.Repeat("a", 64), InstanceRows: 1, LevelRows: 2,
		},
		Instances: []sceneInstanceRecord{{ID: 50, MapConfigID: 80}},
		LevelMonsters: []sceneLevelMonsterRecord{
			{MapID: 80, EntityID: 900, MonsterIDs: []uint64{700}},
			{MapID: 80, EntityID: 901, MonsterIDs: []uint64{701, 702}},
		},
	}
	path := filepath.Join(dir, "entity-identity.json")
	writeFixtureJSON(t, path, source)
	identity, err := loadSceneIdentity(path, manifest)
	if err != nil {
		t.Fatal(err)
	}
	master := &gameMaster{sceneIdentity: identity}
	if got, ok := master.sceneMonster(50, 900); !ok || got != 700 {
		t.Fatalf("unique map-scoped MonsterId = %d, ok=%t", got, ok)
	}
	if _, ok := master.sceneMonster(50, 901); ok {
		t.Fatal("ambiguous map-scoped MonsterId must remain unresolved")
	}
	if _, ok := master.sceneMonster(51, 900); ok {
		t.Fatal("unmapped InstanceId must not resolve by ConfigId alone")
	}

	source.Manifest.GameVersion = "3.7.0"
	writeFixtureJSON(t, path, source)
	if _, err := loadSceneIdentity(path, manifest); err == nil {
		t.Fatal("cross-version scene identity data should be rejected")
	}
}

func TestLoadGameMasterRejectsLocalizedNameCountDrift(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{
		GameVersion: "3.7.0", ResourceVersion: "3.7.8", ClientRegion: "Global", Language: "zh-Hant",
		SourceURL: "https://example.invalid/source", SourceCommit: "test-commit",
		Verification: "STATIC_DATA_ONLY", SourceTables: []string{"roles"}, CharacterCount: 1,
		LocalizedCharacterNameCount: 1, BuffStatus: "UNAVAILABLE",
	}
	writeFixtureJSON(t, filepath.Join(dir, "manifest.json"), manifest)
	writeFixtureJSON(t, filepath.Join(dir, "characters.json"), []masterRecord{{
		ID: 1313, Name: "", NameStatus: string(resolutionUnknown), GameVersion: "3.7.0", ResourceVersion: "3.7.8",
		SourceCommit: "test-commit", Language: "zh-Hant", Status: string(resolutionVerified), SourceTable: "roles",
	}})
	writeFixtureJSON(t, filepath.Join(dir, "monsters.json"), []masterRecord{})
	writeFixtureJSON(t, filepath.Join(dir, "skills.json"), []masterRecord{})
	if _, err := loadGameMaster(dir); err == nil {
		t.Fatal("localized-name coverage drift should be rejected")
	}
}

func writeFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
