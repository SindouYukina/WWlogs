package capture

import (
	"fmt"
	"time"
)

type censusKey struct {
	CommandID uint16 `json:"commandId"`
	Direction string `json:"direction"`
}

type commandCensusEntry struct {
	CommandID             uint16         `json:"commandId"`
	Name                  string         `json:"candidateName,omitempty"`
	Direction             string         `json:"direction"`
	CRCValidFrames        int            `json:"crcValidFrames"`
	CRCFailedHeaders      int            `json:"crcFailedHeaderIds"`
	DecryptedFrames       int            `json:"decryptedFrames"`
	GenericProtoValid     int            `json:"genericProtobufSyntacticallyValid"`
	DecodedFrames         int            `json:"schemaDecodedFrames"`
	Status                string         `json:"status"`
	SchemaBasis           string         `json:"schemaBasis,omitempty"`
	FirstSeen             time.Time      `json:"firstSeen"`
	LastSeen              time.Time      `json:"lastSeen"`
	Sessions              map[int]int    `json:"framesBySession"`
	FailureReasons        map[string]int `json:"failureReasons,omitempty"`
	TopLevelFields        map[string]int `json:"topLevelFieldCounts,omitempty"`
	TopLevelFieldOverflow int            `json:"topLevelFieldOverflow,omitempty"`
	ImplementedWireBodies int            `json:"implementedWireBodies,omitempty"`
}

type crcFailureObservation struct {
	Direction      string                    `json:"direction"`
	Timestamp      time.Time                 `json:"timestamp"`
	Offset         int                       `json:"applicationOffset"`
	MessageType    byte                      `json:"messageType"`
	Sequence       uint32                    `json:"sequence"`
	Token          uint16                    `json:"token,omitempty"`
	CommandID      uint16                    `json:"commandId"`
	DeclaredLength uint16                    `json:"declaredLength"`
	FrameLength    int                       `json:"frameLength"`
	BodyLength     int                       `json:"bodyLength"`
	ReceivedCRC    uint32                    `json:"receivedCRC32"`
	ComputedCRC    uint32                    `json:"computedCRC32"`
	RemainingBytes int                       `json:"remainingApplicationBytes"`
	PreviousValid  *frameBoundaryObservation `json:"previousValidFrame,omitempty"`
	NextValid      *frameBoundaryObservation `json:"nextValidFrame,omitempty"`
}

type frameBoundaryObservation struct {
	Offset      int    `json:"applicationOffset"`
	MessageType byte   `json:"messageType"`
	Sequence    uint32 `json:"sequence"`
	Token       uint16 `json:"token,omitempty"`
	CommandID   uint16 `json:"commandId"`
	FrameLength int    `json:"frameLength"`
}

type movePackageEntityObservation struct {
	EntityID        uint64
	HasOriginator   bool
	OriginatorValue uint64
}

type movePackageObservation struct {
	Entities      []movePackageEntityObservation
	SceneOwnerIDs []uint64
}

type playerFollowerEntity struct {
	Type        uint64 `json:"typeRawValue"`
	HasType     bool   `json:"hasType"`
	EntityID    uint64 `json:"entityId"`
	HasEntityID bool   `json:"hasEntityId"`
}

type playerFollowerObservation struct {
	PlayerID    uint64                 `json:"playerId"`
	HasPlayerID bool                   `json:"hasPlayerId"`
	Followers   []playerFollowerEntity `json:"followers"`
}

type entityKeyCensusKey struct {
	commandID uint16
	field     uint64
	value     uint64
}

type entityKeyObservation struct {
	CommandID   uint16 `json:"commandId"`
	FieldNumber uint64 `json:"fieldNumber"`
	RawID       uint64 `json:"rawId"`
	Count       int    `json:"observations"`
}

type moveEntityAggregate struct {
	observations int
	originators  map[uint64]int
}

type malformedObservation struct {
	Reason         string    `json:"reason"`
	Timestamp      time.Time `json:"timestamp"`
	Offset         int       `json:"applicationOffset"`
	DeclaredLength uint16    `json:"declaredLength"`
	AvailableBytes int       `json:"availableApplicationBytes"`
}

