package capture

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
	// Element is damage.element; HasElement is false when the row leaves it empty.
	Element     int32  `json:"element,omitempty"`
	HasElement  bool   `json:"hasElement,omitempty"`
	SourceHash  string `json:"sourceHash,omitempty"`
	SourceTable string `json:"sourceTable"`
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
	// DevLabel is the developer note of a template without an official name (templateconfig_dev_label_locale).
	DevLabel *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
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
	// visionDevLabels holds the dev_label_locale text of every Vision row (master locale, else zh-CN).
	visionDevLabels map[uint64]*combatEventLabelTextJSONL
	// concomitantVisionSummonSkills holds keys resolved by summon_skill_vision_ref.
	concomitantVisionSummonSkills map[uint64]struct{}
	// buffDefinitions holds normalized buff.Id keys for exact static-definition lookups.
	buffDefinitions map[uint64]struct{}
	// buffStatics holds the static parameters of a buff row that a live timer or display may quote.
	buffStatics map[uint64]buffStaticParameters
	// buffTerms holds the official glossary text tied to a buff row through buff_term_ref.
	buffTerms map[uint64]effectTermRecord
	// shieldDefinitions holds normalized shield.Id keys for ShieldUpdateNotify references.
	shieldDefinitions map[uint64]struct{}
	// commonBullets holds CDT_CommonBulletData row keys with their developer label.
	commonBullets map[uint64]string
	// preloadBullets holds BulletPreload.BulletId values (actor-level bullet row keys).
	preloadBullets map[uint64]struct{}
	// Static actor chain: skin mesh / blueprint type / summon config -> model -> actor asset -> bullet row keys.
	roleSkinMesh         map[uint64]uint64
	modelActor           map[uint64]string
	blueprintModel       map[string]uint64
	blueprintEntityTypes map[string]map[string]struct{}
	summonBlueprint      map[uint64]string
	visionCast           map[uint64][]uint64
	actorBullets         map[string]map[uint64]struct{}
	assetPath            map[string]string
	actorSkills          map[string]map[uint64]struct{}
	// skillActors maps EntitySkillPreload.SkillId to the distinct actor assets that list it.
	skillActors map[uint64]map[string]struct{}
	// commonBulletDamage maps a CDT_CommonBulletData row key to the row's own DamageId (absent when the row names none).
	commonBulletDamage map[uint64]uint64
	// damageOwners maps a DamageId to the skills and Buffs that reference it (damage_owners.go).
	damageOwners map[uint64][]damageOwnerJSONL
	// commonBulletDevLabels holds the dev_label_locale text of every CDT_CommonBulletData row (master locale, else zh-CN).
	commonBulletDevLabels map[uint64]*combatEventLabelTextJSONL
	// skillLabels maps a SkillInfo row key to the distinct developer labels (zh-CN) found across SkillInfo tables.
	skillLabels map[uint64]map[string]struct{}
	// buffOwners maps a Buff id to the named features (chain node, echo set, weapon passive, skill) that list it.
	buffOwners map[uint64][]buffOwnerText
	// propertyNames maps a PropertyIndex id (the attribute id of the protocol) to its key and localized name.
	propertyNames map[int64]propertyName
	// abnormalDamageIDs holds the DamageIds named by the columns Abnormal<id> of AbnormalDamageConfig.
	abnormalDamageIDs map[uint64]struct{}
	// fsm holds the AI state-machine node identities used to name ChangeStateNotify ids.
	fsm *fsmMaster
	// skillLabelText holds skill_dev_label_locale rows (master locale and zh-CN) by skill id and locale.
	skillLabelText map[uint64]map[string]localizedSkillLabel
	// elementNames maps damage.element to the ElementInfo name of the master locale.
	elementNames  map[int32]string
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

// templateByID returns the TemplateConfig candidate whose id equals a runtime EntityPb.ConfigId.
func (m *gameMaster) templateByID(templateID uint64) (sceneTemplateCandidate, bool) {
	if m == nil || m.sceneIdentity == nil {
		return sceneTemplateCandidate{}, false
	}
	candidate, ok := m.sceneIdentity.templateCatalog[templateID]
	return candidate, ok
}

