package capture

import (
	"fmt"
	"time"

	"wwlogs/internal/versioninfo"
)

type censusKey struct {
	CommandID uint16 `json:"commandId"`
	Direction string `json:"direction"`
}

type commandCensusEntry struct {
	CommandID uint16 `json:"commandId"`
	// InternalCommandID is the command the decoders read this wire command as, when it differs from the wire number.
	InternalCommandID     uint16         `json:"internalCommandId,omitempty"`
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
	case 103:
		return "LoginRequest"
	case 116:
		return "CheckClientPush"
	case 112:
		return "ProtoKeyResponse"
	case 18559:
		return "JoinSceneNotify"
	case 17286:
		return "UpdateSceneGroupDataNotify"
	case 22492:
		return "PlayerLivingStatesNotify"
	case 21803:
		return "AddSysBuffNotify"
	case 25830:
		return "ApplyGameplayEffectNotify"
	case 29488:
		return "ApplyGameplayEffectPush"
	case 18345:
		return "RemoveGameplayEffectNotify"
	case 20358:
		return "RemoveGameplayEffectPush"
	case 17893:
		return "SimpleCombatEntityBuffUpdateNotify"
	case 22102:
		return "SimpleCombatEntityBuffLayerCountNotify"
	case 29363:
		return "BuffStackCountNotify"
	case 16974:
		return "BuffDurationNotify"
	case 22973:
		return "ApplyGameplayEffectRequest"
	case 22915:
		return "ApplyGameplayEffectResponse"
	case 19884:
		return "RemoveGameplayEffectRequest"
	case 28077:
		return "RemoveGameplayEffectResponse"
	case 21800:
		return "OrderApplyBuffRequest"
	case 27266:
		return "OrderApplyBuffResponse"
	case 29255:
		return "OrderRemoveBuffRequest"
	case 17452:
		return "OrderRemoveBuffResponse"
	case 18641:
		return "ApplyBuffS2cRequestNotify"
	case 16890:
		return "ApplyBuffS2cResponsePush"
	case 20022:
		return "RemoveBuffS2cRequestNotify"
	case 23971:
		return "RemoveBuffS2cResponsePush"
	case 19482:
		return "RemoveBuffByIdS2cRequestNotify"
	case 19162:
		return "RemoveBuffByIdS2cResponsePush"
	case 17149:
		return "ActivateBuffRequest"
	case 19917:
		return "ActivateBuffResponse"
	case 28221:
		return "ActivateBuffNotify"
	case 20926:
		return "ActivateBuffPush"
	case 19820:
		return "BuffStackCountRequest"
	case 26251:
		return "BuffStackCountResponse"
	case 21666:
		return "BuffStackCountPush"
	case 22540:
		return "BuffEffectRequest"
	case 16967:
		return "BuffEffectResponse"
	case 20706:
		return "BuffEffectPush"
	case 26094:
		return "BuffEffectExecuteRequest"
	case 17533:
		return "BuffEffectExecuteResponse"
	case 18099:
		return "BuffEffectExecutePush"
	case 18682:
		return "EntityBuffProducerRequest"
	case 22006:
		return "EntityBuffProducerResponse"
	case 26503:
		return "EntityBuffProducerOperateRequest"
	case 27203:
		return "EntityBuffProducerOperateResponse"
	case 22742:
		return "OrderRemoveBuffByTagsRequest"
	case 16551:
		return "OrderRemoveBuffByTagsResponse"
	case 24520:
		return "OrderRemoveBuffByTagsNotify"
	case 23494:
		return "RemoveBuffByServerIdS2cRequestNotify"
	case 16727:
		return "AtomicProcessAddBuffRequest"
	case 19678:
		return "AtomicProcessAddBuffResponse"
	case 27565:
		return "AtomicProcessRemoveBuffRequest"
	case 23095:
		return "AtomicProcessRemoveBuffResponse"
	case 27991:
		return "TransformBuffStackNotify"
	case 26471:
		return "OrderApplyBuffNotify"
	case 17807:
		return "OrderRemoveBuffNotify"
	case 27178:
		return "RefreshBuffDurationPush"
	case 24831:
		return "RemoveSysBuffNotify"
	case 16764:
		return "SimpleCombatEntityAttributeUpdateNotify"
	case 29113:
		return "SimpleCombatEntitySyncAttributePush"
	case 29655:
		return "UpdateSysBuffNotify"
	case 21051:
		return "UpdatePlayerAllFightRoleNotify"
	case 18899:
		return "UpdatePlayersFightRolesInfoNotify"
	case 18580:
		return "UpdatePlayerSingleFightRoleNotify"
	case 27633:
		return "ChangePlayerFightRoleNotify"
	case 19470:
		return "CombatRequest (outer transport)"
	case 20332:
		return "CombatResponse (outer transport)"
	case 25706:
		return "CombatReceivePackNotify"
	case 22022:
		return "DamageExecute RPC (inner candidate)"
	case 22535:
		return "PhantomAttrRecommendResponse"
	case 19212:
		return "EntityRemoveDynamicInteractNotify"
	case 19566:
		return "EntityCommonTagNotify"
	case 23517:
		return "EntityStateReadyNotify"
	case 24756:
		return "MovePackagePush"
	case 27918:
		return "PlayerFollowerNotify"
	case 25185:
		return "EntityAddNotify (candidate)"
	case 26702:
		return "EntityActiveResponse (candidate)"
	case 28481:
		return "EntityActiveRequest (candidate)"
	case 17769:
		return "EntityRemoveNotify (candidate)"
	default:
		return ""
	}
}