type commandCensus struct {
	entries               map[censusKey]*commandCensusEntry
	innerEntries          map[string]int
	combatNotifyFields    map[uint64]int
	transport             map[string]int
	moveEntities          map[uint64]*moveEntityAggregate
	moveSceneOwners       map[uint64]int
	playerFollowers       []playerFollowerObservation
	entityKeyValues       map[entityKeyCensusKey]int
	moveEntityOmitted     int
	moveOwnerOmitted      int
	playerFollowerOmitted int
	sessions              int
	crcFailures           []crcFailureObservation
	crcOmitted            int
	malformedRecords      []malformedObservation
	malformedOmitted      int
}

const maxFrameErrorObservations = 128

func newCommandCensus() *commandCensus {
	return &commandCensus{
		entries:            make(map[censusKey]*commandCensusEntry),
		innerEntries:       make(map[string]int),
		combatNotifyFields: make(map[uint64]int),
		transport:          make(map[string]int),
		moveEntities:       make(map[uint64]*moveEntityAggregate),
		moveSceneOwners:    make(map[uint64]int),
		entityKeyValues:    make(map[entityKeyCensusKey]int),
	}
}

func commandName(id uint16) string {
	switch id {
	case cmdLoginRequest:
		return "LoginRequest"
	case cmdCheckClientPush:
		return "CheckClientPush"
	case cmdProtoKeyResponse:
		return "ProtoKeyResponse"
	case cmdJoinSceneNotify:
		return "JoinSceneNotify"
	case cmdUpdateSceneGroupDataNotify:
		return "UpdateSceneGroupDataNotify"
	case cmdPlayerLivingStatesNotify:
		return "PlayerLivingStatesNotify"
	case cmdAddSysBuffNotify:
		return "AddSysBuffNotify"
	case cmdApplyGameplayEffectNotify:
		return "ApplyGameplayEffectNotify"
	case cmdApplyGameplayEffectPush:
		return "ApplyGameplayEffectPush"
	case cmdRemoveGameplayEffectNotify:
		return "RemoveGameplayEffectNotify"
	case cmdRemoveGameplayEffectPush:
		return "RemoveGameplayEffectPush"
	case cmdSimpleCombatEntityBuffUpdateNotify:
		return "SimpleCombatEntityBuffUpdateNotify"
	case cmdSimpleCombatEntityBuffLayerCountNotify:
		return "SimpleCombatEntityBuffLayerCountNotify"
	case cmdBuffStackCountNotify:
		return "BuffStackCountNotify"
	case cmdBuffDurationNotify:
		return "BuffDurationNotify"
	case cmdApplyGameplayEffectRequest:
		return "ApplyGameplayEffectRequest"
	case cmdApplyGameplayEffectResponse:
		return "ApplyGameplayEffectResponse"
	case cmdRemoveGameplayEffectRequest:
		return "RemoveGameplayEffectRequest"
	case cmdRemoveGameplayEffectResponse:
		return "RemoveGameplayEffectResponse"
	case cmdOrderApplyBuffRequest:
		return "OrderApplyBuffRequest"
	case cmdOrderApplyBuffResponse:
		return "OrderApplyBuffResponse"
	case cmdOrderRemoveBuffRequest:
		return "OrderRemoveBuffRequest"
	case cmdOrderRemoveBuffResponse:
		return "OrderRemoveBuffResponse"
	case cmdApplyBuffS2cRequestNotify:
		return "ApplyBuffS2cRequestNotify"
	case cmdApplyBuffS2cResponsePush:
		return "ApplyBuffS2cResponsePush"
	case cmdRemoveBuffS2cRequestNotify:
		return "RemoveBuffS2cRequestNotify"
	case cmdRemoveBuffS2cResponsePush:
		return "RemoveBuffS2cResponsePush"
	case cmdRemoveBuffByIDS2cRequestNotify:
		return "RemoveBuffByIdS2cRequestNotify"
	case cmdRemoveBuffByIDS2cResponsePush:
		return "RemoveBuffByIdS2cResponsePush"
	case cmdActivateBuffRequest:
		return "ActivateBuffRequest"
	case cmdActivateBuffResponse:
		return "ActivateBuffResponse"
	case cmdActivateBuffNotify:
		return "ActivateBuffNotify"
	case cmdActivateBuffPush:
		return "ActivateBuffPush"
	case cmdBuffStackCountRequest:
		return "BuffStackCountRequest"
	case cmdBuffStackCountResponse:
		return "BuffStackCountResponse"
	case cmdBuffStackCountPush:
		return "BuffStackCountPush"
	case cmdBuffEffectRequest:
		return "BuffEffectRequest"
	case cmdBuffEffectResponse:
		return "BuffEffectResponse"
	case cmdBuffEffectPush:
		return "BuffEffectPush"
	case cmdBuffEffectExecuteRequest:
		return "BuffEffectExecuteRequest"
	case cmdBuffEffectExecuteResponse:
		return "BuffEffectExecuteResponse"
	case cmdBuffEffectExecutePush:
		return "BuffEffectExecutePush"
	case cmdEntityBuffProducerRequest:
		return "EntityBuffProducerRequest"
	case cmdEntityBuffProducerResponse:
		return "EntityBuffProducerResponse"
	case cmdEntityBuffProducerOperateRequest:
		return "EntityBuffProducerOperateRequest"
	case cmdEntityBuffProducerOperateResponse:
		return "EntityBuffProducerOperateResponse"
	case cmdOrderRemoveBuffByTagsRequest:
		return "OrderRemoveBuffByTagsRequest"
	case cmdOrderRemoveBuffByTagsResponse:
		return "OrderRemoveBuffByTagsResponse"
	case cmdOrderRemoveBuffByTagsNotify:
		return "OrderRemoveBuffByTagsNotify"
	case cmdRemoveBuffByServerIDS2cRequestNotify:
		return "RemoveBuffByServerIdS2cRequestNotify"
	case cmdAtomicProcessAddBuffRequest:
		return "AtomicProcessAddBuffRequest"
	case cmdAtomicProcessAddBuffResponse:
		return "AtomicProcessAddBuffResponse"
	case cmdAtomicProcessRemoveBuffRequest:
		return "AtomicProcessRemoveBuffRequest"
	case cmdAtomicProcessRemoveBuffResponse:
		return "AtomicProcessRemoveBuffResponse"
	case cmdTransformBuffStackNotify:
		return "TransformBuffStackNotify"
	case cmdOrderApplyBuffNotify:
		return "OrderApplyBuffNotify"
	case cmdOrderRemoveBuffNotify:
		return "OrderRemoveBuffNotify"
	case cmdRefreshBuffDurationPush:
		return "RefreshBuffDurationPush"
	case cmdRemoveSysBuffNotify:
		return "RemoveSysBuffNotify"
	case cmdSimpleCombatEntityAttributeUpdateNotify:
		return "SimpleCombatEntityAttributeUpdateNotify"
	case cmdSimpleCombatEntitySyncAttributePush:
		return "SimpleCombatEntitySyncAttributePush"
	case cmdUpdateSysBuffNotify:
		return "UpdateSysBuffNotify"
	case cmdUpdatePlayerAllFightRoleNotify:
		return "UpdatePlayerAllFightRoleNotify"
	case cmdUpdatePlayersFightRolesInfoNotify:
		return "UpdatePlayersFightRolesInfoNotify"
	case cmdUpdatePlayerSingleFightRoleNotify:
		return "UpdatePlayerSingleFightRoleNotify"
	case cmdChangePlayerFightRoleNotify:
		return "ChangePlayerFightRoleNotify"
	case cmdCombatSendPackRequest:
		return "CombatRequest (outer transport)"
	case cmdCombatSendPackResponse:
		return "CombatResponse (outer transport)"
	case cmdCombatReceivePackNotify:
		return "CombatReceivePackNotify"
	case cmdDamageExecuteRequest:
		return "DamageExecute RPC (inner candidate)"
	case cmdPhantomAttrRecommendResponse:
		return "PhantomAttrRecommendResponse"
	case cmdEntityRemoveDynamicInteractNotify:
		return "EntityRemoveDynamicInteractNotify"
	case cmdEntityCommonTagNotify:
		return "EntityCommonTagNotify"
	case cmdEntityStateReadyNotify:
		return "EntityStateReadyNotify"
	case cmdMovePackagePush:
		return "MovePackagePush"
	case cmdPlayerFollowerNotify:
		return "PlayerFollowerNotify"
	case cmdEntityAddNotify:
		return "EntityAddNotify (candidate)"
	case cmdEntityActiveResponse:
		return "EntityActiveResponse (candidate)"
	case cmdEntityActiveRequest:
		return "EntityActiveRequest (candidate)"
	case cmdEntityRemoveNotify:
		return "EntityRemoveNotify (candidate)"
	default:
		return ""
	}
}

