package main

import (
	"fmt"
)

type resolutionStatus string

const (
	resolutionVerified   resolutionStatus = "VERIFIED"
	resolutionCorrelated resolutionStatus = "CORRELATED"
	resolutionHypothesis resolutionStatus = "HYPOTHESIS"
	resolutionUnknown    resolutionStatus = "UNKNOWN"
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
	phantomRoleEntityID       uint64
	hasPhantomRoleEntityID    bool
	concomitantVisionIDs      []uint64
	concomitantCustomIDs      []uint64
	weakVisionEntityID        uint64
	hasWeakVisionEntityID     bool
	followEntityID            uint64
	hasFollowEntityID         bool
	playerFollowers           []runtimePlayerFollower
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

const maxRuntimeComponentEntityRefs = 256

type entityResolver struct {
	entities           map[uint64]runtimeEntity
	sequence           uint64
	limit              int
	sceneInstanceID    uint64
	hasSceneInstanceID bool
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
	Monster            masterRecord
	HasMonster         bool
	MonsterStatus      resolutionStatus
	MonsterProof       string
	TemplateCandidates []sceneTemplateCandidate
	TemplateStatus     resolutionStatus
	TemplateProof      string
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
	if characterID == 0 && !result.HasCharacter && !entity.roleConflict && masters != nil && entity.hasRoleSkinID {
		if character, ok := masters.characterBySkin(entity.roleSkinID); ok {
			result.Character = character
			result.HasCharacter = true
			result.CharacterStatus = resolutionStatus(character.Status)
			result.CharacterProof = "EntityPb.RoleSkinId(23) -> RoleInfo.SkinId -> RoleInfo.Id; client build observed, master compatibility unverified"
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
			// The field path is verified against Windows Global UHD 3.7.1, but the
			// observed client build has not been matched to this versioned master.
			if result.MonsterStatus == resolutionVerified {
				result.MonsterStatus = resolutionCorrelated
			}
			result.MonsterProof = "MonsterCaptureComponentPb.MonsterId -> MonsterInfo.Id; Windows Global UHD 3.7.1 schema, client build observed, master compatibility unverified"
		}
	}
	if masters != nil && !entity.hasMonsterID && entity.hasConfigID && entity.hasConfigType && entity.configType == 1 &&
		entity.sourceMessageID == 18559 && entity.hasSceneInstanceID {
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
		(entity.sourceMessageID == 18559 || entity.sourceMessageID == 25185) {
		sceneInstanceID, hasSceneInstanceID = resolver.sceneInstanceID, true
		sceneContextSource = "current session JoinSceneNotify context"
	}
	if masters != nil && entity.hasConfigID && entity.hasConfigType && entity.configType == 1 &&
		(entity.sourceMessageID == 18559 || entity.sourceMessageID == 25185) && hasSceneInstanceID {
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
	// client Global 3.7.1 Protocol.js defines Proto_Character as enum value 3.
	// SetPbDataByProtocol assigns EntityData.PbDataId to RoleId only in this branch.
	if entity.hasConfigType && entity.configType == 3 && entity.hasConfigID {
		if characterID != 0 && characterID != entity.configID {
			return 0, "", false, true
		}
		if characterID == 0 {
			characterID = entity.configID
			proof = "EntityPb.ConfigId(field 2) + ConfigType(field 3)=Proto_Character(3) -> CreateEntityData.Init.PbDataId -> CreatureDataComponent.SetPbDataByProtocol.SetRoleId"
			fromConfig = true
		}
	}
	return characterID, proof, fromConfig, false
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
	case 25185:
		return "EntityAddNotify.EntityPb.Id"
	case 17286:
		return "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].EntityId"
	case 28481:
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
	r.lifecycles = make(map[uint64]entityLifecycle)
	r.lifecycleSequence = 0
}

func (r *entityResolver) observeActiveRequest(body []byte) error {
	fields, err := parseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse EntityActiveRequest: %w", err)
	}
	for _, field := range fields {
		if field.number != 4 || field.wire != 0 || field.value == 0 {
			continue
		}
		r.sequence++
		entity := r.entities[field.value]
		entity.entityID = field.value
		if entity.sourceMessageID != 25185 {
			entity.sourceMessageID = 28481
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
		if field.number != 6 || field.wire != 2 {
			continue
		}
		entity, err := parseEntityPb(field.data)
		if err != nil {
			return added, err
		}
		if entity.entityID == 0 {
			continue
		}
		r.sequence++
		if previous, ok := r.entities[entity.entityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
		}
		entity.sourceMessageID = 25185
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
		if field.number == 5 && field.wire == 2 {
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
	var instanceID uint64
	var hasInstanceID bool
	for _, field := range sceneFields {
		if field.number == 2 && field.wire == 0 {
			instanceID, hasInstanceID = field.value, field.value != 0
			break
		}
	}

	r.entities = make(map[uint64]runtimeEntity)
	r.sceneInstanceID, r.hasSceneInstanceID = instanceID, hasInstanceID
	snapshotIDs := make([]uint64, 0, len(entities))
	for _, entity := range entities {
		if entity.entityID == 0 {
			continue
		}
		if previous, ok := r.entities[entity.entityID]; ok {
			entity = mergeRuntimeEntity(previous, entity)
		}
		entity.sceneInstanceID, entity.hasSceneInstanceID = instanceID, hasInstanceID
		r.sequence++
		entity.sourceMessageID = 18559
		entity.lastSeenSequence = r.sequence
		r.entities[entity.entityID] = entity
		snapshotIDs = append(snapshotIDs, entity.entityID)
		r.evictIfNeeded()
	}
	r.observeLifecycleSnapshot(snapshotIDs)
	return len(r.entities), nil
}

// applyUpdateSceneGroupDataNotify adds the direct role-to-entity mapping carried
// by Windows Global UHD 3.7.1 UpdateSceneGroupDataNotify. Other entity metadata
// already observed for the same ID is retained, and conflicting RoleIds remain
// unresolved through mergeRuntimeEntity.
func (r *entityResolver) applyUpdateSceneGroupDataNotify(body []byte) (int, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return 0, fmt.Errorf("parse UpdateSceneGroupDataNotify: %w", err)
	}
	updates := make([]runtimeEntity, 0)
	for _, field := range fields {
		if field.number != 2 || field.wire != 2 { // UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[]
			continue
		}
		playerFields, err := parseProtoFields(field.data)
		if err != nil {
			return 0, fmt.Errorf("parse PlayerSceneGroupDataList: %w", err)
		}
		var playerID uint64
		for _, playerField := range playerFields {
			if playerField.number == 1 && playerField.wire == 0 {
				playerID = playerField.value
				break
			}
		}
		for _, playerField := range playerFields {
			if playerField.number != 2 || playerField.wire != 2 { // PlayerSceneGroupDataList.PlayerSceneGroupData[]
				continue
			}
			groupFields, err := parseProtoFields(playerField.data)
			if err != nil {
				return 0, fmt.Errorf("parse PlayerSceneGroupData: %w", err)
			}
			for _, groupField := range groupFields {
				if groupField.number != 2 || groupField.wire != 2 { // PlayerSceneGroupData.FightRoleInfos[]
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
					case 1: // SceneRoleInformation.RoleId
						entity.roleID, entity.hasRoleID = roleField.value, roleField.value != 0
					case 2: // SceneRoleInformation.EntityId
						entity.entityID = roleField.value
					}
				}
				if entity.entityID == 0 || !entity.hasRoleID {
					continue
				}
				entity.sourceMessageID = 17286
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
			if previous.sourceMessageID == 25185 || previous.sourceMessageID == 18559 {
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
		case 4: // SceneInformation.PlayerInfos[]
			roleEntities, err := parseScenePlayerRoleEntities(field.data)
			if err != nil {
				return nil, err
			}
			entities = append(entities, roleEntities...)
		case 5: // SceneInformation.DynamicEntityList[]
			entity, err := parseDynamicEntityInformation(field.data, "JoinSceneNotify.SceneInfo.DynamicEntityList.Id")
			if err != nil {
				return nil, err
			}
			entities = append(entities, entity)
		case 11: // SceneInformation.AoiData
			aoiFields, err := parseProtoFields(field.data)
			if err != nil {
				return nil, fmt.Errorf("parse PlayerSceneAoiData: %w", err)
			}
			for _, aoiField := range aoiFields {
				if aoiField.wire != 2 {
					continue
				}
				switch aoiField.number {
				case 1: // PlayerSceneAoiData.DynamicEntityList[]
					entity, err := parseDynamicEntityInformation(aoiField.data, "JoinSceneNotify.SceneInfo.AoiData.DynamicEntityList.Id")
					if err != nil {
						return nil, err
					}
					entities = append(entities, entity)
				case 3: // PlayerSceneAoiData.Entities[]: EntityPb
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
		if field.number == 1 && field.wire == 0 {
			playerID = field.value
			break
		}
	}
	entities := make([]runtimeEntity, 0)
	for _, field := range fields {
		if field.number != 11 || field.wire != 2 { // ScenePlayerInformation.PlayerSceneGroupData[]
			continue
		}
		groups, err := parseProtoFields(field.data)
		if err != nil {
			return nil, fmt.Errorf("parse PlayerSceneGroupData: %w", err)
		}
		for _, groupField := range groups {
			if groupField.number != 2 || groupField.wire != 2 { // PlayerSceneGroupData.FightRoleInfos[]
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
				case 1: // SceneRoleInformation.RoleId
					entity.roleID, entity.hasRoleID = roleField.value, roleField.value != 0
				case 2: // SceneRoleInformation.EntityId; same client CreatureDataId namespace
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
			case 1:
				entity.entityID = field.value
			case 2:
				entity.entityTypeCode, entity.hasEntityType = field.value, true
			case 3:
				entity.configID, entity.hasConfigID = field.value, true
			case 4:
				entity.playerID, entity.hasPlayerID = field.value, true
			case 5:
				entity.ownerID, entity.hasOwnerID = field.value, true
			}
			continue
		}
		if field.wire != 2 {
			continue
		}
		switch field.number {
		case 31: // DynamicEntityInformation.SummonInfo
			if err := applySummonInfo(&entity, field.data); err != nil {
				return runtimeEntity{}, err
			}
		case 32: // DynamicEntityInformation.ComponentPbs[]
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
		case 1:
			entity.sceneSummonConfigID, entity.hasSceneSummonConfigID = field.value, true
		case 2:
			entity.sceneSummonerID, entity.hasSceneSummonerID = field.value, true
		case 3:
			entity.sceneSummonSkillID, entity.hasSceneSummonSkillID = field.value, true
		}
	}
	return nil
}

func (r *entityResolver) applyEntityRemoveNotify(body []byte) (int, []uint64, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return 0, nil, fmt.Errorf("parse EntityRemoveNotify: %w", err)
	}
	remove := false
	for _, field := range fields {
		if field.number == 4 && field.wire == 0 {
			remove = field.value != 0
		}
	}
	if !remove {
		return 0, nil, nil
	}
	removed := 0
	removedIDs := make([]uint64, 0)
	for _, field := range fields {
		if field.number != 5 || field.wire != 2 {
			continue
		}
		info, err := parseProtoFields(field.data)
		if err != nil {
			return removed, removedIDs, fmt.Errorf("parse EntityRemoveInfo: %w", err)
		}
		for _, value := range info {
			if value.number == 1 && value.wire == 0 && value.value != 0 {
				removedIDs = append(removedIDs, value.value)
				r.observeLifecycleRemove(value.value)
				if _, ok := r.entities[value.value]; ok {
					delete(r.entities, value.value)
					removed++
				}
			}
		}
	}
	return removed, removedIDs, nil
}

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
	update.componentSourceMessageID = 26702
	update.componentSourceFieldPath = "EntityActiveRequest frame token match + field4 EntityId -> EntityActiveResponse.field11 ComponentPbs"
	update = mergeRuntimeEntity(previous, update)
	r.sequence++
	update.lastSeenSequence = r.sequence
	r.entities[entityID] = update
	r.evictIfNeeded()
	return observed, nil
}

func isRuntimeIdentityComponent(componentNumber uint64) bool {
	switch componentNumber {
	case 4, 18, 25, 42, 53, 85:
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
			if field.number == 11 && field.wire == 2 {
				if err := applyEntityComponent(&entity, field.data); err != nil {
					return runtimeEntity{}, err
				}
			}
			continue
		}
		switch field.number {
		case 1: // EntityPb.Id
			entity.entityID = field.value
		case 2: // EntityPb.ConfigId
			entity.configID, entity.hasConfigID = field.value, true
		case 3: // EntityPb.ConfigType
			entity.configType, entity.hasConfigType = field.value, true
		case 4: // EntityPb.EntityType
			entity.entityTypeCode, entity.hasEntityType = field.value, true
		case 10: // EntityPb.PlayerId
			entity.playerID, entity.hasPlayerID = field.value, true
		case 21: // EntityPb.OwnerIncId is not EntityPb.Id or DynamicEntityInformation.OwnerId.
			entity.ownerIncID, entity.hasOwnerIncID = field.value, true
		case 23: // EntityPb.RoleSkinId is a skin reference, not CharacterId.
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
	case 4: // SummonerComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse SummonerComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case 1:
				entity.summonerID, entity.hasSummonerID = field.value, true
			case 2:
				entity.summonConfigID, entity.hasSummonConfigID = field.value, true
			case 3:
				entity.summonSkillID, entity.hasSummonSkillID = field.value, true
			case 4:
				entity.summonerPlayerID, entity.hasSummonerPlayerID = field.value, true
			case 5:
				entity.summonType, entity.hasSummonType = field.value, true
			}
		}
	case 18: // MonsterCaptureComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse MonsterCaptureComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case 1:
				entity.templateID, entity.hasTemplateID = field.value, true
			case 2:
				entity.monsterCaptureEntityID, entity.hasMonsterCaptureEntityID = field.value, true
			case 3:
				entity.monsterID, entity.hasMonsterID = field.value, true
			}
		}
	case 42: // PlayerFollowerSelfComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse PlayerFollowerSelfComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case 1:
				entity.playerEntityID, entity.hasPlayerEntityID = field.value, true
			case 2:
				entity.followerSummonConfigID, entity.hasFollowerSummonConfigID = field.value, true
			}
		}
	case 25: // ConcomitantsComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse ConcomitantsComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.wire != 0 {
				continue
			}
			switch field.number {
			case 1: // VisionEntityIds[]
				if len(entity.concomitantVisionIDs) >= maxRuntimeComponentEntityRefs {
					return fmt.Errorf("ConcomitantsComponentPb.VisionEntityIds exceeds limit %d", maxRuntimeComponentEntityRefs)
				}
				entity.concomitantVisionIDs = append(entity.concomitantVisionIDs, field.value)
			case 2: // CustomEntityIds[]
				if len(entity.concomitantCustomIDs) >= maxRuntimeComponentEntityRefs {
					return fmt.Errorf("ConcomitantsComponentPb.CustomEntityIds exceeds limit %d", maxRuntimeComponentEntityRefs)
				}
				entity.concomitantCustomIDs = append(entity.concomitantCustomIDs, field.value)
			case 3: // PhantomRoleEid
				entity.phantomRoleEntityID, entity.hasPhantomRoleEntityID = field.value, true
			case 4: // WeakVisionEid
				entity.weakVisionEntityID, entity.hasWeakVisionEntityID = field.value, true
			}
		}
	case 53: // PlayerFollowersComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse PlayerFollowersComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.number != 1 || field.wire != 2 {
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
				case 1: // Type
					follower.typeID, follower.hasType = followerField.value, true
				case 2: // EntityId
					follower.entityID, follower.hasEntity = followerField.value, true
				}
			}
			entity.playerFollowers = append(entity.playerFollowers, follower)
		}
	case 85: // FollowEntityComponentPb
		fields, err := parseProtoFields(data)
		if err != nil {
			return fmt.Errorf("parse FollowEntityComponentPb: %w", err)
		}
		for _, field := range fields {
			if field.number == 1 && field.wire == 0 {
				entity.followEntityID, entity.hasFollowEntityID = field.value, true
			}
		}
	}
	return nil
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
	if entity.sourceMessageID == 25185 {
		return entity, resolutionVerified
	}
	if entity.sourceMessageID == 18559 {
		return entity, resolutionVerified
	}
	if entity.sourceMessageID == 17286 {
		return entity, resolutionVerified
	}
	if entity.sourceMessageID == 28481 {
		return entity, resolutionCorrelated
	}
	if entity.sourceMessageID == 26702 {
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