func (m *gameMaster) sceneMapConfigID(instanceID uint64) (uint64, bool) {
	if m == nil || m.sceneIdentity == nil {
		return 0, false
	}
	mapID, ok := m.sceneIdentity.instanceMaps[instanceID]
	return mapID, ok
}

type vehicleActorMasterRecord struct {
	ConfigID       uint64
	BlueprintType  string
	EntityType     string
	ActorBlueprint string
}

// vehicleActorForFollowerConfig resolves a follower config only when its static chain
// identifies one BattleVehicle actor and that actor lists the observed SkillId and BulletId.
func (m *gameMaster) vehicleActorForFollowerConfig(configID, skillID, bulletID uint64) (vehicleActorMasterRecord, bool) {
	if m == nil || configID == 0 || skillID == 0 || bulletID == 0 {
		return vehicleActorMasterRecord{}, false
	}
	blueprint, ok := m.summonBlueprint[configID]
	if !ok {
		return vehicleActorMasterRecord{}, false
	}
	entityTypes := m.blueprintEntityTypes[blueprint]
	if len(entityTypes) != 1 {
		return vehicleActorMasterRecord{}, false
	}
	if _, ok := entityTypes["BattleVehicle"]; !ok {
		return vehicleActorMasterRecord{}, false
	}
	actor, ok := m.actorOfBlueprint(blueprint)
	if !ok {
		return vehicleActorMasterRecord{}, false
	}
	if _, ok := m.actorSkills[actor][skillID]; !ok {
		return vehicleActorMasterRecord{}, false
	}
	if _, ok := m.actorBullets[actor][bulletID]; !ok {
		return vehicleActorMasterRecord{}, false
	}
	actorBlueprint := m.assetPath[actor]
	if actorBlueprint == "" {
		return vehicleActorMasterRecord{}, false
	}
	return vehicleActorMasterRecord{
		ConfigID: configID, BlueprintType: blueprint, EntityType: "BattleVehicle", ActorBlueprint: actorBlueprint,
	}, true
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

// hasBuffDefinition reports whether buff has a normalized row with this key.
func (m *gameMaster) hasBuffDefinition(id uint64) bool {
	if m == nil {
		return false
	}
	_, ok := m.buffDefinitions[id]
	return ok
}

// buffStaticParameters are the definition fields of one buff row. They describe the
// definition, never a live instance, and carry no official display name.
type buffStaticParameters struct {
	durationPolicy    int64
	hasDurationPolicy bool
	stackLimit        uint64
	hasStackLimit     bool
	durationSeconds   float64
	hasDuration       bool
	tags              []string
}

// effectTermRecord is the localized glossary term of an element-ailment buff row. The link comes
// from the row's GameplayTag, so it is correlated evidence and never an engine-keyed name.
type effectTermRecord struct {
	termID      uint64
	name        string
	description string
	status      resolutionStatus
}

// buffTerm returns the glossary term tied to a buff row, in the master locale.
func (m *gameMaster) buffTerm(id uint64) (effectTermRecord, bool) {
	if m == nil {
		return effectTermRecord{}, false
	}
	value, ok := m.buffTerms[id]
	return value, ok
}

// buffStatic returns the static parameters of an exact buff row.
func (m *gameMaster) buffStatic(id uint64) (buffStaticParameters, bool) {
	if m == nil {
		return buffStaticParameters{}, false
	}
	value, ok := m.buffStatics[id]
	return value, ok
}

// hasShieldDefinition reports whether shield has a normalized row with this key.
func (m *gameMaster) hasShieldDefinition(id uint64) bool {
	if m == nil {
		return false
	}
	_, ok := m.shieldDefinitions[id]
	return ok
}

// hasPhantomSkill reports whether PhantomSkill has a row with this PhantomSkillId.
func (m *gameMaster) hasPhantomSkill(id uint64) bool {
	_, ok := m.phantomSkills[id]
	return ok
}

// hasConcomitantVisionSummonSkill reports whether the normalized Type 1 crosswalk contains the key.
func (m *gameMaster) hasConcomitantVisionSummonSkill(id uint64) bool {
	if m == nil {
		return false
	}
	_, ok := m.concomitantVisionSummonSkills[id]
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
