package capture

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

type ResolutionStatus string

// EntityTypeMonster follows the EntityPb.EntityType enum of the client protocol.
const EntityTypeMonster uint64 = 2

// EntityTypePlayerEntity is EntityPb.EntityType Proto_PlayerEntity: the one entity per player that the client registers
// with FormationDataController and whose cues the HUD shows on the current role (PlayerGameplayCueComponent).
const EntityTypePlayerEntity uint64 = 11

const (
	ResolutionVerified   ResolutionStatus = "VERIFIED"
	ResolutionCorrelated ResolutionStatus = "CORRELATED"
	resolutionHypothesis ResolutionStatus = "HYPOTHESIS"
	ResolutionUnknown    ResolutionStatus = "UNKNOWN"
	// ResolutionNotApplicable marks a role that the event structurally does not have, as
	// opposed to a role that exists but could not be resolved.
	ResolutionNotApplicable ResolutionStatus = "NOT_APPLICABLE"
)

// RuntimeEntity keeps the network entity identity separate from its static
// character/monster identity. Zero-valued optional IDs are never treated as
// resolved values; their presence flags carry that distinction.
type RuntimeEntity struct {
	EntityID                  uint64
	SceneInstanceID           uint64
	HasSceneInstanceID        bool
	EntityTypeCode            uint64
	HasEntityType             bool
	ConfigID                  uint64
	HasConfigID               bool
	ConfigType                uint64
	HasConfigType             bool
	PrefabID                  uint64 // EntityPb.PrefabId: with ConfigType Prefab (5), the PrefabConfig that holds ConfigId
	HasPrefabID               bool
	PlayerID                  uint64
	HasPlayerID               bool
	OwnerID                   uint64
	HasOwnerID                bool
	OwnerIncID                uint64
	HasOwnerIncID             bool
	SummonerID                uint64
	HasSummonerID             bool
	SummonerPlayerID          uint64
	HasSummonerPlayerID       bool
	SummonConfigID            uint64
	HasSummonConfigID         bool
	SummonSkillID             uint64
	HasSummonSkillID          bool
	SummonType                uint64
	HasSummonType             bool
	SceneSummonerID           uint64
	HasSceneSummonerID        bool
	SceneSummonConfigID       uint64
	HasSceneSummonConfigID    bool
	SceneSummonSkillID        uint64
	HasSceneSummonSkillID     bool
	PlayerEntityID            uint64
	HasPlayerEntityID         bool
	FollowerSummonConfigID    uint64
	HasFollowerSummonConfigID bool
	// FSMHashCode is EntityFsmComponentPb.HashCode, the version of the state-machine config the entity runs.
	FSMHashCode    int64
	HasFsmHashCode bool
	// FSMCommonHashCode is EntityFsmComponentPb.CommonHashCode, the version of the shared common state machine.
	FSMCommonHashCode         int64
	HasFsmCommonHashCode      bool
	PhantomRoleEntityID       uint64
	HasPhantomRoleEntityID    bool
	ConcomitantVisionIDs      []uint64
	ConcomitantCustomIDs      []uint64
	WeakVisionEntityID        uint64
	HasWeakVisionEntityID     bool
	FollowEntityID            uint64
	HasFollowEntityID         bool
	PlayerFollowers           []runtimePlayerFollower
	HasPartComponent          bool
	PartInformation           map[int32]RuntimePartInformation
	PartComponentSource       string
	MonsterID                 uint64
	HasMonsterID              bool
	MonsterCaptureEntityID    uint64
	HasMonsterCaptureEntityID bool
	TemplateID                uint64
	HasTemplateID             bool
	RoleSkinID                uint64
	HasRoleSkinID             bool
	RoleID                    uint64
	HasRoleID                 bool
	RoleConflict              bool
	RoleSourceFieldPath       string
	SourceMessageID           uint16
	SourceFieldPath           string
	ComponentSourceMessageID  uint16
	ComponentSourceFieldPath  string
	lastSeenSequence          uint64
}

type runtimePlayerFollower struct {
	TypeID    uint64
	HasType   bool
	EntityID  uint64
	HasEntity bool
}

type RuntimePartInformation struct {
	PartID       int32
	hasPartID    bool
	PartTag      int32
	HasPartTag   bool
	LifeMax      float32
	HasLifeMax   bool
	Activated    bool
	HasActivated bool
	Conflicted   bool
}

const maxRuntimeComponentEntityRefs = 256
const maxRuntimePartInformationRows = 256

type EntityResolver struct {
	Entities           map[uint64]RuntimeEntity
	sequence           uint64
	limit              int
	sceneInstanceID    uint64
	hasSceneInstanceID bool
	// SceneCombatEntityID is SceneInformation.SceneEntityId of the current scene: the entity the server names as attacker
	// of the negative-status damage it executes (damage_notify.go).
	SceneCombatEntityID    uint64
	HasSceneCombatEntityID bool
	// lifecycles outlives runtime entries: a removed entity keeps its record so
	// that REMOVED is distinguishable from never observed. See lifecycle.go.
	lifecycles        map[uint64]entityLifecycle
	lifecycleSequence uint64
}

type EntityIdentity struct {
	Runtime            RuntimeEntity
	RuntimeStatus      ResolutionStatus
	RuntimeProof       string
	Lifecycle          EntityLifecycleView
	Character          MasterRecord
	HasCharacter       bool
	CharacterStatus    ResolutionStatus
	CharacterProof     string
	RoleSkin           *roleSkinResolution
	SummonSkill        *summonSkillResolution
	Monster            MasterRecord
	HasMonster         bool
	MonsterStatus      ResolutionStatus
	MonsterProof       string
	TemplateCandidates []SceneTemplateCandidate
	TemplateStatus     ResolutionStatus
	TemplateProof      string
}

// roleSkinResolution records how EntityPb.RoleSkinId translates through the
// normalized RoleSkin table and whether it agrees with the entity's own RoleId.
type roleSkinResolution struct {
	SkinID       uint64
	MasterRoleID uint64
	Status       ResolutionStatus
	Proof        string
}

// resolveRoleSkin translates a RoleSkinId into the RoleSkin row's RoleId. The
// result is CORRELATED when the entity also carries a RoleId or Proto_Character
// ConfigId that equals the row's RoleId, and UNKNOWN (with the reason) when the
// row is absent or names a different role.
func resolveRoleSkin(masters *GameMaster, entity RuntimeEntity, characterID uint64) *roleSkinResolution {
	if masters == nil || !entity.HasRoleSkinID {
		return nil
	}
	result := &roleSkinResolution{SkinID: entity.RoleSkinID, Status: ResolutionUnknown}
	roleID, ok := masters.roleSkinRole(entity.RoleSkinID)
	if !ok {
		result.Proof = "RoleSkinId has no RoleSkin row in the loaded master"
		return result
	}
	result.MasterRoleID = roleID
	switch {
	case characterID == 0:
		result.Status = ResolutionCorrelated
		result.Proof = fmt.Sprintf("EntityPb.RoleSkinId(%d) -> RoleSkin.Id -> RoleSkin.RoleId; the entity carries no RoleId to cross-check; client build observed, master compatibility unverified", fieldEntityPbRoleSkinID)
	case characterID == roleID:
		result.Status = ResolutionCorrelated
		result.Proof = fmt.Sprintf("EntityPb.RoleSkinId(%d) -> RoleSkin.Id -> RoleSkin.RoleId=%d equals the same entity's own RoleId; client build observed, master compatibility unverified", fieldEntityPbRoleSkinID, roleID)
	default:
		result.Proof = fmt.Sprintf("CONFLICT: RoleSkin.RoleId=%d differs from the same entity's own RoleId=%d", roleID, characterID)
	}
	return result
}

// summonSkillResolution records how SummonerComponentPb.SummonSkillId translates.
// ConcomitantVision (Type 1) uses the normalized PhantomSkill-to-Vision crosswalk.
// ConcomitantMotorcycle (Type 5) remains a correlated Vision candidate.
type summonSkillResolution struct {
	SummonSkillID uint64
	SummonType    uint64
	HasSummonType bool
	Namespace     string
	Status        ResolutionStatus
	Proof         string
	// DevLabel is the developer note of the Vision row the key resolves to; never an official name.
	DevLabel *CombatEventLabelTextJSONL
}

const (
	SummonTypeConcomitantVision     = 1
	SummonTypeConcomitantMotorcycle = 5
)

