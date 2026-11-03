package capture

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"wwlogs/internal/versioninfo"
)

// The local client owns the gameplay effects of the entities it controls: it applies,
// refreshes and removes them itself and reports each change to the server inside an
// outbound CombatSendPackRequest. The server does not echo these reports back as
// notifies, so they are the only wire evidence for the local team's own effects.
// Field numbers follow the Windows Global 3.7.1 Aki.Protocol metadata:
// CombatSendPackRequest.Data(14) -> CombatSendData.Push(2) -> CombatPushData.
const (
	combatPushCommonField                    = 1
	combatPushApplyBuffS2CResponseField      = 4
	combatPushRemoveBuffByIDS2CResponseField = 6
	combatPushApplyGameplayEffectField       = 9
	combatPushRemoveGameplayEffectField      = 10
	combatPushBuffStackCountField            = 37
)

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
	fields, err := parseProtoFields(body)
	if err != nil {
		return common, err
	}
	for _, field := range fields {
		if field.number == 4 && field.wire == 5 && len(field.data) == 4 {
			value := math.Float32frombits(binary.LittleEndian.Uint32(field.data))
			common.timeStamp = &value
			continue
		}
		if field.wire != 0 {
			continue
		}
		value := field.value
		switch field.number {
		case 1:
			common.preMessageID = &value
		case 2:
			common.messageID = &value
		case 3:
			common.originatorID = &value
		case 5:
			common.entityID = &value
		}
	}
	return common, nil
}

