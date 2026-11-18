package devcapture

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"wwlogs/internal/capture"
)

var combatNotifyAuditSchemaBasis = capture.ProtocolSchemaBasis()

var combatNotifyAuditNames = map[uint64]string{
	capture.FieldCombatNotifyDataDamageExecuteNotify:            "DamageExecuteNotify",
	capture.FieldCombatNotifyDataApplyGameplayEffectNotify:      "ApplyGameplayEffectNotify",
	capture.FieldCombatNotifyDataRemoveGameplayEffectNotify:     "RemoveGameplayEffectNotify",
	capture.FieldCombatNotifyDataAttributeChangedNotify:         "AttributeChangedNotify",
	capture.FieldCombatNotifyDataActivateBuffNotify:             "ActivateBuffNotify",
	capture.FieldCombatNotifyDataChangeStateNotify:              "ChangeStateNotify",
	capture.FieldCombatNotifyDataApplyBuffS2CRequestNotify:      "ApplyBuffS2CRequestNotify",
	capture.FieldCombatNotifyDataRecoverPropChangedNotify:       "RecoverPropChangedNotify",
	capture.FieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify: "RemoveBuffByIdS2CRequestNotify",
}

type combatNotifyFieldShape struct {
	Number    uint64 `json:"fieldNumber"`
	WireType  uint64 `json:"wireType"`
	Count     int    `json:"count"`
	ByteCount int    `json:"byteCount,omitempty"`
}

type combatNotifyNumericValue struct {
	FieldNumber uint64   `json:"fieldNumber"`
	WireType    uint64   `json:"wireType"`
	Unsigned    *uint64  `json:"unsignedValue,omitempty"`
	Float       *float32 `json:"float32Value,omitempty"`
}

type combatNotifyNestedRecord struct {
	FieldNumber uint64                     `json:"fieldNumber"`
	SchemaName  string                     `json:"schemaName,omitempty"`
	FieldShapes []combatNotifyFieldShape   `json:"fieldShapes"`
	Values      []combatNotifyNumericValue `json:"values,omitempty"`
}

type combatNotifyAuditRow struct {
	SchemaVersion       int                        `json:"schemaVersion"`
	EventType           string                     `json:"eventType"`
	SessionNumber       int                        `json:"sessionNumber"`
	Timestamp           time.Time                  `json:"timestamp"`
	OuterCommandID      uint16                     `json:"outerCommandId"`
	Direction           string                     `json:"direction"`
	InnerFieldNumber    uint64                     `json:"innerFieldNumber"`
	InnerMessageName    string                     `json:"innerMessageName,omitempty"`
	SchemaBasis         string                     `json:"schemaBasis"`
	CombatCommonEntity  *uint64                    `json:"combatCommonEntityId,omitempty"`
	CombatCommonSource  *uint64                    `json:"combatCommonOriginatorId,omitempty"`
	CombatCommonMessage *uint64                    `json:"combatCommonMessageId,omitempty"`
	FieldShapes         []combatNotifyFieldShape   `json:"fieldShapes"`
	NestedRecords       []combatNotifyNestedRecord `json:"nestedRecords,omitempty"`
	DecodeStatus        string                     `json:"decodeStatus"`
}

// AuditCombatNotify writes the de-identified audit row of one CombatNotifyData field (-combat-notify-audit-out) while the
// core reads the frame. A failed audit row must not skip the rest of the frame (its damage and effect notifies), so it is
// counted (diagnostic_isolation.go) and the frame continues.
func (p *processor) AuditCombatNotify(frame capture.GameFrame, notification capture.ProtoField, commonEntityID, commonOriginator, commonMessageID *uint64) {
	if p.dev.combatNotifyAudit == nil || notification.Number == capture.FieldCombatNotifyDataCombatCommon {
		return
	}
	if err := writeCombatNotifyAudit(p.dev.combatNotifyAudit, frame, notification, commonEntityID, commonOriginator, commonMessageID, p.SessionNumber); err != nil {
		p.noteDiagnosticFailure("combat-notify-audit", err)
	}
}

