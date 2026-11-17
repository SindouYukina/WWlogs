package capture

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

type RawBuffSnapshot struct {
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

type BuffLifecycleAuditRow struct {
	SchemaVersion           int       `json:"schemaVersion"`
	EventType               string    `json:"eventType"`
	SessionNumber           int       `json:"sessionNumber"`
	MessageID               uint16    `json:"messageId"`
	Timestamp               time.Time `json:"timestamp"`
	CommandID               uint16    `json:"commandId"`
	InnerMessageFieldNumber *uint64   `json:"innerMessageFieldNumber,omitempty"`
	// MessagePath is the wire path of an inner message for the Development research outputs, which derive it from
	// CommandID and InnerMessageFieldNumber when they write the row (buffMessagePath); the core leaves it empty.
	MessagePath            string           `json:"messagePath,omitempty"`
	Direction              string           `json:"direction"`
	MessageName            string           `json:"messageName"`
	EventKind              string           `json:"eventKind"`
	EventTypeStatus        ResolutionStatus `json:"eventTypeStatus"`
	SchemaBasis            string           `json:"schemaBasis"`
	TargetEntityID         *uint64          `json:"targetEntityIdRaw,omitempty"`
	TargetStatus           ResolutionStatus `json:"targetSemanticsStatus"`
	TargetEvidence         string           `json:"targetEvidence,omitempty"`
	BuffID                 *uint64          `json:"buffId,omitempty"`
	BuffDefinitionStatus   ResolutionStatus `json:"buffDefinitionStatus,omitempty"`
	BuffDefinitionTable    string           `json:"buffDefinitionTable,omitempty"`
	BuffDefinitionID       *uint64          `json:"buffDefinitionId,omitempty"`
	BuffDefinitionEvidence string           `json:"buffDefinitionEvidence,omitempty"`
	ID                     *uint64          `json:"id,omitempty"`
	// BuffOwners names the features that list the linked Buff id (typed, apply Id or handle-inherited) in an explicit reference table.
	BuffOwners *BuffOwnersJSON `json:"buffOwners,omitempty"`
	// GenericID* link the untyped Id of apply-type messages to buff.id. They are separate from the
	// typed BuffDefinition* fields: the field is not named BuffId, so the basis is capture-internal evidence.
	GenericIDDefinitionStatus   ResolutionStatus `json:"genericIdDefinitionStatus,omitempty"`
	GenericIDDefinitionTable    string           `json:"genericIdDefinitionTable,omitempty"`
	GenericIDDefinitionID       *uint64          `json:"genericIdDefinitionId,omitempty"`
	GenericIDDefinitionEvidence string           `json:"genericIdDefinitionEvidence,omitempty"`
	// HandleLinked* tie a handle-only event (duration, stack, effect removal) to the apply event that created the handle.
	HandleLinkedID                         *uint64          `json:"handleLinkedId,omitempty"`
	HandleLinkedStatus                     ResolutionStatus `json:"handleLinkedStatus,omitempty"`
	HandleLinkedEvidence                   string           `json:"handleLinkedEvidence,omitempty"`
	ConfBuffID                             *uint64          `json:"confBuffId,omitempty"`
	InstigatorID                           *uint64          `json:"instigatorIdRaw,omitempty"`
	InstigatorStatus                       ResolutionStatus `json:"instigatorSemanticsStatus"`
	InstigatorEvidence                     string           `json:"instigatorEvidence,omitempty"`
	InstigatorCharacterCandidateID         *uint64          `json:"instigatorCharacterCandidateId,omitempty"`
	InstigatorCharacterCandidateName       string           `json:"instigatorCharacterCandidateName,omitempty"`
	InstigatorCharacterCandidateStatus     ResolutionStatus `json:"instigatorCharacterCandidateStatus"`
	InstigatorCharacterCandidateNameStatus ResolutionStatus `json:"instigatorCharacterCandidateNameStatus"`
	InstigatorCharacterCandidateEvidence   string           `json:"instigatorCharacterCandidateEvidence,omitempty"`
	ProviderStatus                         ResolutionStatus `json:"providerStatus"`
	ProviderNameStatus                     ResolutionStatus `json:"providerNameStatus"`
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
	RequestLinkStatus   ResolutionStatus `json:"requestLinkStatus,omitempty"`
	RequestLinkEvidence string           `json:"requestLinkEvidence,omitempty"`
	// CombatClass classifies the effect the row is about (its apply Id or the Id inherited through
	// the handle); CombatClassBasis names the evidence. Absent when no effect Id is known.
	CombatClass      EffectCombatClass `json:"combatClass,omitempty"`
	CombatClassBasis string            `json:"combatClassBasis,omitempty"`
	// ConsoleSuppressed marks a stack report that changed neither the stack nor the linked
	// effect; it still reaches the tracker and JSONL.
	ConsoleSuppressed bool `json:"-"`
	// PlayerRelated reports that the holder or the source is a local-team entity.
	PlayerRelated bool `json:"-"`
}

// classifyLifecycleRow classifies a row from the definition class of its effect and the relations of
// its holder and source as known now.
func (p *Processor) classifyLifecycleRow(row *BuffLifecycleAuditRow, class EffectCombatClass, basis string, holder uint64, hasHolder bool, source uint64, hasSource bool) {
	parties := effectParties{holder: EffectRelationUnknown, source: EffectRelationUnknown}
	if hasHolder {
		parties = p.effectPartiesFor(holder, source, hasSource)
	}
	row.CombatClass, row.CombatClassBasis = classifyEffectInstance(class, basis, parties)
	row.PlayerRelated = localEffectRelation(parties.holder) || localEffectRelation(parties.source)
}

const BuffLifecycleSchemaVersion = 6

func DecodeBuffLifecycleMessage(frame GameFrame, body []byte) (BuffLifecycleAuditRow, error) {
	return decodeBuffLifecycleMessageForCommand(frame, body, frame.CommandID, nil)
}

func decodeBuffLifecycleMessageForCommand(frame GameFrame, body []byte, schemaCommandID uint16, innerFieldNumber *uint64) (BuffLifecycleAuditRow, error) {
	row := BuffLifecycleAuditRow{
		SchemaVersion: BuffLifecycleSchemaVersion, EventType: "BUFF_LIFECYCLE", MessageID: frame.CommandID,
		Timestamp: frame.Timestamp, CommandID: frame.CommandID, Direction: frame.Direction,
		EventTypeStatus: ResolutionCorrelated, TargetStatus: ResolutionUnknown, InstigatorStatus: ResolutionUnknown,
		InstigatorCharacterCandidateStatus: ResolutionUnknown, InstigatorCharacterCandidateNameStatus: ResolutionUnknown,
		ProviderStatus: ResolutionUnknown, ProviderNameStatus: ResolutionUnknown,
		SchemaBasis: ProtocolSchemaBasis(),
	}
	if innerFieldNumber != nil {
		fieldNumber := *innerFieldNumber
		row.InnerMessageFieldNumber = &fieldNumber
	}
	switch schemaCommandID {
	case CmdApplyGameplayEffectNotify:
		row.MessageName, row.EventKind = "ApplyGameplayEffectNotify", "GAMEPLAY_EFFECT_APPLY"
	case CmdRemoveGameplayEffectNotify:
		row.MessageName, row.EventKind = "RemoveGameplayEffectNotify", "GAMEPLAY_EFFECT_REMOVE"
	case CmdRemoveGameplayEffectPush:
		row.MessageName, row.EventKind = "RemoveGameplayEffectPush", "GAMEPLAY_EFFECT_REMOVE"
	case CmdSimpleCombatEntityBuffUpdateNotify:
		row.MessageName, row.EventKind = "SimpleCombatEntityBuffUpdateNotify", "SIMPLE_BUFF_UPDATE"
	case CmdSimpleCombatEntityBuffLayerCountNotify:
		row.MessageName, row.EventKind = "SimpleCombatEntityBuffLayerCountNotify", "SIMPLE_BUFF_LAYER_COUNT_CHANGE"
	case CmdBuffStackCountNotify:
		row.MessageName, row.EventKind = "BuffStackCountNotify", "BUFF_STACK_COUNT_CHANGE"
	case CmdBuffDurationNotify:
		row.MessageName, row.EventKind = "BuffDurationNotify", "BUFF_DURATION_CHANGE"
	case CmdApplyBuffS2cRequestNotify:
		row.MessageName, row.EventKind = "ApplyBuffS2cRequestNotify", "BUFF_APPLY_REQUEST_NOTIFY"
	case CmdRemoveBuffS2cRequestNotify:
		row.MessageName, row.EventKind = "RemoveBuffS2cRequestNotify", "BUFF_REMOVE_REQUEST_NOTIFY"
	case CmdRemoveBuffByIDS2cRequestNotify:
		row.MessageName, row.EventKind = "RemoveBuffByIdS2cRequestNotify", "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY"
	default:
		return row, fmt.Errorf("unsupported Buff lifecycle schema command %d", schemaCommandID)
	}
	fields, err := ParseProtoFields(body)
	if err != nil {
		return row, fmt.Errorf("parse %s: %w", row.MessageName, err)
	}
	for _, field := range fields {
		var uintTarget **uint64
		var boolTarget **bool
		var floatTarget **float32
		switch schemaCommandID {
		case CmdApplyGameplayEffectNotify:
			switch field.Number {
			case FieldApplyGameplayEffectNotifyHandle:
				uintTarget = &row.HandleID
			case FieldApplyGameplayEffectNotifyID:
				uintTarget = &row.ID
			case fieldApplyGameplayEffectNotifyLevel:
				uintTarget = &row.Level
			case FieldApplyGameplayEffectNotifyEntityID:
				uintTarget = &row.TargetEntityID
			case FieldApplyGameplayEffectNotifyInstigatorID:
				uintTarget = &row.InstigatorID
			case fieldApplyGameplayEffectNotifyApplyType:
				uintTarget = &row.ApplyType
			case FieldApplyGameplayEffectNotifyDuration:
				floatTarget = &row.DurationSeconds
			case fieldApplyGameplayEffectNotifyIsActive:
				boolTarget = &row.IsActive
			case fieldApplyGameplayEffectNotifyServerID:
				uintTarget = &row.ServerID
			case FieldApplyGameplayEffectNotifyStackCount:
				uintTarget = &row.StackCount
			case FieldApplyGameplayEffectNotifyLeftDuration:
				floatTarget = &row.RemainingDuration
			case FieldApplyGameplayEffectNotifyConfBuffID:
				uintTarget = &row.ConfBuffID
			}
		case CmdRemoveGameplayEffectNotify:
			switch field.Number {
			case FieldRemoveGameplayEffectNotifyHandle:
				uintTarget = &row.HandleID
			case FieldRemoveGameplayEffectNotifyEntityID:
				uintTarget = &row.TargetEntityID
			case FieldRemoveGameplayEffectNotifyInstigatorID:
				uintTarget = &row.InstigatorID
			}
		case CmdRemoveGameplayEffectPush:
			switch field.Number {
			case FieldRemoveGameplayEffectPushHandle:
				uintTarget = &row.HandleID
			case FieldRemoveGameplayEffectPushEntityID:
				uintTarget = &row.TargetEntityID
			case FieldRemoveGameplayEffectPushIsPrematureRemoval:
				boolTarget = &row.IsPrematureRemoval
			case fieldRemoveGameplayEffectPushReason:
				uintTarget = &row.Reason
			case fieldRemoveGameplayEffectPushInstigatorID:
				uintTarget = &row.InstigatorID
			}
		case CmdSimpleCombatEntityBuffUpdateNotify:
			switch field.Number {
			case FieldSimpleCombatEntityBuffUpdateNotifyEntityID:
				uintTarget = &row.TargetEntityID
			case fieldSimpleCombatEntityBuffUpdateNotifyIsBuffAdd:
				boolTarget = &row.IsBuffAdd
			case fieldSimpleCombatEntityBuffUpdateNotifyBuffID:
				uintTarget = &row.BuffID
			}
		case CmdSimpleCombatEntityBuffLayerCountNotify:
			switch field.Number {
			case fieldSimpleCombatEntityBuffLayerCountNotifyEntityID:
				uintTarget = &row.TargetEntityID
			case fieldSimpleCombatEntityBuffLayerCountNotifyLayerCount:
				uintTarget = &row.StackCount
			case fieldSimpleCombatEntityBuffLayerCountNotifyBuffID:
				uintTarget = &row.BuffID
			}
		case CmdBuffStackCountNotify:
			switch field.Number {
			case FieldBuffStackCountNotifyHandleID:
				uintTarget = &row.HandleID
			case FieldBuffStackCountNotifyNewStackCount:
				uintTarget = &row.StackCount
			case FieldBuffStackCountNotifyDuration:
				floatTarget = &row.DurationSeconds
			case FieldBuffStackCountNotifyLeftDuration:
				floatTarget = &row.RemainingDuration
			case FieldBuffStackCountNotifyInstigatorID:
				uintTarget = &row.InstigatorID
			case fieldBuffStackCountNotifyNotRefreshDuration:
				boolTarget = &row.NotRefreshDuration
			case fieldBuffStackCountNotifyNotRefreshPeriod:
				boolTarget = &row.NotRefreshPeriod
			}
		case CmdBuffDurationNotify:
			switch field.Number {
			case FieldBuffDurationNotifyHandleID:
				uintTarget = &row.HandleID
			case FieldBuffDurationNotifyDuration:
				floatTarget = &row.DurationSeconds
			case FieldBuffDurationNotifyLeftDuration:
				floatTarget = &row.RemainingDuration
			}
		case CmdApplyBuffS2cRequestNotify:
			switch field.Number {
			case FieldApplyBuffS2cRequestNotifyID:
				uintTarget = &row.ID
			case fieldApplyBuffS2cRequestNotifyLevel:
				uintTarget = &row.Level
			case FieldApplyBuffS2cRequestNotifyInstigatorID:
				uintTarget = &row.InstigatorID
			case fieldApplyBuffS2cRequestNotifyApplyType:
				uintTarget = &row.ApplyType
			case fieldApplyBuffS2cRequestNotifyDuration:
				floatTarget = &row.DurationSeconds
			case fieldApplyBuffS2cRequestNotifyServerID:
				uintTarget = &row.ServerID
			case fieldApplyBuffS2cRequestNotifyStackCount:
				uintTarget = &row.StackCount
			case fieldApplyBuffS2cRequestNotifyIsIterable:
				boolTarget = &row.IsIterable
			case fieldApplyBuffS2cRequestNotifyReason:
				uintTarget = &row.Reason
			}
		case CmdRemoveBuffS2cRequestNotify:
			switch field.Number {
			case fieldRemoveBuffS2cRequestNotifyHandle:
				uintTarget = &row.HandleID
			case fieldRemoveBuffS2cRequestNotifyStackCount:
				uintTarget = &row.StackCount
			case fieldRemoveBuffS2cRequestNotifyReason:
				uintTarget = &row.Reason
			case fieldRemoveBuffS2cRequestNotifyInstigatorID:
				uintTarget = &row.InstigatorID
			}
		case CmdRemoveBuffByIDS2cRequestNotify:
			switch field.Number {
			case FieldRemoveBuffByIDS2cRequestNotifyBuffID:
				uintTarget = &row.BuffID
			case fieldRemoveBuffByIDS2cRequestNotifyStackCount:
				uintTarget = &row.StackCount
			case fieldRemoveBuffByIDS2cRequestNotifyReason:
				uintTarget = &row.Reason
			case FieldRemoveBuffByIDS2cRequestNotifyInstigatorID:
				uintTarget = &row.InstigatorID
			}
		}
		if uintTarget != nil {
			if field.Wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint", row.MessageName, field.Number, field.Wire)
			}
			value := field.Value
			*uintTarget = &value
		} else if boolTarget != nil {
			if field.Wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint bool", row.MessageName, field.Number, field.Wire)
			}
			value := field.Value != 0
			*boolTarget = &value
		} else if floatTarget != nil {
			if field.Wire != 5 || len(field.Data) != 4 {
				return row, fmt.Errorf("%s field %d has wire type %d, want fixed32 float", row.MessageName, field.Number, field.Wire)
			}
			value := math.Float32frombits(binary.LittleEndian.Uint32(field.Data))
			*floatTarget = &value
		} else {
			row.UnknownFieldNumbers = append(row.UnknownFieldNumbers, field.Number)
		}
	}
	if row.InstigatorID != nil {
		row.InstigatorEvidence = "Raw InstigatorId field of the " + CurrentProtocol() + "; provider semantics are UNKNOWN"
		row.ProviderEvidence = "Provider status UNKNOWN: InstigatorId semantics and time-scoped active actor linkage are unverified"
	}
	if row.TargetEntityID != nil {
		row.TargetStatus = ResolutionUnknown
		row.TargetEvidence = "Raw target candidate field of the " + CurrentProtocol() + "; target semantics are UNKNOWN"
	}
	return row, nil
}

