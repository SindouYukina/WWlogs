//go:build wwdev

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
)

func writeCombatNotifyAudit(w io.Writer, frame gameFrame, notification protoField, commonEntityID, commonOriginator, commonMessageID *uint64, sessionNumber int) error {
	fields, err := parseProtoFields(notification.data)
	if err != nil {
		return fmt.Errorf("parse inner message: %w", err)
	}
	row := combatNotifyAuditRow{
		SchemaVersion: 1, EventType: "COMBAT_NOTIFY_INNER_FIELD_AUDIT", SessionNumber: sessionNumber,
		Timestamp: frame.timestamp, OuterCommandID: frame.commandID, Direction: frame.direction,
		InnerFieldNumber: notification.number, InnerMessageName: combatNotifyAuditNames[notification.number],
		SchemaBasis: combatNotifyAuditSchemaBasis, CombatCommonEntity: commonEntityID,
		CombatCommonSource: commonOriginator, CombatCommonMessage: commonMessageID,
		FieldShapes: summarizeProtoFields(fields), DecodeStatus: "STRUCTURE_DECODED_VALUES_WHITELISTED",
	}

	switch notification.number {
	case 22:
		for _, field := range fields {
			if field.number != 8 || field.wire != 2 {
				continue
			}
			nestedFields, parseErr := parseProtoFields(field.data)
			if parseErr != nil {
				return fmt.Errorf("parse AttributeChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: 8, SchemaName: "GameplayAttributeData", FieldShapes: summarizeProtoFields(nestedFields)}
			for _, value := range nestedFields {
				if value.number < 1 || value.number > 3 {
					continue
				}
				numeric, ok := numericProtoValue(value)
				if ok {
					record.Values = append(record.Values, numeric)
				}
			}
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case 29:
		row.NestedRecords = append(row.NestedRecords, combatNotifyNestedRecord{
			FieldNumber: 0, SchemaName: "ActivateBuffNotify", FieldShapes: summarizeProtoFields(fields),
			Values: numericValuesForFields(fields, 1, 8),
		})
	case 58:
		for _, field := range fields {
			if field.number != 6 || field.wire != 2 {
				continue
			}
			nestedFields, parseErr := parseProtoFields(field.data)
			if parseErr != nil {
				return fmt.Errorf("parse RecoverPropChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: 6, SchemaName: "RecoverAttr", FieldShapes: summarizeProtoFields(nestedFields)}
			record.Values = numericValuesForFields(nestedFields, 1, 2, 4, 5)
			row.NestedRecords = append(row.NestedRecords, record)
		}
	case 87:
		for _, field := range fields {
			if field.number != 13 || field.wire != 2 {
				continue
			}
			nestedFields, parseErr := parseProtoFields(field.data)
			if parseErr != nil {
				return fmt.Errorf("parse SquadPropChangedNotify.Attributes: %w", parseErr)
			}
			record := combatNotifyNestedRecord{FieldNumber: 13, SchemaName: "GameplayAttributeData", FieldShapes: summarizeProtoFields(nestedFields)}
			for _, value := range nestedFields {
				if value.number < 1 || value.number > 3 {
					continue
				}
				if numeric, ok := numericProtoValue(value); ok {
					record.Values = append(record.Values, numeric)
				}
			}
			row.NestedRecords = append(row.NestedRecords, record)
		}
	}
	if notification.number == 22 || notification.number == 29 || notification.number == 58 || notification.number == 87 {
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