func ResolveSummonSkill(masters *GameMaster, entity RuntimeEntity) *summonSkillResolution {
	if masters == nil || !entity.HasSummonSkillID {
		return nil
	}
	result := &summonSkillResolution{SummonSkillID: entity.SummonSkillID, SummonType: entity.SummonType, HasSummonType: entity.HasSummonType, Status: ResolutionUnknown}
	switch {
	case !entity.HasSummonType:
		result.Proof = "SummonSkillId present without SummonerComponent.Type; namespace not selected"
	case entity.SummonType != SummonTypeConcomitantVision && entity.SummonType != SummonTypeConcomitantMotorcycle:
		result.Proof = fmt.Sprintf("SummonerComponent.Type=%d is not mapped; no static SummonSkillId namespace is established for it", entity.SummonType)
	case entity.SummonType == SummonTypeConcomitantVision && !masters.hasConcomitantVisionSummonSkill(entity.SummonSkillID):
		result.Proof = "ConcomitantVision SummonSkillId has no unique row in the normalized PhantomSkill-to-Vision crosswalk"
	case entity.SummonType == SummonTypeConcomitantMotorcycle && !masters.hasVision(entity.SummonSkillID):
		result.Proof = "ConcomitantMotorcycle SummonSkillId has no DT_Vision row in the loaded master"
	default:
		result.Namespace = "Vision.Id"
		result.DevLabel = masters.VisionDevLabels[entity.SummonSkillID]
		if entity.SummonType == SummonTypeConcomitantVision {
			result.Status = ResolutionVerified
			result.Proof = "SummonerComponent.Type=ConcomitantVision selects the normalized summon_skill_vision_ref crosswalk from the unique PhantomSkill.PhantomSkillId key to Vision.Id; saved same-entity SkillId references and verified damage requests validate this Type 1 mapping for multiple distinct keys"
		} else {
			result.Status = ResolutionCorrelated
			result.Proof = "SummonerComponent.Type=ConcomitantMotorcycle and a matching DT_Vision.Id identify a candidate; the Type 5 field consumer is not established"
		}
	}
	return result
}

// skillLabel is the developer label (Simplified Chinese, not an official localized name) of a SkillInfo row.
type skillLabel struct {
	Label    string
	Status   ResolutionStatus
	Evidence string
}

// ResolveSkillLabel returns the SkillInfo developer label for a skill id. A key whose rows carry
// different labels in different SkillInfo tables is ambiguous and stays unknown.
func ResolveSkillLabel(masters *GameMaster, skillID uint64) *skillLabel {
	if masters == nil || skillID == 0 || skillID == ^uint64(0) {
		return nil
	}
	labels := masters.skillLabels[skillID]
	switch len(labels) {
	case 0:
		return nil
	case 1:
		for label := range labels {
			return &skillLabel{Label: label, Status: ResolutionCorrelated,
				Evidence: "DamageContext.SkillId is a SkillInfo row key (DT_SkillInfo family) whose developer label is identical in every table that holds it; the label is a zh-CN developer note, not an official localized name; client build observed, master compatibility unverified"}
		}
	}
	return &skillLabel{Status: ResolutionUnknown, Evidence: fmt.Sprintf("SkillId has %d different developer labels across SkillInfo tables; the owning table is not selected", len(labels))}
}

func (m *GameMaster) ActorOfModel(model uint64) (string, bool) {
	actor, ok := m.ModelActor[model]
	return actor, ok
}

func (m *GameMaster) ActorOfBlueprint(blueprint string) (string, bool) {
	model, ok := m.BlueprintModel[blueprint]
	if !ok {
		return "", false
	}
	return m.ActorOfModel(model)
}

type SummonerCharacterIdentity struct {
	EntityIDs      []uint64
	CharacterID    uint64
	HasCharacterID bool
	Character      MasterRecord
	HasCharacter   bool
	Status         ResolutionStatus
	NameStatus     ResolutionStatus
	Proof          string
}

type sourceRoleEvidence struct {
	roleID   uint64
	skillIDs map[uint64]struct{}
	conflict bool
	lastSeen uint64
}

type sourceRoleResolver struct {
	bySource map[uint64]sourceRoleEvidence
	sequence uint64
	limit    int
}

func newSourceRoleResolver() *sourceRoleResolver {
	return &sourceRoleResolver{bySource: make(map[uint64]sourceRoleEvidence), limit: 8192}
}

func (r *sourceRoleResolver) reset() {
	r.bySource = make(map[uint64]sourceRoleEvidence)
	r.sequence = 0
}

func (r *sourceRoleResolver) Observe(sourceID uint64, skill MasterRecord) {
	if sourceID == 0 || skill.ID == 0 || skill.RoleID == 0 || skill.Status != string(ResolutionVerified) || skill.NameKind != "SkillName" {
		return
	}
	r.sequence++
	evidence, ok := r.bySource[sourceID]
	if !ok {
		evidence = sourceRoleEvidence{roleID: skill.RoleID, skillIDs: make(map[uint64]struct{})}
	} else if evidence.roleID != skill.RoleID {
		evidence.conflict = true
	}
	evidence.skillIDs[skill.ID] = struct{}{}
	evidence.lastSeen = r.sequence
	r.bySource[sourceID] = evidence
	r.evictIfNeeded()
}

func (r *sourceRoleResolver) CorrelatedRole(sourceID uint64) (uint64, bool) {
	evidence, ok := r.bySource[sourceID]
	if !ok || evidence.conflict || len(evidence.skillIDs) < 2 {
		return 0, false
	}
	return evidence.roleID, true
}

func (r *sourceRoleResolver) remove(sourceID uint64) {
	delete(r.bySource, sourceID)
}

func (r *sourceRoleResolver) evictIfNeeded() {
	if r.limit <= 0 || len(r.bySource) <= r.limit {
		return
	}
	var oldestID, oldestSequence uint64
	first := true
	for id, evidence := range r.bySource {
		if first || evidence.lastSeen < oldestSequence {
			oldestID, oldestSequence, first = id, evidence.lastSeen, false
		}
	}
	if !first {
		delete(r.bySource, oldestID)
	}
}

// VisionSummonConfigs returns the sorted SummonCfg ids of ConcomitantVision entities whose
// SummonerComponent names summonerEntityID as the summoner.
func (r *EntityResolver) VisionSummonConfigs(summonerEntityID uint64) []uint64 {
	if r == nil || summonerEntityID == 0 {
		return nil
	}
	seen := make(map[uint64]struct{})
	for _, entity := range r.Entities {
		if entity.HasSummonerID && entity.SummonerID == summonerEntityID && entity.HasSummonType && entity.SummonType == SummonTypeConcomitantVision && entity.HasSummonConfigID {
			seen[entity.SummonConfigID] = struct{}{}
		}
	}
	configs := make([]uint64, 0, len(seen))
	for cfg := range seen {
		configs = append(configs, cfg)
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i] < configs[j] })
	return configs
}

