//go:build wwdev

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

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
