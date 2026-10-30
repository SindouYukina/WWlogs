package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"
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
	// BuffSemanticStatus keeps static-key versus runtime-effect interpretation separate.
	BuffSemanticStatus        resolutionStatus `json:"buffSemanticStatus"`
	BuffSemanticEvidence      string           `json:"buffSemanticEvidence"`
	InstigatorSemantics       resolutionStatus `json:"instigatorSemantics"`
	InstigatorEvidence        string           `json:"instigatorEvidence,omitempty"`
	clientSchemaVersion      string           `json:"clientSchemaVersion"`
	WindowsSchemaVerification string           `json:"windowsSchemaVerification"`
	Buff                      rawBuffSnapshot  `json:"buff"`
}

const buffSnapshotSchemaVersion = 2

type buffLifecycleAuditRow struct {
	SchemaVersion                          int              `json:"schemaVersion"`
	EventType                              string           `json:"eventType"`
	SessionNumber                          int              `json:"sessionNumber"`
	MessageID                              uint16           `json:"messageId"`
	Timestamp                              time.Time        `json:"timestamp"`
	CommandID                              uint16           `json:"commandId"`
	InnerMessageFieldNumber                *uint64          `json:"innerMessageFieldNumber,omitempty"`
	MessagePath                            string           `json:"messagePath,omitempty"`
	Direction                              string           `json:"direction"`
	MessageName                            string           `json:"messageName"`
	EventKind                              string           `json:"eventKind"`
	EventTypeStatus                        resolutionStatus `json:"eventTypeStatus"`
	SchemaBasis                            string           `json:"schemaBasis"`
	TargetEntityID                         *uint64          `json:"targetEntityIdRaw,omitempty"`
	TargetStatus                           resolutionStatus `json:"targetSemanticsStatus"`
	TargetEvidence                         string           `json:"targetEvidence,omitempty"`
	BuffID                                 *uint64          `json:"buffId,omitempty"`
	ID                                     *uint64          `json:"id,omitempty"`
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
}

func decodeBuffLifecycleMessage(frame gameFrame, body []byte) (buffLifecycleAuditRow, error) {
	return decodeBuffLifecycleMessageForCommand(frame, body, frame.commandID, nil)
}

func decodeBuffLifecycleMessageForCommand(frame gameFrame, body []byte, schemaCommandID uint16, innerFieldNumber *uint64) (buffLifecycleAuditRow, error) {
	row := buffLifecycleAuditRow{
		SchemaVersion: 3, EventType: "BUFF_LIFECYCLE", MessageID: frame.commandID,
		Timestamp: frame.timestamp, CommandID: frame.commandID, Direction: frame.direction,
		EventTypeStatus: resolutionCorrelated, TargetStatus: resolutionUnknown, InstigatorStatus: resolutionUnknown,
		InstigatorCharacterCandidateStatus: resolutionUnknown, InstigatorCharacterCandidateNameStatus: resolutionUnknown,
		ProviderStatus: resolutionUnknown, ProviderNameStatus: resolutionUnknown,
		SchemaBasis: "Windows Global UHD 3.7.1 Aki.Protocol metadata; client version observed, schema compatibility unverified",
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
		row.InstigatorEvidence = "Raw InstigatorId field decoded from Windows Global UHD 3.7.1 Aki.Protocol metadata; provider semantics and compatibility with the observed client build are UNKNOWN"
		row.ProviderEvidence = "Provider status UNKNOWN: InstigatorId semantics and time-scoped active actor linkage are unverified"
	}
	if row.TargetEntityID != nil {
		row.TargetStatus = resolutionUnknown
		row.TargetEvidence = "Raw target candidate field decoded from Windows Global UHD 3.7.1 Aki.Protocol metadata; target semantics and compatibility with the observed client build are UNKNOWN"
	}
	return row, nil
}