func ResolveEntityIdentity(resolver *EntityResolver, masters *GameMaster, entityID uint64) EntityIdentity {
	entity, runtimeStatus := resolver.Resolve(entityID)
	result := EntityIdentity{Runtime: entity, RuntimeStatus: runtimeStatus, RuntimeProof: entityProof(entity), CharacterStatus: ResolutionUnknown, MonsterStatus: ResolutionUnknown, Lifecycle: resolver.lifecycleView(entityID)}
	characterID, characterProof, characterFromConfig, conflict := runtimeCharacterCandidate(entity)
	if conflict {
		result.CharacterProof = fmt.Sprintf("conflicting character IDs: scene RoleId=%d, EntityPb.ConfigId=%d with ConfigType=Proto_Character", entity.RoleID, entity.ConfigID)
		return result
	}
	if characterID != 0 {
		result.CharacterProof = characterProof
		if masters != nil {
			if character, ok := masters.Characters[characterID]; ok {
				result.Character = character
				result.HasCharacter = true
				result.CharacterStatus = ResolutionStatus(character.Status)
				result.CharacterProof = characterProof
				if characterFromConfig && entity.SourceMessageID != 0 && result.CharacterStatus == ResolutionVerified {
					// The client build is recorded, but its compatibility with this
					// versioned master has not been established.
					result.CharacterStatus = ResolutionCorrelated
				}
			}
		}
	}
	result.RoleSkin = resolveRoleSkin(masters, entity, characterID)
	result.SummonSkill = ResolveSummonSkill(masters, entity)
	if characterID == 0 && !result.HasCharacter && !entity.RoleConflict && masters != nil && entity.HasRoleSkinID {
		if character, ok := masters.characterBySkin(entity.RoleSkinID); ok {
			result.Character = character
			result.HasCharacter = true
			result.CharacterStatus = ResolutionStatus(character.Status)
			result.CharacterProof = fmt.Sprintf("EntityPb.RoleSkinId(%d) -> RoleInfo.SkinId -> RoleInfo.Id; client build observed, master compatibility unverified", fieldEntityPbRoleSkinID)
			if result.CharacterStatus == ResolutionVerified {
				// Keep the versioned skin join correlated until compatibility is checked.
				result.CharacterStatus = ResolutionCorrelated
			}
		}
	}
	if masters != nil && entity.HasMonsterID {
		if monster, ok := masters.Monsters[entity.MonsterID]; ok {
			result.Monster = monster
			result.HasMonster = true
			result.MonsterStatus = ResolutionStatus(monster.Status)
			// The field path is the current client protocol's, but the observed client
			// build has not been matched to this versioned master.
			if result.MonsterStatus == ResolutionVerified {
				result.MonsterStatus = ResolutionCorrelated
			}
			result.MonsterProof = "MonsterCaptureComponentPb.MonsterId (" + CurrentProtocol() + ") -> MonsterInfo.Id in the combat master; client build observed, master compatibility unverified"
		}
	}
	if masters != nil && !entity.HasMonsterID && entity.HasConfigID && entity.HasConfigType && entity.ConfigType == 1 &&
		entity.SourceMessageID == CmdJoinSceneNotify && entity.HasSceneInstanceID {
		if monsterID, ok := masters.sceneMonster(entity.SceneInstanceID, entity.ConfigID); ok {
			if monster, found := masters.Monsters[monsterID]; found {
				result.Monster = monster
				result.HasMonster = true
				// Keep this versioned static crosswalk correlated until client/master
				// compatibility is established.
				result.MonsterStatus = ResolutionCorrelated
				result.MonsterProof = fmt.Sprintf("JoinSceneNotify.SceneInfo.InstanceId=%d -> InstanceDungeon.MapConfigId; EntityPb.ConfigId=%d + ConfigType=Proto_Level(1) -> LevelEntityConfig(MapId, EntityId).BlueprintType -> MonsterInfo.MonsterEntityID -> MonsterInfo.Id=%d", entity.SceneInstanceID, entity.ConfigID, monsterID)
			}
		}
	}
	sceneInstanceID, hasSceneInstanceID := entity.SceneInstanceID, entity.HasSceneInstanceID
	sceneContextSource := "entity-scoped JoinSceneNotify context"
	if !hasSceneInstanceID && resolver != nil && resolver.hasSceneInstanceID &&
		(entity.SourceMessageID == CmdJoinSceneNotify || entity.SourceMessageID == CmdEntityAddNotify) {
		sceneInstanceID, hasSceneInstanceID = resolver.sceneInstanceID, true
		sceneContextSource = "current session JoinSceneNotify context"
	}
	if masters != nil && entity.HasConfigID && entity.HasConfigType && entity.ConfigType == 1 &&
		(entity.SourceMessageID == CmdJoinSceneNotify || entity.SourceMessageID == CmdEntityAddNotify) && hasSceneInstanceID {
		if candidates, ok := masters.sceneTemplates(sceneInstanceID, entity.ConfigID); ok {
			result.TemplateCandidates = candidates
			result.TemplateStatus = ResolutionUnknown
			result.TemplateProof = fmt.Sprintf(
				"%s (InstanceId=%d) -> InstanceDungeon.MapConfigId; runtime EntityPb.ConfigId=%d + ConfigType=Proto_Level(1) -> LevelEntityConfig(MapId, EntityId).BlueprintType -> TemplateConfig candidates; combat master locale %s, client/master compatibility unverified",
				sceneContextSource, sceneInstanceID, entity.ConfigID, masters.Locale,
			)
			if len(candidates) == 1 {
				result.TemplateStatus = ResolutionCorrelated
			}
		}
	}
	// EntityConfigType.Template (4): the entity was spawned from a template, so ConfigId is a TemplateConfig id and no
	// scene context is needed. This is how dynamically spawned bosses (for example Matrix challenge bosses) are named.
	if masters != nil && entity.HasConfigID && entity.HasConfigType && entity.ConfigType == 4 && len(result.TemplateCandidates) == 0 {
		if candidate, ok := masters.templateByID(entity.ConfigID); ok {
			result.TemplateCandidates = []SceneTemplateCandidate{candidate}
			result.TemplateStatus = ResolutionCorrelated
			result.TemplateProof = fmt.Sprintf("EntityPb.ConfigType=Template(4) in the protocol enum EntityConfigType -> EntityPb.ConfigId=%d is a TemplateConfig.Id; combat master locale %s, client/master compatibility unverified", entity.ConfigID, masters.Locale)
		}
	}
	// EntityConfigType.Prefab (5): the entity is one entity of a prefab. The client reads PrefabConfig by EntityPb.PrefabId,
	// takes the Entities entry whose EntityData.Id is EntityPb.ConfigId and finds its TemplateConfig by that entry's
	// BlueprintType (CreateEntityData.InitPbEntityData -> CreatureModel.GetEntityPrefab -> GetEntityTemplate). Challenge
	// stages such as the Tower of Adversity spawn their enemies this way; the entity carries the prefab itself, so no scene
	// context is needed. The relations are static and versioned, so the identity is at most CORRELATED, and a key that the
	// prefab relation answers with several rows stays unresolved.
	if masters != nil && entity.HasConfigID && entity.HasConfigType && entity.ConfigType == 5 && entity.HasPrefabID {
		if !result.HasMonster && !entity.HasMonsterID {
			if monsterID, ok := masters.prefabMonster(entity.PrefabID, entity.ConfigID); ok {
				if monster, found := masters.Monsters[monsterID]; found {
					result.Monster = monster
					result.HasMonster = true
					result.MonsterStatus = ResolutionCorrelated
					result.MonsterProof = fmt.Sprintf("EntityPb.ConfigType=Prefab(5) + EntityPb.PrefabId=%d + EntityPb.ConfigId=%d -> PrefabConfig(PrefabId).Entities[EntityData.Id=ConfigId].BlueprintType -> MonsterInfo.MonsterEntityID -> MonsterInfo.Id=%d", entity.PrefabID, entity.ConfigID, monsterID)
				}
			}
		}
		if len(result.TemplateCandidates) == 0 {
			if candidates, ok := masters.prefabTemplates(entity.PrefabID, entity.ConfigID); ok {
				result.TemplateCandidates = candidates
				result.TemplateStatus = ResolutionUnknown
				result.TemplateProof = fmt.Sprintf("EntityPb.ConfigType=Prefab(5) + EntityPb.PrefabId=%d + EntityPb.ConfigId=%d -> PrefabConfig(PrefabId).Entities[EntityData.Id=ConfigId].BlueprintType -> TemplateConfig candidates; combat master locale %s, client/master compatibility unverified", entity.PrefabID, entity.ConfigID, masters.Locale)
				if len(candidates) == 1 {
					result.TemplateStatus = ResolutionCorrelated
				}
			}
		}
	}
	return result
}

func runtimeCharacterCandidate(entity RuntimeEntity) (characterID uint64, proof string, fromConfig, conflict bool) {
	if entity.HasRoleID && !entity.RoleConflict {
		characterID = entity.RoleID
		proof = entity.RoleSourceFieldPath + " -> " + entity.SourceFieldPath
		if entity.RoleSourceFieldPath == "" || entity.SourceFieldPath == "" {
			proof = "scene role mapping -> runtime EntityId"
		}
	}
	// The client protocol's EntityConfigType defines Proto_Character as enum value 3.
	// SetPbDataByProtocol assigns EntityData.PbDataId to RoleId only in this branch.
	if entity.HasConfigType && entity.ConfigType == 3 && entity.HasConfigID {
		if characterID != 0 && characterID != entity.ConfigID {
			return 0, "", false, true
		}
		if characterID == 0 {
			characterID = entity.ConfigID
			proof = fmt.Sprintf("EntityPb.ConfigId(field %d) + ConfigType(field %d)=Proto_Character(3) -> CreateEntityData.Init.PbDataId -> CreatureDataComponent.SetPbDataByProtocol.SetRoleId", FieldEntityPbConfigID, FieldEntityPbConfigType)
			fromConfig = true
		}
	}
	return characterID, proof, fromConfig, false
}

