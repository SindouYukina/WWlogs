//go:build wwdev

package capture

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

var combatNotifyAuditSchemaBasis = ProtocolSchemaBasis()

var combatNotifyAuditNames = map[uint64]string{
	FieldCombatNotifyDataDamageExecuteNotify:            "DamageExecuteNotify",
	FieldCombatNotifyDataApplyGameplayEffectNotify:      "ApplyGameplayEffectNotify",
	FieldCombatNotifyDataRemoveGameplayEffectNotify:     "RemoveGameplayEffectNotify",
	FieldCombatNotifyDataAttributeChangedNotify:         "AttributeChangedNotify",
	FieldCombatNotifyDataActivateBuffNotify:             "ActivateBuffNotify",
	FieldCombatNotifyDataChangeStateNotify:              "ChangeStateNotify",
	FieldCombatNotifyDataApplyBuffS2CRequestNotify:      "ApplyBuffS2CRequestNotify",
	FieldCombatNotifyDataRecoverPropChangedNotify:       "RecoverPropChangedNotify",
	FieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify: "RemoveBuffByIdS2CRequestNotify",
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
// counted (diagnostic_isolation_development.go) and the frame continues.
func (p *Processor) AuditCombatNotify(frame GameFrame, notification ProtoField, commonEntityID, commonOriginator, commonMessageID *uint64) {
	if p.Dev.combatNotifyAudit == nil || notification.Number == FieldCombatNotifyDataCombatCommon {
		return
	}
	if err := writeCombatNotifyAudit(p.Dev.combatNotifyAudit, frame, notification, commonEntityID, commonOriginator, commonMessageID, p.SessionNumber); err != nil {
		p.noteDiagnosticFailure("combat-notify-audit", err)
	}
}

func writeCombatNotifyAudit(w io.Writer, frame GameFrame, notification ProtoField, commonEntityID, commonOriginator, commonMessageID *uint64, sessionNumber int) error {
	fields, err := ParseProtoFields(notification.Data)
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
	case FieldCombatNotifyDataDamageExecuteNotify:
		record := combatNotifyNestedRecord{FieldNumber: 0, SchemaName: "DamageExecuteNotify", FieldShapes: summarizeProtoFields(fields)}
		record.Values = numericValuesForFields(fields, FieldDamageExecuteNotifyImmuneType, FieldDamageExecuteNotifyChangeWeakness, FieldDamageExecuteNotifyKilledTarget, FieldDamageExecuteNotifyShieldCoverDamage, FieldDamageExecuteNotifyElementType, FieldDamageExecuteNotifySkillLevel, FieldDamageExecuteNotifyDamageID, FieldDamageExecuteNotifyIsCrit, FieldDamageExecuteNotifyKillerEntityID, FieldDamageExecuteNotifyChangeLife, FieldDamageExecuteNotifyPartID, FieldDamageExecuteNotifyTargetEntityID, FieldDamageExecuteNotifyDamage, FieldDamageExecuteNotifyAttackerEntityID)
		row.NestedRecords = append(row.NestedRecords, record)
		for _, field := range fields {
			if field.Number != FieldDamageExecuteNotifyDamageContext || field.Wire != 2 {
				continue
			}
			nestedFields, parseErr := ParseProtoFields(field.Data)
			if parseErr != nil {
				return fmt.Errorf("parse DamageExecuteNotify.DamageContext: %w", parseErr)
			}
			context := combatNotifyNestedRecord{FieldNumber: FieldDamageExecuteNotifyDamageContext, SchemaName: "DamageContext", FieldShapes: summarizeProtoFields(nestedFields)}
			context.Values = numericValuesForFields(nestedFields, FieldDamageContextSourceType, FieldDamageContextBulletID, FieldDamageContextBulletTags, FieldDamageContextSkillID, FieldDamageContextSkillMessageID)
			row.NestedRecords = append(row.NestedRecords, context)
		}
	case FieldCombatNotifyDataAttributeChangedNotify:
		for _, field := range fields {
			if field.Number != FieldAttributeChangedNotifyAttributes || field.Wire != 2 {
				continue
			}
			nestedFields, parseErr := ParseProtoFields(field.Data)
			if parseErr != nil {
				return fmt.Errorf("parse AttributeChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: FieldAttributeChangedNotifyAttributes, SchemaName: "GameplayAttributeData", FieldShapes: summarizeProtoFields(nestedFields)}
			record.Values = numericValuesForFields(nestedFields, FieldGameplayAttributeDataBaseValue, FieldGameplayAttributeDataCurrentValue, FieldGameplayAttributeDataAttributeType)
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case FieldCombatNotifyDataActivateBuffNotify:
		row.NestedRecords = append(row.NestedRecords, combatNotifyNestedRecord{
			FieldNumber: 0, SchemaName: "ActivateBuffNotify", FieldShapes: summarizeProtoFields(fields),
			Values: numericValuesForFields(fields, FieldActivateBuffNotifyOn, FieldActivateBuffNotifyHandle),
		})
	case FieldCombatNotifyDataRecoverPropChangedNotify:
		for _, field := range fields {
			if field.Number != FieldRecoverPropChangedNotifyAttributes || field.Wire != 2 {
				continue
			}
			nestedFields, parseErr := ParseProtoFields(field.Data)
			if parseErr != nil {
				return fmt.Errorf("parse RecoverPropChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: FieldRecoverPropChangedNotifyAttributes, SchemaName: "RecoverAttr", FieldShapes: summarizeProtoFields(nestedFields)}
			record.Values = numericValuesForFields(nestedFields, FieldRecoverAttrAttrID, FieldRecoverAttrRatio, FieldRecoverAttrMaxValue, FieldRecoverAttrCurrentValue)
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case FieldCombatNotifyDataChangeStateNotify:
		row.NestedRecords = append(row.NestedRecords, combatNotifyNestedRecord{
			FieldNumber: 0, SchemaName: "ChangeStateNotify", FieldShapes: summarizeProtoFields(fields),
			Values: numericValuesForFields(fields, FieldChangeStateNotifyToState, FieldChangeStateNotifyFromState, FieldChangeStateNotifyFsmID),
		})
	}
	switch notification.Number {
	case FieldCombatNotifyDataDamageExecuteNotify, FieldCombatNotifyDataAttributeChangedNotify, FieldCombatNotifyDataActivateBuffNotify,
		FieldCombatNotifyDataChangeStateNotify, FieldCombatNotifyDataRecoverPropChangedNotify:
		row.DecodeStatus = "CANDIDATE_SCHEMA_FIELDS_DECODED"
	}
	if err := json.NewEncoder(w).Encode(row); err != nil {
		return err
	}
	return nil
}

func summarizeProtoFields(fields []ProtoField) []combatNotifyFieldShape {
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

func numericValuesForFields(fields []ProtoField, allowlist ...uint64) []combatNotifyNumericValue {
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

func numericProtoValue(field ProtoField) (combatNotifyNumericValue, bool) {
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
