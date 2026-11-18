package capture

import (
	"testing"
)

func TestSceneMonsterResolutionRequiresUniqueMapScopedMatch(t *testing.T) {
	master := &GameMaster{SceneIdentity: &SceneIdentityMaster{
		InstanceMaps:  map[uint64]uint64{50: 80},
		levelMonsters: map[SceneLevelKey]uint64{{MapID: 80, EntityID: 900}: 700},
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
	master := testSceneTemplateMaster([]SceneTemplateCandidate{
		{TemplateID: 667000003, BlueprintType: "Monster_branch1.3_013", RawName: "candidate one"},
		{TemplateID: 667000004, BlueprintType: "Monster_branch1.3_013", RawName: "candidate two"},
	})
	resolver := &EntityResolver{
		Entities: map[uint64]RuntimeEntity{
			123: {EntityID: 123, ConfigID: 244750005, HasConfigID: true, ConfigType: 1, HasConfigType: true, SourceMessageID: CmdEntityAddNotify},
		},
		sceneInstanceID: 50, hasSceneInstanceID: true,
	}
	identity := ResolveEntityIdentity(resolver, master, 123)
	if identity.HasMonster || identity.Monster.ID != 0 {
		t.Fatalf("TemplateConfig must not be exposed as a MonsterId: %+v", identity)
	}
	if identity.TemplateStatus != ResolutionUnknown || len(identity.TemplateCandidates) != 2 {
		t.Fatalf("ambiguous template join must remain UNKNOWN with both candidates: %+v", identity)
	}
	if identity.TemplateCandidates[0].TemplateID != 667000003 || identity.TemplateCandidates[1].TemplateID != 667000004 {
		t.Fatalf("ambiguous template candidates changed order: %+v", identity.TemplateCandidates)
	}
	if identity.TemplateProof == "" {
		t.Fatal("ambiguous template evidence is missing")
	}
}

func testSceneTemplateMaster(candidates []SceneTemplateCandidate) *GameMaster {
	identity := &SceneIdentityMaster{
		InstanceMaps:    map[uint64]uint64{50: 3926},
		LevelTemplates:  map[SceneLevelKey][]SceneTemplateCandidate{{MapID: 3926, EntityID: 244750005}: candidates},
		TemplateCatalog: make(map[uint64]SceneTemplateCandidate, len(candidates)),
	}
	for _, candidate := range candidates {
		identity.TemplateCatalog[candidate.TemplateID] = candidate
	}
	return &GameMaster{Locale: "zh-TW", SceneIdentity: identity, Monsters: map[uint64]MasterRecord{}}
}