func writeCombatNotifyAudit(w io.Writer, frame capture.GameFrame, notification capture.ProtoField, commonEntityID, commonOriginator, commonMessageID *uint64, sessionNumber int) error {
	fields, err := capture.ParseProtoFields(notification.Data)
	if err != nil {
		return fmt.Errorf("parse inner message: %w", err)
	}
	row := combatNotifyAuditRow{
		SchemaVersion: 2, EventType: "COMBAT_NOTIFY_INNER_FIELD_AUDIT", SessionNumber: sessionNumber,
		Timestamp: frame.Timestamp, OuterCommandID: frame.CommandID, Direction: frame.Direction,
		InnerFieldNumber: notification.Number, InnerMessageName: combatNotifyAuditNames[notification.Number],
		SchemaBasis: combatNotifyAuditSchemaBasis, CombatCommonEntity: commonEntityID,
		CombatCommonSource: commonOriginator, CombatCommonMessage: commonMessageID,
		FieldShapes: summarizeProtoFields(fields), DecodeStatus: "STRUCTURE_DECODED_VALUES_WHITELISTED",
	}

	switch notification.Number {
	case capture.FieldCombatNotifyDataDamageExecuteNotify:
		record := combatNotifyNestedRecord{FieldNumber: 0, SchemaName: "DamageExecuteNotify", FieldShapes: summarizeProtoFields(fields)}
		record.Values = numericValuesForFields(fields, capture.FieldDamageExecuteNotifyImmuneType, capture.FieldDamageExecuteNotifyChangeWeakness, capture.FieldDamageExecuteNotifyKilledTarget, capture.FieldDamageExecuteNotifyShieldCoverDamage, capture.FieldDamageExecuteNotifyElementType, capture.FieldDamageExecuteNotifySkillLevel, capture.FieldDamageExecuteNotifyDamageID, capture.FieldDamageExecuteNotifyIsCrit, capture.FieldDamageExecuteNotifyKillerEntityID, capture.FieldDamageExecuteNotifyChangeLife, capture.FieldDamageExecuteNotifyPartID, capture.FieldDamageExecuteNotifyTargetEntityID, capture.FieldDamageExecuteNotifyDamage, capture.FieldDamageExecuteNotifyAttackerEntityID)
		row.NestedRecords = append(row.NestedRecords, record)
		for _, field := range fields {
			if field.Number != capture.FieldDamageExecuteNotifyDamageContext || field.Wire != 2 {
				continue
			}
			nestedFields, parseErr := capture.ParseProtoFields(field.Data)
			if parseErr != nil {
				return fmt.Errorf("parse DamageExecuteNotify.DamageContext: %w", parseErr)
			}
			context := combatNotifyNestedRecord{FieldNumber: capture.FieldDamageExecuteNotifyDamageContext, SchemaName: "DamageContext", FieldShapes: summarizeProtoFields(nestedFields)}
			context.Values = numericValuesForFields(nestedFields, capture.FieldDamageContextSourceType, capture.FieldDamageContextBulletID, capture.FieldDamageContextBulletTags, capture.FieldDamageContextSkillID, capture.FieldDamageContextSkillMessageID)
			row.NestedRecords = append(row.NestedRecords, context)
		}
	case capture.FieldCombatNotifyDataAttributeChangedNotify:
		for _, field := range fields {
			if field.Number != capture.FieldAttributeChangedNotifyAttributes || field.Wire != 2 {
				continue
			}
			nestedFields, parseErr := capture.ParseProtoFields(field.Data)
			if parseErr != nil {
				return fmt.Errorf("parse AttributeChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: capture.FieldAttributeChangedNotifyAttributes, SchemaName: "GameplayAttributeData", FieldShapes: summarizeProtoFields(nestedFields)}
			record.Values = numericValuesForFields(nestedFields, capture.FieldGameplayAttributeDataBaseValue, capture.FieldGameplayAttributeDataCurrentValue, capture.FieldGameplayAttributeDataAttributeType)
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case capture.FieldCombatNotifyDataActivateBuffNotify:
		row.NestedRecords = append(row.NestedRecords, combatNotifyNestedRecord{
			FieldNumber: 0, SchemaName: "ActivateBuffNotify", FieldShapes: summarizeProtoFields(fields),
			Values: numericValuesForFields(fields, capture.FieldActivateBuffNotifyOn, capture.FieldActivateBuffNotifyHandle),
		})
	case capture.FieldCombatNotifyDataRecoverPropChangedNotify:
		for _, field := range fields {
			if field.Number != capture.FieldRecoverPropChangedNotifyAttributes || field.Wire != 2 {
				continue
			}
			nestedFields, parseErr := capture.ParseProtoFields(field.Data)
			if parseErr != nil {
				return fmt.Errorf("parse RecoverPropChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: capture.FieldRecoverPropChangedNotifyAttributes, SchemaName: "RecoverAttr", FieldShapes: summarizeProtoFields(nestedFields)}
			record.Values = numericValuesForFields(nestedFields, capture.FieldRecoverAttrAttrID, capture.FieldRecoverAttrRatio, capture.FieldRecoverAttrMaxValue, capture.FieldRecoverAttrCurrentValue)
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case capture.FieldCombatNotifyDataChangeStateNotify:
		row.NestedRecords = append(row.NestedRecords, combatNotifyNestedRecord{
			FieldNumber: 0, SchemaName: "ChangeStateNotify", FieldShapes: summarizeProtoFields(fields),
			Values: numericValuesForFields(fields, capture.FieldChangeStateNotifyToState, capture.FieldChangeStateNotifyFromState, capture.FieldChangeStateNotifyFsmID),
		})
	}
	switch notification.Number {
	case capture.FieldCombatNotifyDataDamageExecuteNotify, capture.FieldCombatNotifyDataAttributeChangedNotify, capture.FieldCombatNotifyDataActivateBuffNotify,
		capture.FieldCombatNotifyDataChangeStateNotify, capture.FieldCombatNotifyDataRecoverPropChangedNotify:
		row.DecodeStatus = "CANDIDATE_SCHEMA_FIELDS_DECODED"
	}
	if err := json.NewEncoder(w).Encode(row); err != nil {
		return err
	}
	return nil
}

func summarizeProtoFields(fields []capture.ProtoField) []combatNotifyFieldShape {
	counts := make(map[[2]uint64]int)
	byteCounts := make(map[[2]uint64]int)
	for _, field := range fields {
		key := [2]uint64{field.Number, field.Wire}
		counts[key]++
		if field.Wire == 1 || field.Wire == 2 || field.Wire == 5 {
			byteCounts[key] += len(field.Data)
		}
	}
	keys := make([][2]uint64, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] == keys[j][0] {
			return keys[i][1] < keys[j][1]
		}
		return keys[i][0] < keys[j][0]
	})
	result := make([]combatNotifyFieldShape, 0, len(keys))
	for _, key := range keys {
		shape := combatNotifyFieldShape{Number: key[0], WireType: key[1], Count: counts[key]}
		if byteCounts[key] > 0 {
			shape.ByteCount = byteCounts[key]
		}
		result = append(result, shape)
	}
	return result
}

func numericValuesForFields(fields []capture.ProtoField, allowlist ...uint64) []combatNotifyNumericValue {
	allowed := make(map[uint64]struct{}, len(allowlist))
	for _, number := range allowlist {
		allowed[number] = struct{}{}
	}
	values := make([]combatNotifyNumericValue, 0)
	for _, field := range fields {
		if _, ok := allowed[field.Number]; !ok {
			continue
		}
		if numeric, ok := numericProtoValue(field); ok {
			values = append(values, numeric)
		}
	}
	return values
}

func numericProtoValue(field capture.ProtoField) (combatNotifyNumericValue, bool) {
	value := combatNotifyNumericValue{FieldNumber: field.Number, WireType: field.Wire}
	switch field.Wire {
	case 0:
		n := field.Value
		value.Unsigned = &n
	case 5:
		if len(field.Data) != 4 {
			return combatNotifyNumericValue{}, false
		}
		f := math.Float32frombits(binary.LittleEndian.Uint32(field.Data))
		value.Float = &f
	default:
		return combatNotifyNumericValue{}, false
	}
	return value, true
}
