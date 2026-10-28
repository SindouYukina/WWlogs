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

func TestResolveSceneTemplateWithoutConflatingMonsterId(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{GameVersion: "3.7.1", ClientRegion: "Global"}
	source := sceneIdentityFile{
		Manifest: sceneIdentityManifest{
			GameVersion: "3.7.1", ClientRegion: "Global", Platform: "Windows UHD",
			SourceHash: strings.Repeat("b", 64), InstanceRows: 1, LevelRows: 0,
			TemplateRows: 1, TemplateCatalogRows: 1,
		},
		Instances: []sceneInstanceRecord{{ID: 50, MapConfigID: 3926}},
		TemplateCatalog: []sceneTemplateCandidate{{
			TemplateID: 667000003, BlueprintType: "Monster_branch1.3_013", RawName: "internal boss label",
		}},
		LevelTemplates: []sceneLevelTemplateRecord{{MapID: 3926, EntityID: 244750005, TemplateIDs: []uint64{667000003}}},
	}
	path := filepath.Join(dir, "entity-identity.json")
	writeFixtureJSON(t, path, source)
	identityData, err := loadSceneIdentity(path, manifest)
	if err != nil {
		t.Fatal(err)
	}
	master := &gameMaster{sceneIdentity: identityData, monsters: map[uint64]masterRecord{}}
	resolver := &entityResolver{entities: map[uint64]runtimeEntity{
		123: {
			entityID: 123, sceneInstanceID: 50, hasSceneInstanceID: true,
			configID: 244750005, hasConfigID: true, configType: 1, hasConfigType: true,
			sourceMessageID: 18559,
		},
	}}
	identity := resolveEntityIdentity(resolver, master, 123)
	if identity.HasMonster || identity.Monster.ID != 0 {
		t.Fatalf("TemplateConfig must not be exposed as a MonsterId: %+v", identity)
	}
	if len(identity.TemplateCandidates) != 1 || identity.TemplateCandidates[0].TemplateID != 667000003 || identity.TemplateStatus != resolutionCorrelated {
		t.Fatalf("unique map-scoped TemplateConfig was not correlated: %+v", identity)
	}
	if identity.TemplateCandidates[0].RawName != "internal boss label" || identity.TemplateProof == "" {
		t.Fatalf("template raw label or evidence missing: %+v", identity)
	}

	view := (combatEvent{Target: identity}).jsonlView(master)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	target := decoded["target"].(map[string]any)
	if _, exists := target["monster"]; exists {
		t.Fatalf("template identity was mislabeled as monster: %s", encoded)
	}
	template, ok := target["sceneTemplate"].(map[string]any)
	if !ok || template["identityType"] != "template-derived" || template["status"] != string(resolutionCorrelated) || template["nameStatus"] != string(resolutionUnknown) {
		t.Fatalf("template identity JSON lost provenance or unknown localization: %s", encoded)
	}
	candidates := template["candidates"].([]any)
	candidate := candidates[0].(map[string]any)
	if candidate["templateId"] != float64(667000003) || candidate["rawName"] != "internal boss label" {
		t.Fatalf("template identity JSON changed raw candidate: %s", encoded)
	}
}