func HasPlayerSceneRoleMapping(entity RuntimeEntity) bool {
	if !entity.HasPlayerID || entity.PlayerID == 0 || !entity.HasRoleID || entity.RoleID == 0 || entity.RoleConflict {
		return false
	}
	switch entity.RoleSourceFieldPath {
	case "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId",
		"UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].RoleId":
		return true
	default:
		return false
	}
}

const maxSummonerEntityDepth = 8

func ResolveSummonerCharacter(resolver *EntityResolver, masters *GameMaster, source RuntimeEntity) SummonerCharacterIdentity {
	result := SummonerCharacterIdentity{Status: ResolutionUnknown, NameStatus: ResolutionUnknown}
	if resolver == nil || !source.HasSummonerID || source.SummonerID == 0 {
		return result
	}
	seen := make(map[uint64]struct{}, maxSummonerEntityDepth+1)
	if source.EntityID != 0 {
		seen[source.EntityID] = struct{}{}
	}
	currentID := source.SummonerID
	for depth := 0; depth < maxSummonerEntityDepth; depth++ {
		if _, duplicate := seen[currentID]; duplicate {
			result.Proof = fmt.Sprintf("summoner entity cycle detected along %v -> %d", result.EntityIDs, currentID)
			return result
		}
		seen[currentID] = struct{}{}
		result.EntityIDs = append(result.EntityIDs, currentID)
		parent, status := resolver.Resolve(currentID)
		if status == ResolutionUnknown {
			result.Proof = fmt.Sprintf("SummonerComponent.SummonerId -> CreatureModel.GetEntity(CreatureDataId); unresolved entity chain %v", result.EntityIDs)
			return result
		}
		characterID, _, _, conflict := runtimeCharacterCandidate(parent)
		if conflict {
			result.Proof = fmt.Sprintf("conflicting character initialization on summoner entity chain %v", result.EntityIDs)
			return result
		}
		if characterID != 0 {
			result.CharacterID = characterID
			result.HasCharacterID = true
			result.Proof = fmt.Sprintf("SummonerComponent.SummonerId -> CreatureModel.GetEntity(CreatureDataId) -> Character RoleId; entity chain %v", result.EntityIDs)
			if masters == nil {
				return result
			}
			character, ok := masters.Characters[characterID]
			if !ok {
				result.Proof += "; Character master missing"
				return result
			}
			result.Character = character
			result.HasCharacter = true
			result.Status = ResolutionStatus(character.Status)
			if result.Status == "" {
				result.Status = ResolutionUnknown
			}
			if result.Status == ResolutionVerified {
				// Keep the derived relation correlated until client/master compatibility
				// is established.
				result.Status = ResolutionCorrelated
			}
			result.NameStatus = ResolutionStatus(character.NameStatus)
			if result.NameStatus == "" {
				result.NameStatus = ResolutionUnknown
			}
			return result
		}
		if !parent.HasSummonerID || parent.SummonerID == 0 {
			result.Proof = fmt.Sprintf("summoner entity chain %v has no direct Character RoleId", result.EntityIDs)
			return result
		}
		currentID = parent.SummonerID
	}
	result.Proof = fmt.Sprintf("summoner entity chain exceeded max depth %d: %v", maxSummonerEntityDepth, result.EntityIDs)
	return result
}

func entityProof(entity RuntimeEntity) string {
	if entity.SourceFieldPath != "" {
		return entity.SourceFieldPath
	}
	switch entity.SourceMessageID {
	case CmdEntityAddNotify:
		return "EntityAddNotify.EntityPb.Id"
	case CmdUpdateSceneGroupDataNotify:
		return "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].EntityId"
	case CmdEntityActiveRequest:
		return "EntityActiveRequest.EntityId; observed only"
	default:
		return ""
	}
}

func NewEntityResolver() *EntityResolver {
	return &EntityResolver{Entities: make(map[uint64]RuntimeEntity), limit: 8192, lifecycles: make(map[uint64]entityLifecycle)}
}

func (r *EntityResolver) reset() {
	r.Entities = make(map[uint64]RuntimeEntity)
	r.sequence = 0
	r.sceneInstanceID, r.hasSceneInstanceID = 0, false
	r.SceneCombatEntityID, r.HasSceneCombatEntityID = 0, false
	r.lifecycles = make(map[uint64]entityLifecycle)
	r.lifecycleSequence = 0
}

func (r *EntityResolver) observeActiveRequest(body []byte) error {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse EntityActiveRequest: %w", err)
	}
	for _, field := range fields {
		if field.Number != FieldEntityActiveRequestEntityID || field.Wire != 0 || field.Value == 0 {
			continue
		}
		r.sequence++
		entity := r.Entities[field.Value]
		entity.EntityID = field.Value
		if entity.SourceMessageID != CmdEntityAddNotify {
			entity.SourceMessageID = CmdEntityActiveRequest
		}
		entity.lastSeenSequence = r.sequence
		r.Entities[field.Value] = entity
		r.evictIfNeeded()
	}
	return nil
}

func (r *EntityResolver) ApplyEntityAddNotify(body []byte) (int, error) {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return 0, fmt.Errorf("parse EntityAddNotify: %w", err)
	}
	added := 0
	for _, field := range fields {
		if field.Number != FieldEntityAddNotifyEntityPbs || field.Wire != 2 {
			continue
		}
		entity, err := parseEntityPb(field.Data)
		if err != nil {
			return added, err
		}
		if entity.EntityID == 0 {
			continue
		}
		if entity.HasPartComponent {
			entity.PartComponentSource = "EntityAddNotify.EntityPbs[].ComponentPbs.PartComponent.PartLifeInfos[]"
		}
		r.sequence++
		if previous, ok := r.Entities[entity.EntityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
		}
		entity.SourceMessageID = CmdEntityAddNotify
		entity.SourceFieldPath = "EntityAddNotify.EntityPb.Id"
		entity.lastSeenSequence = r.sequence
		r.Entities[entity.EntityID] = entity
		r.ObserveLifecycleAdd(entity)
		r.evictIfNeeded()
		added++
	}
	return added, nil
}

// applyJoinSceneNotify replaces stale entity observations with the new scene
// snapshot. Owner, owner-incarnation, and summoner IDs remain distinct.
func (r *EntityResolver) applyJoinSceneNotify(body []byte) (int, error) {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return 0, fmt.Errorf("parse JoinSceneNotify: %w", err)
	}
	var sceneInfo []byte
	for _, field := range fields {
		if field.Number == FieldJoinSceneNotifySceneInfo && field.Wire == 2 {
			sceneInfo = field.Data
			break
		}
	}
	if sceneInfo == nil {
		return 0, nil
	}
	entities, err := parseSceneEntitySnapshot(sceneInfo)
	if err != nil {
		return 0, err
	}
	sceneFields, err := ParseProtoFields(sceneInfo)
	if err != nil {
		return 0, fmt.Errorf("parse SceneInformation: %w", err)
	}
	var instanceID, sceneEntityID uint64
	var hasInstanceID, hasSceneEntityID bool
	for _, field := range sceneFields {
		if field.Wire != 0 {
			continue
		}
		switch field.Number {
		case fieldSceneInformationInstanceID:
			instanceID, hasInstanceID = field.Value, field.Value != 0
		case fieldSceneInformationSceneEntityID:
			sceneEntityID, hasSceneEntityID = field.Value, field.Value != 0
		}
	}

	r.Entities = make(map[uint64]RuntimeEntity)
	r.sceneInstanceID, r.hasSceneInstanceID = instanceID, hasInstanceID
	r.SceneCombatEntityID, r.HasSceneCombatEntityID = sceneEntityID, hasSceneEntityID
	snapshotIDs := make([]uint64, 0, len(entities))
	for _, entity := range entities {
		if entity.EntityID == 0 {
			continue
		}
		if entity.HasPartComponent {
			entity.PartComponentSource = "JoinSceneNotify.SceneInfo entity snapshot.ComponentPbs.PartComponent.PartLifeInfos[]"
		}
		if previous, ok := r.Entities[entity.EntityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
		}
		entity.SceneInstanceID, entity.HasSceneInstanceID = instanceID, hasInstanceID
		r.sequence++
		entity.SourceMessageID = CmdJoinSceneNotify
		entity.lastSeenSequence = r.sequence
		r.Entities[entity.EntityID] = entity
		snapshotIDs = append(snapshotIDs, entity.EntityID)
		r.evictIfNeeded()
	}
	r.observeLifecycleSnapshot(snapshotIDs)
	return len(r.Entities), nil
}