// hasCurrentProtocolSchema reports the commands whose census entries record the current client protocol as their
// schema basis.
func hasCurrentProtocolSchema(id uint16) bool {
	switch id {
	case cmdUpdateSceneGroupDataNotify, cmdPlayerLivingStatesNotify, cmdAddSysBuffNotify, cmdApplyGameplayEffectNotify, cmdApplyGameplayEffectPush, cmdRemoveGameplayEffectNotify, cmdRemoveGameplayEffectPush, cmdSimpleCombatEntityBuffUpdateNotify, cmdSimpleCombatEntityBuffLayerCountNotify, cmdBuffStackCountNotify, cmdBuffDurationNotify, cmdOrderApplyBuffNotify, cmdOrderRemoveBuffNotify, cmdRefreshBuffDurationPush, cmdRemoveSysBuffNotify, cmdSimpleCombatEntityAttributeUpdateNotify, cmdSimpleCombatEntitySyncAttributePush, cmdUpdateSysBuffNotify,
		cmdApplyGameplayEffectRequest, cmdApplyGameplayEffectResponse, cmdRemoveGameplayEffectRequest, cmdRemoveGameplayEffectResponse, cmdOrderApplyBuffRequest, cmdOrderApplyBuffResponse, cmdOrderRemoveBuffRequest, cmdOrderRemoveBuffResponse, cmdApplyBuffS2cRequestNotify, cmdApplyBuffS2cResponsePush, cmdRemoveBuffS2cRequestNotify, cmdRemoveBuffS2cResponsePush, cmdRemoveBuffByIDS2cRequestNotify, cmdRemoveBuffByIDS2cResponsePush, cmdActivateBuffRequest, cmdActivateBuffResponse, cmdActivateBuffNotify, cmdActivateBuffPush,
		cmdBuffStackCountRequest, cmdBuffStackCountResponse, cmdBuffStackCountPush, cmdBuffEffectRequest, cmdBuffEffectResponse, cmdBuffEffectPush, cmdBuffEffectExecuteRequest, cmdBuffEffectExecuteResponse, cmdBuffEffectExecutePush, cmdEntityBuffProducerRequest, cmdEntityBuffProducerResponse, cmdEntityBuffProducerOperateRequest, cmdEntityBuffProducerOperateResponse, cmdOrderRemoveBuffByTagsRequest, cmdOrderRemoveBuffByTagsResponse, cmdOrderRemoveBuffByTagsNotify, cmdRemoveBuffByServerIDS2cRequestNotify,
		cmdAtomicProcessAddBuffRequest, cmdAtomicProcessAddBuffResponse, cmdAtomicProcessRemoveBuffRequest, cmdAtomicProcessRemoveBuffResponse, cmdTransformBuffStackNotify, cmdJoinSceneNotify, cmdEntityAddNotify, cmdEntityRemoveNotify, cmdEntityActiveRequest, cmdUpdatePlayerAllFightRoleNotify, cmdUpdatePlayersFightRolesInfoNotify, cmdUpdatePlayerSingleFightRoleNotify, cmdChangePlayerFightRoleNotify, cmdCombatReceivePackNotify,
		cmdEntityActiveResponse, cmdPhantomAttrRecommendResponse, cmdMovePackagePush, cmdPlayerFollowerNotify, cmdEntityRemoveDynamicInteractNotify, cmdEntityCommonTagNotify, cmdEntityStateReadyNotify:
		return true
	default:
		return false
	}
}

