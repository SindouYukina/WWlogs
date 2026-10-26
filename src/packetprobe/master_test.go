package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
