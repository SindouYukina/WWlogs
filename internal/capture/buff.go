package capture

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"wwlogs/internal/versioninfo"
)

type rawBuffSnapshot struct {
	HandleID         uint64   `json:"handleId,omitempty"`
	HasHandleID      bool     `json:"hasHandleId"`
	ID               uint64   `json:"buffId,omitempty"`
	HasID            bool     `json:"hasBuffId"`
	Level            uint64   `json:"level,omitempty"`
	HasLevel         bool     `json:"hasLevel"`
	Stack            uint64   `json:"stack,omitempty"`
	HasStack         bool     `json:"hasStack"`
	InstigatorID     uint64   `json:"instigatorId,omitempty"`
	HasInstigatorID  bool     `json:"hasInstigatorId"`
	EntityID         uint64   `json:"entityId,omitempty"`
	HasEntityID      bool     `json:"hasEntityId"`
	ApplyType        uint64   `json:"applyType,omitempty"`
	HasApplyType     bool     `json:"hasApplyType"`
	Duration         float32  `json:"duration,omitempty"`
	HasDuration      bool     `json:"hasDuration"`
	Remaining        float32  `json:"remainingDuration,omitempty"`
	HasRemaining     bool     `json:"hasRemainingDuration"`
	Active           bool     `json:"active,omitempty"`
	HasActive        bool     `json:"hasActive"`
	ServerID         uint64   `json:"serverId,omitempty"`
	HasServerID      bool     `json:"hasServerId"`
	MessageID        uint64   `json:"messageId,omitempty"`
	HasMessageID     bool     `json:"hasMessageId"`
	ConfBuffID       uint64   `json:"confBuffId,omitempty"`
	HasConfBuffID    bool     `json:"hasConfBuffId"`
	Context          [][]byte `json:"context,omitempty"`
	UnknownFieldNums []uint64 `json:"unknownFieldNumbers,omitempty"`
}

type buffSnapshotAuditRow struct {
	SchemaVersion     int              `json:"schemaVersion"`
	EventType         string           `json:"eventType"`
	SessionNumber     int              `json:"sessionNumber"`
	MessageID         uint16           `json:"messageId"`
	Timestamp         time.Time        `json:"timestamp"`
	ResponseCommandID uint16           `json:"responseCommandId"`
	ResponseDirection string           `json:"responseDirection"`
	RequestCommandID  uint16           `json:"requestCommandId"`
	FrameToken        uint16           `json:"frameToken"`
	EntityID          *uint64          `json:"entityId,omitempty"`
	EntityIDStatus    resolutionStatus `json:"entityIdStatus"`
	ComponentPath     string           `json:"componentPath"`
	// BuffIDStatus records the captured field mapping, not static-definition resolution.
	BuffIDStatus resolutionStatus `json:"buffIdStatus"`
	// BuffDefinitionStatus records an exact link from BuffId to the normalized static buff table.
	BuffDefinitionStatus   resolutionStatus `json:"buffDefinitionStatus"`
	BuffDefinitionTable    string           `json:"buffDefinitionTable,omitempty"`
	BuffDefinitionID       *uint64          `json:"buffDefinitionId,omitempty"`
	BuffDefinitionEvidence string           `json:"buffDefinitionEvidence"`
	// BuffOwners names the features that list this BuffId in an explicit reference table; the Buff has no name of its own.
	BuffOwners *buffOwnersJSON `json:"buffOwners,omitempty"`
	// BuffSemanticStatus covers runtime application and effect semantics, not static row lookup.
	BuffSemanticStatus        resolutionStatus `json:"buffSemanticStatus"`
	BuffSemanticEvidence      string           `json:"buffSemanticEvidence"`
	InstigatorSemantics       resolutionStatus `json:"instigatorSemantics"`
	InstigatorEvidence        string           `json:"instigatorEvidence,omitempty"`
	ReferenceSchema           string           `json:"referenceSchema"`
	WindowsSchemaVerification string           `json:"windowsSchemaVerification"`
	Buff                      rawBuffSnapshot  `json:"buff"`
}

const buffSnapshotSchemaVersion = 4

