//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// undecodableReason is the census reason for a frame decodes refused.
func undecodableReason(s *protocolSession) string {
	switch s.state {
	case protocolPending:
		return protocolPendingFailureReason
	case protocolUnsupported:
		return protocolUnsupportedFailureReason
	default:
		return protocolNoHandshakeReason
	}
}

// Census reasons for frames the current protocol session cannot hand to a decoder.
const (
	protocolNoHandshakeReason        = "no handshake in this capture; protocol unknown"
	protocolPendingFailureReason     = "client version not reported yet; protocol unknown"
	protocolUnsupportedFailureReason = "not the current game version; not decoded"
)

// maxTopLevelFieldKeys bounds the per-command field histogram. Random or
// corrupted bodies can produce many field numbers, so further distinct keys
// are counted as overflow instead of growing the census without limit.
const maxTopLevelFieldKeys = 64

// observeTopLevelFields counts field number and wire type pairs for a body
// that is syntactically valid Protobuf at the top level. The keys are wire
// facts only. They do not show that a field has a semantic meaning, and nested
// messages are not inspected here.
func (e *commandCensusEntry) observeTopLevelFields(fields []protoField) {
	for _, field := range fields {
		key := fmt.Sprintf("%d/w%d", field.number, field.wire)
		if e.TopLevelFields == nil {
			e.TopLevelFields = make(map[string]int)
		}
		if _, known := e.TopLevelFields[key]; !known && len(e.TopLevelFields) >= maxTopLevelFieldKeys {
			e.TopLevelFieldOverflow++
			continue
		}
		e.TopLevelFields[key]++
	}
}

// observeKnownBodyWire records top-level wire facts for a decrypted body of an
// implemented command. It reads the same plaintext as the typed decoder and
// must not change the typed decoder's result. The field counts include fields
// the typed decoder already uses, so they are not an unknown-field census by
// themselves.
func (e *commandCensusEntry) ObserveKnownBodyWire(plain []byte) {
	fields, err := parseProtoFields(plain)
	if err != nil {
		e.FailureReasons["implemented body top-level wire parse failed"]++
		return
	}
	e.ImplementedWireBodies++
	e.observeTopLevelFields(fields)
}

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
	// census is the census of the entry, for the inspection of its undecoded frames.
	census *commandCensus
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

// commandCensus is the Development command census (-command-census-out): every command the capture carries, how far
// each frame was decoded, and the framing failures. It implements the core's commandCensusObserver; processor is the
// processor it inspects undecoded frames for.
// crcFailureRecord is a CRC failure the census keeps, linked to the next frame that passed its checks once the parser
// finds one. The JSON of the record is the observation's, then nextValidFrame.
type crcFailureRecord struct {
	crcFailureObservation
	NextValid *frameBoundaryObservation `json:"nextValidFrame,omitempty"`
}

type commandCensus struct {
	processor             *processor
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
	crcFailures           []crcFailureRecord
	crcOmitted            int
	malformedRecords      []malformedObservation
	malformedOmitted      int
}

const maxFrameErrorObservations = 128