func TestResolveSceneTemplateUsesCurrentSceneForEntityAdd(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{GameVersion: "3.7.1", ClientRegion: "Global"}
	source := sceneIdentityFile{
		Manifest: sceneIdentityManifest{
			GameVersion: "3.7.1", ClientRegion: "Global", Platform: "Windows UHD",
			SourceHash: strings.Repeat("c", 64), InstanceRows: 1, LevelRows: 0,
			TemplateRows: 1, TemplateCatalogRows: 1,
		},
		Instances:       []sceneInstanceRecord{{ID: 50, MapConfigID: 3926}},
		TemplateCatalog: []sceneTemplateCandidate{{TemplateID: 667000003, BlueprintType: "Monster_branch1.3_013", RawName: "internal boss label"}},
		LevelTemplates:  []sceneLevelTemplateRecord{{MapID: 3926, EntityID: 244750005, TemplateIDs: []uint64{667000003}}},
	}
	path := filepath.Join(dir, "entity-identity.json")
	writeFixtureJSON(t, path, source)
	identityData, err := loadSceneIdentity(path, manifest)
	if err != nil {
		t.Fatal(err)
	}
	master := &gameMaster{sceneIdentity: identityData, monsters: map[uint64]masterRecord{}}
	resolver := &entityResolver{
		entities: map[uint64]runtimeEntity{
			123: {
				entityID: 123, configID: 244750005, hasConfigID: true,
				configType: 1, hasConfigType: true, sourceMessageID: 25185,
			},
		},
		sceneInstanceID: 50, hasSceneInstanceID: true,
	}
	identity := resolveEntityIdentity(resolver, master, 123)
	if identity.HasMonster || identity.Monster.ID != 0 {
		t.Fatalf("TemplateConfig must not be exposed as a MonsterId: %+v", identity)
	}
	if len(identity.TemplateCandidates) != 1 || identity.TemplateCandidates[0].TemplateID != 667000003 || identity.TemplateStatus != resolutionCorrelated {
		t.Fatalf("EntityAddNotify entity did not use the active session scene: %+v", identity)
	}
	if !strings.Contains(identity.TemplateProof, "current session JoinSceneNotify context") {
		t.Fatalf("template proof omits the scene-context source: %q", identity.TemplateProof)
	}

	resolver.hasSceneInstanceID = false
	identity = resolveEntityIdentity(resolver, master, 123)
	if len(identity.TemplateCandidates) != 0 || identity.TemplateStatus != "" {
		t.Fatalf("template candidate must remain unresolved without scene context: %+v", identity)
	}
}

func TestResolveSceneTemplateKeepsAmbiguousMatchesUnknown(t *testing.T) {
	dir := t.TempDir()
	manifest := masterManifest{GameVersion: "3.7.1", ClientRegion: "Global"}
	source := sceneIdentityFile{
		Manifest: sceneIdentityManifest{
			GameVersion: "3.7.1", ClientRegion: "Global", Platform: "Windows UHD",
			SourceHash: strings.Repeat("d", 64), InstanceRows: 1, LevelRows: 0,
			TemplateRows: 1, TemplateCatalogRows: 2,
		},
		Instances: []sceneInstanceRecord{{ID: 50, MapConfigID: 3926}},
		TemplateCatalog: []sceneTemplateCandidate{
			{TemplateID: 667000003, BlueprintType: "Monster_branch1.3_013", RawName: "candidate one"},
			{TemplateID: 667000004, BlueprintType: "Monster_branch1.3_013", RawName: "candidate two"},
		},
		LevelTemplates: []sceneLevelTemplateRecord{{MapID: 3926, EntityID: 244750005, TemplateIDs: []uint64{667000003, 667000004}}},
	}
	path := filepath.Join(dir, "entity-identity.json")
	writeFixtureJSON(t, path, source)
	identityData, err := loadSceneIdentity(path, manifest)
	if err != nil {
		t.Fatal(err)
	}
	master := &gameMaster{sceneIdentity: identityData, monsters: map[uint64]masterRecord{}}
	resolver := &entityResolver{
		entities: map[uint64]runtimeEntity{
			123: {entityID: 123, configID: 244750005, hasConfigID: true, configType: 1, hasConfigType: true, sourceMessageID: 25185},
		},
		sceneInstanceID: 50, hasSceneInstanceID: true,
	}
	identity := resolveEntityIdentity(resolver, master, 123)
	if identity.TemplateStatus != resolutionUnknown || len(identity.TemplateCandidates) != 2 {
		t.Fatalf("ambiguous template join must remain UNKNOWN with both candidates: %+v", identity)
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