type buffLifecycleAuditRow struct {
	SchemaVersion           int              `json:"schemaVersion"`
	EventType               string           `json:"eventType"`
	SessionNumber           int              `json:"sessionNumber"`
	MessageID               uint16           `json:"messageId"`
	Timestamp               time.Time        `json:"timestamp"`
	CommandID               uint16           `json:"commandId"`
	InnerMessageFieldNumber *uint64          `json:"innerMessageFieldNumber,omitempty"`
	MessagePath             string           `json:"messagePath,omitempty"`
	Direction               string           `json:"direction"`
	MessageName             string           `json:"messageName"`
	EventKind               string           `json:"eventKind"`
	EventTypeStatus         resolutionStatus `json:"eventTypeStatus"`
	SchemaBasis             string           `json:"schemaBasis"`
	TargetEntityID          *uint64          `json:"targetEntityIdRaw,omitempty"`
	TargetStatus            resolutionStatus `json:"targetSemanticsStatus"`
	TargetEvidence          string           `json:"targetEvidence,omitempty"`
	BuffID                  *uint64          `json:"buffId,omitempty"`
	BuffDefinitionStatus    resolutionStatus `json:"buffDefinitionStatus,omitempty"`
	BuffDefinitionTable     string           `json:"buffDefinitionTable,omitempty"`
	BuffDefinitionID        *uint64          `json:"buffDefinitionId,omitempty"`
	BuffDefinitionEvidence  string           `json:"buffDefinitionEvidence,omitempty"`
	ID                      *uint64          `json:"id,omitempty"`
	// BuffOwners names the features that list the linked Buff id (typed, apply Id or handle-inherited) in an explicit reference table.
	BuffOwners *buffOwnersJSON `json:"buffOwners,omitempty"`
	// GenericID* link the untyped Id of apply-type messages to buff.id. They are separate from the
	// typed BuffDefinition* fields: the field is not named BuffId, so the basis is capture-internal evidence.
	GenericIDDefinitionStatus   resolutionStatus `json:"genericIdDefinitionStatus,omitempty"`
	GenericIDDefinitionTable    string           `json:"genericIdDefinitionTable,omitempty"`
	GenericIDDefinitionID       *uint64          `json:"genericIdDefinitionId,omitempty"`
	GenericIDDefinitionEvidence string           `json:"genericIdDefinitionEvidence,omitempty"`
	// HandleLinked* tie a handle-only event (duration, stack, effect removal) to the apply event that created the handle.
	HandleLinkedID                         *uint64          `json:"handleLinkedId,omitempty"`
	HandleLinkedStatus                     resolutionStatus `json:"handleLinkedStatus,omitempty"`
	HandleLinkedEvidence                   string           `json:"handleLinkedEvidence,omitempty"`
	ConfBuffID                             *uint64          `json:"confBuffId,omitempty"`
	InstigatorID                           *uint64          `json:"instigatorIdRaw,omitempty"`
	InstigatorStatus                       resolutionStatus `json:"instigatorSemanticsStatus"`
	InstigatorEvidence                     string           `json:"instigatorEvidence,omitempty"`
	InstigatorCharacterCandidateID         *uint64          `json:"instigatorCharacterCandidateId,omitempty"`
	InstigatorCharacterCandidateName       string           `json:"instigatorCharacterCandidateName,omitempty"`
	InstigatorCharacterCandidateStatus     resolutionStatus `json:"instigatorCharacterCandidateStatus"`
	InstigatorCharacterCandidateNameStatus resolutionStatus `json:"instigatorCharacterCandidateNameStatus"`
	InstigatorCharacterCandidateEvidence   string           `json:"instigatorCharacterCandidateEvidence,omitempty"`
	ProviderStatus                         resolutionStatus `json:"providerStatus"`
	ProviderNameStatus                     resolutionStatus `json:"providerNameStatus"`
	ProviderEvidence                       string           `json:"providerEvidence,omitempty"`
	ProviderCharacterID                    *uint64          `json:"providerCharacterId,omitempty"`
	ProviderName                           string           `json:"providerName,omitempty"`
	CombatCommonEntityID                   *uint64          `json:"combatCommonEntityId,omitempty"`
	CombatCommonOriginator                 *uint64          `json:"combatCommonOriginatorId,omitempty"`
	CombatCommonMessageID                  *uint64          `json:"combatCommonMessageId,omitempty"`
	HandleID                               *uint64          `json:"handleId,omitempty"`
	Level                                  *uint64          `json:"level,omitempty"`
	StackCount                             *uint64          `json:"stackCount,omitempty"`
	ApplyType                              *uint64          `json:"applyTypeRaw,omitempty"`
	DurationSeconds                        *float32         `json:"durationSeconds,omitempty"`
	RemainingDuration                      *float32         `json:"remainingDurationSeconds,omitempty"`
	IsBuffAdd                              *bool            `json:"isBuffAdd,omitempty"`
	IsActive                               *bool            `json:"isActive,omitempty"`
	IsIterable                             *bool            `json:"isIterable,omitempty"`
	IsPrematureRemoval                     *bool            `json:"isPrematureRemoval,omitempty"`
	NotRefreshDuration                     *bool            `json:"notRefreshDuration,omitempty"`
	NotRefreshPeriod                       *bool            `json:"notRefreshPeriod,omitempty"`
	Reason                                 *uint64          `json:"reasonRaw,omitempty"`
	ServerID                               *uint64          `json:"serverId,omitempty"`
	UnknownFieldNumbers                    []uint64         `json:"unknownFieldNumbers,omitempty"`
	// The fields below appear only on rows decoded from outbound client pushes and the
	// server requests they answer; server notify rows keep their earlier layout.
	EvidenceSource           EffectEvidence `json:"evidenceSource,omitempty"`
	CombatCommonPreMessageID *uint64        `json:"combatCommonPreMessageId,omitempty"`
	// ClientTimeStamp is CombatCommon.TimeStamp of a client push, in seconds on the client's own clock.
	ClientTimeStamp *float32 `json:"clientTimeStamp,omitempty"`
	// ClientReasonRaw is the client's free-text reason string, kept verbatim as developer evidence.
	ClientReasonRaw     string           `json:"clientReasonRaw,omitempty"`
	ErrorCode           *uint64          `json:"errorCodeRaw,omitempty"`
	RequestLinkedID     *uint64          `json:"requestLinkedId,omitempty"`
	RequestLinkStatus   resolutionStatus `json:"requestLinkStatus,omitempty"`
	RequestLinkEvidence string           `json:"requestLinkEvidence,omitempty"`
	// consoleSuppressed marks a client stack report that changed neither the stack nor the
	// linked effect; it still reaches the tracker and JSONL.
	consoleSuppressed bool
}