// applyUpdateSceneGroupDataNotify adds the direct role-to-entity mapping carried
// by the client protocol's UpdateSceneGroupDataNotify. Other entity metadata
// already observed for the same ID is retained, and conflicting RoleIds remain
// unresolved through mergeRuntimeEntity.
func (r *EntityResolver) applyUpdateSceneGroupDataNotify(body []byte) (int, error) {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return 0, fmt.Errorf("parse UpdateSceneGroupDataNotify: %w", err)
	}
	updates := make([]RuntimeEntity, 0)
	for _, field := range fields {
		if field.Number != fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList || field.Wire != 2 { // UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[]
			continue
		}
		playerFields, err := ParseProtoFields(field.Data)
		if err != nil {
			return 0, fmt.Errorf("parse PlayerSceneGroupDataList: %w", err)
		}
		var playerID uint64
		for _, playerField := range playerFields {
			if playerField.Number == fieldPlayerSceneGroupDataListPlayerID && playerField.Wire == 0 {
				playerID = playerField.Value
				break
			}
		}
		for _, playerField := range playerFields {
			if playerField.Number != fieldPlayerSceneGroupDataListPlayerSceneGroupData || playerField.Wire != 2 { // PlayerSceneGroupDataList.PlayerSceneGroupData[]
				continue
			}
			groupFields, err := ParseProtoFields(playerField.Data)
			if err != nil {
				return 0, fmt.Errorf("parse PlayerSceneGroupData: %w", err)
			}
			for _, groupField := range groupFields {
				if groupField.Number != FieldPlayerSceneGroupDataFightRoleInfos || groupField.Wire != 2 { // PlayerSceneGroupData.FightRoleInfos[]
					continue
				}
				roleFields, err := ParseProtoFields(groupField.Data)
				if err != nil {
					return 0, fmt.Errorf("parse SceneRoleInformation: %w", err)
				}
				var entity RuntimeEntity
				entity.PlayerID, entity.HasPlayerID = playerID, playerID != 0
				entity.RoleSourceFieldPath = "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
				for _, roleField := range roleFields {
					if roleField.Wire != 0 {
						continue
					}
					switch roleField.Number {
					case FieldSceneRoleInformationRoleID: // SceneRoleInformation.RoleId
						entity.RoleID, entity.HasRoleID = roleField.Value, roleField.Value != 0
					case FieldSceneRoleInformationEntityID: // SceneRoleInformation.EntityId
						entity.EntityID = roleField.Value
					}
				}
				if entity.EntityID == 0 || !entity.HasRoleID {
					continue
				}
				entity.SourceMessageID = CmdUpdateSceneGroupDataNotify
				entity.SourceFieldPath = "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].EntityId"
				updates = append(updates, entity)
			}
		}
	}
	for _, entity := range updates {
		r.sequence++
		if previous, ok := r.Entities[entity.EntityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
			// Preserve the stronger entity-lifecycle source while retaining the
			// role mapping and its separate provenance.
			if previous.SourceMessageID == CmdEntityAddNotify || previous.SourceMessageID == CmdJoinSceneNotify {
				entity.SourceMessageID = previous.SourceMessageID
				entity.SourceFieldPath = previous.SourceFieldPath
			}
		}
		entity.lastSeenSequence = r.sequence
		r.Entities[entity.EntityID] = entity
		r.evictIfNeeded()
	}
	return len(updates), nil
}

func parseSceneEntitySnapshot(sceneInfo []byte) ([]RuntimeEntity, error) {
	fields, err := ParseProtoFields(sceneInfo)
	if err != nil {
		return nil, fmt.Errorf("parse SceneInformation: %w", err)
	}
	entities := make([]RuntimeEntity, 0)
	for _, field := range fields {
		if field.Wire != 2 {
			continue
		}
		switch field.Number {
		case FieldSceneInformationPlayerInfos: // SceneInformation.PlayerInfos[]
			roleEntities, err := parseScenePlayerRoleEntities(field.Data)
			if err != nil {
				return nil, err
			}
			entities = append(entities, roleEntities...)
		case fieldSceneInformationDynamicEntityList: // SceneInformation.DynamicEntityList[]
			entity, err := parseDynamicEntityInformation(field.Data, "JoinSceneNotify.SceneInfo.DynamicEntityList.Id")
			if err != nil {
				return nil, err
			}
			entities = append(entities, entity)
		case fieldSceneInformationAoiData: // SceneInformation.AoiData
			aoiFields, err := ParseProtoFields(field.Data)
			if err != nil {
				return nil, fmt.Errorf("parse PlayerSceneAoiData: %w", err)
			}
			for _, aoiField := range aoiFields {
				if aoiField.Wire != 2 {
					continue
				}
				switch aoiField.Number {
				case fieldPlayerSceneAoiDataDynamicEntityList: // PlayerSceneAoiData.DynamicEntityList[]
					entity, err := parseDynamicEntityInformation(aoiField.Data, "JoinSceneNotify.SceneInfo.AoiData.DynamicEntityList.Id")
					if err != nil {
						return nil, err
					}
					entities = append(entities, entity)
				case FieldPlayerSceneAoiDataEntities: // PlayerSceneAoiData.Entities[]: EntityPb
					entity, err := parseEntityPb(aoiField.Data)
					if err != nil {
						return nil, fmt.Errorf("parse JoinSceneNotify AoiData EntityPb: %w", err)
					}
					entity.SourceFieldPath = "JoinSceneNotify.SceneInfo.AoiData.Entities.EntityPb.Id"
					entities = append(entities, entity)
				}
			}
		}
	}
	return entities, nil
}

func parseScenePlayerRoleEntities(data []byte) ([]RuntimeEntity, error) {
	fields, err := ParseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse ScenePlayerInformation: %w", err)
	}
	var playerID uint64
	for _, field := range fields {
		if field.Number == FieldScenePlayerInformationPlayerID && field.Wire == 0 {
			playerID = field.Value
			break
		}
	}
	entities := make([]RuntimeEntity, 0)
	for _, field := range fields {
		if field.Number != FieldScenePlayerInformationPlayerSceneGroupData || field.Wire != 2 { // ScenePlayerInformation.PlayerSceneGroupData[]
			continue
		}
		groups, err := ParseProtoFields(field.Data)
		if err != nil {
			return nil, fmt.Errorf("parse PlayerSceneGroupData: %w", err)
		}
		for _, groupField := range groups {
			if groupField.Number != FieldPlayerSceneGroupDataFightRoleInfos || groupField.Wire != 2 { // PlayerSceneGroupData.FightRoleInfos[]
				continue
			}
			roleFields, err := ParseProtoFields(groupField.Data)
			if err != nil {
				return nil, fmt.Errorf("parse SceneRoleInformation: %w", err)
			}
			var entity RuntimeEntity
			entity.PlayerID, entity.HasPlayerID = playerID, playerID != 0
			entity.RoleSourceFieldPath = "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
			for _, roleField := range roleFields {
				if roleField.Wire != 0 {
					continue
				}
				switch roleField.Number {
				case FieldSceneRoleInformationRoleID: // SceneRoleInformation.RoleId
					entity.RoleID, entity.HasRoleID = roleField.Value, roleField.Value != 0
				case FieldSceneRoleInformationEntityID: // SceneRoleInformation.EntityId; same client CreatureDataId namespace
					entity.EntityID = roleField.Value
				}
			}
			if entity.EntityID != 0 && entity.HasRoleID {
				entity.SourceFieldPath = "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].EntityId"
				entities = append(entities, entity)
			}
		}
	}
	return entities, nil
}

func parseDynamicEntityInformation(data []byte, sourceFieldPath string) (RuntimeEntity, error) {
	fields, err := ParseProtoFields(data)
	if err != nil {
		return RuntimeEntity{}, fmt.Errorf("parse DynamicEntityInformation: %w", err)
	}
	var entity RuntimeEntity
	entity.SourceFieldPath = sourceFieldPath
	for _, field := range fields {
		if field.Wire == 0 {
			switch field.Number {
			case fieldDynamicEntityInformationID:
				entity.EntityID = field.Value
			case fieldDynamicEntityInformationEntityType:
				entity.EntityTypeCode, entity.HasEntityType = field.Value, true
			case fieldDynamicEntityInformationConfigID:
				entity.ConfigID, entity.HasConfigID = field.Value, true
			case fieldDynamicEntityInformationPlayerID:
				entity.PlayerID, entity.HasPlayerID = field.Value, true
			case fieldDynamicEntityInformationOwnerID:
				entity.OwnerID, entity.HasOwnerID = field.Value, true
			}
			continue
		}
		if field.Wire != 2 {
			continue
		}
		switch field.Number {
		case fieldDynamicEntityInformationSummonInfo: // DynamicEntityInformation.SummonInfo
			if err := applySummonInfo(&entity, field.Data); err != nil {
				return RuntimeEntity{}, err
			}
		case fieldDynamicEntityInformationComponentPbs: // DynamicEntityInformation.ComponentPbs[]
			if err := applyEntityComponent(&entity, field.Data); err != nil {
				return RuntimeEntity{}, err
			}
		}
	}
	return entity, nil
}

