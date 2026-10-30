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
	RawName       string `json:"rawName,omitempty"`
	Name          string `json:"name,omitempty"`
	NameStatus    string `json:"nameStatus"`
	NameTextKey   string `json:"nameTextKey,omitempty"`
	Subtitle      string `json:"subtitle,omitempty"`
	Language      string `json:"language,omitempty"`
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
	commonSkills   map[uint64]masterRecord
	// roleSkins maps RoleSkin.Id to RoleSkin.RoleId from the normalized roleskin table.
	roleSkins map[uint64]uint64
	// trialRoleParents maps TrialRoleInfo.Id to its canonical RoleInfo.Id.
	trialRoleParents map[uint64]uint64
	// phantomSkills holds PhantomSkill.PhantomSkillId values, the key space shared with PhantomItem.SkillId.
	phantomSkills map[uint64]struct{}
	// visions holds DT_Vision row keys from the typed vision table.
	visions map[uint64]struct{}
	// commonBullets holds CDT_CommonBulletData row keys with their developer label.
	commonBullets map[uint64]string
	// preloadBullets holds BulletPreload.BulletId values (actor-level bullet row keys).
	preloadBullets map[uint64]struct{}
	// Static actor chain: skin mesh / blueprint type / summon config -> model -> actor asset -> bullet row keys.
	roleSkinMesh    map[uint64]uint64
	modelActor      map[uint64]string
	blueprintModel  map[string]uint64
	summonBlueprint map[uint64]string
	visionCast      map[uint64][]uint64
	actorBullets    map[string]map[uint64]struct{}
	assetPath       map[string]string
	actorSkills     map[string]map[uint64]struct{}
	// skillActors maps EntitySkillPreload.SkillId to the distinct actor assets that list it.
	skillActors map[uint64]map[string]struct{}
	// commonBulletDamage maps a CDT_CommonBulletData row key to the row's own DamageId (absent when the row names none).
	commonBulletDamage map[uint64]uint64
	// skillLabels maps a SkillInfo row key to the distinct developer labels (zh-CN) found across SkillInfo tables.
	skillLabels   map[uint64]map[string]struct{}
	damage        map[uint64]damageMasterRecord
	sceneIdentity *sceneIdentityMaster
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

// hasVision reports whether the vision table has a row with this key.
func (m *gameMaster) hasVision(id uint64) bool {
	_, ok := m.visions[id]
	return ok
}

// hasPhantomSkill reports whether PhantomSkill has a row with this PhantomSkillId.
func (m *gameMaster) hasPhantomSkill(id uint64) bool {
	_, ok := m.phantomSkills[id]
	return ok
}

// roleSkinRole returns RoleSkin.RoleId for a RoleSkin.Id and reports whether the row exists.
func (m *gameMaster) roleSkinRole(skinID uint64) (uint64, bool) {
	value, ok := m.roleSkins[skinID]
	return value, ok
}

func (m *gameMaster) damageSkill(skillID uint64) (masterRecord, bool) {
	value, ok := m.skills[skillID]
	return value, ok
}

func (m *gameMaster) commonSkill(skillID uint64) (masterRecord, bool) {
	value, ok := m.commonSkills[skillID]
	return value, ok
}

// uniqueActorForSkill returns an actor only when a non-common SkillId belongs to exactly one
// distinct actor asset in the loaded EntitySkillPreload rows.
func (m *gameMaster) uniqueActorForSkill(skillID uint64) (string, bool) {
	if m == nil || skillID == 0 {
		return "", false
	}
	if _, common := m.commonSkills[skillID]; common {
		return "", false
	}
	owners := m.skillActors[skillID]
	if len(owners) != 1 {
		return "", false
	}
	for actor := range owners {
		return actor, true
	}
	return "", false
}
