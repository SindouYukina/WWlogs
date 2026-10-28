package main

import (
	"testing"
)

func TestSceneMonsterResolutionRequiresUniqueMapScopedMatch(t *testing.T) {
	master := &gameMaster{sceneIdentity: &sceneIdentityMaster{
		instanceMaps:  map[uint64]uint64{50: 80},
		levelMonsters: map[sceneLevelKey]uint64{{mapID: 80, entityID: 900}: 700},
	}}
	if id, ok := master.sceneMonster(50, 900); !ok || id != 700 {
		t.Fatalf("unique map-scoped MonsterId = %d, ok=%t", id, ok)
	}
	if _, ok := master.sceneMonster(50, 901); ok {
		t.Fatal("ambiguous or absent MonsterId must remain unresolved")
	}
	if _, ok := master.sceneMonster(51, 900); ok {
		t.Fatal("unmapped InstanceId must not resolve by entity ID alone")
	}
}

func TestSceneTemplateResolutionUsesActiveSessionSceneAndPreservesAmbiguity(t *testing.T) {
	master := testSceneTemplateMaster([]sceneTemplateCandidate{
		{TemplateID: 667000003, BlueprintType: "Monster_branch1.3_013", RawName: "candidate one"},
		{TemplateID: 667000004, BlueprintType: "Monster_branch1.3_013", RawName: "candidate two"},
	})
	resolver := &entityResolver{
		entities: map[uint64]runtimeEntity{
			123: {entityID: 123, configID: 244750005, hasConfigID: true, configType: 1, hasConfigType: true, sourceMessageID: 25185},
		},
		sceneInstanceID: 50, hasSceneInstanceID: true,
	}
	identity := resolveEntityIdentity(resolver, master, 123)
	if identity.HasMonster || identity.Monster.ID != 0 {
		t.Fatalf("TemplateConfig must not be exposed as a MonsterId: %+v", identity)
	}
	if identity.TemplateStatus != resolutionUnknown || len(identity.TemplateCandidates) != 2 {
		t.Fatalf("ambiguous template join must remain UNKNOWN with both candidates: %+v", identity)
	}
	if identity.TemplateCandidates[0].TemplateID != 667000003 || identity.TemplateCandidates[1].TemplateID != 667000004 {
		t.Fatalf("ambiguous template candidates changed order: %+v", identity.TemplateCandidates)
	}
	if identity.TemplateProof == "" {
		t.Fatal("ambiguous template evidence is missing")
	}
}

func testSceneTemplateMaster(candidates []sceneTemplateCandidate) *gameMaster {
	identity := &sceneIdentityMaster{
		instanceMaps:    map[uint64]uint64{50: 3926},
		levelTemplates:  map[sceneLevelKey][]sceneTemplateCandidate{{mapID: 3926, entityID: 244750005}: candidates},
		templateCatalog: make(map[uint64]sceneTemplateCandidate, len(candidates)),
	}
	for _, candidate := range candidates {
		identity.templateCatalog[candidate.TemplateID] = candidate
	}
	return &gameMaster{locale: "zh-TW", sceneIdentity: identity, monsters: map[uint64]masterRecord{}}
}