// handleClientEffectPushes decodes the effect reports of one outbound CombatSendPackRequest.
func (p *processor) handleClientEffectPushes(frame gameFrame, body []byte) error {
	outer, err := parseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse CombatSendPackRequest: %w", err)
	}
	for _, dataField := range outer {
		if dataField.number != combatSendPackDataField || dataField.wire != 2 {
			continue
		}
		dataFields, err := parseProtoFields(dataField.data)
		if err != nil {
			return fmt.Errorf("parse CombatSendPackRequest.Data item: %w", err)
		}
		for _, pushField := range dataFields {
			if pushField.number != combatSendPackDataPushField || pushField.wire != 2 {
				continue
			}
			pushes, err := parseProtoFields(pushField.data)
			if err != nil {
				return fmt.Errorf("parse CombatSendData.Push: %w", err)
			}
			var common combatCommonContext
			for _, push := range pushes {
				if push.number == combatPushCommonField && push.wire == 2 {
					if common, err = decodeCombatCommon(push.data); err != nil {
						return fmt.Errorf("parse CombatCommon: %w", err)
					}
				}
			}
			for _, push := range pushes {
				if push.wire != 2 {
					continue
				}
				switch push.number {
				case combatPushApplyGameplayEffectField, combatPushRemoveGameplayEffectField, combatPushBuffStackCountField,
					combatPushApplyBuffS2CResponseField, combatPushRemoveBuffByIDS2CResponseField:
				default:
					continue
				}
				row, err := decodeClientEffectPush(frame, push.number, push.data, common)
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

func decodeClientEffectPush(frame gameFrame, fieldNumber uint64, body []byte, common combatCommonContext) (buffLifecycleAuditRow, error) {
	number := fieldNumber
	row := buffLifecycleAuditRow{
		SchemaVersion: buffLifecycleSchemaVersion, EventType: "BUFF_LIFECYCLE", MessageID: frame.commandID,
		Timestamp: frame.timestamp, CommandID: frame.commandID, Direction: frame.direction,
		InnerMessageFieldNumber: &number,
		MessagePath:             fmt.Sprintf("CombatSendPackRequest.field14(CombatSendData[]).field2(CombatPushData).field%d", fieldNumber),
		EventTypeStatus:         resolutionCorrelated, TargetStatus: resolutionUnknown, InstigatorStatus: resolutionUnknown,
		InstigatorCharacterCandidateStatus: resolutionUnknown, InstigatorCharacterCandidateNameStatus: resolutionUnknown,
		ProviderStatus: resolutionUnknown, ProviderNameStatus: resolutionUnknown,
		SchemaBasis:          versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; client version observed, schema compatibility unverified",
		EvidenceSource:       EffectEvidenceClientReport,
		CombatCommonEntityID: common.entityID, CombatCommonOriginator: common.originatorID, CombatCommonMessageID: common.messageID,
		CombatCommonPreMessageID: common.preMessageID, ClientTimeStamp: common.timeStamp,
	}
	switch fieldNumber {
	case combatPushApplyGameplayEffectField:
		row.MessageName, row.EventKind = "ApplyGameplayEffectPush", "CLIENT_EFFECT_APPLY_REPORT"
	case combatPushRemoveGameplayEffectField:
		row.MessageName, row.EventKind = "RemoveGameplayEffectPush", "CLIENT_EFFECT_REMOVE_REPORT"
	case combatPushBuffStackCountField:
		row.MessageName, row.EventKind = "BuffStackCountPush", "CLIENT_EFFECT_STACK_REPORT"
	case combatPushApplyBuffS2CResponseField:
		row.MessageName, row.EventKind = "ApplyBuffS2cResponsePush", "BUFF_APPLY_REQUEST_RESPONSE"
	case combatPushRemoveBuffByIDS2CResponseField:
		row.MessageName, row.EventKind = "RemoveBuffByIdS2cResponsePush", "BUFF_REMOVE_BY_ID_REQUEST_RESPONSE"
	default:
		return row, fmt.Errorf("unsupported client effect push field %d", fieldNumber)
	}
	fields, err := parseProtoFields(body)
	if err != nil {
		return row, fmt.Errorf("parse %s: %w", row.MessageName, err)
	}
	for _, field := range fields {
		var uintTarget **uint64
		var boolTarget **bool
		var floatTarget **float32
		var stringTarget *string
		switch fieldNumber {
		case combatPushApplyGameplayEffectField:
			switch field.number {
			case 2:
				stringTarget = &row.ClientReasonRaw
			case 3:
				uintTarget = &row.HandleID
			case 4:
				uintTarget = &row.StackCount
			case 5:
				uintTarget = &row.ID
			case 6:
				boolTarget = &row.IsActive
			case 7:
				uintTarget = &row.Level
			case 8:
				floatTarget = &row.DurationSeconds
			case 10:
				uintTarget = &row.InstigatorID
			case 12:
				uintTarget = &row.ConfBuffID
			case 13:
				uintTarget = &row.ApplyType
			case 14:
				uintTarget = &row.ServerID
			}
		case combatPushRemoveGameplayEffectField:
			switch field.number {
			case 1:
				stringTarget = &row.ClientReasonRaw
			case 4:
				uintTarget = &row.InstigatorID
			case 10:
				boolTarget = &row.IsPrematureRemoval
			case 14:
				uintTarget = &row.HandleID
			case 15:
				uintTarget = &row.TargetEntityID
			}
		case combatPushBuffStackCountField:
			switch field.number {
			case 2:
				uintTarget = &row.HandleID
			case 3:
				stringTarget = &row.ClientReasonRaw
			case 4:
				boolTarget = &row.NotRefreshDuration
			case 5:
				boolTarget = &row.NotRefreshPeriod
			case 8:
				boolTarget = &row.IsPrematureRemoval
			case 12:
				uintTarget = &row.InstigatorID
			case 13:
				floatTarget = &row.DurationSeconds
			case 14:
				uintTarget = &row.StackCount
			}
		case combatPushApplyBuffS2CResponseField:
			switch field.number {
			case 4:
				uintTarget = &row.ErrorCode
			case 5:
				boolTarget = &row.IsActive
			case 11:
				uintTarget = &row.HandleID
			}
		case combatPushRemoveBuffByIDS2CResponseField:
			if field.number == 15 {
				uintTarget = &row.ErrorCode
			}
		}
		switch {
		case uintTarget != nil:
			if field.wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint", row.MessageName, field.number, field.wire)
			}
			value := field.value
			*uintTarget = &value
		case boolTarget != nil:
			if field.wire != 0 {
				return row, fmt.Errorf("%s field %d has wire type %d, want varint bool", row.MessageName, field.number, field.wire)
			}
			value := field.value != 0
			*boolTarget = &value
		case floatTarget != nil:
			if field.wire != 5 || len(field.data) != 4 {
				return row, fmt.Errorf("%s field %d has wire type %d, want fixed32 float", row.MessageName, field.number, field.wire)
			}
			value := math.Float32frombits(binary.LittleEndian.Uint32(field.data))
			*floatTarget = &value
		case stringTarget != nil:
			if field.wire != 2 {
				return row, fmt.Errorf("%s field %d has wire type %d, want string", row.MessageName, field.number, field.wire)
			}
			*stringTarget = string(field.data)
		default:
			row.UnknownFieldNumbers = append(row.UnknownFieldNumbers, field.number)
		}
	}
	if row.InstigatorID != nil {
		row.InstigatorEvidence = "Raw InstigatorId field decoded from " + versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; provider semantics and compatibility with the observed client build are UNKNOWN"
		row.ProviderEvidence = "Provider status UNKNOWN: InstigatorId semantics and time-scoped active actor linkage are unverified"
	}
	if row.TargetEntityID != nil {
		row.TargetEvidence = "RemoveGameplayEffectPush.EntityId differs from CombatCommon.EntityId; the holder is not established"
		if common.entityID != nil && *common.entityID == *row.TargetEntityID {
			row.TargetStatus = resolutionCorrelated
			row.TargetEvidence = "RemoveGameplayEffectPush.EntityId matches CombatCommon.EntityId on this push"
		}
	}
	return row, nil
}

// clientEffectHolder returns the entity that holds the effect a client push reports.
// An apply or stack push names no target of its own, so the holder is CombatCommon.EntityId;
// a remove push also carries EntityId, which must agree with it.
func clientEffectHolder(row buffLifecycleAuditRow) (uint64, bool) {
	if row.CombatCommonEntityID == nil || *row.CombatCommonEntityID == 0 {
		return 0, false
	}
	if row.TargetEntityID != nil && row.TargetStatus != resolutionCorrelated {
		return 0, false
	}
	return *row.CombatCommonEntityID, true
}

func (p *processor) handleClientEffectRow(row buffLifecycleAuditRow) error {
	row.SessionNumber = p.sessionNumber
	_, hasHolder := clientEffectHolder(row)
	p.linkLifecycleInstigator(&row, row.EventKind == "CLIENT_EFFECT_APPLY_REPORT" && hasHolder,
		"the holder is CombatCommon.EntityId of this client apply report")
	p.linkServerBuffRequest(&row)
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

func (p *processor) rememberServerBuffRequest(row buffLifecycleAuditRow) {
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
func (p *processor) linkServerBuffRequest(row *buffLifecycleAuditRow) {
	var wantKind string
	switch row.EventKind {
	case "BUFF_APPLY_REQUEST_RESPONSE":
		wantKind = "BUFF_APPLY_REQUEST_NOTIFY"
	case "BUFF_REMOVE_BY_ID_REQUEST_RESPONSE":
		wantKind = "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY"
	default:
		return
	}
	row.RequestLinkStatus = resolutionUnknown
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
		row.RequestLinkStatus = resolutionCorrelated
		row.RequestLinkEvidence = "CombatCommon.PreMessageId equals the CombatCommon.MessageId of the server request on the same entity"
	}
}
