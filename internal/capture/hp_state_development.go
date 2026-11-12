//go:build wwdev

package capture

import (
	"encoding/json"
	"strconv"
)

// archiveHPState writes Life and LifeMax attribute values from one decoded
// CombatReceivePackNotify. It reads the schema tree only, so the typed pipeline
// is unaffected.
func (p *processor) archiveHPState(frame gameFrame, decoded *decodedMessage) {
	if p.hpStateOut == nil || decoded == nil {
		return
	}
	order := 0
	for _, data := range childMessages(decoded, "Data") {
		for _, notify := range childMessages(data, "CombatNotifyData") {
			entityID, hasEntity := combatCommonEntityID(notify)
			if !hasEntity {
				continue
			}
			for _, change := range childMessages(notify, "AttributeChangedNotify") {
				for _, attribute := range childMessages(change, "Attributes") {
					typeValue, ok := scalarValue(attribute, "AttributeType")
					if !ok || (typeValue != attributeTypeLife && typeValue != attributeTypeLifeMax) {
						continue
					}
					baseValue, _ := scalarValue(attribute, "BaseValue")
					currentValue, _ := scalarValue(attribute, "CurrentValue")
					record := hpStateRecord{
						SchemaVersion:   hpStateSchemaVersion,
						FrameOrdinal:    p.frameOrdinal - 1,
						SessionNumber:   p.sessionNumber,
						Timestamp:       formatTimestamp(frame.timestamp),
						EntityID:        entityID,
						AttributeType:   typeValue,
						BaseValue:       baseValue,
						CurrentValue:    currentValue,
						EventOrderInMsg: order,
					}
					if name, known := p.protocolSchema.enumName("EAttributeType", typeValue); known {
						record.AttributeName = name
					}
					order++
					line, err := json.Marshal(record)
					if err != nil {
						p.hpStateErrors++
						continue
					}
					if _, err := p.hpStateOut.Write(append(line, '\n')); err != nil {
						p.hpStateErrors++
					}
				}
			}
		}
	}
}

// childMessages returns every decoded child message with the given field name,
// in wire order. Repeated fields appear once per element.
func childMessages(parent *decodedMessage, name string) []*decodedMessage {
	if parent == nil {
		return nil
	}
	var out []*decodedMessage
	for _, field := range parent.Fields {
		if field.Name == name && field.Message != nil {
			out = append(out, field.Message)
		}
	}
	return out
}

// scalarValue reads an integer field from a decoded message. A missing field is
// reported as absent, and the caller decides whether that means zero.
func scalarValue(message *decodedMessage, name string) (int64, bool) {
	if message == nil {
		return 0, false
	}
	for _, field := range message.Fields {
		if field.Name != name || field.Value == nil {
			continue
		}
		switch value := field.Value.(type) {
		case int32:
			return int64(value), true
		case int64:
			return value, true
		case uint32:
			return int64(value), true
		case uint64:
			return int64(value), true
		case float64:
			return int64(value), true
		case string:
			number, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				return number, true
			}
		}
		return 0, false
	}
	return 0, false
}

// combatCommonEntityID returns the entity that a CombatNotifyData concerns, from
// its CombatCommon message. Missing CombatCommon means no entity is observed.
func combatCommonEntityID(notify *decodedMessage) (uint64, bool) {
	common := childMessages(notify, "CombatCommon")
	if len(common) == 0 {
		return 0, false
	}
	value, ok := scalarValue(common[0], "EntityId")
	if !ok {
		return 0, false
	}
	if value < 0 {
		return 0, false
	}
	return uint64(value), true
}

// archiveHPStateFrame decodes a combat frame for HP output when that output is
// enabled. It runs on the same plaintext as the other observers.
func (p *processor) archiveHPStateFrame(frame gameFrame, plain []byte) {
	if p.hpStateOut == nil || p.protocolSchema == nil || (frame.commandID != cmdCombatReceivePackNotify && frame.commandID != cmdCombatSendPackResponse) {
		return
	}
	payloads, err := receivePackNotifyBodies(frame.commandID, plain)
	if err != nil {
		p.hpStateErrors++
		return
	}
	for _, payload := range payloads {
		decoded, err := decodeSchemaMessage(p.protocolSchema, "CombatReceivePackNotify", payload, 0)
		if err != nil {
			p.hpStateErrors++
			continue
		}
		p.archiveHPState(frame, decoded)
	}
}