func (p *Processor) HandleBuffLifecycleMessage(frame GameFrame, body []byte) error {
	row, err := DecodeBuffLifecycleMessage(frame, body)
	if err != nil {
		return err
	}
	row.SessionNumber = p.SessionNumber
	p.linkBuffLifecycleDefinition(&row)
	p.linkBuffLifecycleGenericAndHandle(&row)
	p.linkBuffLifecycleOwners(&row)
	if p.observer != nil {
		p.observer.WriteBuffLifecycleRow(row)
	}
	p.observeEffectLifecycle(row)
	if p.observer != nil {
		p.observer.WriteStructuredEventJSONL(row)
	}
	if p.observer != nil {
		p.observer.WriteBuffLifecycleConsole(row)
	}
	return nil
}

var combatNotifyBuffSchemaCommands = map[uint64]uint16{
	FieldCombatNotifyDataApplyGameplayEffectNotify:      CmdApplyGameplayEffectNotify,      // ApplyGameplayEffectNotify
	FieldCombatNotifyDataRemoveGameplayEffectNotify:     CmdRemoveGameplayEffectNotify,     // RemoveGameplayEffectNotify
	FieldCombatNotifyDataBuffStackCountNotify:           CmdBuffStackCountNotify,           // BuffStackCountNotify
	FieldCombatNotifyDataBuffDurationNotify:             CmdBuffDurationNotify,             // BuffDurationNotify
	FieldCombatNotifyDataApplyBuffS2CRequestNotify:      CmdApplyBuffS2cRequestNotify,      // ApplyBuffS2CRequestNotify
	fieldCombatNotifyDataRemoveBuffS2CRequestNotify:     CmdRemoveBuffS2cRequestNotify,     // RemoveBuffS2CRequestNotify
	FieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify: CmdRemoveBuffByIDS2cRequestNotify, // RemoveBuffByIdS2CRequestNotify
}

