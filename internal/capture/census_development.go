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
func undecodableReason(s *ProtocolSession) string {
	switch s.State {
	case ProtocolPending:
		return protocolPendingFailureReason
	case ProtocolUnsupported:
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
func (e *commandCensusEntry) observeTopLevelFields(fields []ProtoField) {
	for _, field := range fields {
		key := fmt.Sprintf("%d/w%d", field.Number, field.Wire)
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
	fields, err := ParseProtoFields(plain)
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
	CRCFailureObservation
	NextValid *FrameBoundaryObservation `json:"nextValidFrame,omitempty"`
}

type commandCensus struct {
	processor             *Processor
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
func newCommandCensus(p *Processor) *commandCensus {
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
	case CmdLoginRequest:
		return "LoginRequest"
	case CmdCheckClientPush:
		return "CheckClientPush"
	case CmdProtoKeyResponse:
		return "ProtoKeyResponse"
	case CmdJoinSceneNotify:
		return "JoinSceneNotify"
	case CmdUpdateSceneGroupDataNotify:
		return "UpdateSceneGroupDataNotify"
	case CmdPlayerLivingStatesNotify:
		return "PlayerLivingStatesNotify"
	case CmdAddSysBuffNotify:
		return "AddSysBuffNotify"
	case CmdApplyGameplayEffectNotify:
		return "ApplyGameplayEffectNotify"
	case CmdApplyGameplayEffectPush:
		return "ApplyGameplayEffectPush"
	case CmdRemoveGameplayEffectNotify:
		return "RemoveGameplayEffectNotify"
	case CmdRemoveGameplayEffectPush:
		return "RemoveGameplayEffectPush"
	case CmdSimpleCombatEntityBuffUpdateNotify:
		return "SimpleCombatEntityBuffUpdateNotify"
	case CmdSimpleCombatEntityBuffLayerCountNotify:
		return "SimpleCombatEntityBuffLayerCountNotify"
	case CmdBuffStackCountNotify:
		return "BuffStackCountNotify"
	case CmdBuffDurationNotify:
		return "BuffDurationNotify"
	case CmdApplyGameplayEffectRequest:
		return "ApplyGameplayEffectRequest"
	case CmdApplyGameplayEffectResponse:
		return "ApplyGameplayEffectResponse"
	case CmdRemoveGameplayEffectRequest:
		return "RemoveGameplayEffectRequest"
	case CmdRemoveGameplayEffectResponse:
		return "RemoveGameplayEffectResponse"
	case CmdOrderApplyBuffRequest:
		return "OrderApplyBuffRequest"
	case CmdOrderApplyBuffResponse:
		return "OrderApplyBuffResponse"
	case CmdOrderRemoveBuffRequest:
		return "OrderRemoveBuffRequest"
	case CmdOrderRemoveBuffResponse:
		return "OrderRemoveBuffResponse"
	case CmdApplyBuffS2cRequestNotify:
		return "ApplyBuffS2cRequestNotify"
	case CmdApplyBuffS2cResponsePush:
		return "ApplyBuffS2cResponsePush"
	case CmdRemoveBuffS2cRequestNotify:
		return "RemoveBuffS2cRequestNotify"
	case CmdRemoveBuffS2cResponsePush:
		return "RemoveBuffS2cResponsePush"
	case CmdRemoveBuffByIDS2cRequestNotify:
		return "RemoveBuffByIdS2cRequestNotify"
	case CmdRemoveBuffByIDS2cResponsePush:
		return "RemoveBuffByIdS2cResponsePush"
	case CmdActivateBuffRequest:
		return "ActivateBuffRequest"
	case CmdActivateBuffResponse:
		return "ActivateBuffResponse"
	case CmdActivateBuffNotify:
		return "ActivateBuffNotify"
	case CmdActivateBuffPush:
		return "ActivateBuffPush"
	case CmdBuffStackCountRequest:
		return "BuffStackCountRequest"
	case CmdBuffStackCountResponse:
		return "BuffStackCountResponse"
	case CmdBuffStackCountPush:
		return "BuffStackCountPush"
	case CmdBuffEffectRequest:
		return "BuffEffectRequest"
	case CmdBuffEffectResponse:
		return "BuffEffectResponse"
	case CmdBuffEffectPush:
		return "BuffEffectPush"
	case CmdBuffEffectExecuteRequest:
		return "BuffEffectExecuteRequest"
	case CmdBuffEffectExecuteResponse:
		return "BuffEffectExecuteResponse"
	case CmdBuffEffectExecutePush:
		return "BuffEffectExecutePush"
	case CmdEntityBuffProducerRequest:
		return "EntityBuffProducerRequest"
	case CmdEntityBuffProducerResponse:
		return "EntityBuffProducerResponse"
	case CmdEntityBuffProducerOperateRequest:
		return "EntityBuffProducerOperateRequest"
	case CmdEntityBuffProducerOperateResponse:
		return "EntityBuffProducerOperateResponse"
	case CmdOrderRemoveBuffByTagsRequest:
		return "OrderRemoveBuffByTagsRequest"
	case CmdOrderRemoveBuffByTagsResponse:
		return "OrderRemoveBuffByTagsResponse"
	case CmdOrderRemoveBuffByTagsNotify:
		return "OrderRemoveBuffByTagsNotify"
	case CmdRemoveBuffByServerIDS2cRequestNotify:
		return "RemoveBuffByServerIdS2cRequestNotify"
	case CmdAtomicProcessAddBuffRequest:
		return "AtomicProcessAddBuffRequest"
	case CmdAtomicProcessAddBuffResponse:
		return "AtomicProcessAddBuffResponse"
	case CmdAtomicProcessRemoveBuffRequest:
		return "AtomicProcessRemoveBuffRequest"
	case CmdAtomicProcessRemoveBuffResponse:
		return "AtomicProcessRemoveBuffResponse"
	case CmdTransformBuffStackNotify:
		return "TransformBuffStackNotify"
	case CmdOrderApplyBuffNotify:
		return "OrderApplyBuffNotify"
	case CmdOrderRemoveBuffNotify:
		return "OrderRemoveBuffNotify"
	case CmdRefreshBuffDurationPush:
		return "RefreshBuffDurationPush"
	case CmdRemoveSysBuffNotify:
		return "RemoveSysBuffNotify"
	case CmdSimpleCombatEntityAttributeUpdateNotify:
		return "SimpleCombatEntityAttributeUpdateNotify"
	case CmdSimpleCombatEntitySyncAttributePush:
		return "SimpleCombatEntitySyncAttributePush"
	case CmdUpdateSysBuffNotify:
		return "UpdateSysBuffNotify"
	case CmdUpdatePlayerAllFightRoleNotify:
		return "UpdatePlayerAllFightRoleNotify"
	case CmdUpdatePlayersFightRolesInfoNotify:
		return "UpdatePlayersFightRolesInfoNotify"
	case CmdUpdatePlayerSingleFightRoleNotify:
		return "UpdatePlayerSingleFightRoleNotify"
	case CmdChangePlayerFightRoleNotify:
		return "ChangePlayerFightRoleNotify"
	case CmdCombatSendPackRequest:
		return "CombatRequest (outer transport)"
	case CmdCombatSendPackResponse:
		return "CombatResponse (outer transport)"
	case CmdCombatReceivePackNotify:
		return "CombatReceivePackNotify"
	case CmdDamageExecuteRequest:
		return "DamageExecute RPC (inner candidate)"
	case CmdPhantomAttrRecommendResponse:
		return "PhantomAttrRecommendResponse"
	case CmdEntityRemoveDynamicInteractNotify:
		return "EntityRemoveDynamicInteractNotify"
	case CmdEntityCommonTagNotify:
		return "EntityCommonTagNotify"
	case CmdEntityStateReadyNotify:
		return "EntityStateReadyNotify"
	case CmdMovePackagePush:
		return "MovePackagePush"
	case CmdPlayerFollowerNotify:
		return "PlayerFollowerNotify"
	case CmdEntityAddNotify:
		return "EntityAddNotify (candidate)"
	case CmdEntityActiveResponse:
		return "EntityActiveResponse (candidate)"
	case CmdEntityActiveRequest:
		return "EntityActiveRequest (candidate)"
	case CmdEntityRemoveNotify:
		return "EntityRemoveNotify (candidate)"
	default:
		return ""
	}
}

// hasCurrentProtocolSchema reports the commands whose census entries record the current client protocol as their
// schema basis.
func hasCurrentProtocolSchema(id uint16) bool {
	switch id {
	case CmdUpdateSceneGroupDataNotify, CmdPlayerLivingStatesNotify, CmdAddSysBuffNotify, CmdApplyGameplayEffectNotify, CmdApplyGameplayEffectPush, CmdRemoveGameplayEffectNotify, CmdRemoveGameplayEffectPush, CmdSimpleCombatEntityBuffUpdateNotify, CmdSimpleCombatEntityBuffLayerCountNotify, CmdBuffStackCountNotify, CmdBuffDurationNotify, CmdOrderApplyBuffNotify, CmdOrderRemoveBuffNotify, CmdRefreshBuffDurationPush, CmdRemoveSysBuffNotify, CmdSimpleCombatEntityAttributeUpdateNotify, CmdSimpleCombatEntitySyncAttributePush, CmdUpdateSysBuffNotify,
		CmdApplyGameplayEffectRequest, CmdApplyGameplayEffectResponse, CmdRemoveGameplayEffectRequest, CmdRemoveGameplayEffectResponse, CmdOrderApplyBuffRequest, CmdOrderApplyBuffResponse, CmdOrderRemoveBuffRequest, CmdOrderRemoveBuffResponse, CmdApplyBuffS2cRequestNotify, CmdApplyBuffS2cResponsePush, CmdRemoveBuffS2cRequestNotify, CmdRemoveBuffS2cResponsePush, CmdRemoveBuffByIDS2cRequestNotify, CmdRemoveBuffByIDS2cResponsePush, CmdActivateBuffRequest, CmdActivateBuffResponse, CmdActivateBuffNotify, CmdActivateBuffPush,
		CmdBuffStackCountRequest, CmdBuffStackCountResponse, CmdBuffStackCountPush, CmdBuffEffectRequest, CmdBuffEffectResponse, CmdBuffEffectPush, CmdBuffEffectExecuteRequest, CmdBuffEffectExecuteResponse, CmdBuffEffectExecutePush, CmdEntityBuffProducerRequest, CmdEntityBuffProducerResponse, CmdEntityBuffProducerOperateRequest, CmdEntityBuffProducerOperateResponse, CmdOrderRemoveBuffByTagsRequest, CmdOrderRemoveBuffByTagsResponse, CmdOrderRemoveBuffByTagsNotify, CmdRemoveBuffByServerIDS2cRequestNotify,
		CmdAtomicProcessAddBuffRequest, CmdAtomicProcessAddBuffResponse, CmdAtomicProcessRemoveBuffRequest, CmdAtomicProcessRemoveBuffResponse, CmdTransformBuffStackNotify, CmdJoinSceneNotify, CmdEntityAddNotify, CmdEntityRemoveNotify, CmdEntityActiveRequest, CmdUpdatePlayerAllFightRoleNotify, CmdUpdatePlayersFightRolesInfoNotify, CmdUpdatePlayerSingleFightRoleNotify, CmdChangePlayerFightRoleNotify, CmdCombatReceivePackNotify,
		CmdEntityActiveResponse, CmdPhantomAttrRecommendResponse, CmdMovePackagePush, CmdPlayerFollowerNotify, CmdEntityRemoveDynamicInteractNotify, CmdEntityCommonTagNotify, CmdEntityStateReadyNotify:
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
		case CmdLoginRequest, CmdCheckClientPush:
			entry.Name = commandName(id)
			entry.SchemaBasis = "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"
		case CmdProtoKeyResponse:
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
	fields, err := ParseProtoFields(data)
	if err != nil {
		return movePackageObservation{}, err
	}
	var observation movePackageObservation
	for _, field := range fields {
		if field.Number == FieldMovePackagePushSceneOwnerID && field.Wire == 0 {
			observation.SceneOwnerIDs = append(observation.SceneOwnerIDs, field.Value)
			continue
		}
		if field.Number != FieldMovePackagePushMovingEntities || field.Wire != 2 {
			continue
		}
		movingEntity, err := ParseProtoFields(field.Data)
		if err != nil {
			return movePackageObservation{}, fmt.Errorf("parse MovePackagePush.MovingEntities: %w", err)
		}
		var entity movePackageEntityObservation
		hasEntityID := false
		for _, nested := range movingEntity {
			if nested.Number == FieldMovingEntityDataEntityID && nested.Wire == 0 {
				entity.EntityID = nested.Value
				hasEntityID = true
			} else if nested.Number == FieldMovingEntityDataOriginator && nested.Wire == 0 {
				entity.OriginatorValue = nested.Value
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
	fields, err := ParseProtoFields(data)
	if err != nil {
		return playerFollowerObservation{}, err
	}
	var observation playerFollowerObservation
	for _, field := range fields {
		if field.Number == FieldPlayerFollowerNotifyPlayerID && field.Wire == 0 {
			observation.PlayerID = field.Value
			observation.HasPlayerID = true
			continue
		}
		if field.Number != FieldPlayerFollowerNotifyListPlayerFollowerPb || field.Wire != 2 {
			continue
		}
		nestedFields, err := ParseProtoFields(field.Data)
		if err != nil {
			return playerFollowerObservation{}, fmt.Errorf("parse PlayerFollowerNotify.ListPlayerFollowerPb: %w", err)
		}
		var follower playerFollowerEntity
		for _, nested := range nestedFields {
			if nested.Number == FieldPlayerFollowerPbType && nested.Wire == 0 {
				follower.Type = nested.Value
				follower.HasType = true
			} else if nested.Number == FieldPlayerFollowerPbEntityID && nested.Wire == 0 {
				follower.EntityID = nested.Value
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
	fields, err := ParseProtoFields(data)
	if err != nil {
		return nil, err
	}
	var values []uint64
	for _, field := range fields {
		if field.Number == fieldNumber && field.Wire == 0 {
			values = append(values, field.Value)
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

func (c *commandCensus) ObserveFrame(frame GameFrame, session int) CensusFrameObserver {
	entry := c.entry(frame.CommandID, frame.Direction)
	entry.CRCValidFrames++
	entry.Sessions[session]++
	if entry.FirstSeen.IsZero() || frame.Timestamp.Before(entry.FirstSeen) {
		entry.FirstSeen = frame.Timestamp
	}
	if entry.LastSeen.IsZero() || frame.Timestamp.After(entry.LastSeen) {
		entry.LastSeen = frame.Timestamp
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

func (c *commandCensus) FailedCRC(observation CRCFailureObservation) int {
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
	c.crcFailures = append(c.crcFailures, crcFailureRecord{CRCFailureObservation: observation})
	return len(c.crcFailures) - 1
}

func (c *commandCensus) SetNextValidFrame(index int, next FrameBoundaryObservation) {
	if index < 0 || index >= len(c.crcFailures) || c.crcFailures[index].NextValid != nil {
		return
	}
	c.crcFailures[index].NextValid = &next
}

// InspectUndecoded counts what the census can learn about a frame no decoder reads. decodable says whether the
// protocol session reads its numbering (protocolSession.decodes); otherwise only the top-level wire shape is counted.
func (entry *commandCensusEntry) InspectUndecoded(frame GameFrame, decodable bool) {
	p := entry.census.processor
	if len(p.Session) != 32 {
		entry.FailureReasons["no implemented schema; session key unavailable"]++
		return
	}
	if len(frame.Body) == 0 {
		entry.FailureReasons[EmptyBodyFailureReason]++
		return
	}
	plain, err := DecryptGameBody(frame.Sequence, frame.Body, p.Session)
	if err != nil {
		entry.FailureReasons["no implemented schema; decrypt failed"]++
		return
	}
	entry.DecryptedFrames++
	if frame.Compressed {
		plain, err = DecompressBody(plain, frame.CompressedLength)
		if err != nil {
			entry.FailureReasons["no implemented schema; decompression failed"]++
			return
		}
	}
	if !decodable {
		if fields, err := ParseProtoFields(plain); err == nil {
			entry.GenericProtoValid++
			entry.observeTopLevelFields(fields)
		}
		entry.FailureReasons[undecodableReason(&p.Protocol)]++
		return
	}
	if fields, err := ParseProtoFields(plain); err == nil {
		entry.GenericProtoValid++
		// The optional external schema describes the current protocol.
		p.ArchiveSchemaDecode(frame, plain)
		entry.observeTopLevelFields(fields)
		entry.FailureReasons["message schema not implemented"]++
		if frame.CommandID == CmdMovePackagePush {
			observation, err := extractMovePackageObservation(plain)
			if err != nil {
				entry.FailureReasons["MovePackagePush schema-field extraction failed"]++
			} else {
				entry.census.observeMovePackage(observation)
			}
		} else if frame.CommandID == CmdPlayerFollowerNotify {
			observation, err := extractPlayerFollowerObservation(plain)
			if err != nil {
				entry.FailureReasons["PlayerFollowerNotify schema-field extraction failed"]++
			} else {
				entry.census.observePlayerFollower(observation)
			}
		} else if fieldNumber, ok := entityNotifyKeyField(frame.CommandID); ok {
			values, err := extractVarintField(plain, fieldNumber)
			if err != nil {
				entry.FailureReasons["entity notify ID extraction failed"]++
			} else {
				entry.census.observeEntityKey(frame.CommandID, fieldNumber, values)
			}
		}
	} else {
		entry.FailureReasons["no implemented schema; generic protobuf parse failed"]++
	}
}

func entityNotifyKeyField(commandID uint16) (uint64, bool) {
	switch commandID {
	case CmdEntityRemoveDynamicInteractNotify:
		return FieldEntityRemoveDynamicInteractNotifyEntityID, true // EntityRemoveDynamicInteractNotify.EntityId
	case CmdEntityCommonTagNotify:
		return FieldEntityCommonTagNotifyID, true // EntityCommonTagNotify.Id
	case CmdEntityStateReadyNotify:
		return FieldEntityStateReadyNotifyEntityID, true // EntityStateReadyNotify.EntityId
	default:
		return 0, false
	}
}

func (c *commandCensus) write(path string, stats CaptureStats, clientVersion *ClientVersionReport, protocol protocolReport) error {
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
		{CmdUpdateSceneGroupDataNotify, "in"}, {CmdPlayerLivingStatesNotify, "in"}, {CmdAddSysBuffNotify, "in"}, {CmdApplyGameplayEffectNotify, "in"}, {CmdApplyGameplayEffectPush, "in"}, {CmdRemoveGameplayEffectNotify, "in"}, {CmdRemoveGameplayEffectPush, "in"}, {CmdSimpleCombatEntityBuffUpdateNotify, "in"}, {CmdSimpleCombatEntityBuffLayerCountNotify, "in"}, {CmdBuffStackCountNotify, "in"}, {CmdBuffDurationNotify, "in"}, {CmdOrderApplyBuffNotify, "in"}, {CmdOrderRemoveBuffNotify, "in"}, {CmdRefreshBuffDurationPush, "in"}, {CmdRemoveSysBuffNotify, "in"}, {CmdSimpleCombatEntityAttributeUpdateNotify, "in"}, {CmdSimpleCombatEntitySyncAttributePush, "in"}, {CmdUpdateSysBuffNotify, "in"}, {CmdJoinSceneNotify, "in"}, {CmdEntityAddNotify, "in"}, {CmdEntityRemoveNotify, "in"}, {CmdEntityActiveRequest, "out"}, {CmdEntityActiveResponse, "in"}, {CmdCombatSendPackRequest, "out"}, {CmdCombatSendPackResponse, "in"},
		{CmdApplyGameplayEffectRequest, "out"}, {CmdApplyGameplayEffectResponse, "in"}, {CmdRemoveGameplayEffectRequest, "out"}, {CmdRemoveGameplayEffectResponse, "in"}, {CmdOrderApplyBuffRequest, "out"}, {CmdOrderApplyBuffResponse, "in"}, {CmdOrderRemoveBuffRequest, "out"}, {CmdOrderRemoveBuffResponse, "in"}, {CmdApplyBuffS2cRequestNotify, "in"}, {CmdApplyBuffS2cResponsePush, "in"}, {CmdRemoveBuffS2cRequestNotify, "in"}, {CmdRemoveBuffS2cResponsePush, "in"}, {CmdRemoveBuffByIDS2cRequestNotify, "in"}, {CmdRemoveBuffByIDS2cResponsePush, "in"}, {CmdActivateBuffRequest, "out"}, {CmdActivateBuffResponse, "in"}, {CmdActivateBuffNotify, "in"}, {CmdActivateBuffPush, "in"},
		{CmdBuffStackCountRequest, "out"}, {CmdBuffStackCountResponse, "in"}, {CmdBuffStackCountPush, "in"}, {CmdBuffEffectRequest, "out"}, {CmdBuffEffectResponse, "in"}, {CmdBuffEffectPush, "in"}, {CmdBuffEffectExecuteRequest, "out"}, {CmdBuffEffectExecuteResponse, "in"}, {CmdBuffEffectExecutePush, "in"}, {CmdEntityBuffProducerRequest, "out"}, {CmdEntityBuffProducerResponse, "in"}, {CmdEntityBuffProducerOperateRequest, "out"}, {CmdEntityBuffProducerOperateResponse, "in"}, {CmdOrderRemoveBuffByTagsRequest, "out"}, {CmdOrderRemoveBuffByTagsResponse, "in"}, {CmdOrderRemoveBuffByTagsNotify, "in"}, {CmdRemoveBuffByServerIDS2cRequestNotify, "in"}, {CmdAtomicProcessAddBuffRequest, "out"}, {CmdAtomicProcessAddBuffResponse, "in"}, {CmdAtomicProcessRemoveBuffRequest, "out"}, {CmdAtomicProcessRemoveBuffResponse, "in"}, {CmdTransformBuffStackNotify, "in"},
		{CmdUpdatePlayerAllFightRoleNotify, "in"}, {CmdUpdatePlayersFightRolesInfoNotify, "in"}, {CmdUpdatePlayerSingleFightRoleNotify, "in"}, {CmdChangePlayerFightRoleNotify, "in"},
		{CmdLoginRequest, "out"}, {CmdCheckClientPush, "out"},
	} {
		key := censusKey{CommandID: candidate.id, Direction: candidate.direction}
		if _, exists := c.entries[key]; !exists {
			entry := commandCensusEntry{CommandID: candidate.id, Name: commandName(candidate.id), Direction: candidate.direction, Status: "NOT OBSERVED"}
			if candidate.id == CmdLoginRequest || candidate.id == CmdCheckClientPush {
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
		ClientVersion      *ClientVersionReport   `json:"clientVersion,omitempty"`
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
	report.Transport["udpPackets"] = stats.UDPPackets
	report.Transport["kcpPushSegments"] = stats.KCPPush
	report.Transport["kcpControlSegments"] = stats.KCPControl
	report.Transport["nonKcpUdpDatagrams"] = stats.NonKCPUDP
	report.Transport["invalidKcpDatagrams"] = stats.InvalidKCP
	report.Transport["kcpOrderedSegments"] = stats.KCPOrdered
	report.Transport["kcpMessages"] = stats.KCPMessages
	report.Transport["crcValidFrames"] = stats.Frames
	report.Transport["badCRCFrames"] = stats.BadCRC
	report.Transport["malformedFrames"] = stats.Malformed
	report.Transport["kernelDrops"] = stats.KernelDrops
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal command census: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write command census: %w", err)
	}
	return nil
}
