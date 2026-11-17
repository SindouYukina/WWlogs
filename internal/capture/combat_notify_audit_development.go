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

var combatNotifyAuditSchemaBasis = protocolSchemaBasis()

var combatNotifyAuditNames = map[uint64]string{
	fieldCombatNotifyDataDamageExecuteNotify:            "DamageExecuteNotify",
	fieldCombatNotifyDataApplyGameplayEffectNotify:      "ApplyGameplayEffectNotify",
	fieldCombatNotifyDataRemoveGameplayEffectNotify:     "RemoveGameplayEffectNotify",
	fieldCombatNotifyDataAttributeChangedNotify:         "AttributeChangedNotify",
	fieldCombatNotifyDataActivateBuffNotify:             "ActivateBuffNotify",
	fieldCombatNotifyDataChangeStateNotify:              "ChangeStateNotify",
	fieldCombatNotifyDataApplyBuffS2CRequestNotify:      "ApplyBuffS2CRequestNotify",
	fieldCombatNotifyDataRecoverPropChangedNotify:       "RecoverPropChangedNotify",
	fieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify: "RemoveBuffByIdS2CRequestNotify",
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

// auditCombatNotify writes the de-identified audit row of one CombatNotifyData field (-combat-notify-audit-out) while the
// core reads the frame. A failed audit row must not skip the rest of the frame (its damage and effect notifies), so it is
// counted (diagnostic_isolation_development.go) and the frame continues.
func (p *processor) auditCombatNotify(frame gameFrame, notification protoField, commonEntityID, commonOriginator, commonMessageID *uint64) {
	if p.dev.combatNotifyAudit == nil || notification.number == fieldCombatNotifyDataCombatCommon {
		return
	}
	if err := writeCombatNotifyAudit(p.dev.combatNotifyAudit, frame, notification, commonEntityID, commonOriginator, commonMessageID, p.sessionNumber); err != nil {
		p.noteDiagnosticFailure("combat-notify-audit", err)
	}
}

func writeCombatNotifyAudit(w io.Writer, frame gameFrame, notification protoField, commonEntityID, commonOriginator, commonMessageID *uint64, sessionNumber int) error {
	fields, err := parseProtoFields(notification.data)
	if err != nil {
		return fmt.Errorf("parse inner message: %w", err)
	}
	row := combatNotifyAuditRow{
		SchemaVersion: 2, EventType: "COMBAT_NOTIFY_INNER_FIELD_AUDIT", SessionNumber: sessionNumber,
		Timestamp: frame.timestamp, OuterCommandID: frame.commandID, Direction: frame.direction,
		InnerFieldNumber: notification.number, InnerMessageName: combatNotifyAuditNames[notification.number],
		SchemaBasis: combatNotifyAuditSchemaBasis, CombatCommonEntity: commonEntityID,
		CombatCommonSource: commonOriginator, CombatCommonMessage: commonMessageID,
		FieldShapes: summarizeProtoFields(fields), DecodeStatus: "STRUCTURE_DECODED_VALUES_WHITELISTED",
	}

	switch notification.number {
	case fieldCombatNotifyDataDamageExecuteNotify:
		record := combatNotifyNestedRecord{FieldNumber: 0, SchemaName: "DamageExecuteNotify", FieldShapes: summarizeProtoFields(fields)}
		record.Values = numericValuesForFields(fields, fieldDamageExecuteNotifyImmuneType, fieldDamageExecuteNotifyChangeWeakness, fieldDamageExecuteNotifyKilledTarget, fieldDamageExecuteNotifyShieldCoverDamage, fieldDamageExecuteNotifyElementType, fieldDamageExecuteNotifySkillLevel, fieldDamageExecuteNotifyDamageID, fieldDamageExecuteNotifyIsCrit, fieldDamageExecuteNotifyKillerEntityID, fieldDamageExecuteNotifyChangeLife, fieldDamageExecuteNotifyPartID, fieldDamageExecuteNotifyTargetEntityID, fieldDamageExecuteNotifyDamage, fieldDamageExecuteNotifyAttackerEntityID)
		row.NestedRecords = append(row.NestedRecords, record)
		for _, field := range fields {
			if field.number != fieldDamageExecuteNotifyDamageContext || field.wire != 2 {
				continue
			}
			nestedFields, parseErr := parseProtoFields(field.data)
			if parseErr != nil {
				return fmt.Errorf("parse DamageExecuteNotify.DamageContext: %w", parseErr)
			}
			context := combatNotifyNestedRecord{FieldNumber: fieldDamageExecuteNotifyDamageContext, SchemaName: "DamageContext", FieldShapes: summarizeProtoFields(nestedFields)}
			context.Values = numericValuesForFields(nestedFields, fieldDamageContextSourceType, fieldDamageContextBulletID, fieldDamageContextBulletTags, fieldDamageContextSkillID, fieldDamageContextSkillMessageID)
			row.NestedRecords = append(row.NestedRecords, context)
		}
	case fieldCombatNotifyDataAttributeChangedNotify:
		for _, field := range fields {
			if field.number != fieldAttributeChangedNotifyAttributes || field.wire != 2 {
				continue
			}
			nestedFields, parseErr := parseProtoFields(field.data)
			if parseErr != nil {
				return fmt.Errorf("parse AttributeChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: fieldAttributeChangedNotifyAttributes, SchemaName: "GameplayAttributeData", FieldShapes: summarizeProtoFields(nestedFields)}
			record.Values = numericValuesForFields(nestedFields, fieldGameplayAttributeDataBaseValue, fieldGameplayAttributeDataCurrentValue, fieldGameplayAttributeDataAttributeType)
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case fieldCombatNotifyDataActivateBuffNotify:
		row.NestedRecords = append(row.NestedRecords, combatNotifyNestedRecord{
			FieldNumber: 0, SchemaName: "ActivateBuffNotify", FieldShapes: summarizeProtoFields(fields),
			Values: numericValuesForFields(fields, fieldActivateBuffNotifyOn, fieldActivateBuffNotifyHandle),
		})
	case fieldCombatNotifyDataRecoverPropChangedNotify:
		for _, field := range fields {
			if field.number != fieldRecoverPropChangedNotifyAttributes || field.wire != 2 {
				continue
			}
			nestedFields, parseErr := parseProtoFields(field.data)
			if parseErr != nil {
				return fmt.Errorf("parse RecoverPropChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: fieldRecoverPropChangedNotifyAttributes, SchemaName: "RecoverAttr", FieldShapes: summarizeProtoFields(nestedFields)}
			record.Values = numericValuesForFields(nestedFields, fieldRecoverAttrAttrID, fieldRecoverAttrRatio, fieldRecoverAttrMaxValue, fieldRecoverAttrCurrentValue)
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case fieldCombatNotifyDataChangeStateNotify:
		row.NestedRecords = append(row.NestedRecords, combatNotifyNestedRecord{
			FieldNumber: 0, SchemaName: "ChangeStateNotify", FieldShapes: summarizeProtoFields(fields),
			Values: numericValuesForFields(fields, fieldChangeStateNotifyToState, fieldChangeStateNotifyFromState, fieldChangeStateNotifyFsmID),
		})
	}
	switch notification.number {
	case fieldCombatNotifyDataDamageExecuteNotify, fieldCombatNotifyDataAttributeChangedNotify, fieldCombatNotifyDataActivateBuffNotify,
		fieldCombatNotifyDataChangeStateNotify, fieldCombatNotifyDataRecoverPropChangedNotify:
		row.DecodeStatus = "CANDIDATE_SCHEMA_FIELDS_DECODED"
	}
	if err := json.NewEncoder(w).Encode(row); err != nil {
		return err
	}
	return nil
}

func summarizeProtoFields(fields []protoField) []combatNotifyFieldShape {
	counts := make(map[[2]uint64]int)
	byteCounts := make(map[[2]uint64]int)
	for _, field := range fields {
		key := [2]uint64{field.number, field.wire}
		counts[key]++
		if field.wire == 1 || field.wire == 2 || field.wire == 5 {
			byteCounts[key] += len(field.data)
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

func numericValuesForFields(fields []protoField, allowlist ...uint64) []combatNotifyNumericValue {
	allowed := make(map[uint64]struct{}, len(allowlist))
	for _, number := range allowlist {
		allowed[number] = struct{}{}
	}
	values := make([]combatNotifyNumericValue, 0)
	for _, field := range fields {
		if _, ok := allowed[field.number]; !ok {
			continue
		}
		if numeric, ok := numericProtoValue(field); ok {
			values = append(values, numeric)
		}
	}
	return values
}

func numericProtoValue(field protoField) (combatNotifyNumericValue, bool) {
	value := combatNotifyNumericValue{FieldNumber: field.number, WireType: field.wire}
	switch field.wire {
	case 0:
		n := field.value
		value.Unsigned = &n
	case 5:
		if len(field.data) != 4 {
			return combatNotifyNumericValue{}, false
		}
		f := math.Float32frombits(binary.LittleEndian.Uint32(field.data))
		value.Float = &f
	default:
		return combatNotifyNumericValue{}, false
	}
	return value, true
}