// censusSchemaBasis is the schema basis of a census entry named from the current client protocol.
func censusSchemaBasis() string {
	return "command defined by the " + CurrentProtocol()
}

func (c *commandCensus) entry(id uint16, direction string) *commandCensusEntry {
	key := censusKey{CommandID: id, Direction: direction}
	entry := c.entries[key]
	if entry == nil {
		entry = &commandCensusEntry{
			CommandID:      id,
			Direction:      direction,
			Status:         "PRESENT BUT UNDECODED",
			Sessions:       make(map[int]int),
			FailureReasons: make(map[string]int),
		}
		switch id {
		case cmdLoginRequest, cmdCheckClientPush:
			entry.Name = commandName(id)
			entry.SchemaBasis = "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"
		case cmdProtoKeyResponse:
			entry.Name = commandName(id)
		}
		c.entries[key] = entry
	}
	return entry
}

// describeCurrentProtocol names an entry, and records its schema basis, from the current client protocol. The
// processor calls it only for a frame the current protocol session decodes, so a frame of another game version, or one
// sent before its session reported a version, never takes a current-protocol name. The handshake and the two version
// reports are read in every protocol session, so their entries are named when they are created.
func (e *commandCensusEntry) describeCurrentProtocol() {
	if e.Name == "" {
		e.Name = commandName(e.CommandID)
	}
	if e.SchemaBasis == "" && hasCurrentProtocolSchema(e.CommandID) {
		e.SchemaBasis = censusSchemaBasis()
	}
}