func hasWindowsProtocolSchema(id uint16) bool {
	switch id {
	case 17286, 22492, 21803, 25830, 29488, 18345, 20358, 17893, 22102, 29363, 16974, 26471, 17807, 27178, 24831, 16764, 29113, 29655,
		22973, 22915, 19884, 28077, 21800, 27266, 29255, 17452, 18641, 16890, 20022, 23971, 19482, 19162, 17149, 19917, 28221, 20926,
		19820, 26251, 21666, 22540, 16967, 20706, 26094, 17533, 18099, 18682, 22006, 26503, 27203, 22742, 16551, 24520, 23494,
		16727, 19678, 27565, 23095, 27991, 18559, 25185, 17769, 28481, 21051, 18899, 18580, 27633, 25706:
		return true
	default:
		return false
	}
}

func (c *commandCensus) entry(id uint16, direction string) *commandCensusEntry {
	key := censusKey{CommandID: id, Direction: direction}
	entry := c.entries[key]
	if entry == nil {
		entry = &commandCensusEntry{
			CommandID:      id,
			Name:           commandName(id),
			Direction:      direction,
			Status:         "PRESENT BUT UNDECODED",
			Sessions:       make(map[int]int),
			FailureReasons: make(map[string]int),
		}
		if id == 103 || id == 116 {
			entry.SchemaBasis = "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"
		} else if hasWindowsProtocolSchema(id) {
			entry.SchemaBasis = versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; client version observed, schema compatibility unverified"
		} else if id == 26702 {
			entry.SchemaBasis = versioninfo.ReferenceSchemaLabel() + " static schema; client version observed, cross-platform schema compatibility unverified"
		} else if id == 22535 || id == 24756 || id == 27918 || id == 19212 || id == 19566 || id == 23517 {
			entry.SchemaBasis = versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; client version observed, schema compatibility unverified"
		}
		c.entries[key] = entry
	}
	return entry
}

func extractMovePackageObservation(data []byte) (movePackageObservation, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return movePackageObservation{}, err
	}
	var observation movePackageObservation
	for _, field := range fields {
		if field.number == 4 && field.wire == 0 {
			observation.SceneOwnerIDs = append(observation.SceneOwnerIDs, field.value)
			continue
		}
		if field.number != 12 || field.wire != 2 {
			continue
		}
		movingEntity, err := parseProtoFields(field.data)
		if err != nil {
			return movePackageObservation{}, fmt.Errorf("parse MovePackagePush.MovingEntities: %w", err)
		}
		var entity movePackageEntityObservation
		hasEntityID := false
		for _, nested := range movingEntity {
			if nested.number == 1 && nested.wire == 0 {
				entity.EntityID = nested.value
				hasEntityID = true
			} else if nested.number == 2 && nested.wire == 0 {
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
		if field.number == 13 && field.wire == 0 {
			observation.PlayerID = field.value
			observation.HasPlayerID = true
			continue
		}
		if field.number != 10 || field.wire != 2 {
			continue
		}
		nestedFields, err := parseProtoFields(field.data)
		if err != nil {
			return playerFollowerObservation{}, fmt.Errorf("parse PlayerFollowerNotify.ListPlayerFollowerPb: %w", err)
		}
		var follower playerFollowerEntity
		for _, nested := range nestedFields {
			if nested.number == 1 && nested.wire == 0 {
				follower.Type = nested.value
				follower.HasType = true
			} else if nested.number == 2 && nested.wire == 0 {
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

// observeInternal records that the protocol session reads this wire command as an internal command. The entry keeps the
// wire number; its name follows the internal command.
func (entry *commandCensusEntry) observeInternal(internal uint16) {
	if entry.InternalCommandID == internal {
		return
	}
	entry.InternalCommandID = internal
	if name := commandName(internal); name != "" && entry.Name == "" {
		entry.Name = name
	}
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
