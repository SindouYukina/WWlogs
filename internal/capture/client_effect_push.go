package capture

import (
	"encoding/binary"
	"fmt"
	"math"
)

// The local client owns the gameplay effects of the entities it controls: it applies,
// refreshes and removes them itself and reports each change to the server inside an
// outbound CombatSendPackRequest. The server does not echo these reports back as
// notifies, so they are the only wire evidence for the local team's own effects.
// The reports are CombatSendPackRequest.Data[].Push (CombatPushData) entries; their numbers are in protocol_numbers.go.

// combatCommonContext is the CombatCommon header shared by every combat message.
type combatCommonContext struct {
	preMessageID *uint64
	messageID    *uint64
	originatorID *uint64
	entityID     *uint64
	timeStamp    *float32
}

func decodeCombatCommon(body []byte) (combatCommonContext, error) {
	var common combatCommonContext
	fields, err := ParseProtoFields(body)
	if err != nil {
		return common, err
	}
	for _, field := range fields {
		if field.Number == fieldCombatCommonTimeStamp && field.Wire == 5 && len(field.Data) == 4 {
			value := math.Float32frombits(binary.LittleEndian.Uint32(field.Data))
			common.timeStamp = &value
			continue
		}
		if field.Wire != 0 {
			continue
		}
		value := field.Value
		switch field.Number {
		case FieldCombatCommonPreMessageID:
			common.preMessageID = &value
		case FieldCombatCommonMessageID:
			common.messageID = &value
		case fieldCombatCommonOriginator:
			common.originatorID = &value
		case FieldCombatCommonEntityID:
			common.entityID = &value
		}
	}
	return common, nil
}