func extractMovePackageObservation(data []byte) (movePackageObservation, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return movePackageObservation{}, err
	}
	var observation movePackageObservation
	for _, field := range fields {
		if field.number == fieldMovePackagePushSceneOwnerID && field.wire == 0 {
			observation.SceneOwnerIDs = append(observation.SceneOwnerIDs, field.value)
			continue
		}
		if field.number != fieldMovePackagePushMovingEntities || field.wire != 2 {
			continue
		}
		movingEntity, err := parseProtoFields(field.data)
		if err != nil {
			return movePackageObservation{}, fmt.Errorf("parse MovePackagePush.MovingEntities: %w", err)
		}
		var entity movePackageEntityObservation
		hasEntityID := false
		for _, nested := range movingEntity {
			if nested.number == fieldMovingEntityDataEntityID && nested.wire == 0 {
				entity.EntityID = nested.value
				hasEntityID = true
			} else if nested.number == fieldMovingEntityDataOriginator && nested.wire == 0 {
				entity.OriginatorValue = nested.value
				entity.HasOriginator = true
			}
		}
		if hasEntityID {
			observation.Entities = append(observation.Entities, entity)
		}
	}
	return observation, nil
}

func (c *commandCensus) observeMovePackage(observation movePackageObservation) {
	for _, entity := range observation.Entities {
		aggregate := c.moveEntities[entity.EntityID]
		if aggregate == nil {
			if len(c.moveEntities) >= maxFrameErrorObservations*32 {
				c.moveEntityOmitted++
				continue
			}
			aggregate = &moveEntityAggregate{originators: make(map[uint64]int)}
			c.moveEntities[entity.EntityID] = aggregate
		}
		aggregate.observations++
		if entity.HasOriginator {
			aggregate.originators[entity.OriginatorValue]++
		}
	}
	for _, id := range observation.SceneOwnerIDs {
		if _, exists := c.moveSceneOwners[id]; !exists && len(c.moveSceneOwners) >= maxFrameErrorObservations {
			c.moveOwnerOmitted++
			continue
		}
		c.moveSceneOwners[id]++
	}
}