// newCommandCensus is an empty census for the frames of p (nil when no undecoded frame is inspected).
func newCommandCensus(p *processor) *commandCensus {
	return &commandCensus{
		processor:          p,
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
			census:         c,
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
func (e *commandCensusEntry) DescribeCurrentProtocol() {
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

func (c *commandCensus) Malformed(reason string, timestamp time.Time, offset int, declaredLength uint16, available int) {
	c.transport["malformed:"+reason]++
	if len(c.malformedRecords) >= maxFrameErrorObservations {
		c.malformedOmitted++
		return
	}
	c.malformedRecords = append(c.malformedRecords, malformedObservation{Reason: reason, Timestamp: timestamp, Offset: offset, DeclaredLength: declaredLength, AvailableBytes: available})
}

func (c *commandCensus) ObserveFrame(frame gameFrame, session int) censusFrameObserver {
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

// ObserveSession records the highest protocol session number seen.
func (c *commandCensus) ObserveSession(session int) {
	if session > c.sessions {
		c.sessions = session
	}
}

// ObserveInnerEntry counts a combat message found inside a container frame.
func (c *commandCensus) ObserveInnerEntry(name string) {
	c.innerEntries[name]++
}

// Fail counts why the frame of an entry was not decoded further.
func (e *commandCensusEntry) Fail(reason string) {
	e.FailureReasons[reason]++
}

// Decrypted counts a frame of the entry whose body decrypted.
func (e *commandCensusEntry) Decrypted() {
	e.DecryptedFrames++
}

// Decoded counts a frame of the entry that a decoder read without error.
func (e *commandCensusEntry) Decoded() {
	e.DecodedFrames++
}

func (c *commandCensus) ObserveCombatNotifyField(fieldNumber uint64) {
	c.combatNotifyFields[fieldNumber]++
}

func (c *commandCensus) FailedCRC(observation crcFailureObservation) int {
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
	c.crcFailures = append(c.crcFailures, crcFailureRecord{crcFailureObservation: observation})
	return len(c.crcFailures) - 1
}

func (c *commandCensus) SetNextValidFrame(index int, next frameBoundaryObservation) {
	if index < 0 || index >= len(c.crcFailures) || c.crcFailures[index].NextValid != nil {
		return
	}
	c.crcFailures[index].NextValid = &next
}

// InspectUndecoded counts what the census can learn about a frame no decoder reads. decodable says whether the
// protocol session reads its numbering (protocolSession.decodes); otherwise only the top-level wire shape is counted.
func (entry *commandCensusEntry) InspectUndecoded(frame gameFrame, decodable bool) {
	p := entry.census.processor
	if len(p.session) != 32 {
		entry.FailureReasons["no implemented schema; session key unavailable"]++
		return
	}
	if len(frame.body) == 0 {
		entry.FailureReasons[emptyBodyFailureReason]++
		return
	}
	plain, err := decryptGameBody(frame.sequence, frame.body, p.session)
	if err != nil {
		entry.FailureReasons["no implemented schema; decrypt failed"]++
		return
	}
	entry.DecryptedFrames++
	if frame.compressed {
		plain, err = decompressBody(plain, frame.compressedLength)
		if err != nil {
			entry.FailureReasons["no implemented schema; decompression failed"]++
			return
		}
	}
	if !decodable {
		if fields, err := parseProtoFields(plain); err == nil {
			entry.GenericProtoValid++
			entry.observeTopLevelFields(fields)
		}
		entry.FailureReasons[undecodableReason(&p.protocol)]++
		return
	}
	if fields, err := parseProtoFields(plain); err == nil {
		entry.GenericProtoValid++
		// The optional external schema describes the current protocol.
		p.archiveSchemaDecode(frame, plain)
		entry.observeTopLevelFields(fields)
		entry.FailureReasons["message schema not implemented"]++
		if frame.commandID == cmdMovePackagePush {
			observation, err := extractMovePackageObservation(plain)
			if err != nil {
				entry.FailureReasons["MovePackagePush schema-field extraction failed"]++
			} else {
				entry.census.observeMovePackage(observation)
			}
		} else if frame.commandID == cmdPlayerFollowerNotify {
			observation, err := extractPlayerFollowerObservation(plain)
			if err != nil {
				entry.FailureReasons["PlayerFollowerNotify schema-field extraction failed"]++
			} else {
				entry.census.observePlayerFollower(observation)
			}
		} else if fieldNumber, ok := entityNotifyKeyField(frame.commandID); ok {
			values, err := extractVarintField(plain, fieldNumber)
			if err != nil {
				entry.FailureReasons["entity notify ID extraction failed"]++
			} else {
				entry.census.observeEntityKey(frame.commandID, fieldNumber, values)
			}
		}
	} else {
		entry.FailureReasons["no implemented schema; generic protobuf parse failed"]++
	}
}

func entityNotifyKeyField(commandID uint16) (uint64, bool) {
	switch commandID {
	case cmdEntityRemoveDynamicInteractNotify:
		return fieldEntityRemoveDynamicInteractNotifyEntityID, true // EntityRemoveDynamicInteractNotify.EntityId
	case cmdEntityCommonTagNotify:
		return fieldEntityCommonTagNotifyID, true // EntityCommonTagNotify.Id
	case cmdEntityStateReadyNotify:
		return fieldEntityStateReadyNotifyEntityID, true // EntityStateReadyNotify.EntityId
	default:
		return 0, false
	}
}

func (c *commandCensus) write(path string, stats captureStats, clientVersion *clientVersionReport, protocol protocolReport) error {
	entries := make([]commandCensusEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		copy := *entry
		if copy.CRCValidFrames == 0 && copy.CRCFailedHeaders > 0 {
			copy.Status = "UNKNOWN DUE TO CRC/CAPTURE LIMITATION"
		} else if copy.CRCValidFrames > 0 && copy.DecodedFrames == copy.CRCValidFrames {
			copy.Status = "PRESENT AND DECODED"
		}
		if len(copy.FailureReasons) == 0 {
			copy.FailureReasons = nil
		}
		entries = append(entries, copy)
	}
	for _, candidate := range []struct {
		id        uint16
		direction string
	}{
		{cmdUpdateSceneGroupDataNotify, "in"}, {cmdPlayerLivingStatesNotify, "in"}, {cmdAddSysBuffNotify, "in"}, {cmdApplyGameplayEffectNotify, "in"}, {cmdApplyGameplayEffectPush, "in"}, {cmdRemoveGameplayEffectNotify, "in"}, {cmdRemoveGameplayEffectPush, "in"}, {cmdSimpleCombatEntityBuffUpdateNotify, "in"}, {cmdSimpleCombatEntityBuffLayerCountNotify, "in"}, {cmdBuffStackCountNotify, "in"}, {cmdBuffDurationNotify, "in"}, {cmdOrderApplyBuffNotify, "in"}, {cmdOrderRemoveBuffNotify, "in"}, {cmdRefreshBuffDurationPush, "in"}, {cmdRemoveSysBuffNotify, "in"}, {cmdSimpleCombatEntityAttributeUpdateNotify, "in"}, {cmdSimpleCombatEntitySyncAttributePush, "in"}, {cmdUpdateSysBuffNotify, "in"}, {cmdJoinSceneNotify, "in"}, {cmdEntityAddNotify, "in"}, {cmdEntityRemoveNotify, "in"}, {cmdEntityActiveRequest, "out"}, {cmdEntityActiveResponse, "in"}, {cmdCombatSendPackRequest, "out"}, {cmdCombatSendPackResponse, "in"},
		{cmdApplyGameplayEffectRequest, "out"}, {cmdApplyGameplayEffectResponse, "in"}, {cmdRemoveGameplayEffectRequest, "out"}, {cmdRemoveGameplayEffectResponse, "in"}, {cmdOrderApplyBuffRequest, "out"}, {cmdOrderApplyBuffResponse, "in"}, {cmdOrderRemoveBuffRequest, "out"}, {cmdOrderRemoveBuffResponse, "in"}, {cmdApplyBuffS2cRequestNotify, "in"}, {cmdApplyBuffS2cResponsePush, "in"}, {cmdRemoveBuffS2cRequestNotify, "in"}, {cmdRemoveBuffS2cResponsePush, "in"}, {cmdRemoveBuffByIDS2cRequestNotify, "in"}, {cmdRemoveBuffByIDS2cResponsePush, "in"}, {cmdActivateBuffRequest, "out"}, {cmdActivateBuffResponse, "in"}, {cmdActivateBuffNotify, "in"}, {cmdActivateBuffPush, "in"},
		{cmdBuffStackCountRequest, "out"}, {cmdBuffStackCountResponse, "in"}, {cmdBuffStackCountPush, "in"}, {cmdBuffEffectRequest, "out"}, {cmdBuffEffectResponse, "in"}, {cmdBuffEffectPush, "in"}, {cmdBuffEffectExecuteRequest, "out"}, {cmdBuffEffectExecuteResponse, "in"}, {cmdBuffEffectExecutePush, "in"}, {cmdEntityBuffProducerRequest, "out"}, {cmdEntityBuffProducerResponse, "in"}, {cmdEntityBuffProducerOperateRequest, "out"}, {cmdEntityBuffProducerOperateResponse, "in"}, {cmdOrderRemoveBuffByTagsRequest, "out"}, {cmdOrderRemoveBuffByTagsResponse, "in"}, {cmdOrderRemoveBuffByTagsNotify, "in"}, {cmdRemoveBuffByServerIDS2cRequestNotify, "in"}, {cmdAtomicProcessAddBuffRequest, "out"}, {cmdAtomicProcessAddBuffResponse, "in"}, {cmdAtomicProcessRemoveBuffRequest, "out"}, {cmdAtomicProcessRemoveBuffResponse, "in"}, {cmdTransformBuffStackNotify, "in"},
		{cmdUpdatePlayerAllFightRoleNotify, "in"}, {cmdUpdatePlayersFightRolesInfoNotify, "in"}, {cmdUpdatePlayerSingleFightRoleNotify, "in"}, {cmdChangePlayerFightRoleNotify, "in"},
		{cmdLoginRequest, "out"}, {cmdCheckClientPush, "out"},
	} {
		key := censusKey{CommandID: candidate.id, Direction: candidate.direction}
		if _, exists := c.entries[key]; !exists {
			entry := commandCensusEntry{CommandID: candidate.id, Name: commandName(candidate.id), Direction: candidate.direction, Status: "NOT OBSERVED"}
			if candidate.id == cmdLoginRequest || candidate.id == cmdCheckClientPush {
				entry.SchemaBasis = "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"
			} else if hasCurrentProtocolSchema(candidate.id) {
				entry.SchemaBasis = censusSchemaBasis()
			}
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CommandID == entries[j].CommandID {
			return entries[i].Direction < entries[j].Direction
		}
		return entries[i].CommandID < entries[j].CommandID
	})
	inner := make(map[string]int, len(c.innerEntries))
	for name, count := range c.innerEntries {
		inner[name] = count
	}
	report := struct {
		GeneratedAt        time.Time              `json:"generatedAt"`
		SessionCount       int                    `json:"sessionCount"`
		ClientVersion      *clientVersionReport   `json:"clientVersion,omitempty"`
		Protocol           protocolReport         `json:"protocol"`
		Transport          map[string]int         `json:"transport"`
		InnerRPCs          map[string]int         `json:"innerRpcCounts"`
		CombatNotifyFields map[uint64]int         `json:"combatNotifyOneofFieldCounts,omitempty"`
		Commands           []commandCensusEntry   `json:"commands"`
		CRCFailures        []crcFailureRecord     `json:"crcFailureObservations,omitempty"`
		CRCOmitted         int                    `json:"crcFailureObservationsOmitted,omitempty"`
		Malformed          []malformedObservation `json:"malformedObservations,omitempty"`
		MalformedOmitted   int                    `json:"malformedObservationsOmitted,omitempty"`
		MoveEntityIDs      []struct {
			EntityID         uint64         `json:"entityId"`
			Count            int            `json:"observations"`
			OriginatorValues map[uint64]int `json:"originatorRawValueCounts,omitempty"`
		} `json:"movePackageEntityIds,omitempty"`
		MoveSceneOwnerIDs []struct {
			EntityID uint64 `json:"sceneOwnerId"`
			Count    int    `json:"observations"`
		} `json:"movePackageSceneOwnerIds,omitempty"`
		PlayerFollowers       []playerFollowerObservation `json:"playerFollowerObservations,omitempty"`
		MoveEntityOmitted     int                         `json:"movePackageEntityIdsOmitted,omitempty"`
		MoveOwnerOmitted      int                         `json:"movePackageSceneOwnerIdsOmitted,omitempty"`
		PlayerFollowerOmitted int                         `json:"playerFollowerObservationsOmitted,omitempty"`
		EntityKeys            []entityKeyObservation      `json:"entityNotifyRawIds,omitempty"`
	}{GeneratedAt: time.Now().UTC(), SessionCount: c.sessions, ClientVersion: clientVersion, Protocol: protocol, Transport: c.transport, InnerRPCs: inner, CombatNotifyFields: c.combatNotifyFields, Commands: entries,
		CRCFailures: c.crcFailures, CRCOmitted: c.crcOmitted, Malformed: c.malformedRecords, MalformedOmitted: c.malformedOmitted}
	report.PlayerFollowers = c.playerFollowers
	report.MoveEntityOmitted = c.moveEntityOmitted
	report.MoveOwnerOmitted = c.moveOwnerOmitted
	report.PlayerFollowerOmitted = c.playerFollowerOmitted
	for key, count := range c.entityKeyValues {
		report.EntityKeys = append(report.EntityKeys, entityKeyObservation{CommandID: key.commandID, FieldNumber: key.field, RawID: key.value, Count: count})
	}
	sort.Slice(report.EntityKeys, func(i, j int) bool {
		if report.EntityKeys[i].CommandID != report.EntityKeys[j].CommandID {
			return report.EntityKeys[i].CommandID < report.EntityKeys[j].CommandID
		}
		if report.EntityKeys[i].FieldNumber != report.EntityKeys[j].FieldNumber {
			return report.EntityKeys[i].FieldNumber < report.EntityKeys[j].FieldNumber
		}
		return report.EntityKeys[i].RawID < report.EntityKeys[j].RawID
	})
	for id, aggregate := range c.moveEntities {
		report.MoveEntityIDs = append(report.MoveEntityIDs, struct {
			EntityID         uint64         `json:"entityId"`
			Count            int            `json:"observations"`
			OriginatorValues map[uint64]int `json:"originatorRawValueCounts,omitempty"`
		}{EntityID: id, Count: aggregate.observations, OriginatorValues: aggregate.originators})
	}
	sort.Slice(report.MoveEntityIDs, func(i, j int) bool { return report.MoveEntityIDs[i].EntityID < report.MoveEntityIDs[j].EntityID })
	for id, count := range c.moveSceneOwners {
		report.MoveSceneOwnerIDs = append(report.MoveSceneOwnerIDs, struct {
			EntityID uint64 `json:"sceneOwnerId"`
			Count    int    `json:"observations"`
		}{EntityID: id, Count: count})
	}
	sort.Slice(report.MoveSceneOwnerIDs, func(i, j int) bool {
		return report.MoveSceneOwnerIDs[i].EntityID < report.MoveSceneOwnerIDs[j].EntityID
	})
	report.Transport["udpPackets"] = stats.udpPackets
	report.Transport["kcpPushSegments"] = stats.kcpPush
	report.Transport["kcpControlSegments"] = stats.kcpControl
	report.Transport["nonKcpUdpDatagrams"] = stats.nonKCPUDP
	report.Transport["invalidKcpDatagrams"] = stats.invalidKCP
	report.Transport["kcpOrderedSegments"] = stats.kcpOrdered
	report.Transport["kcpMessages"] = stats.kcpMessages
	report.Transport["crcValidFrames"] = stats.frames
	report.Transport["badCRCFrames"] = stats.badCRC
	report.Transport["malformedFrames"] = stats.malformed
	report.Transport["kernelDrops"] = stats.kernelDrops
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal command census: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write command census: %w", err)
	}
	return nil
}
