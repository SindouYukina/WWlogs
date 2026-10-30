package main

import (
	"testing"
)

func TestTemplateConfigTypeNamesDynamicallySpawnedEntities(t *testing.T) {
	resolver := newEntityResolver()
	resolver.entities[500] = runtimeEntity{entityID: 500, configID: 650000050, hasConfigID: true, configType: 4, hasConfigType: true, sourceMessageID: 25185}
	resolver.entities[501] = runtimeEntity{entityID: 501, configID: 650000050, hasConfigID: true, configType: 1, hasConfigType: true, sourceMessageID: 25185}
	masters := &gameMaster{locale: "zh-TW", sceneIdentity: &sceneIdentityMaster{
		templateCatalog: map[uint64]sceneTemplateCandidate{650000050: {TemplateID: 650000050, Name: "Boss", NameStatus: "VERIFIED"}},
	}}

	named := resolveEntityIdentity(resolver, masters, 500)
	if len(named.TemplateCandidates) != 1 || named.TemplateCandidates[0].Name != "Boss" || named.TemplateStatus != resolutionCorrelated {
		t.Fatalf("a Template(4) ConfigId must resolve to its TemplateConfig row: %+v", named)
	}
	level := resolveEntityIdentity(resolver, masters, 501)
	if len(level.TemplateCandidates) != 0 {
		t.Fatalf("a Level(1) ConfigId is not a TemplateConfig id and must not be named this way: %+v", level)
	}
}