func applySummonInfo(entity *RuntimeEntity, data []byte) error {
	fields, err := ParseProtoFields(data)
	if err != nil {
		return fmt.Errorf("parse SummonInfo: %w", err)
	}
	for _, field := range fields {
		if field.Wire != 0 {
			continue
		}
		switch field.Number {
		case fieldSummonInfoSummonCfgID:
			entity.SceneSummonConfigID, entity.HasSceneSummonConfigID = field.Value, true
		case fieldSummonInfoSummonerID:
			entity.SceneSummonerID, entity.HasSceneSummonerID = field.Value, true
		case fieldSummonInfoSummonSkillID:
			entity.SceneSummonSkillID, entity.HasSceneSummonSkillID = field.Value, true
		}
	}
	return nil
}

// entityRemoveNotifyIDs returns the entities an EntityRemoveNotify removes. On a malformed EntityRemoveInfo it returns
// the IDs read before it together with the error.
func entityRemoveNotifyIDs(body []byte) ([]uint64, error) {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return nil, fmt.Errorf("parse EntityRemoveNotify: %w", err)
	}
	remove := false
	for _, field := range fields {
		if field.Number == FieldEntityRemoveNotifyIsRemove && field.Wire == 0 {
			remove = field.Value != 0
		}
	}
	if !remove {
		return nil, nil
	}
	ids := make([]uint64, 0)
	for _, field := range fields {
		if field.Number != FieldEntityRemoveNotifyRemoveInfos || field.Wire != 2 {
			continue
		}
		info, err := ParseProtoFields(field.Data)
		if err != nil {
			return ids, fmt.Errorf("parse EntityRemoveInfo: %w", err)
		}
		for _, value := range info {
			if value.Number == FieldEntityRemoveInfoEntityID && value.Wire == 0 && value.Value != 0 {
				ids = append(ids, value.Value)
			}
		}
	}
	return ids, nil
}

func (r *EntityResolver) ApplyEntityRemoveNotify(body []byte) (int, []uint64, error) {
	removedIDs, err := entityRemoveNotifyIDs(body)
	removed := 0
	for _, id := range removedIDs {
		r.observeLifecycleRemove(id)
		if _, ok := r.Entities[id]; ok {
			delete(r.Entities, id)
			removed++
		}
	}
	return removed, removedIDs, err
}

// activeResponseComponentPath is the provenance of components read from an EntityActiveResponse matched to its request.
var activeResponseComponentPath = fmt.Sprintf("EntityActiveRequest frame token match + field%d EntityId -> EntityActiveResponse.field%d ComponentPbs",
	FieldEntityActiveRequestEntityID, FieldEntityActiveResponseComponentPbs)

func (r *EntityResolver) applyActiveResponseIdentityComponents(entityID uint64, components []ProtoField) (int, error) {
	update := RuntimeEntity{EntityID: entityID}
	observed := 0
	for _, component := range components {
		if component.Wire != 2 || !isRuntimeIdentityComponent(component.Number) {
			continue
		}
		if err := applyEntityComponentPayload(&update, component.Number, component.Data); err != nil {
			return observed, err
		}
		observed++
	}
	if observed == 0 || entityID == 0 {
		return observed, nil
	}
	previous, exists := r.Entities[entityID]
	if !exists {
		return observed, nil
	}
	update.ComponentSourceMessageID = CmdEntityActiveResponse
	update.ComponentSourceFieldPath = activeResponseComponentPath
	update = mergeRuntimeEntity(previous, update)
	r.sequence++
	update.lastSeenSequence = r.sequence
	r.Entities[entityID] = update
	r.evictIfNeeded()
	return observed, nil
}

// applyActiveResponseFsmComponents records the state-machine hashes of an entity from an EntityActiveResponse. It changes
// only those two fields: the entity's identity and its component provenance stay as they were.
func (r *EntityResolver) applyActiveResponseFsmComponents(entityID uint64, components []ProtoField) error {
	previous, exists := r.Entities[entityID]
	if !exists || entityID == 0 {
		return nil
	}
	update := RuntimeEntity{EntityID: entityID}
	for _, component := range components {
		if err := applyEntityComponentPayload(&update, fieldEntityComponentPbEntityFsmComponentPb, component.Data); err != nil {
			return err
		}
	}
	if update.HasFsmHashCode {
		previous.FSMHashCode, previous.HasFsmHashCode = update.FSMHashCode, true
	}
	if update.HasFsmCommonHashCode {
		previous.FSMCommonHashCode, previous.HasFsmCommonHashCode = update.FSMCommonHashCode, true
	}
	r.Entities[entityID] = previous
	return nil
}

func (r *EntityResolver) applyActiveResponsePartComponents(entityID uint64, components []ProtoField) (int, error) {
	update := RuntimeEntity{EntityID: entityID}
	observed := 0
	for _, component := range components {
		if component.Number != FieldEntityComponentPbPartComponent || component.Wire != 2 {
			continue
		}
		if err := applyEntityComponentPayload(&update, component.Number, component.Data); err != nil {
			return observed, err
		}
		observed++
	}
	if observed == 0 || entityID == 0 {
		return observed, nil
	}
	previous, exists := r.Entities[entityID]
	if !exists {
		return observed, nil
	}
	update.PartComponentSource = activeResponseComponentPath + ".PartComponent.PartLifeInfos[]"
	update = mergeRuntimeEntity(previous, update)
	r.sequence++
	update.lastSeenSequence = r.sequence
	r.Entities[entityID] = update
	r.evictIfNeeded()
	return observed, nil
}

func (r *EntityResolver) invalidatePartComponents() {
	for entityID, entity := range r.Entities {
		entity.HasPartComponent = false
		entity.PartInformation = nil
		entity.PartComponentSource = ""
		r.Entities[entityID] = entity
	}
}

func isRuntimeIdentityComponent(componentNumber uint64) bool {
	switch componentNumber {
	case FieldEntityComponentPbSummonerComponent, FieldEntityComponentPbMonsterCaptureComponent, fieldEntityComponentPbConcomitantsComponentPb, fieldEntityComponentPbPlayerFollowerSelfComponentPb, fieldEntityComponentPbPlayerFollowersComponentPb, fieldEntityComponentPbFollowEntityComponentPb:
		return true
	default:
		return false
	}
}

func parseEntityPb(data []byte) (RuntimeEntity, error) {
	fields, err := ParseProtoFields(data)
	if err != nil {
		return RuntimeEntity{}, fmt.Errorf("parse EntityPb: %w", err)
	}
	var entity RuntimeEntity
	for _, field := range fields {
		if field.Wire != 0 {
			if field.Number == FieldEntityPbComponentPbs && field.Wire == 2 {
				if err := applyEntityComponent(&entity, field.Data); err != nil {
					return RuntimeEntity{}, err
				}
			}
			continue
		}
		switch field.Number {
		case FieldEntityPbID: // EntityPb.Id
			entity.EntityID = field.Value
		case FieldEntityPbConfigID: // EntityPb.ConfigId
			entity.ConfigID, entity.HasConfigID = field.Value, true
		case FieldEntityPbConfigType: // EntityPb.ConfigType
			entity.ConfigType, entity.HasConfigType = field.Value, true
		case fieldEntityPbPrefabID: // EntityPb.PrefabId (CreateEntityData.Init: PrefabId)
			entity.PrefabID, entity.HasPrefabID = field.Value, true
		case fieldEntityPbEntityType: // EntityPb.EntityType
			entity.EntityTypeCode, entity.HasEntityType = field.Value, true
		case fieldEntityPbPlayerID: // EntityPb.PlayerId
			entity.PlayerID, entity.HasPlayerID = field.Value, true
		case fieldEntityPbOwnerIncID: // EntityPb.OwnerIncId is not EntityPb.Id or DynamicEntityInformation.OwnerId.
			entity.OwnerIncID, entity.HasOwnerIncID = field.Value, true
		case fieldEntityPbRoleSkinID: // EntityPb.RoleSkinId is a skin reference, not CharacterId.
			entity.RoleSkinID, entity.HasRoleSkinID = field.Value, true
		}
	}
	return entity, nil
}

