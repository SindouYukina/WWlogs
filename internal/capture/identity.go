package capture

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

type resolutionStatus string

// entityTypeMonster follows the EntityPb.EntityType enum of the client protocol.
const entityTypeMonster uint64 = 2

// entityTypePlayerEntity is EntityPb.EntityType Proto_PlayerEntity: the one entity per player that the client registers
// with FormationDataController and whose cues the HUD shows on the current role (PlayerGameplayCueComponent).
const entityTypePlayerEntity uint64 = 11

const (
	resolutionVerified   resolutionStatus = "VERIFIED"
	resolutionCorrelated resolutionStatus = "CORRELATED"
	resolutionHypothesis resolutionStatus = "HYPOTHESIS"
	resolutionUnknown    resolutionStatus = "UNKNOWN"
	// resolutionNotApplicable marks a role that the event structurally does not have, as
	// opposed to a role that exists but could not be resolved.
	resolutionNotApplicable resolutionStatus = "NOT_APPLICABLE"
)

// runtimeEntity keeps the network entity identity separate from its static
// character/monster identity. Zero-valued optional IDs are never treated as
// resolved values; their presence flags carry that distinction.
type runtimeEntity struct {
	entityID                  uint64
	sceneInstanceID           uint64
	hasSceneInstanceID        bool
	entityTypeCode            uint64
	hasEntityType             bool
	configID                  uint64
	hasConfigID               bool
	configType                uint64
	hasConfigType             bool
	playerID                  uint64
	hasPlayerID               bool
	ownerID                   uint64
	hasOwnerID                bool
	ownerIncID                uint64
	hasOwnerIncID             bool
	summonerID                uint64
	hasSummonerID             bool
	summonerPlayerID          uint64
	hasSummonerPlayerID       bool
	summonConfigID            uint64
	hasSummonConfigID         bool
	summonSkillID             uint64
	hasSummonSkillID          bool
	summonType                uint64
	hasSummonType             bool
	sceneSummonerID           uint64
	hasSceneSummonerID        bool
	sceneSummonConfigID       uint64
	hasSceneSummonConfigID    bool
	sceneSummonSkillID        uint64
	hasSceneSummonSkillID     bool
	playerEntityID            uint64
	hasPlayerEntityID         bool
	followerSummonConfigID    uint64
	hasFollowerSummonConfigID bool
	// fsmHashCode is EntityFsmComponentPb.HashCode, the version of the state-machine config the entity runs.
	fsmHashCode    int64
	hasFsmHashCode bool
	// fsmCommonHashCode is EntityFsmComponentPb.CommonHashCode, the version of the shared common state machine.
	fsmCommonHashCode         int64
	hasFsmCommonHashCode      bool
	phantomRoleEntityID       uint64
	hasPhantomRoleEntityID    bool
	concomitantVisionIDs      []uint64
	concomitantCustomIDs      []uint64
	weakVisionEntityID        uint64
	hasWeakVisionEntityID     bool
	followEntityID            uint64
	hasFollowEntityID         bool
	playerFollowers           []runtimePlayerFollower
	hasPartComponent          bool
	partInformation           map[int32]runtimePartInformation
	partComponentSource       string
	monsterID                 uint64
	hasMonsterID              bool
	monsterCaptureEntityID    uint64
	hasMonsterCaptureEntityID bool
	templateID                uint64
	hasTemplateID             bool
	roleSkinID                uint64
	hasRoleSkinID             bool
	roleID                    uint64
	hasRoleID                 bool
	roleConflict              bool
	roleSourceFieldPath       string
	sourceMessageID           uint16
	sourceFieldPath           string
	componentSourceMessageID  uint16
	componentSourceFieldPath  string
	lastSeenSequence          uint64
}

type runtimePlayerFollower struct {
	typeID    uint64
	hasType   bool
	entityID  uint64
	hasEntity bool
}

type runtimePartInformation struct {
	partID       int32
	hasPartID    bool
	partTag      int32
	hasPartTag   bool
	lifeMax      float32
	hasLifeMax   bool
	activated    bool
	hasActivated bool
	conflicted   bool
}

type runtimePartReference struct {
	partID          int32
	status          partReferenceStatus
	information     *runtimePartInformation
	informationFrom string
	evidence        string
}

type partReferenceStatus string

const (
	partReferenceCorrelated partReferenceStatus = "CORRELATED"
	partReferenceNoPart     partReferenceStatus = "NO_PART"
	partReferenceUnknown    partReferenceStatus = "UNKNOWN"
)

const maxRuntimeComponentEntityRefs = 256
const maxRuntimePartInformationRows = 256

type entityResolver struct {
	entities           map[uint64]runtimeEntity
	sequence           uint64
	limit              int
	sceneInstanceID    uint64
	hasSceneInstanceID bool
	// sceneCombatEntityID is SceneInformation.SceneEntityId of the current scene: the entity the server names as attacker
	// of the negative-status damage it executes (damage_notify.go).
	sceneCombatEntityID    uint64
	hasSceneCombatEntityID bool
	// lifecycles outlives runtime entries: a removed entity keeps its record so
	// that REMOVED is distinguishable from never observed. See lifecycle.go.
	lifecycles        map[uint64]entityLifecycle
	lifecycleSequence uint64
}

type entityIdentity struct {
	Runtime            runtimeEntity
	RuntimeStatus      resolutionStatus
	RuntimeProof       string
	Lifecycle          entityLifecycleView
	Character          masterRecord
	HasCharacter       bool
	CharacterStatus    resolutionStatus
	CharacterProof     string
	RoleSkin           *roleSkinResolution
	SummonSkill        *summonSkillResolution
	Monster            masterRecord
	HasMonster         bool
	MonsterStatus      resolutionStatus
	MonsterProof       string
	TemplateCandidates []sceneTemplateCandidate
	TemplateStatus     resolutionStatus
	TemplateProof      string
}

// roleSkinResolution records how EntityPb.RoleSkinId translates through the
// normalized RoleSkin table and whether it agrees with the entity's own RoleId.
type roleSkinResolution struct {
	SkinID       uint64
	MasterRoleID uint64
	Status       resolutionStatus
	Proof        string
}

