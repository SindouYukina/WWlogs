//go:build wwdev

package capture

import "fmt"

// hasShieldDefinition reports whether shield has a normalized row with this key.
func hasShieldDefinition(m *gameMaster, id uint64) bool {
	if m == nil {
		return false
	}
	_, ok := m.shieldDefinitions[id]
	return ok
}

const shieldRefSchemaVersion = 1

// shieldConfigRefRecord links one typed ShieldUpdateInfo.ConfigId to the normalized shield table.
type shieldConfigRefRecord struct {
	SchemaVersion          int              `json:"schemaVersion"`
	FrameOrdinal           uint64           `json:"frameOrdinal"`
	SessionNumber          int              `json:"sessionNumber"`
	Timestamp              string           `json:"timestamp"`
	FrameToken             uint16           `json:"frameToken"`
	OuterCommandID         uint16           `json:"outerCommandId"`
	Direction              string           `json:"direction"`
	FieldPath              string           `json:"fieldPath"`
	ShieldIndex            int              `json:"shieldIndex"`
	Handle                 *int64           `json:"handle,omitempty"`
	ConfigID               *int64           `json:"configId,omitempty"`
	ShieldValue            *int64           `json:"shieldValue,omitempty"`
	UpdateType             *int64           `json:"updateType,omitempty"`
	ShieldDefinitionStatus resolutionStatus `json:"shieldDefinitionStatus"`
	ShieldDefinitionTable  string           `json:"shieldDefinitionTable,omitempty"`
	ShieldDefinitionID     *uint64          `json:"shieldDefinitionId,omitempty"`
	Evidence               string           `json:"evidence"`
}

// observeShieldReferences resolves only ConfigId values in the named ShieldUpdateNotify context.
func (p *processor) observeShieldReferences(frame gameFrame, data *decodedMessage, direction string) {
	p.walkShieldReferences(frame, data, "Data", direction)
}

func (p *processor) walkShieldReferences(frame gameFrame, message *decodedMessage, path, direction string) {
	if message == nil {
		return
	}
	if message.Type == "ShieldUpdateNotify" {
		for index, shield := range childMessages(message, "Shields") {
			p.writeShieldReference(frame, shield, fmt.Sprintf("%s.Shields[%d]", path, index), index, direction)
		}
	}
	for _, field := range message.Fields {
		if field.Message != nil {
			p.walkShieldReferences(frame, field.Message, path+"."+field.Name, direction)
		}
	}
}

func (p *processor) writeShieldReference(frame gameFrame, message *decodedMessage, path string, index int, direction string) {
	record := shieldConfigRefRecord{
		SchemaVersion:          shieldRefSchemaVersion,
		FrameOrdinal:           p.dev.frameOrdinal - 1,
		SessionNumber:          p.sessionNumber,
		Timestamp:              formatTimestamp(frame.timestamp),
		FrameToken:             frame.token,
		OuterCommandID:         frame.commandID,
		Direction:              direction,
		FieldPath:              path + ".ConfigId",
		ShieldIndex:            index,
		ShieldDefinitionStatus: resolutionUnknown,
		Evidence:               "ShieldUpdateInfo.ConfigId is absent; no static definition lookup was performed",
	}
	if value, ok := scalarValue(message, "Handle"); ok {
		record.Handle = &value
	}
	if value, ok := scalarValue(message, "ConfigId"); ok {
		record.ConfigID = &value
		record.Evidence = "No exact normalized shield.id row has been linked; raw ConfigId remains unchanged and no other ID namespace is substituted"
		if value >= 0 && p.masters != nil && hasShieldDefinition(p.masters, uint64(value)) {
			definitionID := uint64(value)
			record.ShieldDefinitionStatus = resolutionCorrelated
			record.ShieldDefinitionTable = "shield"
			record.ShieldDefinitionID = &definitionID
			record.Evidence = "Protocol schema path CombatNotifyData.ShieldUpdateNotify.Shields[].ConfigId has an exact normalized shield.id row; same-valued IDs in other namespaces remain separate"
		}
	}
	if value, ok := scalarValue(message, "ShieldValue"); ok {
		record.ShieldValue = &value
	}
	if value, ok := scalarValue(message, "UpdateType"); ok {
		record.UpdateType = &value
	}
	writeResearchRecord(p.dev.shieldRefOut, record, &p.dev.shieldRefErrors)
}
