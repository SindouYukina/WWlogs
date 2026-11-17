package capture

type MasterRecord struct {
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

type DamageMasterRecord struct {
	ID            uint64 `json:"id"`
	CalculateType int32  `json:"calculateType"`
	// Element is damage.element; HasElement is false when the row leaves it empty.
	Element     int32  `json:"element,omitempty"`
	HasElement  bool   `json:"hasElement,omitempty"`
	SourceHash  string `json:"sourceHash,omitempty"`
	SourceTable string `json:"sourceTable"`
}

type MasterManifest struct {
	ClientRegion string
	Language     string
	SourceHash   string
}

type SceneTemplateCandidate struct {
	TemplateID    uint64 `json:"templateId"`
	BlueprintType string `json:"blueprintType"`
	RawName       string `json:"rawName,omitempty"`
	Name          string `json:"name,omitempty"`
	NameStatus    string `json:"nameStatus"`
	NameTextKey   string `json:"nameTextKey,omitempty"`
	Subtitle      string `json:"subtitle,omitempty"`
	Language      string `json:"language,omitempty"`
	// DevLabel is the developer note of a template without an official name (templateconfig_dev_label_locale).
	DevLabel *CombatEventLabelTextJSONL `json:"devLabel,omitempty"`
}

type SceneIdentityMaster struct {
	InstanceMaps    map[uint64]uint64
	levelMonsters   map[SceneLevelKey]uint64
	LevelTemplates  map[SceneLevelKey][]SceneTemplateCandidate
	TemplateCatalog map[uint64]SceneTemplateCandidate
	// prefabMonsters and prefabTemplates are the same relations for an entity of a prefab (EntityConfigType Prefab),
	// keyed by PrefabConfig.Id and the entity's EntityData.Id inside it instead of by map and level entity.
	prefabMonsters  map[prefabEntityKey]uint64
	prefabTemplates map[prefabEntityKey][]SceneTemplateCandidate
}

type SceneLevelKey struct {
	MapID    uint64
	EntityID uint64
}

// prefabEntityKey is one entity of a prefab: PrefabConfig.Id (EntityPb.PrefabId) and EntityData.Id (EntityPb.ConfigId).
type prefabEntityKey struct {
	prefabID uint64
	entityID uint64
}

type GameMaster struct {
	Manifest       MasterManifest
	DatabasePath   string
	Locale         string
	Characters     map[uint64]MasterRecord
	charactersSkin map[uint64]MasterRecord
	ambiguousSkins map[uint64]struct{}
	Monsters       map[uint64]MasterRecord
	Skills         map[uint64]MasterRecord
	CommonSkills   map[uint64]MasterRecord
	// roleSkins maps RoleSkin.Id to RoleSkin.RoleId from the normalized roleskin table.
	roleSkins map[uint64]uint64
	// TrialRoleParents maps TrialRoleInfo.Id to its canonical RoleInfo.Id.
	TrialRoleParents map[uint64]uint64
	// phantomSkills holds PhantomSkill.PhantomSkillId values, the key space shared with PhantomItem.SkillId.
	phantomSkills map[uint64]struct{}
	// Visions holds DT_Vision row keys from the typed vision table.
	Visions map[uint64]struct{}
	// VisionDevLabels holds the dev_label_locale text of every Vision row (master locale, else zh-CN).
	VisionDevLabels map[uint64]*CombatEventLabelTextJSONL
	// concomitantVisionSummonSkills holds keys resolved by summon_skill_vision_ref.
	concomitantVisionSummonSkills map[uint64]struct{}
	// BuffDefinitions holds normalized buff.Id keys for exact static-definition lookups.
	BuffDefinitions map[uint64]struct{}
	// BuffStatics holds the static parameters of a buff row that a live timer or display may quote.
	BuffStatics map[uint64]BuffStaticParameters
	// BuffTerms holds the official glossary text tied to a buff row through buff_term_ref.
	BuffTerms map[uint64]EffectTermRecord
	// ShieldDefinitions holds normalized shield.Id keys for ShieldUpdateNotify references.
	ShieldDefinitions map[uint64]struct{}
	// CommonBullets holds CDT_CommonBulletData row keys with their developer label.
	CommonBullets map[uint64]string
	// PreloadBullets holds BulletPreload.BulletId values (actor-level bullet row keys).
	PreloadBullets map[uint64]struct{}
	// Static actor chain: skin mesh / blueprint type / summon config -> model -> actor asset -> bullet row keys.
	RoleSkinMesh         map[uint64]uint64
	ModelActor           map[uint64]string
	BlueprintModel       map[string]uint64
	BlueprintEntityTypes map[string]map[string]struct{}
	SummonBlueprint      map[uint64]string
	VisionCast           map[uint64][]uint64
	ActorBullets         map[string]map[uint64]struct{}
	AssetPath            map[string]string
	ActorSkills          map[string]map[uint64]struct{}
	// SkillActors maps EntitySkillPreload.SkillId to the distinct actor assets that list it.
	SkillActors map[uint64]map[string]struct{}
	// CommonBulletDamage maps a CDT_CommonBulletData row key to the row's own DamageId (absent when the row names none).
	CommonBulletDamage map[uint64]uint64
	// DamageOwners maps a DamageId to the skills and Buffs that reference it (damage_owners.go).
	DamageOwners map[uint64][]DamageOwnerJSONL
	// CommonBulletDevLabels holds the dev_label_locale text of every CDT_CommonBulletData row (master locale, else zh-CN).
	CommonBulletDevLabels map[uint64]*CombatEventLabelTextJSONL
	// skillLabels maps a SkillInfo row key to the distinct developer labels (zh-CN) found across SkillInfo tables.
	skillLabels map[uint64]map[string]struct{}
	// buffOwners maps a Buff id to the named features (chain node, echo set, weapon passive, skill) that list it.
	buffOwners map[uint64][]BuffOwnerText
	// PropertyNames maps a PropertyIndex id (the attribute id of the protocol) to its key and localized name.
	PropertyNames map[int64]PropertyName
	// AbnormalDamageIDs holds the negative-status DamageIds: those named by the columns Abnormal<id> of AbnormalDamageConfig and
	// every damage row of the server-only negative-status formula (FormulaType 9), such as 1007 of the Glacio Chafe variant.
	AbnormalDamageIDs map[uint64]struct{}
	// NegativeStatusExecutors maps a Buff whose extra effect type is one of abnormalDamageIDs to that type: the negative-status
	// DamageId the server executes for the status (damage_notify.go).
	NegativeStatusExecutors map[uint64]uint64
	// NegativeStatusSettlements holds how each executing status Buff settles (negative_status_credit.go).
	NegativeStatusSettlements map[uint64]NegativeStatusSettlementRule
	// InvokePeriodTargets maps an InvokePeriod Buff (extra effect type 67) to the negative-status Buffs whose execution it
	// forces at once (negative_status_credit.go).
	InvokePeriodTargets map[uint64][]uint64
	// negativeStatusTerms names a negative-status DamageId by the glossary term its executing status Buffs share.
	negativeStatusTerms map[uint64]negativeStatusTerm
	// FSM holds the AI state-machine node identities used to name ChangeStateNotify ids.
	FSM *FSMMaster
	// skillLabelText holds skill_dev_label_locale rows (master locale and zh-CN) by skill id and locale.
	skillLabelText map[uint64]map[string]LocalizedSkillLabel
	// ElementNames maps damage.element to the ElementInfo name of the master locale.
	ElementNames  map[int32]string
	Damage        map[uint64]DamageMasterRecord
	SceneIdentity *SceneIdentityMaster
	// skillNames holds the static relations behind derived skill-row names (skill_names.go).
	skillNames *skillNameMaster
}

func (m *GameMaster) sceneMonster(instanceID, entityID uint64) (uint64, bool) {
	if m == nil || m.SceneIdentity == nil {
		return 0, false
	}
	mapID, ok := m.sceneMapConfigID(instanceID)
	if !ok {
		return 0, false
	}
	monsterID, ok := m.SceneIdentity.levelMonsters[SceneLevelKey{MapID: mapID, EntityID: entityID}]
	return monsterID, ok
}

func (m *GameMaster) sceneTemplates(instanceID, entityID uint64) ([]SceneTemplateCandidate, bool) {
	if m == nil || m.SceneIdentity == nil {
		return nil, false
	}
	mapID, ok := m.sceneMapConfigID(instanceID)
	if !ok {
		return nil, false
	}
	templates, ok := m.SceneIdentity.LevelTemplates[SceneLevelKey{MapID: mapID, EntityID: entityID}]
	return templates, ok
}

// prefabMonster returns the MonsterInfo of an entity of a prefab when exactly one MonsterInfo shares its BlueprintType.
func (m *GameMaster) prefabMonster(prefabID, entityID uint64) (uint64, bool) {
	if m == nil || m.SceneIdentity == nil {
		return 0, false
	}
	monsterID, ok := m.SceneIdentity.prefabMonsters[prefabEntityKey{prefabID: prefabID, entityID: entityID}]
	return monsterID, ok
}

// prefabTemplates returns the TemplateConfig candidates of an entity of a prefab.
func (m *GameMaster) prefabTemplates(prefabID, entityID uint64) ([]SceneTemplateCandidate, bool) {
	if m == nil || m.SceneIdentity == nil {
		return nil, false
	}
	templates, ok := m.SceneIdentity.prefabTemplates[prefabEntityKey{prefabID: prefabID, entityID: entityID}]
	return templates, ok
}

// PrefabEntityIdentity is how the product identifies an entity of a prefab (EntityConfigType Prefab): every TemplateConfig
// its relation names in the Master's order, and the MonsterInfo when exactly one shares its BlueprintType (0 otherwise). The
// resolver names the entity only when exactly one template answers. The version readiness gate compares it with the
// client's own PrefabConfig.
type PrefabEntityIdentity struct {
	TemplateIDs []uint64
	MonsterID   uint64
}

// PrefabEntity returns the identity the product gives the entity entityID of prefab prefabID; ok is false when the Master
// has no relation for it.
func (m *MasterData) PrefabEntity(prefabID, entityID uint64) (PrefabEntityIdentity, bool) {
	if m == nil {
		return PrefabEntityIdentity{}, false
	}
	templates, hasTemplates := m.masters.prefabTemplates(prefabID, entityID)
	monster, hasMonster := m.masters.prefabMonster(prefabID, entityID)
	identity := PrefabEntityIdentity{MonsterID: monster}
	for _, candidate := range templates {
		identity.TemplateIDs = append(identity.TemplateIDs, candidate.TemplateID)
	}
	return identity, hasTemplates || hasMonster
}

// templateByID returns the TemplateConfig candidate whose id equals a runtime EntityPb.ConfigId.
func (m *GameMaster) templateByID(templateID uint64) (SceneTemplateCandidate, bool) {
	if m == nil || m.SceneIdentity == nil {
		return SceneTemplateCandidate{}, false
	}
	candidate, ok := m.SceneIdentity.TemplateCatalog[templateID]
	return candidate, ok
}

func (m *GameMaster) sceneMapConfigID(instanceID uint64) (uint64, bool) {
	if m == nil || m.SceneIdentity == nil {
		return 0, false
	}
	mapID, ok := m.SceneIdentity.InstanceMaps[instanceID]
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
func (m *GameMaster) vehicleActorForFollowerConfig(configID, skillID, bulletID uint64) (vehicleActorMasterRecord, bool) {
	if m == nil || configID == 0 || skillID == 0 || bulletID == 0 {
		return vehicleActorMasterRecord{}, false
	}
	blueprint, ok := m.SummonBlueprint[configID]
	if !ok {
		return vehicleActorMasterRecord{}, false
	}
	entityTypes := m.BlueprintEntityTypes[blueprint]
	if len(entityTypes) != 1 {
		return vehicleActorMasterRecord{}, false
	}
	if _, ok := entityTypes["BattleVehicle"]; !ok {
		return vehicleActorMasterRecord{}, false
	}
	actor, ok := m.ActorOfBlueprint(blueprint)
	if !ok {
		return vehicleActorMasterRecord{}, false
	}
	if _, ok := m.ActorSkills[actor][skillID]; !ok {
		return vehicleActorMasterRecord{}, false
	}
	if _, ok := m.ActorBullets[actor][bulletID]; !ok {
		return vehicleActorMasterRecord{}, false
	}
	actorBlueprint := m.AssetPath[actor]
	if actorBlueprint == "" {
		return vehicleActorMasterRecord{}, false
	}
	return vehicleActorMasterRecord{
		ConfigID: configID, BlueprintType: blueprint, EntityType: "BattleVehicle", ActorBlueprint: actorBlueprint,
	}, true
}

func (m *GameMaster) characterBySkin(skinID uint64) (MasterRecord, bool) {
	if _, ambiguous := m.ambiguousSkins[skinID]; ambiguous {
		return MasterRecord{}, false
	}
	value, ok := m.charactersSkin[skinID]
	return value, ok
}

// hasVision reports whether the vision table has a row with this key.
func (m *GameMaster) hasVision(id uint64) bool {
	_, ok := m.Visions[id]
	return ok
}

// HasBuffDefinition reports whether buff has a normalized row with this key.
func (m *GameMaster) HasBuffDefinition(id uint64) bool {
	if m == nil {
		return false
	}
	_, ok := m.BuffDefinitions[id]
	return ok
}

// BuffStaticParameters are the definition fields of one buff row. They describe the
// definition, never a live instance, and carry no official display name.
type BuffStaticParameters struct {
	DurationPolicy    int64
	HasDurationPolicy bool
	StackLimit        uint64
	HasStackLimit     bool
	DurationSeconds   float64
	HasDuration       bool
	Tags              []string
	// Attribute is buff.game_attribute_id, the protocol EAttributeType the row's modifier changes;
	// hasModifier reports at least one buff_modifier_magnitude row for it.
	Attribute    uint64
	HasAttribute bool
	HasModifier  bool
	// hudCueTypes are the distinct HUD cue types (ascending) of the row's GameplayCueIds that have a Path
	// (effect_hud_cues.go): the game draws the effect in a HUD buff bar.
	hudCueTypes []int32
	// hudPolarity and hudHiddenOnMonsterHead are the direction and monster-head visibility of the row's first HUD cue
	// (effect_hud_cues.go); polarity stays empty for a row without one.
	hudPolarity            EffectPolarity
	hudHiddenOnMonsterHead bool
}

// EffectTermRecord is the localized glossary term of an element-ailment buff row. The link comes
// from the row's GameplayTag, so it is correlated evidence and never an engine-keyed name.
type EffectTermRecord struct {
	TermID      uint64
	Name        string
	Description string
	Status      ResolutionStatus
}

// buffTerm returns the glossary term tied to a buff row, in the master locale.
func (m *GameMaster) buffTerm(id uint64) (EffectTermRecord, bool) {
	if m == nil {
		return EffectTermRecord{}, false
	}
	value, ok := m.BuffTerms[id]
	return value, ok
}

// buffStatic returns the static parameters of an exact buff row.
func (m *GameMaster) buffStatic(id uint64) (BuffStaticParameters, bool) {
	if m == nil {
		return BuffStaticParameters{}, false
	}
	value, ok := m.BuffStatics[id]
	return value, ok
}

// hasPhantomSkill reports whether PhantomSkill has a row with this PhantomSkillId.
func (m *GameMaster) hasPhantomSkill(id uint64) bool {
	_, ok := m.phantomSkills[id]
	return ok
}

// hasConcomitantVisionSummonSkill reports whether the normalized Type 1 crosswalk contains the key.
func (m *GameMaster) hasConcomitantVisionSummonSkill(id uint64) bool {
	if m == nil {
		return false
	}
	_, ok := m.concomitantVisionSummonSkills[id]
	return ok
}

// StaticRoleID is the RoleInfo whose static data describes the runtime RoleId roleID. A trial character
// (TrialRoleInfo.Id, such as 15127004) plays its parent role (TrialRoleInfo.ParentId, 1512): its skills, Outro,
// element and name are the parent's, and every DamageId the 2026-11-12 Global 3.8 capture credited to its three trial
// roles is listed by a UI skill of the parent role. Statistics keep the runtime RoleId; only Master lookups use this.
func (m *GameMaster) StaticRoleID(roleID uint64) uint64 {
	if m != nil {
		if parent, ok := m.TrialRoleParents[roleID]; ok {
			return parent
		}
	}
	return roleID
}

// roleSkinRole returns RoleSkin.RoleId for a RoleSkin.Id and reports whether the row exists.
func (m *GameMaster) roleSkinRole(skinID uint64) (uint64, bool) {
	value, ok := m.roleSkins[skinID]
	return value, ok
}

func (m *GameMaster) DamageSkill(skillID uint64) (MasterRecord, bool) {
	value, ok := m.Skills[skillID]
	return value, ok
}

func (m *GameMaster) commonSkill(skillID uint64) (MasterRecord, bool) {
	value, ok := m.CommonSkills[skillID]
	return value, ok
}