func (p *Processor) HandleCombatReceivePackNotify(frame GameFrame, body []byte) error {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse CombatReceivePackNotify: %w", err)
	}
	for _, field := range fields {
		if field.Number != FieldCombatReceivePackNotifyData || field.Wire != 2 { // CombatReceivePackNotify.Data
			continue
		}
		entryFields, err := ParseProtoFields(field.Data)
		if err != nil {
			return fmt.Errorf("parse CombatReceivePackNotify.Data item: %w", err)
		}
		for _, entry := range entryFields {
			if entry.Number != FieldCombatReceiveDataCombatNotifyData || entry.Wire != 2 { // CombatReceiveData.CombatNotifyData
				continue
			}
			notifications, err := ParseProtoFields(entry.Data)
			if err != nil {
				return fmt.Errorf("parse CombatReceiveData.CombatNotifyData: %w", err)
			}
			var commonEntityID, commonOriginator, commonMessageID *uint64
			for _, notification := range notifications {
				if notification.Number == FieldCombatNotifyDataCombatCommon && notification.Wire == 2 { // CombatNotifyData.CombatCommon
					commonEntityID, commonOriginator, commonMessageID, err = decodeCombatCommonContext(notification.Data)
					if err != nil {
						return fmt.Errorf("parse CombatCommon: %w", err)
					}
				}
			}
			for _, notification := range notifications {
				if p.Census != nil {
					p.Census.ObserveCombatNotifyField(notification.Number)
				}
				if p.observer != nil {
					p.observer.AuditCombatNotify(frame, notification, commonEntityID, commonOriginator, commonMessageID)
				}
				if notification.Number == FieldCombatNotifyDataDamageExecuteNotify && notification.Wire == 2 {
					p.handleDamageExecuteNotify(frame, notification.Data, p.frameNotifyOrdinal)
					p.frameNotifyOrdinal++
					continue
				}
				if notification.Number == FieldCombatNotifyDataPlayerBattleStateChangeNotify && notification.Wire == 2 {
					if err := p.handlePlayerBattleState(frame, combatNotifyContainer(frame.CommandID), notification.Data); err != nil {
						return err
					}
					continue
				}
				schemaCommandID, supported := combatNotifyBuffSchemaCommands[notification.Number]
				if !supported || notification.Wire != 2 {
					continue
				}
				if err := p.HandleInnerBuffLifecycleMessage(frame, notification.Number, schemaCommandID, notification.Data, commonEntityID, commonOriginator, commonMessageID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func decodeCombatCommonContext(body []byte) (entityID, originatorID, messageID *uint64, err error) {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, field := range fields {
		if field.Wire != 0 {
			continue
		}
		value := field.Value
		switch field.Number {
		case FieldCombatCommonMessageID:
			messageID = &value
		case fieldCombatCommonOriginator:
			originatorID = &value
		case FieldCombatCommonEntityID:
			entityID = &value
		}
	}
	return entityID, originatorID, messageID, nil
}

func (p *Processor) HandleInnerBuffLifecycleMessage(frame GameFrame, innerFieldNumber uint64, schemaCommandID uint16, body []byte, commonEntityID, commonOriginator, commonMessageID *uint64) error {
	row, err := decodeBuffLifecycleMessageForCommand(frame, body, schemaCommandID, &innerFieldNumber)
	if err != nil {
		return err
	}
	row.CombatCommonEntityID = commonEntityID
	row.CombatCommonOriginator = commonOriginator
	row.CombatCommonMessageID = commonMessageID
	if (row.EventKind == "GAMEPLAY_EFFECT_APPLY" || row.EventKind == "GAMEPLAY_EFFECT_REMOVE") &&
		row.TargetEntityID != nil && commonEntityID != nil && *row.TargetEntityID == *commonEntityID {
		row.TargetStatus = ResolutionCorrelated
		row.TargetEvidence = "Gameplay-effect TargetEntityId matches CombatCommon.EntityId on this event"
	}
	p.linkLifecycleInstigator(&row, row.EventKind == "GAMEPLAY_EFFECT_APPLY" && row.TargetStatus == ResolutionCorrelated,
		"this event's explicit target matches CombatCommon.EntityId")
	row.SessionNumber = p.SessionNumber
	p.rememberServerBuffRequest(row)
	p.linkBuffLifecycleDefinition(&row)
	p.linkBuffLifecycleGenericAndHandle(&row)
	p.linkBuffLifecycleOwners(&row)
	if p.observer != nil {
		p.observer.WriteBuffLifecycleRow(row)
	}
	if p.observer != nil {
		p.observer.ObserveBuffInterval(row)
	}
	p.observeEffectLifecycle(row)
	if p.observer != nil {
		p.observer.WriteStructuredEventJSONL(row)
	}
	if p.observer != nil {
		p.observer.WriteBuffLifecycleConsole(row)
	}
	return nil
}

// linkLifecycleInstigator names the character behind a raw InstigatorId. The provider
// fields are set only when providerEligible (the row's holder is established) and the
// instigator resolves to a verified character; holderBasis completes the evidence text.
func (p *Processor) linkLifecycleInstigator(row *BuffLifecycleAuditRow, providerEligible bool, holderBasis string) {
	if row.InstigatorID == nil {
		return
	}
	identity := ResolveEntityIdentity(p.Entities, p.Masters, *row.InstigatorID)
	if !identity.HasCharacter {
		return
	}
	characterID := identity.Character.ID
	row.InstigatorCharacterCandidateID = &characterID
	row.InstigatorCharacterCandidateName = identity.Character.Name
	row.InstigatorCharacterCandidateStatus = identity.CharacterStatus
	row.InstigatorCharacterCandidateNameStatus = ResolutionStatus(identity.Character.NameStatus)
	row.InstigatorCharacterCandidateEvidence = "Raw InstigatorId matches a session runtime EntityId that resolves to a Character master; provider attribution requires the correlated gameplay-effect path"
	if !providerEligible || identity.CharacterStatus != ResolutionVerified {
		return
	}
	providerID := identity.Character.ID
	row.ProviderCharacterID = &providerID
	row.ProviderStatus = ResolutionCorrelated
	row.InstigatorStatus = ResolutionCorrelated
	row.ProviderEvidence = row.MessageName + " InstigatorId resolves through this session's runtime Entity to a verified Character; " + holderBasis
	row.InstigatorEvidence = row.ProviderEvidence
	if identity.Character.Name != "" && identity.Character.NameStatus == string(ResolutionVerified) {
		row.ProviderName = identity.Character.Name
		row.ProviderNameStatus = ResolutionCorrelated
	}
}

// linkBuffLifecycleOwners attaches the named owners of the Buff id this row is tied to.
func (p *Processor) linkBuffLifecycleOwners(row *BuffLifecycleAuditRow) {
	if row == nil || p.Masters == nil {
		return
	}
	if id, ok := row.buffOwnerLookupID(); ok {
		row.BuffOwners = ResolveBuffOwners(p.Masters, id)
	} else if row.BuffID != nil {
		// No static definition: only an observed-role owner can exist for the raw BuffId.
		row.BuffOwners = ResolveBuffOwners(p.Masters, *row.BuffID)
	}
}

const maxBuffHandles = 1 << 16

// BuffHandleKey scopes a lifecycle handle id to the session and the entity that holds the buff.
type BuffHandleKey struct {
	Session  int
	EntityID uint64
	HandleID uint64
}

type buffHandleEntry struct {
	ID         uint64
	Definition bool
	// client marks a handle created by a client apply report.
	client bool
	// class and basis classify the definition; source is the apply row's instigator. Later rows
	// re-evaluate the runtime rules with the relations known at their own time.
	class     EffectCombatClass
	basis     string
	source    uint64
	hasSource bool
	// stack is the last stack count reported for a client handle; it only decides console output.
	stack    uint64
	hasStack bool
}

func LifecycleEntity(row *BuffLifecycleAuditRow) (uint64, bool) {
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
func (p *Processor) linkBuffLifecycleGenericAndHandle(row *BuffLifecycleAuditRow) {
	if row == nil {
		return
	}
	entityID, hasEntity := LifecycleEntity(row)
	switch row.EventKind {
	case "BUFF_APPLY_REQUEST_NOTIFY", "GAMEPLAY_EFFECT_APPLY", "CLIENT_EFFECT_APPLY_REPORT":
		if row.ID == nil {
			return
		}
		row.GenericIDDefinitionStatus = ResolutionUnknown
		row.GenericIDDefinitionEvidence = "No exact normalized buff.id row for the untyped Id; raw Id unchanged"
		found := p.Masters != nil && p.Masters.HasBuffDefinition(*row.ID)
		if found {
			definitionID := *row.ID
			row.GenericIDDefinitionStatus = ResolutionCorrelated
			row.GenericIDDefinitionTable = "buff"
			row.GenericIDDefinitionID = &definitionID
			row.GenericIDDefinitionEvidence = "The untyped Id of this apply message has an exact normalized buff.id row; namespace evidence is capture-internal (see the lifecycle Id namespace audit), not a typed BuffId field. Static row reference only: application effect and uptime are unresolved"
		}
		definitionClass, definitionBasis := p.effectDefinitionClass(*row.ID)
		var source uint64
		if row.InstigatorID != nil {
			source = *row.InstigatorID
		}
		p.classifyLifecycleRow(row, definitionClass, definitionBasis, entityID, hasEntity, source, row.InstigatorID != nil && source != 0)
		if (row.EventKind == "GAMEPLAY_EFFECT_APPLY" || row.EventKind == "CLIENT_EFFECT_APPLY_REPORT") && row.HandleID != nil && hasEntity {
			if p.BuffHandles == nil {
				p.BuffHandles = make(map[BuffHandleKey]buffHandleEntry)
			}
			key := BuffHandleKey{p.SessionNumber, entityID, *row.HandleID}
			if previous, exists := p.BuffHandles[key]; exists {
				if previous.ID != *row.ID {
					p.BuffHandles[key] = buffHandleEntry{ID: 0}
				}
			} else {
				p.buffHandleOrder = append(p.buffHandleOrder, key)
				entry := buffHandleEntry{ID: *row.ID, Definition: found, client: row.EventKind == "CLIENT_EFFECT_APPLY_REPORT",
					class: definitionClass, basis: definitionBasis, source: source, hasSource: row.InstigatorID != nil && source != 0}
				if row.StackCount != nil {
					entry.stack, entry.hasStack = *row.StackCount, true
				}
				p.BuffHandles[key] = entry
			}
			for len(p.buffHandleOrder) > maxBuffHandles {
				delete(p.BuffHandles, p.buffHandleOrder[0])
				p.buffHandleOrder = p.buffHandleOrder[1:]
			}
		}
	case "BUFF_DURATION_CHANGE", "BUFF_STACK_COUNT_CHANGE", "GAMEPLAY_EFFECT_REMOVE", "BUFF_REMOVE_REQUEST_NOTIFY",
		"CLIENT_EFFECT_STACK_REPORT", "CLIENT_EFFECT_REMOVE_REPORT":
		if row.HandleID == nil || !hasEntity {
			return
		}
		row.HandleLinkedStatus = ResolutionUnknown
		row.HandleLinkedEvidence = "No earlier ApplyGameplayEffect with this (session, entity, handle) was observed"
		key := BuffHandleKey{p.SessionNumber, entityID, *row.HandleID}
		entry, ok := p.BuffHandles[key]
		if ok && entry.ID != 0 && (row.EventKind == "CLIENT_EFFECT_STACK_REPORT" || row.EventKind == "BUFF_STACK_COUNT_CHANGE") && row.StackCount != nil {
			// A stack message repeats on every refresh; only a changed count is console-worthy.
			row.ConsoleSuppressed = entry.hasStack && entry.stack == *row.StackCount
			entry.stack, entry.hasStack = *row.StackCount, true
			p.BuffHandles[key] = entry
		}
		switch {
		case !ok:
		case entry.ID == 0:
			row.HandleLinkedEvidence = "The handle was applied more than once with different ids; the link is rejected"
		default:
			linked := entry.ID
			row.HandleLinkedID = &linked
			row.HandleLinkedEvidence = "Same (session, entity, handle) as an earlier ApplyGameplayEffectNotify; the Id is inherited from that apply message"
			if entry.client {
				row.HandleLinkedEvidence = "Same (session, entity, handle) as an earlier client ApplyGameplayEffectPush; the Id is inherited from that apply report"
			}
			row.HandleLinkedStatus = ResolutionCorrelated
			p.classifyLifecycleRow(row, entry.class, entry.basis, entityID, true, entry.source, entry.hasSource)
			if !entry.Definition {
				row.HandleLinkedStatus = ResolutionUnknown
				row.HandleLinkedEvidence += "; that Id has no exact buff.id row"
			}
		}
	}
}

// linkBuffLifecycleDefinition resolves only explicitly decoded BuffId fields to exact buff.Id rows.
func (p *Processor) linkBuffLifecycleDefinition(row *BuffLifecycleAuditRow) {
	if row == nil || row.BuffID == nil {
		return
	}
	row.BuffDefinitionStatus = ResolutionUnknown
	row.BuffDefinitionEvidence = "No exact normalized buff.id row has been linked; raw BuffId remains unchanged"
	if p.Masters != nil && p.Masters.HasBuffDefinition(*row.BuffID) {
		definitionID := *row.BuffID
		row.BuffDefinitionStatus = ResolutionCorrelated
		row.BuffDefinitionTable = "buff"
		row.BuffDefinitionID = &definitionID
		row.BuffDefinitionEvidence = "The typed BuffId field has an exact normalized buff.id row; this is a static row reference and does not by itself prove that an effect was applied or its uptime"
	}
}

func (p *Processor) HandleActiveRequest(token uint16, body []byte) error {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse EntityActiveRequest: %w", err)
	}
	if err := p.Entities.observeActiveRequest(body); err != nil {
		return err
	}
	request := PendingActiveRequest{}
	for _, field := range fields {
		if field.Number == FieldEntityActiveRequestEntityID && field.Wire == 0 {
			request.EntityID = field.Value
			request.HasEntityID = field.Value != 0
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

func (p *Processor) HandleActiveResponse(frame GameFrame, body []byte) error {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse EntityActiveResponse: %w", err)
	}
	request, matched := p.activeRequests[frame.Token]
	delete(p.activeRequests, frame.Token)
	p.Stats.BuffSnapshotResponses++
	for _, field := range fields {
		if field.Number != FieldEntityActiveResponseComponentPbs || field.Wire != 2 {
			continue
		}
		components, err := ParseProtoFields(field.Data)
		if err != nil {
			return fmt.Errorf("parse EntityActiveResponse ComponentPb: %w", err)
		}
		identityComponents := make([]ProtoField, 0, 2)
		partComponents := make([]ProtoField, 0, 1)
		fsmComponents := make([]ProtoField, 0, 1)
		for _, component := range components {
			if component.Wire != 2 {
				continue
			}
			if isRuntimeIdentityComponent(component.Number) {
				identityComponents = append(identityComponents, component)
			}
			if component.Number == FieldEntityComponentPbPartComponent {
				partComponents = append(partComponents, component)
			}
			if component.Number == fieldEntityComponentPbEntityFsmComponentPb {
				fsmComponents = append(fsmComponents, component)
			}
			switch component.Number {
			case FieldEntityComponentPbSummonerComponent:
				p.Stats.ActiveResponseSummonerComponents++
			case FieldEntityComponentPbMonsterCaptureComponent:
				p.Stats.ActiveResponseMonsterComponents++
			case fieldEntityComponentPbPlayerFollowerSelfComponentPb:
				p.Stats.ActiveResponseFollowerComponents++
			}
			if component.Number != FieldEntityComponentPbFightBuffComponent {
				continue
			}
			buffs, err := ParseProtoFields(component.Data)
			if err != nil {
				return fmt.Errorf("parse ComponentPb.vys: %w", err)
			}
			for _, buffField := range buffs {
				if buffField.Number != FieldFightBuffComponentPbFightBuffInfos || buffField.Wire != 2 {
					continue
				}
				record, err := parseRawBuffSnapshot(buffField.Data)
				if err != nil {
					return fmt.Errorf("parse xAs.SIs buff record: %w", err)
				}
				p.Stats.ObservedBuffEvents++
				p.observeEffectSnapshot(frame, record, request.EntityID, matched && request.HasEntityID)
				if p.observer != nil {
					p.observer.RecordBuffSnapshotDiagnostics(frame, record, request, matched)
				}
			}
		}
		if len(identityComponents) > 0 {
			entityID := uint64(0)
			if matched && request.HasEntityID {
				entityID = request.EntityID
			}
			observed, err := p.Entities.applyActiveResponseIdentityComponents(entityID, identityComponents)
			if err != nil {
				return fmt.Errorf("parse EntityActiveResponse identity components: %w", err)
			}
			p.Stats.ActiveResponseIdentityComponents += observed
		}
		if len(fsmComponents) > 0 && matched && request.HasEntityID {
			if err := p.Entities.applyActiveResponseFsmComponents(request.EntityID, fsmComponents); err != nil {
				return fmt.Errorf("parse EntityActiveResponse FSM components: %w", err)
			}
		}
		if len(partComponents) > 0 {
			entityID := uint64(0)
			if matched && request.HasEntityID {
				entityID = request.EntityID
			}
			if entityID == 0 {
				p.Entities.invalidatePartComponents()
			} else if _, err := p.Entities.applyActiveResponsePartComponents(entityID, partComponents); err != nil {
				return fmt.Errorf("parse EntityActiveResponse part components: %w", err)
			}
		}
	}
	p.refreshEffectRelations()
	return nil
}

func parseRawBuffSnapshot(data []byte) (RawBuffSnapshot, error) {
	fields, err := ParseProtoFields(data)
	if err != nil {
		return RawBuffSnapshot{}, err
	}
	var record RawBuffSnapshot
	for _, field := range fields {
		if field.Wire == 0 {
			switch field.Number {
			case FieldFightBuffInformationHandleID:
				record.HandleID, record.HasHandleID = field.Value, true
			case FieldFightBuffInformationBuffID:
				record.ID, record.HasID = field.Value, true
			case FieldFightBuffInformationLevel:
				record.Level, record.HasLevel = field.Value, true
			case FieldFightBuffInformationStackCount:
				record.Stack, record.HasStack = field.Value, true
			case FieldFightBuffInformationInstigatorID:
				record.InstigatorID, record.HasInstigatorID = field.Value, true
			case FieldFightBuffInformationEntityID:
				record.EntityID, record.HasEntityID = field.Value, true
			case FieldFightBuffInformationApplyType:
				record.ApplyType, record.HasApplyType = field.Value, true
			case FieldFightBuffInformationIsActive:
				record.Active, record.HasActive = field.Value != 0, true
			case fieldFightBuffInformationServerID:
				record.ServerID, record.HasServerID = field.Value, true
			case fieldFightBuffInformationMessageID:
				record.MessageID, record.HasMessageID = field.Value, true
			case FieldFightBuffInformationConfBuffID:
				record.ConfBuffID, record.HasConfBuffID = field.Value, true
			default:
				record.UnknownFieldNums = append(record.UnknownFieldNums, field.Number)
			}
			continue
		}
		if field.Number == FieldFightBuffInformationDuration && field.Wire == 5 && len(field.Data) == 4 {
			record.Duration, record.HasDuration = math.Float32frombits(binary.LittleEndian.Uint32(field.Data)), true
		} else if field.Number == FieldFightBuffInformationLeftDuration && field.Wire == 5 && len(field.Data) == 4 {
			record.Remaining, record.HasRemaining = math.Float32frombits(binary.LittleEndian.Uint32(field.Data)), true
		} else if field.Number == FieldFightBuffInformationContext && field.Wire == 2 {
			record.Context = append(record.Context, append([]byte(nil), field.Data...))
		} else {
			record.UnknownFieldNums = append(record.UnknownFieldNums, field.Number)
		}
	}
	return record, nil
}