func applyEntityComponent(entity *RuntimeEntity, data []byte) error {
	components, err := ParseProtoFields(data)
	if err != nil {
		return fmt.Errorf("parse EntityComponentPb: %w", err)
	}
	for _, component := range components {
		if component.Wire != 2 {
			continue
		}
		if err := applyEntityComponentPayload(entity, component.Number, component.Data); err != nil {
			return err
		}
	}
	return nil
}

func applyEntityComponentPayload(entity *RuntimeEntity, componentNumber uint64, data []byte) error {
	switch componentNumber {
	case FieldEntityComponentPbSummonerComponent: // SummonerComponentPb
		fields, err := ParseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse SummonerComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.Wire != 0 {
				continue
			}
			switch field.Number {
			case FieldSummonerComponentPbSummonerID:
				entity.SummonerID, entity.HasSummonerID = field.Value, true
			case FieldSummonerComponentPbSummonCfgID:
				entity.SummonConfigID, entity.HasSummonConfigID = field.Value, true
			case fieldSummonerComponentPbSummonSkillID:
				entity.SummonSkillID, entity.HasSummonSkillID = field.Value, true
			case fieldSummonerComponentPbPlayerID:
				entity.SummonerPlayerID, entity.HasSummonerPlayerID = field.Value, true
			case FieldSummonerComponentPbType:
				entity.SummonType, entity.HasSummonType = field.Value, true
			}
		}
	case fieldEntityComponentPbEntityFsmComponentPb: // EntityFsmComponentPb
		fields, err := ParseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse EntityFsmComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.Number == fieldEntityFsmComponentPbCommonHashCode && field.Wire == 0 { // CommonHashCode (int32)
				entity.FSMCommonHashCode, entity.HasFsmCommonHashCode = int64(int32(uint32(field.Value))), true
			}
			if field.Number == fieldEntityFsmComponentPbHashCode && field.Wire == 0 { // HashCode (int32)
				entity.FSMHashCode, entity.HasFsmHashCode = int64(int32(uint32(field.Value))), true
			}
		}
	case FieldEntityComponentPbPartComponent: // PartComponentPb
		information, err := parseRuntimePartInformation(data)
		if err != nil {
			return err
		}
		entity.HasPartComponent = true
		entity.PartInformation = information
	case FieldEntityComponentPbMonsterCaptureComponent: // MonsterCaptureComponentPb
		fields, err := ParseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse MonsterCaptureComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.Wire != 0 {
				continue
			}
			switch field.Number {
			case FieldMonsterCaptureComponentPbTemplateID:
				entity.TemplateID, entity.HasTemplateID = field.Value, true
			case fieldMonsterCaptureComponentPbEntityID:
				entity.MonsterCaptureEntityID, entity.HasMonsterCaptureEntityID = field.Value, true
			case FieldMonsterCaptureComponentPbMonsterID:
				entity.MonsterID, entity.HasMonsterID = field.Value, true
			}
		}
	case fieldEntityComponentPbPlayerFollowerSelfComponentPb: // PlayerFollowerSelfComponentPb
		fields, err := ParseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse PlayerFollowerSelfComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.Wire != 0 {
				continue
			}
			switch field.Number {
			case fieldPlayerFollowerSelfComponentPbPlayerEntityID:
				entity.PlayerEntityID, entity.HasPlayerEntityID = field.Value, true
			case fieldPlayerFollowerSelfComponentPbSummonConfigID:
				entity.FollowerSummonConfigID, entity.HasFollowerSummonConfigID = field.Value, true
			}
		}
	case fieldEntityComponentPbConcomitantsComponentPb: // ConcomitantsComponentPb
		fields, err := ParseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse ConcomitantsComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.Wire != 0 {
				continue
			}
			switch field.Number {
			case fieldConcomitantsComponentPbVisionEntityIDs: // VisionEntityIds[]
				if len(entity.ConcomitantVisionIDs) >= maxRuntimeComponentEntityRefs {
					return fmt.Errorf("ConcomitantsComponentPb.VisionEntityIds exceeds limit %d", maxRuntimeComponentEntityRefs)
				}
				entity.ConcomitantVisionIDs = append(entity.ConcomitantVisionIDs, field.Value)
			case fieldConcomitantsComponentPbCustomEntityIDs: // CustomEntityIds[]
				if len(entity.ConcomitantCustomIDs) >= maxRuntimeComponentEntityRefs {
					return fmt.Errorf("ConcomitantsComponentPb.CustomEntityIds exceeds limit %d", maxRuntimeComponentEntityRefs)
				}
				entity.ConcomitantCustomIDs = append(entity.ConcomitantCustomIDs, field.Value)
			case fieldConcomitantsComponentPbPhantomRoleEid: // PhantomRoleEid
				entity.PhantomRoleEntityID, entity.HasPhantomRoleEntityID = field.Value, true
			case fieldConcomitantsComponentPbWeakVisionEid: // WeakVisionEid
				entity.WeakVisionEntityID, entity.HasWeakVisionEntityID = field.Value, true
			}
		}
	case fieldEntityComponentPbPlayerFollowersComponentPb: // PlayerFollowersComponentPb
		fields, err := ParseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse PlayerFollowersComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.Number != fieldPlayerFollowersComponentPbListPlayerFollowerPb || field.Wire != 2 {
				continue
			}
			if len(entity.PlayerFollowers) >= maxRuntimeComponentEntityRefs {
				return fmt.Errorf("PlayerFollowersComponentPb.ListPlayerFollowerPb exceeds limit %d", maxRuntimeComponentEntityRefs)
			}
			followerFields, err := ParseProtoFields(field.Data)
			if err != nil {
				return fmt.Errorf("parse PlayerFollowerPb: %w", err)
			}
			var follower runtimePlayerFollower
			for _, followerField := range followerFields {
				if followerField.Wire != 0 {
					continue
				}
				switch followerField.Number {
				case FieldPlayerFollowerPbType: // Type
					follower.TypeID, follower.HasType = followerField.Value, true
				case FieldPlayerFollowerPbEntityID: // EntityId
					follower.EntityID, follower.HasEntity = followerField.Value, true
				}
			}
			entity.PlayerFollowers = append(entity.PlayerFollowers, follower)
		}
	case fieldEntityComponentPbFollowEntityComponentPb: // FollowEntityComponentPb
		fields, err := ParseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse FollowEntityComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.Number == fieldFollowEntityComponentPbEntityID && field.Wire == 0 {
				entity.FollowEntityID, entity.HasFollowEntityID = field.Value, true
			}
		}
	}
	return nil
}

func parseRuntimePartInformation(data []byte) (map[int32]RuntimePartInformation, error) {
	fields, err := ParseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse PartComponentPb: %w", err)
	}
	parts := make(map[int32]RuntimePartInformation)
	rowsSeen := 0
	for _, field := range fields {
		if field.Number != FieldPartComponentPbPartLifeInfos || field.Wire != 2 { // PartComponentPb.PartLifeInfos[]
			continue
		}
		rowsSeen++
		if rowsSeen > maxRuntimePartInformationRows {
			return nil, fmt.Errorf("PartComponentPb.PartLifeInfos exceeds limit %d", maxRuntimePartInformationRows)
		}
		rowFields, err := ParseProtoFields(field.Data)
		if err != nil {
			return nil, fmt.Errorf("parse PartInformation: %w", err)
		}
		var information RuntimePartInformation
		for _, rowField := range rowFields {
			switch {
			case rowField.Number == FieldPartInformationPartID && rowField.Wire == 0: // PartInformation.PartId
				information.PartID = int32(uint32(rowField.Value))
				information.hasPartID = true
			case rowField.Number == FieldPartInformationLifeMax && rowField.Wire == 5: // PartInformation.LifeMax
				information.LifeMax = math.Float32frombits(binary.LittleEndian.Uint32(rowField.Data))
				if math.IsNaN(float64(information.LifeMax)) || math.IsInf(float64(information.LifeMax), 0) {
					return nil, fmt.Errorf("PartInformation.LifeMax is not finite")
				}
				information.HasLifeMax = true
			case rowField.Number == FieldPartInformationActivated && rowField.Wire == 0: // PartInformation.Activated
				information.Activated = rowField.Value != 0
				information.HasActivated = true
			case rowField.Number == FieldPartInformationPartTag && rowField.Wire == 0: // PartInformation.PartTag
				information.PartTag = int32(uint32(rowField.Value))
				information.HasPartTag = true
			}
		}
		if !information.hasPartID {
			continue
		}
		if previous, exists := parts[information.PartID]; exists {
			if previous != information {
				previous.Conflicted = true
				parts[information.PartID] = previous
			}
			continue
		}
		parts[information.PartID] = information
	}
	return parts, nil
}