// resolveRoleSkin translates a RoleSkinId into the RoleSkin row's RoleId. The
// result is CORRELATED when the entity also carries a RoleId or Proto_Character
// ConfigId that equals the row's RoleId, and UNKNOWN (with the reason) when the
// row is absent or names a different role.
func resolveRoleSkin(masters *gameMaster, entity runtimeEntity, characterID uint64) *roleSkinResolution {
	if masters == nil || !entity.hasRoleSkinID {
		return nil
	}
	result := &roleSkinResolution{SkinID: entity.roleSkinID, Status: resolutionUnknown}
	roleID, ok := masters.roleSkinRole(entity.roleSkinID)
	if !ok {
		result.Proof = "RoleSkinId has no RoleSkin row in the loaded master"
		return result
	}
	result.MasterRoleID = roleID
	switch {
	case characterID == 0:
		result.Status = resolutionCorrelated
		result.Proof = fmt.Sprintf("EntityPb.RoleSkinId(%d) -> RoleSkin.Id -> RoleSkin.RoleId; the entity carries no RoleId to cross-check; client build observed, master compatibility unverified", fieldEntityPbRoleSkinID)
	case characterID == roleID:
		result.Status = resolutionCorrelated
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
	Status        resolutionStatus
	Proof         string
	// DevLabel is the developer note of the Vision row the key resolves to; never an official name.
	DevLabel *combatEventLabelTextJSONL
}

const (
	summonTypeConcomitantVision     = 1
	summonTypeConcomitantMotorcycle = 5
)

func resolveSummonSkill(masters *gameMaster, entity runtimeEntity) *summonSkillResolution {
	if masters == nil || !entity.hasSummonSkillID {
		return nil
	}
	result := &summonSkillResolution{SummonSkillID: entity.summonSkillID, SummonType: entity.summonType, HasSummonType: entity.hasSummonType, Status: resolutionUnknown}
	switch {
	case !entity.hasSummonType:
		result.Proof = "SummonSkillId present without SummonerComponent.Type; namespace not selected"
	case entity.summonType != summonTypeConcomitantVision && entity.summonType != summonTypeConcomitantMotorcycle:
		result.Proof = fmt.Sprintf("SummonerComponent.Type=%d is not mapped; no static SummonSkillId namespace is established for it", entity.summonType)
	case entity.summonType == summonTypeConcomitantVision && !masters.hasConcomitantVisionSummonSkill(entity.summonSkillID):
		result.Proof = "ConcomitantVision SummonSkillId has no unique row in the normalized PhantomSkill-to-Vision crosswalk"
	case entity.summonType == summonTypeConcomitantMotorcycle && !masters.hasVision(entity.summonSkillID):
		result.Proof = "ConcomitantMotorcycle SummonSkillId has no DT_Vision row in the loaded master"
	default:
		result.Namespace = "Vision.Id"
		result.DevLabel = masters.visionDevLabels[entity.summonSkillID]
		if entity.summonType == summonTypeConcomitantVision {
			result.Status = resolutionVerified
			result.Proof = "SummonerComponent.Type=ConcomitantVision selects the normalized summon_skill_vision_ref crosswalk from the unique PhantomSkill.PhantomSkillId key to Vision.Id; saved same-entity SkillId references and verified damage requests validate this Type 1 mapping for multiple distinct keys"
		} else {
			result.Status = resolutionCorrelated
			result.Proof = "SummonerComponent.Type=ConcomitantMotorcycle and a matching DT_Vision.Id identify a candidate; the Type 5 field consumer is not established"
		}
	}
	return result
}

// skillLabel is the developer label (Simplified Chinese, not an official localized name) of a SkillInfo row.
type skillLabel struct {
	Label    string
	Status   resolutionStatus
	Evidence string
}

// resolveSkillLabel returns the SkillInfo developer label for a skill id. A key whose rows carry
// different labels in different SkillInfo tables is ambiguous and stays unknown.
func resolveSkillLabel(masters *gameMaster, skillID uint64) *skillLabel {
	if masters == nil || skillID == 0 || skillID == ^uint64(0) {
		return nil
	}
	labels := masters.skillLabels[skillID]
	switch len(labels) {
	case 0:
		return nil
	case 1:
		for label := range labels {
			return &skillLabel{Label: label, Status: resolutionCorrelated,
				Evidence: "DamageContext.SkillId is a SkillInfo row key (DT_SkillInfo family) whose developer label is identical in every table that holds it; the label is a zh-CN developer note, not an official localized name; client build observed, master compatibility unverified"}
		}
	}
	return &skillLabel{Status: resolutionUnknown, Evidence: fmt.Sprintf("SkillId has %d different developer labels across SkillInfo tables; the owning table is not selected", len(labels))}
}

// bulletResolution records which static bullet row-key namespace a BulletId belongs to.
// BulletId alone does not name a table: it is a row key in one of several DataTable
// families, and the same number can recur across actors. It is translated only when
// the source chain leads to exactly one actor (or the key belongs to exactly one family).
type bulletResolution struct {
	BulletID       uint64
	Namespace      string
	ActorBlueprint string
	Via            string
	DevLabelZh     string
	// DevLabel is the localized developer note of the CommonBulletData row; never an official name.
	DevLabel *combatEventLabelTextJSONL
	// RowDamageID is the DamageId written in the matched CDT_CommonBulletData row (0 when none).
	RowDamageID uint64
	Status      resolutionStatus
	Proof       string
}

// bulletSource is what an event knows about the entity that fired a bullet.
type bulletSource struct {
	RoleSkinID          uint64
	HasRoleSkin         bool
	BlueprintTypes      []string
	FollowerSummonCfgID uint64
	HasFollower         bool
	SkillID             uint64
	// VisionSummonCfgs are SummonCfg ids of live ConcomitantVision entities summoned by the source entity.
	VisionSummonCfgs []uint64
	// OwnSummonCfgID is the source entity's own SummonerComponent.SummonCfgId, set when the source is itself a summon.
	OwnSummonCfgID    uint64
	HasOwnSummonCfgID bool
}

type bulletChain struct {
	via   string
	actor string
}

// newBulletSource collects what an entity identity knows about a bullet's origin.
func newBulletSource(identity entityIdentity, skillID uint64, visionSummonCfgs []uint64) bulletSource {
	source := bulletSource{SkillID: skillID, VisionSummonCfgs: visionSummonCfgs}
	source.RoleSkinID, source.HasRoleSkin = identity.Runtime.roleSkinID, identity.Runtime.hasRoleSkinID
	source.OwnSummonCfgID, source.HasOwnSummonCfgID = identity.Runtime.summonConfigID, identity.Runtime.hasSummonConfigID
	source.FollowerSummonCfgID, source.HasFollower = identity.Runtime.followerSummonConfigID, identity.Runtime.hasFollowerSummonConfigID
	for _, candidate := range identity.TemplateCandidates {
		source.BlueprintTypes = append(source.BlueprintTypes, candidate.BlueprintType)
	}
	return source
}

func (m *gameMaster) actorOfModel(model uint64) (string, bool) {
	actor, ok := m.modelActor[model]
	return actor, ok
}

func (m *gameMaster) actorOfBlueprint(blueprint string) (string, bool) {
	model, ok := m.blueprintModel[blueprint]
	if !ok {
		return "", false
	}
	return m.actorOfModel(model)
}

func (m *gameMaster) bulletChains(source bulletSource) []bulletChain {
	var chains []bulletChain
	if source.HasRoleSkin {
		if mesh, ok := m.roleSkinMesh[source.RoleSkinID]; ok {
			if actor, ok := m.actorOfModel(mesh); ok {
				chains = append(chains, bulletChain{"EntityPb.RoleSkinId -> RoleSkin.MeshId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	for _, blueprint := range source.BlueprintTypes {
		if actor, ok := m.actorOfBlueprint(blueprint); ok {
			chains = append(chains, bulletChain{"scene TemplateConfig.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
		}
	}
	if source.HasOwnSummonCfgID {
		if blueprint, ok := m.summonBlueprint[source.OwnSummonCfgID]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"source entity SummonerComponent.SummonCfgId -> SummonCfg.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	if source.HasFollower {
		if blueprint, ok := m.summonBlueprint[source.FollowerSummonCfgID]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"FollowerSummonConfigId -> SummonCfg.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	for _, cfg := range source.VisionSummonCfgs {
		if blueprint, ok := m.summonBlueprint[cfg]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"ConcomitantVision SummonerComponent(SummonerId=source entity).SummonCfgId -> SummonCfg.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	for _, vision := range m.visionCast[source.SkillID] {
		if blueprint, ok := m.summonBlueprint[vision]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"DamageContext.SkillId -> Vision.BirthCastSkillId -> SummonCfg(Vision.Id).BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	return chains
}

func resolveBullet(masters *gameMaster, bulletID uint64, source bulletSource) *bulletResolution {
	if masters == nil {
		return nil
	}
	result := &bulletResolution{BulletID: bulletID, Status: resolutionUnknown}
	label, inCommon := masters.commonBullets[bulletID]
	_, inPreload := masters.preloadBullets[bulletID]
	chains := masters.bulletChains(source)
	if len(chains) == 0 && source.SkillID != 0 {
		if actor, ok := masters.uniqueActorForSkill(source.SkillID); ok {
			if _, listed := masters.actorBullets[actor][bulletID]; listed {
				chains = append(chains, bulletChain{
					via:   "same combat reference SkillId -> unique EntitySkillPreload actor asset; BulletId -> that actor's BulletPreload.BulletId",
					actor: actor,
				})
			}
		}
	}
	matched := make(map[string]string)
	for _, chain := range chains {
		if _, ok := masters.actorBullets[chain.actor][bulletID]; ok {
			if _, seen := matched[chain.actor]; !seen {
				matched[chain.actor] = chain.via
			}
		}
	}
	switch {
	case len(matched) > 1:
		result.Proof = fmt.Sprintf("BulletId is held by %d distinct source-chain actors; the serving actor is not determined", len(matched))
	case len(matched) == 1 && inCommon:
		result.Proof = "BulletId is held by the source actor and by CDT_CommonBulletData; the table that served this bullet is not determined"
	case len(matched) == 1:
		for actor, via := range matched {
			result.Namespace = "BulletPreload.ActorBlueprint"
			result.ActorBlueprint = masters.assetPath[actor]
			result.Via = via
			result.Status = resolutionCorrelated
			result.Proof = via + "; the actor's BulletPreload rows in the combat master list this BulletId; client build observed, master compatibility unverified"
		}
	case inCommon && inPreload:
		result.Proof = "BulletId is a row key in both CDT_CommonBulletData and BulletPreload and no source-chain actor holds it; the serving table is not determined"
	case inCommon:
		result.Namespace = "CDT_CommonBulletData.RowKey"
		result.DevLabelZh = label
		result.DevLabel = masters.commonBulletDevLabels[bulletID]
		result.RowDamageID = masters.commonBulletDamage[bulletID]
		result.Status = resolutionCorrelated
		result.Proof = "BulletId is a row key of CDT_CommonBulletData and of no actor-level BulletPreload family; the loader that serves this common table in normal combat is unproven, client build observed, master compatibility unverified"
	case inPreload:
		result.Proof = fmt.Sprintf("BulletId matches an actor-level BulletPreload row key but none of the %d source-chain actors holds it; only a numeric match is recorded", len(chains))
	default:
		result.Proof = "BulletId has no row in the loaded bullet definitions"
	}
	return result
}

type summonerCharacterIdentity struct {
	EntityIDs      []uint64
	CharacterID    uint64
	HasCharacterID bool
	Character      masterRecord
	HasCharacter   bool
	Status         resolutionStatus
	NameStatus     resolutionStatus
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

func (r *sourceRoleResolver) observe(sourceID uint64, skill masterRecord) {
	if sourceID == 0 || skill.ID == 0 || skill.RoleID == 0 || skill.Status != string(resolutionVerified) || skill.NameKind != "SkillName" {
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

func (r *sourceRoleResolver) correlatedRole(sourceID uint64) (uint64, bool) {
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

// visionSummonConfigs returns the sorted SummonCfg ids of ConcomitantVision entities whose
// SummonerComponent names summonerEntityID as the summoner.
func (r *entityResolver) visionSummonConfigs(summonerEntityID uint64) []uint64 {
	if r == nil || summonerEntityID == 0 {
		return nil
	}
	seen := make(map[uint64]struct{})
	for _, entity := range r.entities {
		if entity.hasSummonerID && entity.summonerID == summonerEntityID && entity.hasSummonType && entity.summonType == summonTypeConcomitantVision && entity.hasSummonConfigID {
			seen[entity.summonConfigID] = struct{}{}
		}
	}
	configs := make([]uint64, 0, len(seen))
	for cfg := range seen {
		configs = append(configs, cfg)
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i] < configs[j] })
	return configs
}

func resolveEntityIdentity(resolver *entityResolver, masters *gameMaster, entityID uint64) entityIdentity {
	entity, runtimeStatus := resolver.resolve(entityID)
	result := entityIdentity{Runtime: entity, RuntimeStatus: runtimeStatus, RuntimeProof: entityProof(entity), CharacterStatus: resolutionUnknown, MonsterStatus: resolutionUnknown, Lifecycle: resolver.lifecycleView(entityID)}
	characterID, characterProof, characterFromConfig, conflict := runtimeCharacterCandidate(entity)
	if conflict {
		result.CharacterProof = fmt.Sprintf("conflicting character IDs: scene RoleId=%d, EntityPb.ConfigId=%d with ConfigType=Proto_Character", entity.roleID, entity.configID)
		return result
	}
	if characterID != 0 {
		result.CharacterProof = characterProof
		if masters != nil {
			if character, ok := masters.characters[characterID]; ok {
				result.Character = character
				result.HasCharacter = true
				result.CharacterStatus = resolutionStatus(character.Status)
				result.CharacterProof = characterProof
				if characterFromConfig && entity.sourceMessageID != 0 && result.CharacterStatus == resolutionVerified {
					// The client build is recorded, but its compatibility with this
					// versioned master has not been established.
					result.CharacterStatus = resolutionCorrelated
				}
			}
		}
	}
	result.RoleSkin = resolveRoleSkin(masters, entity, characterID)
	result.SummonSkill = resolveSummonSkill(masters, entity)
	if characterID == 0 && !result.HasCharacter && !entity.roleConflict && masters != nil && entity.hasRoleSkinID {
		if character, ok := masters.characterBySkin(entity.roleSkinID); ok {
			result.Character = character
			result.HasCharacter = true
			result.CharacterStatus = resolutionStatus(character.Status)
			result.CharacterProof = fmt.Sprintf("EntityPb.RoleSkinId(%d) -> RoleInfo.SkinId -> RoleInfo.Id; client build observed, master compatibility unverified", fieldEntityPbRoleSkinID)
			if result.CharacterStatus == resolutionVerified {
				// Keep the versioned skin join correlated until compatibility is checked.
				result.CharacterStatus = resolutionCorrelated
			}
		}
	}
	if masters != nil && entity.hasMonsterID {
		if monster, ok := masters.monsters[entity.monsterID]; ok {
			result.Monster = monster
			result.HasMonster = true
			result.MonsterStatus = resolutionStatus(monster.Status)
			// The field path is the current client protocol's, but the observed client
			// build has not been matched to this versioned master.
			if result.MonsterStatus == resolutionVerified {
				result.MonsterStatus = resolutionCorrelated
			}
			result.MonsterProof = "MonsterCaptureComponentPb.MonsterId (" + CurrentProtocol() + ") -> MonsterInfo.Id in the combat master; client build observed, master compatibility unverified"
		}
	}
	if masters != nil && !entity.hasMonsterID && entity.hasConfigID && entity.hasConfigType && entity.configType == 1 &&
		entity.sourceMessageID == cmdJoinSceneNotify && entity.hasSceneInstanceID {
		if monsterID, ok := masters.sceneMonster(entity.sceneInstanceID, entity.configID); ok {
			if monster, found := masters.monsters[monsterID]; found {
				result.Monster = monster
				result.HasMonster = true
				// Keep this versioned static crosswalk correlated until client/master
				// compatibility is established.
				result.MonsterStatus = resolutionCorrelated
				result.MonsterProof = fmt.Sprintf("JoinSceneNotify.SceneInfo.InstanceId=%d -> InstanceDungeon.MapConfigId; EntityPb.ConfigId=%d + ConfigType=Proto_Level(1) -> LevelEntityConfig(MapId, EntityId).BlueprintType -> MonsterInfo.MonsterEntityID -> MonsterInfo.Id=%d", entity.sceneInstanceID, entity.configID, monsterID)
			}
		}
	}
	sceneInstanceID, hasSceneInstanceID := entity.sceneInstanceID, entity.hasSceneInstanceID
	sceneContextSource := "entity-scoped JoinSceneNotify context"
	if !hasSceneInstanceID && resolver != nil && resolver.hasSceneInstanceID &&
		(entity.sourceMessageID == cmdJoinSceneNotify || entity.sourceMessageID == cmdEntityAddNotify) {
		sceneInstanceID, hasSceneInstanceID = resolver.sceneInstanceID, true
		sceneContextSource = "current session JoinSceneNotify context"
	}
	if masters != nil && entity.hasConfigID && entity.hasConfigType && entity.configType == 1 &&
		(entity.sourceMessageID == cmdJoinSceneNotify || entity.sourceMessageID == cmdEntityAddNotify) && hasSceneInstanceID {
		if candidates, ok := masters.sceneTemplates(sceneInstanceID, entity.configID); ok {
			result.TemplateCandidates = candidates
			result.TemplateStatus = resolutionUnknown
			result.TemplateProof = fmt.Sprintf(
				"%s (InstanceId=%d) -> InstanceDungeon.MapConfigId; runtime EntityPb.ConfigId=%d + ConfigType=Proto_Level(1) -> LevelEntityConfig(MapId, EntityId).BlueprintType -> TemplateConfig candidates; combat master locale %s, client/master compatibility unverified",
				sceneContextSource, sceneInstanceID, entity.configID, masters.locale,
			)
			if len(candidates) == 1 {
				result.TemplateStatus = resolutionCorrelated
			}
		}
	}
	// EntityConfigType.Template (4): the entity was spawned from a template, so ConfigId is a TemplateConfig id and no
	// scene context is needed. This is how dynamically spawned bosses (for example Matrix challenge bosses) are named.
	if masters != nil && entity.hasConfigID && entity.hasConfigType && entity.configType == 4 && len(result.TemplateCandidates) == 0 {
		if candidate, ok := masters.templateByID(entity.configID); ok {
			result.TemplateCandidates = []sceneTemplateCandidate{candidate}
			result.TemplateStatus = resolutionCorrelated
			result.TemplateProof = fmt.Sprintf("EntityPb.ConfigType=Template(4) in the protocol enum EntityConfigType -> EntityPb.ConfigId=%d is a TemplateConfig.Id; combat master locale %s, client/master compatibility unverified", entity.configID, masters.locale)
		}
	}
	return result
}

func runtimeCharacterCandidate(entity runtimeEntity) (characterID uint64, proof string, fromConfig, conflict bool) {
	if entity.hasRoleID && !entity.roleConflict {
		characterID = entity.roleID
		proof = entity.roleSourceFieldPath + " -> " + entity.sourceFieldPath
		if entity.roleSourceFieldPath == "" || entity.sourceFieldPath == "" {
			proof = "scene role mapping -> runtime EntityId"
		}
	}
	// The client protocol's EntityConfigType defines Proto_Character as enum value 3.
	// SetPbDataByProtocol assigns EntityData.PbDataId to RoleId only in this branch.
	if entity.hasConfigType && entity.configType == 3 && entity.hasConfigID {
		if characterID != 0 && characterID != entity.configID {
			return 0, "", false, true
		}
		if characterID == 0 {
			characterID = entity.configID
			proof = fmt.Sprintf("EntityPb.ConfigId(field %d) + ConfigType(field %d)=Proto_Character(3) -> CreateEntityData.Init.PbDataId -> CreatureDataComponent.SetPbDataByProtocol.SetRoleId", fieldEntityPbConfigID, fieldEntityPbConfigType)
			fromConfig = true
		}
	}
	return characterID, proof, fromConfig, false
}

func hasPlayerSceneRoleMapping(entity runtimeEntity) bool {
	if !entity.hasPlayerID || entity.playerID == 0 || !entity.hasRoleID || entity.roleID == 0 || entity.roleConflict {
		return false
	}
	switch entity.roleSourceFieldPath {
	case "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId",
		"UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].RoleId":
		return true
	default:
		return false
	}
}

const maxSummonerEntityDepth = 8

func resolveSummonerCharacter(resolver *entityResolver, masters *gameMaster, source runtimeEntity) summonerCharacterIdentity {
	result := summonerCharacterIdentity{Status: resolutionUnknown, NameStatus: resolutionUnknown}
	if resolver == nil || !source.hasSummonerID || source.summonerID == 0 {
		return result
	}
	seen := make(map[uint64]struct{}, maxSummonerEntityDepth+1)
	if source.entityID != 0 {
		seen[source.entityID] = struct{}{}
	}
	currentID := source.summonerID
	for depth := 0; depth < maxSummonerEntityDepth; depth++ {
		if _, duplicate := seen[currentID]; duplicate {
			result.Proof = fmt.Sprintf("summoner entity cycle detected along %v -> %d", result.EntityIDs, currentID)
			return result
		}
		seen[currentID] = struct{}{}
		result.EntityIDs = append(result.EntityIDs, currentID)
		parent, status := resolver.resolve(currentID)
		if status == resolutionUnknown {
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
			character, ok := masters.characters[characterID]
			if !ok {
				result.Proof += "; Character master missing"
				return result
			}
			result.Character = character
			result.HasCharacter = true
			result.Status = resolutionStatus(character.Status)
			if result.Status == "" {
				result.Status = resolutionUnknown
			}
			if result.Status == resolutionVerified {
				// Keep the derived relation correlated until client/master compatibility
				// is established.
				result.Status = resolutionCorrelated
			}
			result.NameStatus = resolutionStatus(character.NameStatus)
			if result.NameStatus == "" {
				result.NameStatus = resolutionUnknown
			}
			return result
		}
		if !parent.hasSummonerID || parent.summonerID == 0 {
			result.Proof = fmt.Sprintf("summoner entity chain %v has no direct Character RoleId", result.EntityIDs)
			return result
		}
		currentID = parent.summonerID
	}
	result.Proof = fmt.Sprintf("summoner entity chain exceeded max depth %d: %v", maxSummonerEntityDepth, result.EntityIDs)
	return result
}

func entityProof(entity runtimeEntity) string {
	if entity.sourceFieldPath != "" {
		return entity.sourceFieldPath
	}
	switch entity.sourceMessageID {
	case cmdEntityAddNotify:
		return "EntityAddNotify.EntityPb.Id"
	case cmdUpdateSceneGroupDataNotify:
		return "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].EntityId"
	case cmdEntityActiveRequest:
		return "EntityActiveRequest.EntityId; observed only"
	default:
		return ""
	}
}

func newEntityResolver() *entityResolver {
	return &entityResolver{entities: make(map[uint64]runtimeEntity), limit: 8192, lifecycles: make(map[uint64]entityLifecycle)}
}

func (r *entityResolver) reset() {
	r.entities = make(map[uint64]runtimeEntity)
	r.sequence = 0
	r.sceneInstanceID, r.hasSceneInstanceID = 0, false
	r.sceneCombatEntityID, r.hasSceneCombatEntityID = 0, false
	r.lifecycles = make(map[uint64]entityLifecycle)
	r.lifecycleSequence = 0
}

func (r *entityResolver) observeActiveRequest(body []byte) error {
	fields, err := parseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse EntityActiveRequest: %w", err)
	}
	for _, field := range fields {
		if field.number != fieldEntityActiveRequestEntityID || field.wire != 0 || field.value == 0 {
			continue
		}
		r.sequence++
		entity := r.entities[field.value]
		entity.entityID = field.value
		if entity.sourceMessageID != cmdEntityAddNotify {
			entity.sourceMessageID = cmdEntityActiveRequest
		}
		entity.lastSeenSequence = r.sequence
		r.entities[field.value] = entity
		r.evictIfNeeded()
	}
	return nil
}

func (r *entityResolver) applyEntityAddNotify(body []byte) (int, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return 0, fmt.Errorf("parse EntityAddNotify: %w", err)
	}
	added := 0
	for _, field := range fields {
		if field.number != fieldEntityAddNotifyEntityPbs || field.wire != 2 {
			continue
		}
		entity, err := parseEntityPb(field.data)
		if err != nil {
			return added, err
		}
		if entity.entityID == 0 {
			continue
		}
		if entity.hasPartComponent {
			entity.partComponentSource = "EntityAddNotify.EntityPbs[].ComponentPbs.PartComponent.PartLifeInfos[]"
		}
		r.sequence++
		if previous, ok := r.entities[entity.entityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
		}
		entity.sourceMessageID = cmdEntityAddNotify
		entity.sourceFieldPath = "EntityAddNotify.EntityPb.Id"
		entity.lastSeenSequence = r.sequence
		r.entities[entity.entityID] = entity
		r.observeLifecycleAdd(entity)
		r.evictIfNeeded()
		added++
	}
	return added, nil
}

// applyJoinSceneNotify replaces stale entity observations with the new scene
// snapshot. Owner, owner-incarnation, and summoner IDs remain distinct.
func (r *entityResolver) applyJoinSceneNotify(body []byte) (int, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return 0, fmt.Errorf("parse JoinSceneNotify: %w", err)
	}
	var sceneInfo []byte
	for _, field := range fields {
		if field.number == fieldJoinSceneNotifySceneInfo && field.wire == 2 {
			sceneInfo = field.data
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
	sceneFields, err := parseProtoFields(sceneInfo)
	if err != nil {
		return 0, fmt.Errorf("parse SceneInformation: %w", err)
	}
	var instanceID, sceneEntityID uint64
	var hasInstanceID, hasSceneEntityID bool
	for _, field := range sceneFields {
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case fieldSceneInformationInstanceID:
			instanceID, hasInstanceID = field.value, field.value != 0
		case fieldSceneInformationSceneEntityID:
			sceneEntityID, hasSceneEntityID = field.value, field.value != 0
		}
	}

	r.entities = make(map[uint64]runtimeEntity)
	r.sceneInstanceID, r.hasSceneInstanceID = instanceID, hasInstanceID
	r.sceneCombatEntityID, r.hasSceneCombatEntityID = sceneEntityID, hasSceneEntityID
	snapshotIDs := make([]uint64, 0, len(entities))
	for _, entity := range entities {
		if entity.entityID == 0 {
			continue
		}
		if entity.hasPartComponent {
			entity.partComponentSource = "JoinSceneNotify.SceneInfo entity snapshot.ComponentPbs.PartComponent.PartLifeInfos[]"
		}
		if previous, ok := r.entities[entity.entityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
		}
		entity.sceneInstanceID, entity.hasSceneInstanceID = instanceID, hasInstanceID
		r.sequence++
		entity.sourceMessageID = cmdJoinSceneNotify
		entity.lastSeenSequence = r.sequence
		r.entities[entity.entityID] = entity
		snapshotIDs = append(snapshotIDs, entity.entityID)
		r.evictIfNeeded()
	}
	r.observeLifecycleSnapshot(snapshotIDs)
	return len(r.entities), nil
}

// applyUpdateSceneGroupDataNotify adds the direct role-to-entity mapping carried
// by the client protocol's UpdateSceneGroupDataNotify. Other entity metadata
// already observed for the same ID is retained, and conflicting RoleIds remain
// unresolved through mergeRuntimeEntity.
func (r *entityResolver) applyUpdateSceneGroupDataNotify(body []byte) (int, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return 0, fmt.Errorf("parse UpdateSceneGroupDataNotify: %w", err)
	}
	updates := make([]runtimeEntity, 0)
	for _, field := range fields {
		if field.number != fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList || field.wire != 2 { // UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[]
			continue
		}
		playerFields, err := parseProtoFields(field.data)
		if err != nil {
			return 0, fmt.Errorf("parse PlayerSceneGroupDataList: %w", err)
		}
		var playerID uint64
		for _, playerField := range playerFields {
			if playerField.number == fieldPlayerSceneGroupDataListPlayerID && playerField.wire == 0 {
				playerID = playerField.value
				break
			}
		}
		for _, playerField := range playerFields {
			if playerField.number != fieldPlayerSceneGroupDataListPlayerSceneGroupData || playerField.wire != 2 { // PlayerSceneGroupDataList.PlayerSceneGroupData[]
				continue
			}
			groupFields, err := parseProtoFields(playerField.data)
			if err != nil {
				return 0, fmt.Errorf("parse PlayerSceneGroupData: %w", err)
			}
			for _, groupField := range groupFields {
				if groupField.number != fieldPlayerSceneGroupDataFightRoleInfos || groupField.wire != 2 { // PlayerSceneGroupData.FightRoleInfos[]
					continue
				}
				roleFields, err := parseProtoFields(groupField.data)
				if err != nil {
					return 0, fmt.Errorf("parse SceneRoleInformation: %w", err)
				}
				var entity runtimeEntity
				entity.playerID, entity.hasPlayerID = playerID, playerID != 0
				entity.roleSourceFieldPath = "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
				for _, roleField := range roleFields {
					if roleField.wire != 0 {
						continue
					}
					switch roleField.number {
					case fieldSceneRoleInformationRoleID: // SceneRoleInformation.RoleId
						entity.roleID, entity.hasRoleID = roleField.value, roleField.value != 0
					case fieldSceneRoleInformationEntityID: // SceneRoleInformation.EntityId
						entity.entityID = roleField.value
					}
				}
				if entity.entityID == 0 || !entity.hasRoleID {
					continue
				}
				entity.sourceMessageID = cmdUpdateSceneGroupDataNotify
				entity.sourceFieldPath = "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].EntityId"
				updates = append(updates, entity)
			}
		}
	}
	for _, entity := range updates {
		r.sequence++
		if previous, ok := r.entities[entity.entityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
			// Preserve the stronger entity-lifecycle source while retaining the
			// role mapping and its separate provenance.
			if previous.sourceMessageID == cmdEntityAddNotify || previous.sourceMessageID == cmdJoinSceneNotify {
				entity.sourceMessageID = previous.sourceMessageID
				entity.sourceFieldPath = previous.sourceFieldPath
			}
		}
		entity.lastSeenSequence = r.sequence
		r.entities[entity.entityID] = entity
		r.evictIfNeeded()
	}
	return len(updates), nil
}

func parseSceneEntitySnapshot(sceneInfo []byte) ([]runtimeEntity, error) {
	fields, err := parseProtoFields(sceneInfo)
	if err != nil {
		return nil, fmt.Errorf("parse SceneInformation: %w", err)
	}
	entities := make([]runtimeEntity, 0)
	for _, field := range fields {
		if field.wire != 2 {
			continue
		}
		switch field.number {
		case fieldSceneInformationPlayerInfos: // SceneInformation.PlayerInfos[]
			roleEntities, err := parseScenePlayerRoleEntities(field.data)
			if err != nil {
				return nil, err
			}
			entities = append(entities, roleEntities...)
		case fieldSceneInformationDynamicEntityList: // SceneInformation.DynamicEntityList[]
			entity, err := parseDynamicEntityInformation(field.data, "JoinSceneNotify.SceneInfo.DynamicEntityList.Id")
			if err != nil {
				return nil, err
			}
			entities = append(entities, entity)
		case fieldSceneInformationAoiData: // SceneInformation.AoiData
			aoiFields, err := parseProtoFields(field.data)
			if err != nil {
				return nil, fmt.Errorf("parse PlayerSceneAoiData: %w", err)
			}
			for _, aoiField := range aoiFields {
				if aoiField.wire != 2 {
					continue
				}
				switch aoiField.number {
				case fieldPlayerSceneAoiDataDynamicEntityList: // PlayerSceneAoiData.DynamicEntityList[]
					entity, err := parseDynamicEntityInformation(aoiField.data, "JoinSceneNotify.SceneInfo.AoiData.DynamicEntityList.Id")
					if err != nil {
						return nil, err
					}
					entities = append(entities, entity)
				case fieldPlayerSceneAoiDataEntities: // PlayerSceneAoiData.Entities[]: EntityPb
					entity, err := parseEntityPb(aoiField.data)
					if err != nil {
						return nil, fmt.Errorf("parse JoinSceneNotify AoiData EntityPb: %w", err)
					}
					entity.sourceFieldPath = "JoinSceneNotify.SceneInfo.AoiData.Entities.EntityPb.Id"
					entities = append(entities, entity)
				}
			}
		}
	}
	return entities, nil
}

func parseScenePlayerRoleEntities(data []byte) ([]runtimeEntity, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse ScenePlayerInformation: %w", err)
	}
	var playerID uint64
	for _, field := range fields {
		if field.number == fieldScenePlayerInformationPlayerID && field.wire == 0 {
			playerID = field.value
			break
		}
	}
	entities := make([]runtimeEntity, 0)
	for _, field := range fields {
		if field.number != fieldScenePlayerInformationPlayerSceneGroupData || field.wire != 2 { // ScenePlayerInformation.PlayerSceneGroupData[]
			continue
		}
		groups, err := parseProtoFields(field.data)
		if err != nil {
			return nil, fmt.Errorf("parse PlayerSceneGroupData: %w", err)
		}
		for _, groupField := range groups {
			if groupField.number != fieldPlayerSceneGroupDataFightRoleInfos || groupField.wire != 2 { // PlayerSceneGroupData.FightRoleInfos[]
				continue
			}
			roleFields, err := parseProtoFields(groupField.data)
			if err != nil {
				return nil, fmt.Errorf("parse SceneRoleInformation: %w", err)
			}
			var entity runtimeEntity
			entity.playerID, entity.hasPlayerID = playerID, playerID != 0
			entity.roleSourceFieldPath = "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
			for _, roleField := range roleFields {
				if roleField.wire != 0 {
					continue
				}
				switch roleField.number {
				case fieldSceneRoleInformationRoleID: // SceneRoleInformation.RoleId
					entity.roleID, entity.hasRoleID = roleField.value, roleField.value != 0
				case fieldSceneRoleInformationEntityID: // SceneRoleInformation.EntityId; same client CreatureDataId namespace
					entity.entityID = roleField.value
				}
			}
			if entity.entityID != 0 && entity.hasRoleID {
				entity.sourceFieldPath = "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].EntityId"
				entities = append(entities, entity)
			}
		}
	}
	return entities, nil
}

func parseDynamicEntityInformation(data []byte, sourceFieldPath string) (runtimeEntity, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return runtimeEntity{}, fmt.Errorf("parse DynamicEntityInformation: %w", err)
	}
	var entity runtimeEntity
	entity.sourceFieldPath = sourceFieldPath
	for _, field := range fields {
		if field.wire == 0 {
			switch field.number {
			case fieldDynamicEntityInformationID:
				entity.entityID = field.value
			case fieldDynamicEntityInformationEntityType:
				entity.entityTypeCode, entity.hasEntityType = field.value, true
			case fieldDynamicEntityInformationConfigID:
				entity.configID, entity.hasConfigID = field.value, true
			case fieldDynamicEntityInformationPlayerID:
				entity.playerID, entity.hasPlayerID = field.value, true
			case fieldDynamicEntityInformationOwnerID:
				entity.ownerID, entity.hasOwnerID = field.value, true
			}
			continue
		}
		if field.wire != 2 {
			continue
		}
		switch field.number {
		case fieldDynamicEntityInformationSummonInfo: // DynamicEntityInformation.SummonInfo
			if err := applySummonInfo(&entity, field.data); err != nil {
				return runtimeEntity{}, err
			}
		case fieldDynamicEntityInformationComponentPbs: // DynamicEntityInformation.ComponentPbs[]
			if err := applyEntityComponent(&entity, field.data); err != nil {
				return runtimeEntity{}, err
			}
		}
	}
	return entity, nil
}

func applySummonInfo(entity *runtimeEntity, data []byte) error {
	fields, err := parseProtoFields(data)
	if err != nil {
		return fmt.Errorf("parse SummonInfo: %w", err)
	}
	for _, field := range fields {
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case fieldSummonInfoSummonCfgID:
			entity.sceneSummonConfigID, entity.hasSceneSummonConfigID = field.value, true
		case fieldSummonInfoSummonerID:
			entity.sceneSummonerID, entity.hasSceneSummonerID = field.value, true
		case fieldSummonInfoSummonSkillID:
			entity.sceneSummonSkillID, entity.hasSceneSummonSkillID = field.value, true
		}
	}
	return nil
}

// entityRemoveNotifyIDs returns the entities an EntityRemoveNotify removes. On a malformed EntityRemoveInfo it returns
// the IDs read before it together with the error.
func entityRemoveNotifyIDs(body []byte) ([]uint64, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return nil, fmt.Errorf("parse EntityRemoveNotify: %w", err)
	}
	remove := false
	for _, field := range fields {
		if field.number == fieldEntityRemoveNotifyIsRemove && field.wire == 0 {
			remove = field.value != 0
		}
	}
	if !remove {
		return nil, nil
	}
	ids := make([]uint64, 0)
	for _, field := range fields {
		if field.number != fieldEntityRemoveNotifyRemoveInfos || field.wire != 2 {
			continue
		}
		info, err := parseProtoFields(field.data)
		if err != nil {
			return ids, fmt.Errorf("parse EntityRemoveInfo: %w", err)
		}
		for _, value := range info {
			if value.number == fieldEntityRemoveInfoEntityID && value.wire == 0 && value.value != 0 {
				ids = append(ids, value.value)
			}
		}
	}
	return ids, nil
}

func (r *entityResolver) applyEntityRemoveNotify(body []byte) (int, []uint64, error) {
	removedIDs, err := entityRemoveNotifyIDs(body)
	removed := 0
	for _, id := range removedIDs {
		r.observeLifecycleRemove(id)
		if _, ok := r.entities[id]; ok {
			delete(r.entities, id)
			removed++
		}
	}
	return removed, removedIDs, err
}

// activeResponseComponentPath is the provenance of components read from an EntityActiveResponse matched to its request.
var activeResponseComponentPath = fmt.Sprintf("EntityActiveRequest frame token match + field%d EntityId -> EntityActiveResponse.field%d ComponentPbs",
	fieldEntityActiveRequestEntityID, fieldEntityActiveResponseComponentPbs)

func (r *entityResolver) applyActiveResponseIdentityComponents(entityID uint64, components []protoField) (int, error) {
	update := runtimeEntity{entityID: entityID}
	observed := 0
	for _, component := range components {
		if component.wire != 2 || !isRuntimeIdentityComponent(component.number) {
			continue
		}
		if err := applyEntityComponentPayload(&update, component.number, component.data); err != nil {
			return observed, err
		}
		observed++
	}
	if observed == 0 || entityID == 0 {
		return observed, nil
	}
	previous, exists := r.entities[entityID]
	if !exists {
		return observed, nil
	}
	update.componentSourceMessageID = cmdEntityActiveResponse
	update.componentSourceFieldPath = activeResponseComponentPath
	update = mergeRuntimeEntity(previous, update)
	r.sequence++
	update.lastSeenSequence = r.sequence
	r.entities[entityID] = update
	r.evictIfNeeded()
	return observed, nil
}

// applyActiveResponseFsmComponents records the state-machine hashes of an entity from an EntityActiveResponse. It changes
// only those two fields: the entity's identity and its component provenance stay as they were.
func (r *entityResolver) applyActiveResponseFsmComponents(entityID uint64, components []protoField) error {
	previous, exists := r.entities[entityID]
	if !exists || entityID == 0 {
		return nil
	}
	update := runtimeEntity{entityID: entityID}
	for _, component := range components {
		if err := applyEntityComponentPayload(&update, fieldEntityComponentPbEntityFsmComponentPb, component.data); err != nil {
			return err
		}
	}
	if update.hasFsmHashCode {
		previous.fsmHashCode, previous.hasFsmHashCode = update.fsmHashCode, true
	}
	if update.hasFsmCommonHashCode {
		previous.fsmCommonHashCode, previous.hasFsmCommonHashCode = update.fsmCommonHashCode, true
	}
	r.entities[entityID] = previous
	return nil
}

func (r *entityResolver) applyActiveResponsePartComponents(entityID uint64, components []protoField) (int, error) {
	update := runtimeEntity{entityID: entityID}
	observed := 0
	for _, component := range components {
		if component.number != fieldEntityComponentPbPartComponent || component.wire != 2 {
			continue
		}
		if err := applyEntityComponentPayload(&update, component.number, component.data); err != nil {
			return observed, err
		}
		observed++
	}
	if observed == 0 || entityID == 0 {
		return observed, nil
	}
	previous, exists := r.entities[entityID]
	if !exists {
		return observed, nil
	}
	update.partComponentSource = activeResponseComponentPath + ".PartComponent.PartLifeInfos[]"
	update = mergeRuntimeEntity(previous, update)
	r.sequence++
	update.lastSeenSequence = r.sequence
	r.entities[entityID] = update
	r.evictIfNeeded()
	return observed, nil
}

func (r *entityResolver) invalidatePartComponents() {
	for entityID, entity := range r.entities {
		entity.hasPartComponent = false
		entity.partInformation = nil
		entity.partComponentSource = ""
		r.entities[entityID] = entity
	}
}

func (r *entityResolver) resolvePartReference(entityID uint64, partID int32) runtimePartReference {
	result := runtimePartReference{partID: partID, status: partReferenceUnknown}
	if partID == -1 {
		result.status = partReferenceNoPart
		result.evidence = "DamageExecuteRequest.PartId=-1 is the observed no-part sentinel; raw value preserved and no PartInformation lookup attempted"
		return result
	}
	if partID <= 0 {
		result.evidence = "Non-positive DamageExecuteRequest.PartId is preserved without assigning a PartInformation row"
		return result
	}
	entity, ok := r.entities[entityID]
	if !ok || !entity.hasPartComponent {
		result.evidence = "No current PartComponent observation is available for the exact target entity in this session"
		return result
	}
	information, ok := entity.partInformation[partID]
	if !ok {
		result.evidence = "Current target PartComponent has no PartLifeInfos row with this exact PartId"
		return result
	}
	if information.conflicted {
		result.evidence = "Current target PartComponent has conflicting PartLifeInfos rows for this PartId"
		return result
	}
	result.status = partReferenceCorrelated
	result.information = &information
	result.informationFrom = entity.partComponentSource
	result.evidence = "DamageExecuteRequest.PartId exactly matches PartComponent.PartLifeInfos.PartId observed for the same TargetEntityId before this request; this is a session-scoped runtime relation, not a static Master key"
	return result
}

func isRuntimeIdentityComponent(componentNumber uint64) bool {
	switch componentNumber {
	case fieldEntityComponentPbSummonerComponent, fieldEntityComponentPbMonsterCaptureComponent, fieldEntityComponentPbConcomitantsComponentPb, fieldEntityComponentPbPlayerFollowerSelfComponentPb, fieldEntityComponentPbPlayerFollowersComponentPb, fieldEntityComponentPbFollowEntityComponentPb:
		return true
	default:
		return false
	}
}

func parseEntityPb(data []byte) (runtimeEntity, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return runtimeEntity{}, fmt.Errorf("parse EntityPb: %w", err)
	}
	var entity runtimeEntity
	for _, field := range fields {
		if field.wire != 0 {
			if field.number == fieldEntityPbComponentPbs && field.wire == 2 {
				if err := applyEntityComponent(&entity, field.data); err != nil {
					return runtimeEntity{}, err
				}
			}
			continue
		}
		switch field.number {
		case fieldEntityPbID: // EntityPb.Id
			entity.entityID = field.value
		case fieldEntityPbConfigID: // EntityPb.ConfigId
			entity.configID, entity.hasConfigID = field.value, true
		case fieldEntityPbConfigType: // EntityPb.ConfigType
			entity.configType, entity.hasConfigType = field.value, true
		case fieldEntityPbEntityType: // EntityPb.EntityType
			entity.entityTypeCode, entity.hasEntityType = field.value, true
		case fieldEntityPbPlayerID: // EntityPb.PlayerId
			entity.playerID, entity.hasPlayerID = field.value, true
		case fieldEntityPbOwnerIncID: // EntityPb.OwnerIncId is not EntityPb.Id or DynamicEntityInformation.OwnerId.
			entity.ownerIncID, entity.hasOwnerIncID = field.value, true
		case fieldEntityPbRoleSkinID: // EntityPb.RoleSkinId is a skin reference, not CharacterId.
			entity.roleSkinID, entity.hasRoleSkinID = field.value, true
		}
	}
	return entity, nil
}

func applyEntityComponent(entity *runtimeEntity, data []byte) error {
	components, err := parseProtoFields(data)
	if err != nil {
		return fmt.Errorf("parse EntityComponentPb: %w", err)
	}
	for _, component := range components {
		if component.wire != 2 {
			continue
		}
		if err := applyEntityComponentPayload(entity, component.number, component.data); err != nil {
			return err
		}
	}
	return nil
}

func applyEntityComponentPayload(entity *runtimeEntity, componentNumber uint64, data []byte) error {
	switch componentNumber {
	case fieldEntityComponentPbSummonerComponent: // SummonerComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse SummonerComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case fieldSummonerComponentPbSummonerID:
				entity.summonerID, entity.hasSummonerID = field.value, true
			case fieldSummonerComponentPbSummonCfgID:
				entity.summonConfigID, entity.hasSummonConfigID = field.value, true
			case fieldSummonerComponentPbSummonSkillID:
				entity.summonSkillID, entity.hasSummonSkillID = field.value, true
			case fieldSummonerComponentPbPlayerID:
				entity.summonerPlayerID, entity.hasSummonerPlayerID = field.value, true
			case fieldSummonerComponentPbType:
				entity.summonType, entity.hasSummonType = field.value, true
			}
		}
	case fieldEntityComponentPbEntityFsmComponentPb: // EntityFsmComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse EntityFsmComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.number == fieldEntityFsmComponentPbCommonHashCode && field.wire == 0 { // CommonHashCode (int32)
				entity.fsmCommonHashCode, entity.hasFsmCommonHashCode = int64(int32(uint32(field.value))), true
			}
			if field.number == fieldEntityFsmComponentPbHashCode && field.wire == 0 { // HashCode (int32)
				entity.fsmHashCode, entity.hasFsmHashCode = int64(int32(uint32(field.value))), true
			}
		}
	case fieldEntityComponentPbPartComponent: // PartComponentPb
		information, err := parseRuntimePartInformation(data)
		if err != nil {
			return err
		}
		entity.hasPartComponent = true
		entity.partInformation = information
	case fieldEntityComponentPbMonsterCaptureComponent: // MonsterCaptureComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse MonsterCaptureComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case fieldMonsterCaptureComponentPbTemplateID:
				entity.templateID, entity.hasTemplateID = field.value, true
			case fieldMonsterCaptureComponentPbEntityID:
				entity.monsterCaptureEntityID, entity.hasMonsterCaptureEntityID = field.value, true
			case fieldMonsterCaptureComponentPbMonsterID:
				entity.monsterID, entity.hasMonsterID = field.value, true
			}
		}
	case fieldEntityComponentPbPlayerFollowerSelfComponentPb: // PlayerFollowerSelfComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse PlayerFollowerSelfComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case fieldPlayerFollowerSelfComponentPbPlayerEntityID:
				entity.playerEntityID, entity.hasPlayerEntityID = field.value, true
			case fieldPlayerFollowerSelfComponentPbSummonConfigID:
				entity.followerSummonConfigID, entity.hasFollowerSummonConfigID = field.value, true
			}
		}
	case fieldEntityComponentPbConcomitantsComponentPb: // ConcomitantsComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse ConcomitantsComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case fieldConcomitantsComponentPbVisionEntityIDs: // VisionEntityIds[]
				if len(entity.concomitantVisionIDs) >= maxRuntimeComponentEntityRefs {
					return fmt.Errorf("ConcomitantsComponentPb.VisionEntityIds exceeds limit %d", maxRuntimeComponentEntityRefs)
				}
				entity.concomitantVisionIDs = append(entity.concomitantVisionIDs, field.value)
			case fieldConcomitantsComponentPbCustomEntityIDs: // CustomEntityIds[]
				if len(entity.concomitantCustomIDs) >= maxRuntimeComponentEntityRefs {
					return fmt.Errorf("ConcomitantsComponentPb.CustomEntityIds exceeds limit %d", maxRuntimeComponentEntityRefs)
				}
				entity.concomitantCustomIDs = append(entity.concomitantCustomIDs, field.value)
			case fieldConcomitantsComponentPbPhantomRoleEid: // PhantomRoleEid
				entity.phantomRoleEntityID, entity.hasPhantomRoleEntityID = field.value, true
			case fieldConcomitantsComponentPbWeakVisionEid: // WeakVisionEid
				entity.weakVisionEntityID, entity.hasWeakVisionEntityID = field.value, true
			}
		}
	case fieldEntityComponentPbPlayerFollowersComponentPb: // PlayerFollowersComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse PlayerFollowersComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.number != fieldPlayerFollowersComponentPbListPlayerFollowerPb || field.wire != 2 {
				continue
			}
			if len(entity.playerFollowers) >= maxRuntimeComponentEntityRefs {
				return fmt.Errorf("PlayerFollowersComponentPb.ListPlayerFollowerPb exceeds limit %d", maxRuntimeComponentEntityRefs)
			}
			followerFields, err := parseProtoFields(field.data)
			if err != nil {
				return fmt.Errorf("parse PlayerFollowerPb: %w", err)
			}
			var follower runtimePlayerFollower
			for _, followerField := range followerFields {
				if followerField.wire != 0 {
					continue
				}
				switch followerField.number {
				case fieldPlayerFollowerPbType: // Type
					follower.typeID, follower.hasType = followerField.value, true
				case fieldPlayerFollowerPbEntityID: // EntityId
					follower.entityID, follower.hasEntity = followerField.value, true
				}
			}
			entity.playerFollowers = append(entity.playerFollowers, follower)
		}
	case fieldEntityComponentPbFollowEntityComponentPb: // FollowEntityComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse FollowEntityComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.number == fieldFollowEntityComponentPbEntityID && field.wire == 0 {
				entity.followEntityID, entity.hasFollowEntityID = field.value, true
			}
		}
	}
	return nil
}