const buffLifecycleSchemaVersion = 6

func decodeBuffLifecycleMessage(frame gameFrame, body []byte) (buffLifecycleAuditRow, error) {
	return decodeBuffLifecycleMessageForCommand(frame, body, frame.commandID, nil)
}

func decodeBuffLifecycleMessageForCommand(frame gameFrame, body []byte, schemaCommandID uint16, innerFieldNumber *uint64) (buffLifecycleAuditRow, error) {
	row := buffLifecycleAuditRow{
		SchemaVersion: buffLifecycleSchemaVersion, EventType: "BUFF_LIFECYCLE", MessageID: frame.commandID,
		Timestamp: frame.timestamp, CommandID: frame.commandID, Direction: frame.direction,
		EventTypeStatus: resolutionCorrelated, TargetStatus: resolutionUnknown, InstigatorStatus: resolutionUnknown,
		InstigatorCharacterCandidateStatus: resolutionUnknown, InstigatorCharacterCandidateNameStatus: resolutionUnknown,
		ProviderStatus: resolutionUnknown, ProviderNameStatus: resolutionUnknown,
		SchemaBasis: versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; client version observed, schema compatibility unverified",
	}
	if innerFieldNumber != nil {
		fieldNumber := *innerFieldNumber
		row.InnerMessageFieldNumber = &fieldNumber
		row.MessagePath = fmt.Sprintf("CombatReceivePackNotify.field5(CombatReceiveData[]).field2(CombatNotifyData).field%d", fieldNumber)
	}
	switch schemaCommandID {
	case 25830:
		row.MessageName, row.EventKind = "ApplyGameplayEffectNotify", "GAMEPLAY_EFFECT_APPLY"
	case 18345:
		row.MessageName, row.EventKind = "RemoveGameplayEffectNotify", "GAMEPLAY_EFFECT_REMOVE"
	case 20358:
		row.MessageName, row.EventKind = "RemoveGameplayEffectPush", "GAMEPLAY_EFFECT_REMOVE"
	case 17893:
		row.MessageName, row.EventKind = "SimpleCombatEntityBuffUpdateNotify", "SIMPLE_BUFF_UPDATE"
	case 22102:
		row.MessageName, row.EventKind = "SimpleCombatEntityBuffLayerCountNotify", "SIMPLE_BUFF_LAYER_COUNT_CHANGE"
	case 29363:
		row.MessageName, row.EventKind = "BuffStackCountNotify", "BUFF_STACK_COUNT_CHANGE"
	case 16974:
		row.MessageName, row.EventKind = "BuffDurationNotify", "BUFF_DURATION_CHANGE"
	case 18641:
		row.MessageName, row.EventKind = "ApplyBuffS2cRequestNotify", "BUFF_APPLY_REQUEST_NOTIFY"
	case 20022:
		row.MessageName, row.EventKind = "RemoveBuffS2cRequestNotify", "BUFF_REMOVE_REQUEST_NOTIFY"
	case 19482:
		row.MessageName, row.EventKind = "RemoveBuffByIdS2cRequestNotify", "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY"
	default:
		return row, fmt.Errorf("unsupported Buff lifecycle schema command %d", schemaCommandID)
	}
	fields, err := parseProtoFields(body)
	if err != nil {
		return row, fmt.Errorf("parse %s: %w", row.MessageName, err)
	}
	for _, field := range fields {
		var uintTarget **uint64
		var boolTarget **bool
		var floatTarget **float32
		switch schemaCommandID {
		case 25830:
			switch field.number {
			case 2:
				uintTarget = &row.HandleID
			case 11:
				uintTarget = &row.ID
			case 5:
				uintTarget = &row.Level
			case 7:
				uintTarget = &row.TargetEntityID
			case 8:
				uintTarget = &row.InstigatorID
			case 9:
				uintTarget = &row.ApplyType
			case 15:
				floatTarget = &row.DurationSeconds
			case 6:
				boolTarget = &row.IsActive
			case 10:
				uintTarget = &row.ServerID
			case 1:
				uintTarget = &row.StackCount
			case 12:
				floatTarget = &row.RemainingDuration
			case 14:
				uintTarget = &row.ConfBuffID
			}
		case 18345:
			switch field.number {
			case 13:
				uintTarget = &row.HandleID
			case 5:
				uintTarget = &row.TargetEntityID
			case 14:
				uintTarget = &row.InstigatorID
			}
		case 20358:
			switch field.number {
			case 14:
				uintTarget = &row.HandleID
			case 15:
				uintTarget = &row.TargetEntityID
			case 10:
				boolTarget = &row.IsPrematureRemoval
			case 1:
				uintTarget = &row.Reason
			case 4:
				uintTarget = &row.InstigatorID
			}
		case 17893:
			switch field.number {
			case 12:
				uintTarget = &row.TargetEntityID
			case 1:
				boolTarget = &row.IsBuffAdd
			case 3:
				uintTarget = &row.BuffID
			}
		case 22102:
			switch field.number {
			case 6:
				uintTarget = &row.TargetEntityID
			case 7:
				uintTarget = &row.StackCount
			case 2:
				uintTarget = &row.BuffID
			}
		case 29363:
			switch field.number {
			case 9:
				uintTarget = &row.HandleID
			case 4:
				uintTarget = &row.StackCount
			case 7:
				floatTarget = &row.DurationSeconds
			case 11:
				floatTarget = &row.RemainingDuration
			case 13:
				uintTarget = &row.InstigatorID
			case 6:
				boolTarget = &row.NotRefreshDuration
			case 1:
				boolTarget = &row.NotRefreshPeriod
			}
		case 16974:
			switch field.number {
			case 12:
				uintTarget = &row.HandleID
			case 2:
				floatTarget = &row.DurationSeconds
			case 5:
				floatTarget = &row.RemainingDuration
			}
		case 18641:
			switch field.number {
			case 4:
				uintTarget = &row.ID
			case 8:
				uintTarget = &row.Level
			case 15:
				uintTarget = &row.InstigatorID
			case 5:
				uintTarget = &row.ApplyType
			case 3:
				floatTarget = &row.DurationSeconds
			case 10:
				uintTarget = &row.ServerID
			case 11:
				uintTarget = &row.StackCount
			case 1:
				boolTarget = &row.IsIterable
			case 9:
				uintTarget = &row.Reason
			}
		case 20022:
			switch field.number {
			case 1:
				uintTarget = &row.HandleID
			case 12:
				uintTarget = &row.StackCount
			case 11:
				uintTarget = &row.Reason
			case 2:
				uintTarget = &row.InstigatorID
			}
		case 19482:
			switch field.number {
			case 4:
				uintTarget = &row.BuffID
			case 13:
				uintTarget = &row.StackCount
			case 10:
				uintTarget = &row.Reason
			case 6:
				uintTarget = &row.InstigatorID
			}
		}
		if uintTarget != nil {
			if field.wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint", row.MessageName, field.number, field.wire)
			}
			value := field.value
			*uintTarget = &value
		} else if boolTarget != nil {
			if field.wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint bool", row.MessageName, field.number, field.wire)
			}
			value := field.value != 0
			*boolTarget = &value
		} else if floatTarget != nil {
			if field.wire != 5 || len(field.data) != 4 {
				return row, fmt.Errorf("%s field %d has wire type %d, want fixed32 float", row.MessageName, field.number, field.wire)
			}
			value := math.Float32frombits(binary.LittleEndian.Uint32(field.data))
			*floatTarget = &value
		} else {
			row.UnknownFieldNumbers = append(row.UnknownFieldNumbers, field.number)
		}
	}
	if row.InstigatorID != nil {
		row.InstigatorEvidence = "Raw InstigatorId field decoded from " + versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; provider semantics and compatibility with the observed client build are UNKNOWN"
		row.ProviderEvidence = "Provider status UNKNOWN: InstigatorId semantics and time-scoped active actor linkage are unverified"
	}
	if row.TargetEntityID != nil {
		row.TargetStatus = resolutionUnknown
		row.TargetEvidence = "Raw target candidate field decoded from " + versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; target semantics and compatibility with the observed client build are UNKNOWN"
	}
	return row, nil
}

