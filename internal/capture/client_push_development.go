//go:build wwdev

package capture

import ()

// archiveClientPushResearch decodes an outbound CombatSendPackRequest a second time with the research schema
// (-protocol-schema) for the client-push, bullet and skill research outputs. It runs after the core role decoder
// (observeClientPushFrame) on the same plaintext and never changes core state.
func (p *processor) archiveClientPushResearch(frame gameFrame, plain []byte) {
	if p.dev.protocolSchema == nil {
		return
	}
	decoded, err := decodeSchemaMessage(p.dev.protocolSchema, "CombatSendPackRequest", plain, 0)
	if err != nil {
		p.dev.clientPushErrors++
		return
	}
	p.archiveClientPushes(frame, decoded)
}

// archiveClientPushes writes the role reports and skill-end pushes from one
// decoded CombatSendPackRequest. It keeps the most recent reported role so that
// each skill row carries the role that was active when it was reported.
func (p *processor) archiveClientPushes(frame gameFrame, decoded *decodedMessage) {
	if decoded == nil {
		return
	}
	for _, data := range childMessages(decoded, "Data") {
		for _, request := range childMessages(data, "Request") {
			p.observeCombatActionRequest(frame, request)
		}
		if p.dev.bulletRefOut != nil {
			p.observeBulletReferences(frame, data, "out")
		}
		if p.dev.skillRefOut != nil {
			p.observeSkillReferences(frame, data, "out")
		}
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

func (p *processor) observeCombatActionRequest(frame gameFrame, request *decodedMessage) {
	if p.dev.combatActionRequests == nil || request == nil {
		return
	}
	common := firstChildMessage(request, "CombatCommon")
	if common == nil {
		return
	}
	messageIDValue, hasMessageID := scalarValue(common, "MessageId")
	if !hasMessageID || messageIDValue <= 0 {
		return
	}
	messageID := uint64(messageIDValue)
	requestType := ""
	var info *decodedMessage
	for _, child := range request.Fields {
		if child.Message == nil {
			continue
		}
		switch child.Name {
		case "UseSkillRequest", "SkillRequest":
			requestType = child.Name
			info = firstChildMessage(child.Message, "UseSkillInfo")
		}
	}
	if info == nil {
		return
	}
	skillID, hasSkillID := scalarValue(info, "SkillId")
	if !hasSkillID || skillID <= 0 {
		return
	}
	entityIDValue, _ := scalarValue(common, "EntityId")
	targetIDValue, _ := scalarValue(info, "TargetId")
	if p.dev.combatActionOrder == nil {
		p.dev.combatActionOrder = make([]combatActionRequestKey, 0, maxCombatActionRequests)
	}
	key := combatActionRequestKey{sessionNumber: p.sessionNumber, messageID: messageID}
	if _, exists := p.dev.combatActionRequests[key]; exists {
		return
	}
	p.dev.combatActionOrder = append(p.dev.combatActionOrder, key)
	p.dev.combatActionRequests[key] = combatActionRequest{
		SessionNumber: p.sessionNumber, MessageID: messageID, EntityID: uint64(max(entityIDValue, 0)), SkillID: uint64(skillID), TargetID: uint64(max(targetIDValue, 0)),
		RequestType: requestType, Timestamp: formatTimestamp(frame.timestamp),
	}
	if p.dev.clientPushOut != nil {
		messageIDInt := messageIDValue
		skillIDInt := skillID
		record := clientPushRecord{
			SchemaVersion: clientPushSchemaVersion,
			FrameOrdinal:  p.dev.frameOrdinal - 1,
			SessionNumber: p.sessionNumber,
			Timestamp:     formatTimestamp(frame.timestamp),
			PushName:      "SkillActionRequest",
			MessageID:     &messageIDInt,
			SkillID:       &skillIDInt,
			RequestType:   requestType,
			Evidence:      "outbound skill request only; zero linked damage results do not establish a miss",
		}
		if entityIDValue > 0 {
			entity := entityIDValue
			record.ActorEntityID = &entity
		}
		if targetIDValue > 0 {
			target := targetIDValue
			record.TargetEntityID = &target
		}
		p.writeClientPush(record)
	}
	for len(p.dev.combatActionRequests) > maxCombatActionRequests && len(p.dev.combatActionOrder) > 0 {
		oldest := p.dev.combatActionOrder[0]
		p.dev.combatActionOrder = p.dev.combatActionOrder[1:]
		delete(p.dev.combatActionRequests, oldest)
	}
}

// linkSkillRequest counts one damage result against the research record of its outbound skill request
// (observeCombatActionRequest) and returns a copy for the event line. writeDamageEventOutputs calls it once per damage
// result; the core neither calls nor reads it. The returned copy is a separate variable so that only a found request is
// allocated: a returned &action would move every lookup to the heap.
func (p *processor) linkSkillRequest(messageID uint64) *combatActionRequest {
	if messageID == 0 {
		return nil
	}
	key := combatActionRequestKey{sessionNumber: p.sessionNumber, messageID: messageID}
	action, ok := p.dev.combatActionRequests[key]
	if !ok {
		return nil
	}
	action.LinkedDamageEvents++
	p.dev.combatActionRequests[key] = action
	linked := action
	return &linked
}

func firstChildMessage(message *decodedMessage, name string) *decodedMessage {
	if message == nil {
		return nil
	}
	for _, field := range message.Fields {
		if field.Name == name && field.Message != nil {
			return field.Message
		}
	}
	return nil
}

func (p *processor) observeRoleReport(frame gameFrame, message *decodedMessage) {
	role, hasRole := scalarValue(message, "CurrentRoleId")
	entity, hasEntity := scalarValue(message, "CurrentEntityId")
	record := clientPushRecord{
		SchemaVersion: clientPushSchemaVersion,
		FrameOrdinal:  p.dev.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Timestamp:     formatTimestamp(frame.timestamp),
		PushName:      "ClientCurrentRoleReportPush",
		Evidence:      "client-reported current role; a change between reports is a candidate character switch",
	}
	if hasRole {
		record.CurrentRoleID = &role
		p.dev.activeRoleID = role
		p.dev.hasActiveRole = true
	}
	// The role entity itself is tracked by the core decoder (observeLocalRoleReports), with its conflict check, for the
	// same push; this research record only reports what the schema decoded.
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
			FrameOrdinal:  p.dev.frameOrdinal - 1,
			SessionNumber: p.sessionNumber,
			Timestamp:     formatTimestamp(frame.timestamp),
			PushName:      "EndSkillPush",
			Evidence:      "client-reported skill end; the skill identifier is as observed and has no name mapping yet",
		}
		// The reported active role is context only. Skills of other entities, such as
		// boss or healer effects, are reported while a character is active. The owner
		// candidate comes from the thousands prefix of SkillId, which matches RoleId in
		// the preload table; it is CORRELATED, not VERIFIED.
		if p.dev.hasActiveRole {
			role := p.dev.activeRoleID
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
	if p.dev.clientPushOut == nil {
		return
	}
	writeResearchRecord(p.dev.clientPushOut, record, &p.dev.clientPushErrors)
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