func parseRuntimePartInformation(data []byte) (map[int32]runtimePartInformation, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse PartComponentPb: %w", err)
	}
	parts := make(map[int32]runtimePartInformation)
	rowsSeen := 0
	for _, field := range fields {
		if field.number != fieldPartComponentPbPartLifeInfos || field.wire != 2 { // PartComponentPb.PartLifeInfos[]
			continue
		}
		rowsSeen++
		if rowsSeen > maxRuntimePartInformationRows {
			return nil, fmt.Errorf("PartComponentPb.PartLifeInfos exceeds limit %d", maxRuntimePartInformationRows)
		}
		rowFields, err := parseProtoFields(field.data)
		if err != nil {
			return nil, fmt.Errorf("parse PartInformation: %w", err)
		}
		var information runtimePartInformation
		for _, rowField := range rowFields {
			switch {
			case rowField.number == fieldPartInformationPartID && rowField.wire == 0: // PartInformation.PartId
				information.partID = int32(uint32(rowField.value))
				information.hasPartID = true
			case rowField.number == fieldPartInformationLifeMax && rowField.wire == 5: // PartInformation.LifeMax
				information.lifeMax = math.Float32frombits(binary.LittleEndian.Uint32(rowField.data))
				if math.IsNaN(float64(information.lifeMax)) || math.IsInf(float64(information.lifeMax), 0) {
					return nil, fmt.Errorf("PartInformation.LifeMax is not finite")
				}
				information.hasLifeMax = true
			case rowField.number == fieldPartInformationActivated && rowField.wire == 0: // PartInformation.Activated
				information.activated = rowField.value != 0
				information.hasActivated = true
			case rowField.number == fieldPartInformationPartTag && rowField.wire == 0: // PartInformation.PartTag
				information.partTag = int32(uint32(rowField.value))
				information.hasPartTag = true
			}
		}
		if !information.hasPartID {
			continue
		}
		if previous, exists := parts[information.partID]; exists {
			if previous != information {
				previous.conflicted = true
				parts[information.partID] = previous
			}
			continue
		}
		parts[information.partID] = information
	}
	return parts, nil
}