func extractPlayerFollowerObservation(data []byte) (playerFollowerObservation, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return playerFollowerObservation{}, err
	}
	var observation playerFollowerObservation
	for _, field := range fields {
		if field.number == fieldPlayerFollowerNotifyPlayerID && field.wire == 0 {
			observation.PlayerID = field.value
			observation.HasPlayerID = true
			continue
		}
		if field.number != fieldPlayerFollowerNotifyListPlayerFollowerPb || field.wire != 2 {
			continue
		}
		nestedFields, err := parseProtoFields(field.data)
		if err != nil {
			return playerFollowerObservation{}, fmt.Errorf("parse PlayerFollowerNotify.ListPlayerFollowerPb: %w", err)
		}
		var follower playerFollowerEntity
		for _, nested := range nestedFields {
			if nested.number == fieldPlayerFollowerPbType && nested.wire == 0 {
				follower.Type = nested.value
				follower.HasType = true
			} else if nested.number == fieldPlayerFollowerPbEntityID && nested.wire == 0 {
				follower.EntityID = nested.value
				follower.HasEntityID = true
			}
		}
		observation.Followers = append(observation.Followers, follower)
	}
	return observation, nil
}

func (c *commandCensus) observePlayerFollower(observation playerFollowerObservation) {
	if len(c.playerFollowers) >= maxFrameErrorObservations {
		c.playerFollowerOmitted++
		return
	}
	c.playerFollowers = append(c.playerFollowers, observation)
}

func extractVarintField(data []byte, fieldNumber uint64) ([]uint64, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return nil, err
	}
	var values []uint64
	for _, field := range fields {
		if field.number == fieldNumber && field.wire == 0 {
			values = append(values, field.value)
		}
	}
	return values, nil
}

func (c *commandCensus) observeEntityKey(commandID uint16, fieldNumber uint64, values []uint64) {
	for _, value := range values {
		key := entityKeyCensusKey{commandID: commandID, field: fieldNumber, value: value}
		if _, exists := c.entityKeyValues[key]; !exists && len(c.entityKeyValues) >= maxFrameErrorObservations*32 {
			continue
		}
		c.entityKeyValues[key]++
	}
}

func (c *commandCensus) malformed(reason string, timestamp time.Time, offset int, declaredLength uint16, available int) {
	c.transport["malformed:"+reason]++
	if len(c.malformedRecords) >= maxFrameErrorObservations {
		c.malformedOmitted++
		return
	}
	c.malformedRecords = append(c.malformedRecords, malformedObservation{Reason: reason, Timestamp: timestamp, Offset: offset, DeclaredLength: declaredLength, AvailableBytes: available})
}

func (c *commandCensus) observeFrame(frame gameFrame, session int) *commandCensusEntry {
	entry := c.entry(frame.commandID, frame.direction)
	entry.CRCValidFrames++
	entry.Sessions[session]++
	if entry.FirstSeen.IsZero() || frame.timestamp.Before(entry.FirstSeen) {
		entry.FirstSeen = frame.timestamp
	}
	if entry.LastSeen.IsZero() || frame.timestamp.After(entry.LastSeen) {
		entry.LastSeen = frame.timestamp
	}
	return entry
}

func (c *commandCensus) observeCombatNotifyField(fieldNumber uint64) {
	c.combatNotifyFields[fieldNumber]++
}

func (c *commandCensus) failedCRC(observation crcFailureObservation) int {
	direction, id, timestamp := observation.Direction, observation.CommandID, observation.Timestamp
	entry := c.entry(id, direction)
	entry.CRCFailedHeaders++
	if entry.FirstSeen.IsZero() || timestamp.Before(entry.FirstSeen) {
		entry.FirstSeen = timestamp
	}
	if entry.LastSeen.IsZero() || timestamp.After(entry.LastSeen) {
		entry.LastSeen = timestamp
	}
	if len(c.crcFailures) >= maxFrameErrorObservations {
		c.crcOmitted++
		return -1
	}
	c.crcFailures = append(c.crcFailures, observation)
	return len(c.crcFailures) - 1
}

func (c *commandCensus) setNextValidFrame(index int, next frameBoundaryObservation) {
	if index < 0 || index >= len(c.crcFailures) || c.crcFailures[index].NextValid != nil {
		return
	}
	c.crcFailures[index].NextValid = &next
}