func (p *processor) handleBuffLifecycleMessage(frame gameFrame, body []byte) error {
	row, err := decodeBuffLifecycleMessage(frame, body)
	if err != nil {
		return err
	}
	row.SessionNumber = p.sessionNumber
	if p.buffLifecycle != nil {
		if err := json.NewEncoder(p.buffLifecycle).Encode(row); err != nil {
			return fmt.Errorf("write Buff lifecycle row: %w", err)
		}
	}
	p.writeStructuredEventJSONL(row)
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
	if row.InstigatorID != nil {
		identity := resolveEntityIdentity(p.entities, p.masters, *row.InstigatorID)
		if identity.HasCharacter {
			characterID := identity.Character.ID
			row.InstigatorCharacterCandidateID = &characterID
			row.InstigatorCharacterCandidateName = identity.Character.Name
			row.InstigatorCharacterCandidateStatus = identity.CharacterStatus
			row.InstigatorCharacterCandidateNameStatus = resolutionStatus(identity.Character.NameStatus)
			row.InstigatorCharacterCandidateEvidence = "Raw InstigatorId matches a session runtime EntityId that resolves to a Character master; provider attribution requires the correlated gameplay-effect path"
			if row.EventKind == "GAMEPLAY_EFFECT_APPLY" &&
				row.TargetStatus == resolutionCorrelated &&
				identity.CharacterStatus == resolutionVerified {
				providerID := identity.Character.ID
				row.ProviderCharacterID = &providerID
				row.ProviderStatus = resolutionCorrelated
				row.InstigatorStatus = resolutionCorrelated
				row.ProviderEvidence = "ApplyGameplayEffectNotify InstigatorId resolves through this session's runtime Entity to a verified Character; this event's explicit target matches CombatCommon.EntityId"
				row.InstigatorEvidence = row.ProviderEvidence
				if identity.Character.Name != "" && identity.Character.NameStatus == string(resolutionVerified) {
					row.ProviderName = identity.Character.Name
					row.ProviderNameStatus = resolutionCorrelated
				}
			}
		}
	}
	row.SessionNumber = p.sessionNumber
	if p.buffLifecycle != nil {
		if err := json.NewEncoder(p.buffLifecycle).Encode(row); err != nil {
			return fmt.Errorf("write Buff lifecycle row: %w", err)
		}
	}
	p.observeBuffInterval(row)
	p.writeStructuredEventJSONL(row)
	return nil
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
		for _, component := range components {
			if component.wire != 2 {
				continue
			}
			if isRuntimeIdentityComponent(component.number) {
				identityComponents = append(identityComponents, component)
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
				if p.buffSnapshots != nil || p.combatEventsJSONL != nil || p.encounterSummary != nil {
					row := buffSnapshotAuditRow{
						SchemaVersion: buffSnapshotSchemaVersion, EventType: "BUFF_SNAPSHOT", SessionNumber: p.sessionNumber,
						MessageID: frame.commandID, Timestamp: frame.timestamp, ResponseCommandID: frame.commandID,
						ResponseDirection: frame.direction, RequestCommandID: 28481,
						FrameToken: frame.token, EntityIDStatus: resolutionUnknown,
						ComponentPath:             "EntityActiveResponse.field11(ComponentPbs[])->EntityComponentPb.field15(FightBuffComponent)->FightBuffComponentPb.field1(FightBuffInfos[])->FightBuffInformation",
						BuffIDStatus:              resolutionCorrelated,
						BuffSemanticStatus:        resolutionUnknown,
						BuffSemanticEvidence:      "FightBuffInformation.BuffId is preserved as a raw typed field; its static-definition versus runtime-effect semantics are unresolved, and ConfBuffId is kept separate when present",
						clientSchemaVersion:      "Global 3.7.1",
						WindowsSchemaVerification: "Windows Global UHD 3.7.1 Aki.Protocol metadata; client/master compatibility UNKNOWN",
						Buff:                      record,
					}
					if record.HasInstigatorID {
						row.InstigatorSemantics = resolutionCorrelated
						row.InstigatorEvidence = "client Global 3.7.1 CharacterBuffComponent compares InstigatorId with CreatureDataId and resolves it through CreatureModel.GetEntity; client/master compatibility UNKNOWN"
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
	}
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