func mergeRuntimeEntity(old, update runtimeEntity) runtimeEntity {
	if update.entityID == 0 {
		update.entityID = old.entityID
	}
	if !update.hasSceneInstanceID && old.hasSceneInstanceID {
		update.sceneInstanceID, update.hasSceneInstanceID = old.sceneInstanceID, true
	}
	if update.sourceMessageID == 0 {
		update.sourceMessageID = old.sourceMessageID
	}
	if !update.hasEntityType && old.hasEntityType {
		update.entityTypeCode, update.hasEntityType = old.entityTypeCode, true
	}
	if !update.hasConfigID && old.hasConfigID {
		update.configID, update.hasConfigID = old.configID, true
	}
	if !update.hasConfigType && old.hasConfigType {
		update.configType, update.hasConfigType = old.configType, true
	}
	if !update.hasPlayerID && old.hasPlayerID {
		update.playerID, update.hasPlayerID = old.playerID, true
	}
	if !update.hasOwnerID && old.hasOwnerID {
		update.ownerID, update.hasOwnerID = old.ownerID, true
	}
	if !update.hasOwnerIncID && old.hasOwnerIncID {
		update.ownerIncID, update.hasOwnerIncID = old.ownerIncID, true
	}
	if !update.hasSummonerID && old.hasSummonerID {
		update.summonerID, update.hasSummonerID = old.summonerID, true
	}
	if !update.hasSummonerPlayerID && old.hasSummonerPlayerID {
		update.summonerPlayerID, update.hasSummonerPlayerID = old.summonerPlayerID, true
	}
	if !update.hasSummonConfigID && old.hasSummonConfigID {
		update.summonConfigID, update.hasSummonConfigID = old.summonConfigID, true
	}
	if !update.hasSummonSkillID && old.hasSummonSkillID {
		update.summonSkillID, update.hasSummonSkillID = old.summonSkillID, true
	}
	if !update.hasSummonType && old.hasSummonType {
		update.summonType, update.hasSummonType = old.summonType, true
	}
	if !update.hasSceneSummonerID && old.hasSceneSummonerID {
		update.sceneSummonerID, update.hasSceneSummonerID = old.sceneSummonerID, true
	}
	if !update.hasSceneSummonConfigID && old.hasSceneSummonConfigID {
		update.sceneSummonConfigID, update.hasSceneSummonConfigID = old.sceneSummonConfigID, true
	}
	if !update.hasSceneSummonSkillID && old.hasSceneSummonSkillID {
		update.sceneSummonSkillID, update.hasSceneSummonSkillID = old.sceneSummonSkillID, true
	}
	if !update.hasPlayerEntityID && old.hasPlayerEntityID {
		update.playerEntityID, update.hasPlayerEntityID = old.playerEntityID, true
	}
	if !update.hasFollowerSummonConfigID && old.hasFollowerSummonConfigID {
		update.followerSummonConfigID, update.hasFollowerSummonConfigID = old.followerSummonConfigID, true
	}
	if !update.hasFsmCommonHashCode && old.hasFsmCommonHashCode {
		update.fsmCommonHashCode, update.hasFsmCommonHashCode = old.fsmCommonHashCode, true
	}
	if !update.hasFsmHashCode && old.hasFsmHashCode {
		update.fsmHashCode, update.hasFsmHashCode = old.fsmHashCode, true
	}
	if !update.hasPhantomRoleEntityID && old.hasPhantomRoleEntityID {
		update.phantomRoleEntityID, update.hasPhantomRoleEntityID = old.phantomRoleEntityID, true
	}
	if len(update.concomitantVisionIDs) == 0 && len(old.concomitantVisionIDs) > 0 {
		update.concomitantVisionIDs = append([]uint64(nil), old.concomitantVisionIDs...)
	}
	if len(update.concomitantCustomIDs) == 0 && len(old.concomitantCustomIDs) > 0 {
		update.concomitantCustomIDs = append([]uint64(nil), old.concomitantCustomIDs...)
	}
	if !update.hasWeakVisionEntityID && old.hasWeakVisionEntityID {
		update.weakVisionEntityID, update.hasWeakVisionEntityID = old.weakVisionEntityID, true
	}
	if !update.hasFollowEntityID && old.hasFollowEntityID {
		update.followEntityID, update.hasFollowEntityID = old.followEntityID, true
	}
	if len(update.playerFollowers) == 0 && len(old.playerFollowers) > 0 {
		update.playerFollowers = append([]runtimePlayerFollower(nil), old.playerFollowers...)
	}
	if !update.hasPartComponent && old.hasPartComponent {
		update.hasPartComponent = true
		update.partInformation = old.partInformation
		update.partComponentSource = old.partComponentSource
	}
	if !update.hasMonsterID && old.hasMonsterID {
		update.monsterID, update.hasMonsterID = old.monsterID, true
	}
	if !update.hasMonsterCaptureEntityID && old.hasMonsterCaptureEntityID {
		update.monsterCaptureEntityID, update.hasMonsterCaptureEntityID = old.monsterCaptureEntityID, true
	}
	if !update.hasTemplateID && old.hasTemplateID {
		update.templateID, update.hasTemplateID = old.templateID, true
	}
	if !update.hasRoleSkinID && old.hasRoleSkinID {
		update.roleSkinID, update.hasRoleSkinID = old.roleSkinID, true
	}
	if old.roleConflict {
		update.roleConflict = true
		update.roleID, update.hasRoleID = 0, false
	} else if update.hasRoleID && old.hasRoleID && update.roleID != old.roleID {
		update.roleConflict = true
		update.roleID, update.hasRoleID = 0, false
	} else if !update.hasRoleID && old.hasRoleID {
		update.roleID, update.hasRoleID = old.roleID, true
	}
	if update.roleSourceFieldPath == "" {
		update.roleSourceFieldPath = old.roleSourceFieldPath
	}
	if update.componentSourceMessageID == 0 {
		update.componentSourceMessageID = old.componentSourceMessageID
	}
	if update.componentSourceFieldPath == "" {
		update.componentSourceFieldPath = old.componentSourceFieldPath
	}
	if update.sourceFieldPath == "" {
		update.sourceFieldPath = old.sourceFieldPath
	}
	return update
}

