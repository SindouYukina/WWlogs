//go:build wwdev

package capture

import ()

// archiveSummonOwners writes ownership facts from one decoded join-scene or
// entity-add message. Entities are walked through the message tree, so nested
// entity lists in any join or add layout are covered.
func (p *processor) archiveSummonOwners(frame gameFrame, command string, decoded *decodedMessage) {
	if p.dev.summonOwnerOut == nil || decoded == nil {
		return
	}
	walkEntities(decoded, func(entity *decodedMessage) {
		entityID, ok := scalarValue(entity, "Id")
		if !ok || entityID <= 0 {
			return
		}
		// Each ComponentPbs element wraps one component message as a field.
		for _, wrapper := range childMessages(entity, "ComponentPbs") {
			for _, field := range wrapper.Fields {
				if field.Message == nil {
					continue
				}
				switch field.Message.Type {
				case "SummonerComponentPb":
					p.writeSummonerRecord(frame, command, uint64(entityID), field.Message)
				case "ConcomitantsComponentPb":
					p.writeRelatedRecords(frame, command, uint64(entityID), field.Message, "concomitant", "VisionEntityIds", "CustomEntityIds")
				case "DependenceComponentPb":
					p.writeRelatedRecords(frame, command, uint64(entityID), field.Message, "dependence", "DependenceEntityIds")
				}
			}
		}
	})
}

// walkEntities calls visit for every EntityPb message in the decoded tree.
func walkEntities(message *decodedMessage, visit func(*decodedMessage)) {
	if message == nil {
		return
	}
	if message.Type == "EntityPb" {
		visit(message)
	}
	for _, field := range message.Fields {
		if field.Message != nil {
			walkEntities(field.Message, visit)
		}
	}
}

func (p *processor) writeSummonerRecord(frame gameFrame, command string, entityID uint64, component *decodedMessage) {
	record := p.summonOwnerBase(frame, command, "summoner", entityID)
	record.Evidence = "SummonerComponentPb on the entity; fields copied as observed, owner meaning per the client SummonCfgId/SummonerPlayerEntityId reads"
	if value, ok := scalarValue(component, "SummonerId"); ok {
		record.SummonerID = &value
	}
	if value, ok := scalarValue(component, "SummonCfgId"); ok {
		record.SummonCfgID = &value
	}
	if value, ok := scalarValue(component, "SummonSkillId"); ok {
		record.SummonSkillID = &value
	}
	if value, ok := scalarValue(component, "PlayerId"); ok {
		record.PlayerID = &value
	}
	if value, ok := scalarValue(component, "Type"); ok {
		record.SummonTypeRaw = &value
		if name, known := p.dev.protocolSchema.enumName("ESummonType", value); known {
			record.SummonType = name
		}
	}
	if record.SummonSkillID != nil && p.masters != nil {
		entity := runtimeEntity{summonSkillID: uint64(*record.SummonSkillID), hasSummonSkillID: true}
		if record.SummonTypeRaw != nil {
			entity.summonType, entity.hasSummonType = uint64(*record.SummonTypeRaw), true
		}
		resolution := resolveSummonSkill(p.masters, entity)
		record.SummonSkillNamespace, record.SummonSkillStatus = resolution.Namespace, resolution.Status
		record.SummonSkillDevLabel = resolution.DevLabel
	}
	p.writeSummonOwnerRecord(record)
}

func (p *processor) writeRelatedRecords(frame gameFrame, command string, entityID uint64, component *decodedMessage, kind string, listFields ...string) {
	for _, list := range listFields {
		for _, field := range component.Fields {
			if field.Name != list {
				continue
			}
			value, ok := field.Value.(int64)
			if !ok {
				if number, numberOK := field.Value.(int32); numberOK {
					value, ok = int64(number), true
				}
			}
			if !ok {
				continue
			}
			related := value
			record := p.summonOwnerBase(frame, command, kind, entityID)
			record.RelatedID = &related
			record.Evidence = "component entity list entry, copied as observed"
			p.writeSummonOwnerRecord(record)
		}
	}
}

func (p *processor) summonOwnerBase(frame gameFrame, command string, kind string, entityID uint64) summonOwnerRecord {
	return summonOwnerRecord{
		SchemaVersion: summonOwnerSchemaVersion,
		FrameOrdinal:  p.dev.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Timestamp:     formatTimestamp(frame.timestamp),
		Command:       command,
		Kind:          kind,
		EntityID:      entityID,
	}
}

func (p *processor) writeSummonOwnerRecord(record summonOwnerRecord) {
	writeResearchRecord(p.dev.summonOwnerOut, record, &p.dev.summonOwnerErrors)
}

// archiveSummonOwnerFrame decodes join-scene and entity-add frames for the owner
// output when that output is enabled. It shares the plaintext with the other
// observers.
func (p *processor) archiveSummonOwnerFrame(frame gameFrame, plain []byte) {
	if p.dev.summonOwnerOut == nil || p.dev.protocolSchema == nil {
		return
	}
	var message string
	switch frame.commandID {
	case cmdJoinSceneNotify:
		message = "JoinSceneNotify"
	case cmdEntityAddNotify:
		message = "EntityAddNotify"
	default:
		return
	}
	decoded, err := decodeSchemaMessage(p.dev.protocolSchema, message, plain, 0)
	if err != nil {
		p.dev.summonOwnerErrors++
		return
	}
	p.archiveSummonOwners(frame, message, decoded)
}
