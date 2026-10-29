//go:build wwdev

package main

import (
	"encoding/json"
	"testing"
)

func TestSceneTemplateDisplayRequiresUniqueLocalizedCandidate(t *testing.T) {
	candidate := sceneTemplateCandidate{TemplateID: 7, Name: "Localized Boss", NameStatus: string(resolutionVerified), NameTextKey: "Entity.Name", Language: "en-US"}
	for _, count := range []int{1, 2} {
		identity := entityIdentity{TemplateStatus: resolutionCorrelated, TemplateCandidates: []sceneTemplateCandidate{candidate}}
		if count == 2 {
			identity.TemplateCandidates = append(identity.TemplateCandidates, sceneTemplateCandidate{TemplateID: 8, Name: "Localized Boss", NameStatus: string(resolutionVerified)})
		}
		destination := combatEventEntityJSONL{}
		applySceneTemplateJSONL(&destination, identity)
		if count == 1 && (destination.SceneTemplate.Name != "Localized Boss" || destination.SceneTemplate.NameStatus != resolutionVerified) {
			t.Fatalf("unique localized name not exposed: %+v", destination.SceneTemplate)
		}
		if count == 2 && (destination.SceneTemplate.Name != "" || destination.SceneTemplate.NameStatus != resolutionUnknown) {
			t.Fatalf("ambiguous names selected a candidate: %+v", destination.SceneTemplate)
		}
	}
}

func TestUnknownTemplateIdentityDoesNotPublishLocalizedTopLevelName(t *testing.T) {
	identity := entityIdentity{TemplateStatus: resolutionUnknown, TemplateCandidates: []sceneTemplateCandidate{{TemplateID: 7, Name: "Candidate", NameStatus: string(resolutionVerified)}}}
	destination := combatEventEntityJSONL{}
	applySceneTemplateJSONL(&destination, identity)
	if destination.SceneTemplate.Name != "" || destination.SceneTemplate.NameStatus != resolutionUnknown {
		t.Fatalf("unknown identity published a display name: %+v", destination.SceneTemplate)
	}
}

func TestSceneTemplateResolutionDoesNotConflateTemplateConfigWithMonsterId(t *testing.T) {
	master := testSceneTemplateMaster([]sceneTemplateCandidate{{
		TemplateID: 667000003, BlueprintType: "Monster_branch1.3_013", RawName: "internal boss label",
	}})
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
		t.Fatalf("map-scoped TemplateConfig was not correlated: %+v", identity)
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
		t.Fatalf("template identity JSON lost evidence or unknown localization: %s", encoded)
	}
}