func (r *entityResolver) resolve(entityID uint64) (runtimeEntity, resolutionStatus) {
	entity, ok := r.entities[entityID]
	if !ok {
		return runtimeEntity{entityID: entityID}, resolutionUnknown
	}
	if entity.sourceMessageID == cmdEntityAddNotify {
		return entity, resolutionVerified
	}
	if entity.sourceMessageID == cmdJoinSceneNotify {
		return entity, resolutionVerified
	}
	if entity.sourceMessageID == cmdUpdateSceneGroupDataNotify {
		return entity, resolutionVerified
	}
	if entity.sourceMessageID == cmdEntityActiveRequest {
		return entity, resolutionCorrelated
	}
	if entity.sourceMessageID == cmdEntityActiveResponse {
		return entity, resolutionCorrelated
	}
	return entity, resolutionUnknown
}

func (r *entityResolver) evictIfNeeded() {
	if r.limit <= 0 || len(r.entities) <= r.limit {
		return
	}
	var oldestID, oldestSequence uint64
	first := true
	for id, entity := range r.entities {
		if first || entity.lastSeenSequence < oldestSequence {
			oldestID, oldestSequence, first = id, entity.lastSeenSequence, false
		}
	}
	if !first {
		delete(r.entities, oldestID)
	}
}
