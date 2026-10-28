//go:build wwdev

package main

import (
	"encoding/json"
)

// archiveClientPushes writes the role reports and skill-end pushes from one
// decoded CombatSendPackRequest. It keeps the most recent reported role so that
// each skill row carries the role that was active when it was reported.
func (p *processor) archiveClientPushes(frame gameFrame, decoded *decodedMessage) {
	if p.clientPushOut == nil || decoded == nil {
		return
	}
	for _, data := range childMessages(decoded, "Data") {
		for _, push := range childMessages(data, "Push") {
			for _, child := range push.Fields {
				if child.Message == nil {
					continue
				}
				switch child.Name {
				case "ClientCurrentRoleReportPush":
					p.observeRoleReport(frame, child.Message)
				case "EndSkillPush":
					p.observeEndSkill(frame, child.Message)
				}
			}
		}
	}
}

func (p *processor) observeRoleReport(frame gameFrame, message *decodedMessage) {
	role, hasRole := scalarValue(message, "CurrentRoleId")
	entity, hasEntity := scalarValue(message, "CurrentEntityId")
	record := clientPushRecord{
		SchemaVersion: clientPushSchemaVersion,
		FrameOrdinal:  p.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Timestamp:     formatTimestamp(frame.timestamp),
		PushName:      "ClientCurrentRoleReportPush",
		Evidence:      "client-reported current role; a change between reports is a candidate character switch",
	}
	if hasRole {
		record.CurrentRoleID = &role
		p.activeRoleID = role
		p.hasActiveRole = true
	}
	if hasEntity {
		record.EntityID = &entity
	}
	p.writeClientPush(record)
}

func (p *processor) observeEndSkill(frame gameFrame, message *decodedMessage) {
	for _, usage := range childMessages(message, "UseSkillInfo") {
		skillID, hasSkill := scalarValue(usage, "SkillId")
		record := clientPushRecord{
			SchemaVersion: clientPushSchemaVersion,
			FrameOrdinal:  p.frameOrdinal - 1,
			SessionNumber: p.sessionNumber,
			Timestamp:     formatTimestamp(frame.timestamp),
			PushName:      "EndSkillPush",
			Evidence:      "client-reported skill end; the skill identifier is as observed and has no name mapping yet",
		}
		// The reported active role is context only. Skills of other entities, such as
		// boss or healer effects, are reported while a character is active. The owner
		// candidate comes from the thousands prefix of SkillId, which matches RoleId in
		// the preload table; it is CORRELATED, not VERIFIED.
		if p.hasActiveRole {
			role := p.activeRoleID
			record.ActiveRoleID = &role
		}
		if hasSkill {
			record.SkillID = &skillID
			prefix := skillID / 1000
			record.SkillRoleKey = &prefix
		}
		if value, ok := floatValue(usage, "TimeStamp"); ok {
			record.SkillTime = &value
		}
		p.writeClientPush(record)
	}
}

func (p *processor) writeClientPush(record clientPushRecord) {
	line, err := json.Marshal(record)
	if err != nil {
		p.clientPushErrors++
		return
	}
	if _, err := p.clientPushOut.Write(append(line, '\n')); err != nil {
		p.clientPushErrors++
	}
}

// floatValue reads a float field from a decoded message.
func floatValue(message *decodedMessage, name string) (float64, bool) {
	if message == nil {
		return 0, false
	}
	for _, field := range message.Fields {
		if field.Name != name || field.Value == nil {
			continue
		}
		if value, ok := field.Value.(float64); ok {
			return value, true
		}
	}
	return 0, false
}