// HandleClientEffectPushes decodes the effect reports of one outbound CombatSendPackRequest.
func (p *Processor) HandleClientEffectPushes(frame GameFrame, body []byte) error {
	outer, err := ParseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse CombatSendPackRequest: %w", err)
	}
	for _, dataField := range outer {
		if dataField.Number != FieldCombatSendPackRequestData || dataField.Wire != 2 {
			continue
		}
		dataFields, err := ParseProtoFields(dataField.Data)
		if err != nil {
			return fmt.Errorf("parse CombatSendPackRequest.Data item: %w", err)
		}
		for _, pushField := range dataFields {
			if pushField.Number != FieldCombatSendDataPush || pushField.Wire != 2 {
				continue
			}
			pushes, err := ParseProtoFields(pushField.Data)
			if err != nil {
				return fmt.Errorf("parse CombatSendData.Push: %w", err)
			}
			var common combatCommonContext
			for _, push := range pushes {
				if push.Number == FieldCombatPushDataCombatCommon && push.Wire == 2 {
					if common, err = decodeCombatCommon(push.Data); err != nil {
						return fmt.Errorf("parse CombatCommon: %w", err)
					}
				}
			}
			for _, push := range pushes {
				if push.Wire != 2 {
					continue
				}
				if push.Number == fieldCombatPushDataRTimeStopPush {
					// A time stop of the local client: it moves the effect clock, not an effect.
					if err := p.observeRTimeStopPush(frame, push.Data, common); err != nil {
						return err
					}
					continue
				}
				switch push.Number {
				case FieldCombatPushDataApplyGameplayEffectPush, FieldCombatPushDataRemoveGameplayEffectPush, FieldCombatPushDataBuffStackCountPush,
					fieldCombatPushDataApplyBuffS2CResponsePush, fieldCombatPushDataRemoveBuffByIDS2CResponsePush:
				default:
					continue
				}
				row, err := decodeClientEffectPush(frame, push.Number, push.Data, common)
				if err != nil {
					return err
				}
				if err := p.handleClientEffectRow(row); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func decodeClientEffectPush(frame GameFrame, fieldNumber uint64, body []byte, common combatCommonContext) (BuffLifecycleAuditRow, error) {
	number := fieldNumber
	row := BuffLifecycleAuditRow{
		SchemaVersion: BuffLifecycleSchemaVersion, EventType: "BUFF_LIFECYCLE", MessageID: frame.CommandID,
		Timestamp: frame.Timestamp, CommandID: frame.CommandID, Direction: frame.Direction,
		InnerMessageFieldNumber: &number,
		EventTypeStatus:         ResolutionCorrelated, TargetStatus: ResolutionUnknown, InstigatorStatus: ResolutionUnknown,
		InstigatorCharacterCandidateStatus: ResolutionUnknown, InstigatorCharacterCandidateNameStatus: ResolutionUnknown,
		ProviderStatus: ResolutionUnknown, ProviderNameStatus: ResolutionUnknown,
		SchemaBasis:          ProtocolSchemaBasis(),
		EvidenceSource:       EffectEvidenceClientReport,
		CombatCommonEntityID: common.entityID, CombatCommonOriginator: common.originatorID, CombatCommonMessageID: common.messageID,
		CombatCommonPreMessageID: common.preMessageID, ClientTimeStamp: common.timeStamp,
	}
	switch fieldNumber {
	case FieldCombatPushDataApplyGameplayEffectPush:
		row.MessageName, row.EventKind = "ApplyGameplayEffectPush", "CLIENT_EFFECT_APPLY_REPORT"
	case FieldCombatPushDataRemoveGameplayEffectPush:
		row.MessageName, row.EventKind = "RemoveGameplayEffectPush", "CLIENT_EFFECT_REMOVE_REPORT"
	case FieldCombatPushDataBuffStackCountPush:
		row.MessageName, row.EventKind = "BuffStackCountPush", "CLIENT_EFFECT_STACK_REPORT"
	case fieldCombatPushDataApplyBuffS2CResponsePush:
		row.MessageName, row.EventKind = "ApplyBuffS2cResponsePush", "BUFF_APPLY_REQUEST_RESPONSE"
	case fieldCombatPushDataRemoveBuffByIDS2CResponsePush:
		row.MessageName, row.EventKind = "RemoveBuffByIdS2cResponsePush", "BUFF_REMOVE_BY_ID_REQUEST_RESPONSE"
	default:
		return row, fmt.Errorf("unsupported client effect push field %d", fieldNumber)
	}
	fields, err := ParseProtoFields(body)
	if err != nil {
		return row, fmt.Errorf("parse %s: %w", row.MessageName, err)
	}
	for _, field := range fields {
		var uintTarget **uint64
		var boolTarget **bool
		var floatTarget **float32
		var stringTarget *string
		switch fieldNumber {
		case FieldCombatPushDataApplyGameplayEffectPush:
			switch field.Number {
			case FieldApplyGameplayEffectPushReason:
				stringTarget = &row.ClientReasonRaw
			case FieldApplyGameplayEffectPushHandle:
				uintTarget = &row.HandleID
			case FieldApplyGameplayEffectPushStackCount:
				uintTarget = &row.StackCount
			case FieldApplyGameplayEffectPushID:
				uintTarget = &row.ID
			case fieldApplyGameplayEffectPushIsActive:
				boolTarget = &row.IsActive
			case fieldApplyGameplayEffectPushLevel:
				uintTarget = &row.Level
			case FieldApplyGameplayEffectPushDuration:
				floatTarget = &row.DurationSeconds
			case FieldApplyGameplayEffectPushInstigatorID:
				uintTarget = &row.InstigatorID
			case fieldApplyGameplayEffectPushConfBuffID:
				uintTarget = &row.ConfBuffID
			case fieldApplyGameplayEffectPushApplyType:
				uintTarget = &row.ApplyType
			case fieldApplyGameplayEffectPushServerID:
				uintTarget = &row.ServerID
			}
		case FieldCombatPushDataRemoveGameplayEffectPush:
			switch field.Number {
			case fieldRemoveGameplayEffectPushReason:
				stringTarget = &row.ClientReasonRaw
			case fieldRemoveGameplayEffectPushInstigatorID:
				uintTarget = &row.InstigatorID
			case FieldRemoveGameplayEffectPushIsPrematureRemoval:
				boolTarget = &row.IsPrematureRemoval
			case FieldRemoveGameplayEffectPushHandle:
				uintTarget = &row.HandleID
			case FieldRemoveGameplayEffectPushEntityID:
				uintTarget = &row.TargetEntityID
			}
		case FieldCombatPushDataBuffStackCountPush:
			switch field.Number {
			case FieldBuffStackCountPushHandleID:
				uintTarget = &row.HandleID
			case fieldBuffStackCountPushReason:
				stringTarget = &row.ClientReasonRaw
			case FieldBuffStackCountPushNotRefreshDuration:
				boolTarget = &row.NotRefreshDuration
			case fieldBuffStackCountPushNotRefreshPeriod:
				boolTarget = &row.NotRefreshPeriod
			case fieldBuffStackCountPushIsPrematureRemoval:
				boolTarget = &row.IsPrematureRemoval
			case FieldBuffStackCountPushInstigatorID:
				uintTarget = &row.InstigatorID
			case FieldBuffStackCountPushDuration:
				floatTarget = &row.DurationSeconds
			case FieldBuffStackCountPushNewStackCount:
				uintTarget = &row.StackCount
			}
		case fieldCombatPushDataApplyBuffS2CResponsePush:
			switch field.Number {
			case fieldApplyBuffS2cResponsePushErrorCode:
				uintTarget = &row.ErrorCode
			case fieldApplyBuffS2cResponsePushIsActive:
				boolTarget = &row.IsActive
			case fieldApplyBuffS2cResponsePushHandle:
				uintTarget = &row.HandleID
			}
		case fieldCombatPushDataRemoveBuffByIDS2CResponsePush:
			if field.Number == fieldRemoveBuffByIDS2cResponsePushErrorCode {
				uintTarget = &row.ErrorCode
			}
		}
		switch {
		case uintTarget != nil:
			if field.Wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint", row.MessageName, field.Number, field.Wire)
			}
			value := field.Value
			*uintTarget = &value
		case boolTarget != nil:
			if field.Wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint bool", row.MessageName, field.Number, field.Wire)
			}
			value := field.Value != 0
			*boolTarget = &value
		case floatTarget != nil:
			if field.Wire != 5 || len(field.Data) != 4 {
				return row, fmt.Errorf("%s field %d has wire type %d, want fixed32 float", row.MessageName, field.Number, field.Wire)
			}
			value := math.Float32frombits(binary.LittleEndian.Uint32(field.Data))
			*floatTarget = &value
		case stringTarget != nil:
			if field.Wire != 2 {
				return row, fmt.Errorf("%s field %d has wire type %d, want string", row.MessageName, field.Number, field.Wire)
			}
			*stringTarget = string(field.Data)
		default:
			row.UnknownFieldNumbers = append(row.UnknownFieldNumbers, field.Number)
		}
	}
	if row.InstigatorID != nil {
		row.InstigatorEvidence = "Raw InstigatorId field of the " + CurrentProtocol() + "; provider semantics are UNKNOWN"
		row.ProviderEvidence = "Provider status UNKNOWN: InstigatorId semantics and time-scoped active actor linkage are unverified"
	}
	if row.TargetEntityID != nil {
		row.TargetEvidence = "RemoveGameplayEffectPush.EntityId differs from CombatCommon.EntityId; the holder is not established"
		if common.entityID != nil && *common.entityID == *row.TargetEntityID {
			row.TargetStatus = ResolutionCorrelated
			row.TargetEvidence = "RemoveGameplayEffectPush.EntityId matches CombatCommon.EntityId on this push"
		}
	}
	return row, nil
}

// ClientEffectHolder returns the entity that holds the effect a client push reports.
// An apply or stack push names no target of its own, so the holder is CombatCommon.EntityId;
// a remove push also carries EntityId, which must agree with it.
func ClientEffectHolder(row BuffLifecycleAuditRow) (uint64, bool) {
	if row.CombatCommonEntityID == nil || *row.CombatCommonEntityID == 0 {
		return 0, false
	}
	if row.TargetEntityID != nil && row.TargetStatus != ResolutionCorrelated {
		return 0, false
	}
	return *row.CombatCommonEntityID, true
}

func (p *Processor) handleClientEffectRow(row BuffLifecycleAuditRow) error {
	row.SessionNumber = p.SessionNumber
	_, hasHolder := ClientEffectHolder(row)
	p.linkLifecycleInstigator(&row, row.EventKind == "CLIENT_EFFECT_APPLY_REPORT" && hasHolder,
		"the holder is CombatCommon.EntityId of this client apply report")
	p.linkServerBuffRequest(&row)
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

// A server buff request (ApplyBuffS2cRequestNotify, RemoveBuffByIdS2cRequestNotify) asks the
// client to change one of its effects. The client answers with a response push whose
// CombatCommon.PreMessageId equals the request's CombatCommon.MessageId on the same entity.
const maxServerBuffRequests = 4096

type serverBuffRequestKey struct {
	session   int
	entityID  uint64
	messageID uint64
}

type serverBuffRequest struct {
	kind string
	id   uint64
}

func (p *Processor) rememberServerBuffRequest(row BuffLifecycleAuditRow) {
	var id *uint64
	switch row.EventKind {
	case "BUFF_APPLY_REQUEST_NOTIFY":
		id = row.ID
	case "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY":
		id = row.BuffID
	default:
		return
	}
	if id == nil || row.CombatCommonEntityID == nil || row.CombatCommonMessageID == nil {
		return
	}
	key := serverBuffRequestKey{row.SessionNumber, *row.CombatCommonEntityID, *row.CombatCommonMessageID}
	if p.serverBuffRequests == nil {
		p.serverBuffRequests = make(map[serverBuffRequestKey]serverBuffRequest)
	}
	if _, exists := p.serverBuffRequests[key]; !exists {
		p.serverBuffRequestOrder = append(p.serverBuffRequestOrder, key)
	}
	p.serverBuffRequests[key] = serverBuffRequest{kind: row.EventKind, id: *id}
	for len(p.serverBuffRequestOrder) > maxServerBuffRequests {
		delete(p.serverBuffRequests, p.serverBuffRequestOrder[0])
		p.serverBuffRequestOrder = p.serverBuffRequestOrder[1:]
	}
}

// linkServerBuffRequest ties a client response push to the server request it answers.
// The pending request is kept so that a response split over several pushes links each one.
func (p *Processor) linkServerBuffRequest(row *BuffLifecycleAuditRow) {
	var wantKind string
	switch row.EventKind {
	case "BUFF_APPLY_REQUEST_RESPONSE":
		wantKind = "BUFF_APPLY_REQUEST_NOTIFY"
	case "BUFF_REMOVE_BY_ID_REQUEST_RESPONSE":
		wantKind = "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY"
	default:
		return
	}
	row.RequestLinkStatus = ResolutionUnknown
	row.RequestLinkEvidence = "No server request with CombatCommon.MessageId equal to this push's PreMessageId on the same entity was observed"
	if row.CombatCommonPreMessageID == nil || row.CombatCommonEntityID == nil {
		row.RequestLinkEvidence = "The response push carries no PreMessageId or EntityId"
		return
	}
	request, ok := p.serverBuffRequests[serverBuffRequestKey{row.SessionNumber, *row.CombatCommonEntityID, *row.CombatCommonPreMessageID}]
	switch {
	case !ok:
	case request.kind != wantKind:
		row.RequestLinkEvidence = "PreMessageId names a server request of another kind; the link is rejected"
	default:
		id := request.id
		row.RequestLinkedID = &id
		row.RequestLinkStatus = ResolutionCorrelated
		row.RequestLinkEvidence = "CombatCommon.PreMessageId equals the CombatCommon.MessageId of the server request on the same entity"
	}
}