func (p *processor) handleBuffLifecycleMessage(frame gameFrame, body []byte) error {
	row, err := decodeBuffLifecycleMessage(frame, body)
	if err != nil {
		return err
	}
	row.SessionNumber = p.sessionNumber
	p.linkBuffLifecycleDefinition(&row)
	p.linkBuffLifecycleGenericAndHandle(&row)
	p.linkBuffLifecycleOwners(&row)
	if p.buffLifecycle != nil {
		if err := json.NewEncoder(p.buffLifecycle).Encode(row); err != nil {
			return fmt.Errorf("write Buff lifecycle row: %w", err)
		}
	}
	p.observeEffectLifecycle(row)
	p.writeStructuredEventJSONL(row)
	p.writeBuffLifecycleConsole(row)
	return nil
}

var combatNotifyBuffSchemaCommands = map[uint64]uint16{
	5:  25830, // ApplyGameplayEffectNotify
	6:  18345, // RemoveGameplayEffectNotify
	41: 29363, // BuffStackCountNotify
	73: 16974, // BuffDurationNotify
	53: 18641, // ApplyBuffS2CRequestNotify
	54: 20022, // RemoveBuffS2CRequestNotify
	59: 19482, // RemoveBuffByIdS2CRequestNotify
}

func (p *processor) handleCombatReceivePackNotify(frame gameFrame, body []byte) error {
	fields, err := parseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse CombatReceivePackNotify: %w", err)
	}
	for _, field := range fields {
		if field.number != 5 || field.wire != 2 { // CombatReceivePackNotify.Data
			continue
		}
		entryFields, err := parseProtoFields(field.data)
		if err != nil {
			return fmt.Errorf("parse CombatReceivePackNotify.Data item: %w", err)
		}
		for _, entry := range entryFields {
			if entry.number != 2 || entry.wire != 2 { // CombatReceiveData.CombatNotifyData
				continue
			}
			notifications, err := parseProtoFields(entry.data)
			if err != nil {
				return fmt.Errorf("parse CombatReceiveData.CombatNotifyData: %w", err)
			}
			var commonEntityID, commonOriginator, commonMessageID *uint64
			for _, notification := range notifications {
				if notification.number == 1 && notification.wire == 2 { // CombatNotifyData.CombatCommon
					commonEntityID, commonOriginator, commonMessageID, err = decodeCombatCommonContext(notification.data)
					if err != nil {
						return fmt.Errorf("parse CombatCommon: %w", err)
					}
				}
			}
			for _, notification := range notifications {
				if p.census != nil {
					p.census.observeCombatNotifyField(notification.number)
				}
				if p.combatNotifyAudit != nil && notification.number != 1 {
					if err := writeCombatNotifyAudit(p.combatNotifyAudit, frame, notification, commonEntityID, commonOriginator, commonMessageID, p.sessionNumber); err != nil {
						return fmt.Errorf("write CombatNotifyData audit field %d: %w", notification.number, err)
					}
				}
				schemaCommandID, supported := combatNotifyBuffSchemaCommands[notification.number]
				if !supported || notification.wire != 2 {
					continue
				}
				if err := p.handleInnerBuffLifecycleMessage(frame, notification.number, schemaCommandID, notification.data, commonEntityID, commonOriginator, commonMessageID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func decodeCombatCommonContext(body []byte) (entityID, originatorID, messageID *uint64, err error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, field := range fields {
		if field.wire != 0 {
			continue
		}
		value := field.value
		switch field.number {
		case 2:
			messageID = &value
		case 3:
			originatorID = &value
		case 5:
			entityID = &value
		}
	}
	return entityID, originatorID, messageID, nil
}

func (p *processor) handleInnerBuffLifecycleMessage(frame gameFrame, innerFieldNumber uint64, schemaCommandID uint16, body []byte, commonEntityID, commonOriginator, commonMessageID *uint64) error {
	row, err := decodeBuffLifecycleMessageForCommand(frame, body, schemaCommandID, &innerFieldNumber)
	if err != nil {
		return err
	}
	row.CombatCommonEntityID = commonEntityID
	row.CombatCommonOriginator = commonOriginator
	row.CombatCommonMessageID = commonMessageID
	if (row.EventKind == "GAMEPLAY_EFFECT_APPLY" || row.EventKind == "GAMEPLAY_EFFECT_REMOVE") &&
		row.TargetEntityID != nil && commonEntityID != nil && *row.TargetEntityID == *commonEntityID {
		row.TargetStatus = resolutionCorrelated
		row.TargetEvidence = "Gameplay-effect TargetEntityId matches CombatCommon.EntityId on this event"
	}
	p.linkLifecycleInstigator(&row, row.EventKind == "GAMEPLAY_EFFECT_APPLY" && row.TargetStatus == resolutionCorrelated,
		"this event's explicit target matches CombatCommon.EntityId")
	row.SessionNumber = p.sessionNumber
	p.rememberServerBuffRequest(row)
	p.linkBuffLifecycleDefinition(&row)
	p.linkBuffLifecycleGenericAndHandle(&row)
	p.linkBuffLifecycleOwners(&row)
	if p.buffLifecycle != nil {
		if err := json.NewEncoder(p.buffLifecycle).Encode(row); err != nil {
			return fmt.Errorf("write Buff lifecycle row: %w", err)
		}
	}
	p.observeBuffInterval(row)
	p.observeEffectLifecycle(row)
	p.writeStructuredEventJSONL(row)
	p.writeBuffLifecycleConsole(row)
	return nil
}

// linkLifecycleInstigator names the character behind a raw InstigatorId. The provider
// fields are set only when providerEligible (the row's holder is established) and the
// instigator resolves to a verified character; holderBasis completes the evidence text.
func (p *processor) linkLifecycleInstigator(row *buffLifecycleAuditRow, providerEligible bool, holderBasis string) {
	if row.InstigatorID == nil {
		return
	}
	identity := resolveEntityIdentity(p.entities, p.masters, *row.InstigatorID)
	if !identity.HasCharacter {
		return
	}
	characterID := identity.Character.ID
	row.InstigatorCharacterCandidateID = &characterID
	row.InstigatorCharacterCandidateName = identity.Character.Name
	row.InstigatorCharacterCandidateStatus = identity.CharacterStatus
	row.InstigatorCharacterCandidateNameStatus = resolutionStatus(identity.Character.NameStatus)
	row.InstigatorCharacterCandidateEvidence = "Raw InstigatorId matches a session runtime EntityId that resolves to a Character master; provider attribution requires the correlated gameplay-effect path"
	if !providerEligible || identity.CharacterStatus != resolutionVerified {
		return
	}
	providerID := identity.Character.ID
	row.ProviderCharacterID = &providerID
	row.ProviderStatus = resolutionCorrelated
	row.InstigatorStatus = resolutionCorrelated
	row.ProviderEvidence = row.MessageName + " InstigatorId resolves through this session's runtime Entity to a verified Character; " + holderBasis
	row.InstigatorEvidence = row.ProviderEvidence
	if identity.Character.Name != "" && identity.Character.NameStatus == string(resolutionVerified) {
		row.ProviderName = identity.Character.Name
		row.ProviderNameStatus = resolutionCorrelated
	}
}

// linkBuffLifecycleOwners attaches the named owners of the Buff id this row is tied to.
func (p *processor) linkBuffLifecycleOwners(row *buffLifecycleAuditRow) {
	if row == nil || p.masters == nil {
		return
	}
	if id, ok := row.buffOwnerLookupID(); ok {
		row.BuffOwners = resolveBuffOwners(p.masters, id)
	} else if row.BuffID != nil {
		// No static definition: only an observed-role owner can exist for the raw BuffId.
		row.BuffOwners = resolveBuffOwners(p.masters, *row.BuffID)
	}
}

const maxBuffHandles = 1 << 16

// buffHandleKey scopes a lifecycle handle id to the session and the entity that holds the buff.
type buffHandleKey struct {
	session  int
	entityID uint64
	handleID uint64
}

type buffHandleEntry struct {
	id         uint64
	definition bool
	// client marks a handle created by a client apply report.
	client bool
	// stack is the last stack count reported for a client handle; it only decides console output.
	stack    uint64
	hasStack bool
}

func lifecycleEntity(row *buffLifecycleAuditRow) (uint64, bool) {
	if row.TargetEntityID != nil {
		return *row.TargetEntityID, true
	}
	if row.CombatCommonEntityID != nil {
		return *row.CombatCommonEntityID, true
	}
	return 0, false
}

// linkBuffLifecycleGenericAndHandle adds two CORRELATED static links that never touch the typed BuffId fields.
//  1. The untyped Id of ApplyBuffS2cRequestNotify and ApplyGameplayEffectNotify is looked up in buff.id.
//     Basis: every saved Id has an exact buff.id row in the sparse id space, and most also occur as typed BuffIds.
//  2. A handle-only event is tied to the earlier apply under the same (session, entity, handle); the link is
//     dropped when the handle was applied with different ids.
func (p *processor) linkBuffLifecycleGenericAndHandle(row *buffLifecycleAuditRow) {
	if row == nil {
		return
	}
	entityID, hasEntity := lifecycleEntity(row)
	switch row.EventKind {
	case "BUFF_APPLY_REQUEST_NOTIFY", "GAMEPLAY_EFFECT_APPLY", "CLIENT_EFFECT_APPLY_REPORT":
		if row.ID == nil {
			return
		}
		row.GenericIDDefinitionStatus = resolutionUnknown
		row.GenericIDDefinitionEvidence = "No exact normalized buff.id row for the untyped Id; raw Id unchanged"
		found := p.masters != nil && p.masters.hasBuffDefinition(*row.ID)
		if found {
			definitionID := *row.ID
			row.GenericIDDefinitionStatus = resolutionCorrelated
			row.GenericIDDefinitionTable = "buff"
			row.GenericIDDefinitionID = &definitionID
			row.GenericIDDefinitionEvidence = "The untyped Id of this apply message has an exact normalized buff.id row; namespace evidence is capture-internal (see the lifecycle Id namespace audit), not a typed BuffId field. Static row reference only: application effect and uptime are unresolved"
		}
		if (row.EventKind == "GAMEPLAY_EFFECT_APPLY" || row.EventKind == "CLIENT_EFFECT_APPLY_REPORT") && row.HandleID != nil && hasEntity {
			if p.buffHandles == nil {
				p.buffHandles = make(map[buffHandleKey]buffHandleEntry)
			}
			key := buffHandleKey{p.sessionNumber, entityID, *row.HandleID}
			if previous, exists := p.buffHandles[key]; exists {
				if previous.id != *row.ID {
					p.buffHandles[key] = buffHandleEntry{id: 0}
				}
			} else {
				p.buffHandleOrder = append(p.buffHandleOrder, key)
				entry := buffHandleEntry{id: *row.ID, definition: found, client: row.EventKind == "CLIENT_EFFECT_APPLY_REPORT"}
				if row.StackCount != nil {
					entry.stack, entry.hasStack = *row.StackCount, true
				}
				p.buffHandles[key] = entry
			}
			for len(p.buffHandleOrder) > maxBuffHandles {
				delete(p.buffHandles, p.buffHandleOrder[0])
				p.buffHandleOrder = p.buffHandleOrder[1:]
			}
		}
	case "BUFF_DURATION_CHANGE", "BUFF_STACK_COUNT_CHANGE", "GAMEPLAY_EFFECT_REMOVE", "BUFF_REMOVE_REQUEST_NOTIFY",
		"CLIENT_EFFECT_STACK_REPORT", "CLIENT_EFFECT_REMOVE_REPORT":
		if row.HandleID == nil || !hasEntity {
			return
		}
		row.HandleLinkedStatus = resolutionUnknown
		row.HandleLinkedEvidence = "No earlier ApplyGameplayEffect with this (session, entity, handle) was observed"
		key := buffHandleKey{p.sessionNumber, entityID, *row.HandleID}
		entry, ok := p.buffHandles[key]
		if ok && entry.id != 0 && row.EventKind == "CLIENT_EFFECT_STACK_REPORT" && row.StackCount != nil {
			// The client repeats a stack push on every refresh; only a changed count is console-worthy.
			row.consoleSuppressed = entry.hasStack && entry.stack == *row.StackCount
			entry.stack, entry.hasStack = *row.StackCount, true
			p.buffHandles[key] = entry
		}
		switch {
		case !ok:
		case entry.id == 0:
			row.HandleLinkedEvidence = "The handle was applied more than once with different ids; the link is rejected"
		default:
			linked := entry.id
			row.HandleLinkedID = &linked
			row.HandleLinkedEvidence = "Same (session, entity, handle) as an earlier ApplyGameplayEffectNotify; the Id is inherited from that apply message"
			if entry.client {
				row.HandleLinkedEvidence = "Same (session, entity, handle) as an earlier client ApplyGameplayEffectPush; the Id is inherited from that apply report"
			}
			row.HandleLinkedStatus = resolutionCorrelated
			if !entry.definition {
				row.HandleLinkedStatus = resolutionUnknown
				row.HandleLinkedEvidence += "; that Id has no exact buff.id row"
			}
		}
	}
}

// linkBuffLifecycleDefinition resolves only explicitly decoded BuffId fields to exact buff.Id rows.
func (p *processor) linkBuffLifecycleDefinition(row *buffLifecycleAuditRow) {
	if row == nil || row.BuffID == nil {
		return
	}
	row.BuffDefinitionStatus = resolutionUnknown
	row.BuffDefinitionEvidence = "No exact normalized buff.id row has been linked; raw BuffId remains unchanged"
	if p.masters != nil && p.masters.hasBuffDefinition(*row.BuffID) {
		definitionID := *row.BuffID
		row.BuffDefinitionStatus = resolutionCorrelated
		row.BuffDefinitionTable = "buff"
		row.BuffDefinitionID = &definitionID
		row.BuffDefinitionEvidence = "The typed BuffId field has an exact normalized buff.id row; this is a static row reference and does not by itself prove that an effect was applied or its uptime"
	}
}

func (p *processor) handleActiveRequest(token uint16, body []byte) error {
	fields, err := parseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse EntityActiveRequest: %w", err)
	}
	if err := p.entities.observeActiveRequest(body); err != nil {
		return err
	}
	request := pendingActiveRequest{}
	for _, field := range fields {
		if field.number == 4 && field.wire == 0 {
			request.entityID = field.value
			request.hasEntityID = field.value != 0
		}
	}
	if len(p.activeRequests) >= maxPendingActiveRequests {
		for oldestToken := range p.activeRequests {
			delete(p.activeRequests, oldestToken)
			break
		}
	}
	p.activeRequests[token] = request
	return nil
}

const maxPendingActiveRequests = 4096

func (p *processor) handleActiveResponse(frame gameFrame, body []byte) error {
	fields, err := parseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse EntityActiveResponse: %w", err)
	}
	request, matched := p.activeRequests[frame.token]
	delete(p.activeRequests, frame.token)
	p.stats.buffSnapshotResponses++
	for _, field := range fields {
		if field.number != 11 || field.wire != 2 {
			continue
		}
		components, err := parseProtoFields(field.data)
		if err != nil {
			return fmt.Errorf("parse EntityActiveResponse ComponentPb: %w", err)
		}
		identityComponents := make([]protoField, 0, 2)
		partComponents := make([]protoField, 0, 1)
		fsmComponents := make([]protoField, 0, 1)
		for _, component := range components {
			if component.wire != 2 {
				continue
			}
			if isRuntimeIdentityComponent(component.number) {
				identityComponents = append(identityComponents, component)
			}
			if component.number == 5 {
				partComponents = append(partComponents, component)
			}
			if component.number == 28 {
				fsmComponents = append(fsmComponents, component)
			}
			switch component.number {
			case 4:
				p.stats.activeResponseSummonerComponents++
			case 18:
				p.stats.activeResponseMonsterComponents++
			case 42:
				p.stats.activeResponseFollowerComponents++
			}
			if component.number != 15 {
				continue
			}
			buffs, err := parseProtoFields(component.data)
			if err != nil {
				return fmt.Errorf("parse ComponentPb.vys: %w", err)
			}
			for _, buffField := range buffs {
				if buffField.number != 1 || buffField.wire != 2 {
					continue
				}
				record, err := parseRawBuffSnapshot(buffField.data)
				if err != nil {
					return fmt.Errorf("parse xAs.SIs buff record: %w", err)
				}
				p.stats.observedBuffEvents++
				p.observeEffectSnapshot(frame, record, request.entityID, matched && request.hasEntityID)
				if p.buffSnapshots != nil || p.combatEventsJSONL != nil || p.encounterSummary != nil {
					row := buffSnapshotAuditRow{
						SchemaVersion: buffSnapshotSchemaVersion, EventType: "BUFF_SNAPSHOT", SessionNumber: p.sessionNumber,
						MessageID: frame.commandID, Timestamp: frame.timestamp, ResponseCommandID: frame.commandID,
						ResponseDirection: frame.direction, RequestCommandID: 28481,
						FrameToken: frame.token, EntityIDStatus: resolutionUnknown,
						ComponentPath:             "EntityActiveResponse.field11(ComponentPbs[])->EntityComponentPb.field15(FightBuffComponent)->FightBuffComponentPb.field1(FightBuffInfos[])->FightBuffInformation",
						BuffIDStatus:              resolutionCorrelated,
						BuffDefinitionStatus:      resolutionUnknown,
						BuffDefinitionEvidence:    "No exact normalized buff.id row has been linked; the raw BuffId remains unchanged",
						BuffSemanticStatus:        resolutionUnknown,
						BuffSemanticEvidence:      "This status covers runtime application and effect semantics; static row lookup is reported separately, and snapshot presence does not establish application or uptime",
						ReferenceSchema:           versioninfo.ReferenceSchemaLabel(),
						WindowsSchemaVerification: versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; client/master compatibility UNKNOWN",
						Buff:                      record,
					}
					if record.HasID && p.masters != nil && p.masters.hasBuffDefinition(record.ID) {
						definitionID := record.ID
						row.BuffDefinitionStatus = resolutionCorrelated
						row.BuffDefinitionTable = "buff"
						row.BuffDefinitionID = &definitionID
						row.BuffDefinitionEvidence = "FightBuffInformation.BuffId follows the client's BuffById lookup; exact normalized buff.id row found. This links static attributes only and does not establish runtime application or effect semantics"
						row.BuffOwners = resolveBuffOwners(p.masters, record.ID)
					} else if record.HasID {
						// No static definition: only an observed-role owner can exist; the definition status stays UNKNOWN.
						row.BuffOwners = resolveBuffOwners(p.masters, record.ID)
					}
					if record.HasInstigatorID {
						row.InstigatorSemantics = resolutionCorrelated
						row.InstigatorEvidence = versioninfo.ReferenceSchemaLabel() + " CharacterBuffComponent compares InstigatorId with CreatureDataId and resolves it through CreatureModel.GetEntity; client/master compatibility UNKNOWN"
					}
					if matched && request.hasEntityID {
						entityID := request.entityID
						row.EntityID = &entityID
						row.EntityIDStatus = resolutionCorrelated
					}
					p.observeBuffSnapshot(row)
					if p.buffSnapshots != nil {
						encoder := json.NewEncoder(p.buffSnapshots)
						if err := encoder.Encode(row); err != nil {
							return fmt.Errorf("write Buff snapshot row: %w", err)
						}
					}
					p.writeStructuredEventJSONL(row)
				}
			}
		}
		if len(identityComponents) > 0 {
			entityID := uint64(0)
			if matched && request.hasEntityID {
				entityID = request.entityID
			}
			observed, err := p.entities.applyActiveResponseIdentityComponents(entityID, identityComponents)
			if err != nil {
				return fmt.Errorf("parse EntityActiveResponse identity components: %w", err)
			}
			p.stats.activeResponseIdentityComponents += observed
		}
		if len(fsmComponents) > 0 && matched && request.hasEntityID {
			if err := p.entities.applyActiveResponseFsmComponents(request.entityID, fsmComponents); err != nil {
				return fmt.Errorf("parse EntityActiveResponse FSM components: %w", err)
			}
		}
		if len(partComponents) > 0 {
			entityID := uint64(0)
			if matched && request.hasEntityID {
				entityID = request.entityID
			}
			if entityID == 0 {
				p.entities.invalidatePartComponents()
			} else if _, err := p.entities.applyActiveResponsePartComponents(entityID, partComponents); err != nil {
				return fmt.Errorf("parse EntityActiveResponse part components: %w", err)
			}
		}
	}
	p.refreshEffectRelations()
	return nil
}

func parseRawBuffSnapshot(data []byte) (rawBuffSnapshot, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return rawBuffSnapshot{}, err
	}
	var record rawBuffSnapshot
	for _, field := range fields {
		if field.wire == 0 {
			switch field.number {
			case 1:
				record.HandleID, record.HasHandleID = field.value, true
			case 2:
				record.ID, record.HasID = field.value, true
			case 3:
				record.Level, record.HasLevel = field.value, true
			case 4:
				record.Stack, record.HasStack = field.value, true
			case 5:
				record.InstigatorID, record.HasInstigatorID = field.value, true
			case 6:
				record.EntityID, record.HasEntityID = field.value, true
			case 7:
				record.ApplyType, record.HasApplyType = field.value, true
			case 11:
				record.Active, record.HasActive = field.value != 0, true
			case 12:
				record.ServerID, record.HasServerID = field.value, true
			case 13:
				record.MessageID, record.HasMessageID = field.value, true
			case 14:
				record.ConfBuffID, record.HasConfBuffID = field.value, true
			default:
				record.UnknownFieldNums = append(record.UnknownFieldNums, field.number)
			}
			continue
		}
		if field.number == 8 && field.wire == 5 && len(field.data) == 4 {
			record.Duration, record.HasDuration = math.Float32frombits(binary.LittleEndian.Uint32(field.data)), true
		} else if field.number == 9 && field.wire == 5 && len(field.data) == 4 {
			record.Remaining, record.HasRemaining = math.Float32frombits(binary.LittleEndian.Uint32(field.data)), true
		} else if field.number == 10 && field.wire == 2 {
			record.Context = append(record.Context, append([]byte(nil), field.data...))
		} else {
			record.UnknownFieldNums = append(record.UnknownFieldNums, field.number)
		}
	}
	return record, nil
}