func mergeRuntimeEntity(old, update RuntimeEntity) RuntimeEntity {
	if update.EntityID == 0 {
		update.EntityID = old.EntityID
	}
	if !update.HasSceneInstanceID && old.HasSceneInstanceID {
		update.SceneInstanceID, update.HasSceneInstanceID = old.SceneInstanceID, true
	}
	if update.SourceMessageID == 0 {
		update.SourceMessageID = old.SourceMessageID
	}
	if !update.HasEntityType && old.HasEntityType {
		update.EntityTypeCode, update.HasEntityType = old.EntityTypeCode, true
	}
	if !update.HasConfigID && old.HasConfigID {
		update.ConfigID, update.HasConfigID = old.ConfigID, true
	}
	if !update.HasConfigType && old.HasConfigType {
		update.ConfigType, update.HasConfigType = old.ConfigType, true
	}
	if !update.HasPrefabID && old.HasPrefabID {
		update.PrefabID, update.HasPrefabID = old.PrefabID, true
	}
	if !update.HasPlayerID && old.HasPlayerID {
		update.PlayerID, update.HasPlayerID = old.PlayerID, true
	}
	if !update.HasOwnerID && old.HasOwnerID {
		update.OwnerID, update.HasOwnerID = old.OwnerID, true
	}
	if !update.HasOwnerIncID && old.HasOwnerIncID {
		update.OwnerIncID, update.HasOwnerIncID = old.OwnerIncID, true
	}
	if !update.HasSummonerID && old.HasSummonerID {
		update.SummonerID, update.HasSummonerID = old.SummonerID, true
	}
	if !update.HasSummonerPlayerID && old.HasSummonerPlayerID {
		update.SummonerPlayerID, update.HasSummonerPlayerID = old.SummonerPlayerID, true
	}
	if !update.HasSummonConfigID && old.HasSummonConfigID {
		update.SummonConfigID, update.HasSummonConfigID = old.SummonConfigID, true
	}
	if !update.HasSummonSkillID && old.HasSummonSkillID {
		update.SummonSkillID, update.HasSummonSkillID = old.SummonSkillID, true
	}
	if !update.HasSummonType && old.HasSummonType {
		update.SummonType, update.HasSummonType = old.SummonType, true
	}
	if !update.HasSceneSummonerID && old.HasSceneSummonerID {
		update.SceneSummonerID, update.HasSceneSummonerID = old.SceneSummonerID, true
	}
	if !update.HasSceneSummonConfigID && old.HasSceneSummonConfigID {
		update.SceneSummonConfigID, update.HasSceneSummonConfigID = old.SceneSummonConfigID, true
	}
	if !update.HasSceneSummonSkillID && old.HasSceneSummonSkillID {
		update.SceneSummonSkillID, update.HasSceneSummonSkillID = old.SceneSummonSkillID, true
	}
	if !update.HasPlayerEntityID && old.HasPlayerEntityID {
		update.PlayerEntityID, update.HasPlayerEntityID = old.PlayerEntityID, true
	}
	if !update.HasFollowerSummonConfigID && old.HasFollowerSummonConfigID {
		update.FollowerSummonConfigID, update.HasFollowerSummonConfigID = old.FollowerSummonConfigID, true
	}
	if !update.HasFsmCommonHashCode && old.HasFsmCommonHashCode {
		update.FSMCommonHashCode, update.HasFsmCommonHashCode = old.FSMCommonHashCode, true
	}
	if !update.HasFsmHashCode && old.HasFsmHashCode {
		update.FSMHashCode, update.HasFsmHashCode = old.FSMHashCode, true
	}
	if !update.HasPhantomRoleEntityID && old.HasPhantomRoleEntityID {
		update.PhantomRoleEntityID, update.HasPhantomRoleEntityID = old.PhantomRoleEntityID, true
	}
	if len(update.ConcomitantVisionIDs) == 0 && len(old.ConcomitantVisionIDs) > 0 {
		update.ConcomitantVisionIDs = append([]uint64(nil), old.ConcomitantVisionIDs...)
	}
	if len(update.ConcomitantCustomIDs) == 0 && len(old.ConcomitantCustomIDs) > 0 {
		update.ConcomitantCustomIDs = append([]uint64(nil), old.ConcomitantCustomIDs...)
	}
	if !update.HasWeakVisionEntityID && old.HasWeakVisionEntityID {
		update.WeakVisionEntityID, update.HasWeakVisionEntityID = old.WeakVisionEntityID, true
	}
	if !update.HasFollowEntityID && old.HasFollowEntityID {
		update.FollowEntityID, update.HasFollowEntityID = old.FollowEntityID, true
	}
	if len(update.PlayerFollowers) == 0 && len(old.PlayerFollowers) > 0 {
		update.PlayerFollowers = append([]runtimePlayerFollower(nil), old.PlayerFollowers...)
	}
	if !update.HasPartComponent && old.HasPartComponent {
		update.HasPartComponent = true
		update.PartInformation = old.PartInformation
		update.PartComponentSource = old.PartComponentSource
	}
	if !update.HasMonsterID && old.HasMonsterID {
		update.MonsterID, update.HasMonsterID = old.MonsterID, true
	}
	if !update.HasMonsterCaptureEntityID && old.HasMonsterCaptureEntityID {
		update.MonsterCaptureEntityID, update.HasMonsterCaptureEntityID = old.MonsterCaptureEntityID, true
	}
	if !update.HasTemplateID && old.HasTemplateID {
		update.TemplateID, update.HasTemplateID = old.TemplateID, true
	}
	if !update.HasRoleSkinID && old.HasRoleSkinID {
		update.RoleSkinID, update.HasRoleSkinID = old.RoleSkinID, true
	}
	if old.RoleConflict {
		update.RoleConflict = true
		update.RoleID, update.HasRoleID = 0, false
	} else if update.HasRoleID && old.HasRoleID && update.RoleID != old.RoleID {
		update.RoleConflict = true
		update.RoleID, update.HasRoleID = 0, false
	} else if !update.HasRoleID && old.HasRoleID {
		update.RoleID, update.HasRoleID = old.RoleID, true
	}
	if update.RoleSourceFieldPath == "" {
		update.RoleSourceFieldPath = old.RoleSourceFieldPath
	}
	if update.ComponentSourceMessageID == 0 {
		update.ComponentSourceMessageID = old.ComponentSourceMessageID
	}
	if update.ComponentSourceFieldPath == "" {
		update.ComponentSourceFieldPath = old.ComponentSourceFieldPath
	}
	if update.SourceFieldPath == "" {
		update.SourceFieldPath = old.SourceFieldPath
	}
	return update
}

func (r *EntityResolver) Resolve(entityID uint64) (RuntimeEntity, ResolutionStatus) {
	entity, ok := r.Entities[entityID]
	if !ok {
		return RuntimeEntity{EntityID: entityID}, ResolutionUnknown
	}
	if entity.SourceMessageID == CmdEntityAddNotify {
		return entity, ResolutionVerified
	}
	if entity.SourceMessageID == CmdJoinSceneNotify {
		return entity, ResolutionVerified
	}
	if entity.SourceMessageID == CmdUpdateSceneGroupDataNotify {
		return entity, ResolutionVerified
	}
	if entity.SourceMessageID == CmdEntityActiveRequest {
		return entity, ResolutionCorrelated
	}
	if entity.SourceMessageID == CmdEntityActiveResponse {
		return entity, ResolutionCorrelated
	}
	return entity, ResolutionUnknown
}

func (r *EntityResolver) evictIfNeeded() {
	if r.limit <= 0 || len(r.Entities) <= r.limit {
		return
	}
	var oldestID, oldestSequence uint64
	first := true
	for id, entity := range r.Entities {
		if first || entity.lastSeenSequence < oldestSequence {
			oldestID, oldestSequence, first = id, entity.lastSeenSequence, false
		}
	}
	if !first {
		delete(r.Entities, oldestID)
	}
}
