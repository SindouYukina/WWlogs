package main

type masterRecord struct {
	ID            uint64 `json:"id"`
	Name          string `json:"name"`
	NameStatus    string `json:"nameStatus"`
	Language      string `json:"language"`
	Status        string `json:"status"`
	SourceTable   string `json:"sourceTable"`
	SkinID        uint64 `json:"skinId,omitempty"`
	RoleID        uint64 `json:"roleId,omitempty"`
	RoleStatus    string `json:"roleStatus,omitempty"`
	CharacterName string `json:"characterName,omitempty"`
	NameKind      string `json:"nameKind,omitempty"`
}

type damageMasterRecord struct {
	ID            uint64 `json:"id"`
	CalculateType int32  `json:"calculateType"`
	SourceHash    string `json:"sourceHash,omitempty"`
	SourceTable   string `json:"sourceTable"`
}

type masterManifest struct {
	ClientRegion string
	Language     string
	SourceHash   string
}

type sceneTemplateCandidate struct {
	TemplateID    uint64 `json:"templateId"`
	BlueprintType string `json:"blueprintType"`
	RawName       string `json:"rawName"`
}

type sceneIdentityMaster struct {
	instanceMaps    map[uint64]uint64
	levelMonsters   map[sceneLevelKey]uint64
	levelTemplates  map[sceneLevelKey][]sceneTemplateCandidate
	templateCatalog map[uint64]sceneTemplateCandidate
}

type sceneLevelKey struct {
	mapID    uint64
	entityID uint64
}

type gameMaster struct {
	manifest       masterManifest
	databasePath   string
	locale         string
	characters     map[uint64]masterRecord
	charactersSkin map[uint64]masterRecord
	ambiguousSkins map[uint64]struct{}
	monsters       map[uint64]masterRecord
	skills         map[uint64]masterRecord
	damage         map[uint64]damageMasterRecord
	sceneIdentity  *sceneIdentityMaster
}

func (m *gameMaster) sceneMonster(instanceID, entityID uint64) (uint64, bool) {
	if m == nil || m.sceneIdentity == nil {
		return 0, false
	}
	mapID, ok := m.sceneMapConfigID(instanceID)
	if !ok {
		return 0, false
	}
	monsterID, ok := m.sceneIdentity.levelMonsters[sceneLevelKey{mapID: mapID, entityID: entityID}]
	return monsterID, ok
}

func (m *gameMaster) sceneTemplates(instanceID, entityID uint64) ([]sceneTemplateCandidate, bool) {
	if m == nil || m.sceneIdentity == nil {
		return nil, false
	}
	mapID, ok := m.sceneMapConfigID(instanceID)
	if !ok {
		return nil, false
	}
	templates, ok := m.sceneIdentity.levelTemplates[sceneLevelKey{mapID: mapID, entityID: entityID}]
	return templates, ok
}

func (m *gameMaster) sceneMapConfigID(instanceID uint64) (uint64, bool) {
	if m == nil || m.sceneIdentity == nil {
		return 0, false
	}
	mapID, ok := m.sceneIdentity.instanceMaps[instanceID]
	return mapID, ok
}

func (m *gameMaster) characterBySkin(skinID uint64) (masterRecord, bool) {
	if _, ambiguous := m.ambiguousSkins[skinID]; ambiguous {
		return masterRecord{}, false
	}
	value, ok := m.charactersSkin[skinID]
	return value, ok
}
