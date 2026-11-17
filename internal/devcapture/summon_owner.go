package devcapture

import (
	"wwlogs/internal/capture"
)

// summonOwnerSchemaVersion identifies the layout of summonOwnerRecord.
const summonOwnerSchemaVersion = 1

// summonOwnerRecord is one ownership or dependence fact from an entity component.
// Kind is one of "summoner" (SummonerComponentPb on the entity), "concomitant"
// (ConcomitantsComponentPb.VisionEntityIds and CustomEntityIds on the entity), or
// "dependence" (DependenceComponentPb.DependenceEntityIds on the entity). The
// facts are copied as observed. Role and summon identity are not inferred here.
type summonOwnerRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	FrameOrdinal  uint64 `json:"frameOrdinal"`
	SessionNumber int    `json:"sessionNumber"`
	Timestamp     string `json:"timestamp"`
	Command       string `json:"command"`
	Kind          string `json:"kind"`
	EntityID      uint64 `json:"entityId"`
	RelatedID     *int64 `json:"relatedEntityId,omitempty"`
	SummonerID    *int64 `json:"summonerId,omitempty"`
	SummonCfgID   *int64 `json:"summonCfgId,omitempty"`
	SummonSkillID *int64 `json:"summonSkillId,omitempty"`
	PlayerID      *int64 `json:"playerId,omitempty"`
	SummonType    string `json:"summonType,omitempty"`
	SummonTypeRaw *int64 `json:"summonTypeRaw,omitempty"`
	// SummonSkillNamespace and SummonSkillStatus are the master translation of SummonSkillId (see resolveSummonSkill).
	SummonSkillNamespace string                   `json:"summonSkillNamespace,omitempty"`
	SummonSkillStatus    capture.ResolutionStatus `json:"summonSkillStatus,omitempty"`
	// SummonSkillDevLabel is the developer note of the Vision row the key resolves to; never an official name.
	SummonSkillDevLabel *capture.CombatEventLabelTextJSONL `json:"summonSkillDevLabel,omitempty"`
	Evidence            string                             `json:"evidence"`
}

// archiveSummonOwners writes ownership facts from one decoded join-scene or
// entity-add message. Entities are walked through the message tree, so nested
// entity lists in any join or add layout are covered.
func (p *processor) archiveSummonOwners(frame capture.GameFrame, command string, decoded *decodedMessage) {
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

func (p *processor) writeSummonerRecord(frame capture.GameFrame, command string, entityID uint64, component *decodedMessage) {
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
	if record.SummonSkillID != nil && p.Masters != nil {
		entity := capture.RuntimeEntity{SummonSkillID: uint64(*record.SummonSkillID), HasSummonSkillID: true}
		if record.SummonTypeRaw != nil {
			entity.SummonType, entity.HasSummonType = uint64(*record.SummonTypeRaw), true
		}
		resolution := capture.ResolveSummonSkill(p.Masters, entity)
		record.SummonSkillNamespace, record.SummonSkillStatus = resolution.Namespace, resolution.Status
		record.SummonSkillDevLabel = resolution.DevLabel
	}
	p.writeSummonOwnerRecord(record)
}

func (p *processor) writeRelatedRecords(frame capture.GameFrame, command string, entityID uint64, component *decodedMessage, kind string, listFields ...string) {
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

func (p *processor) summonOwnerBase(frame capture.GameFrame, command string, kind string, entityID uint64) summonOwnerRecord {
	return summonOwnerRecord{
		SchemaVersion: summonOwnerSchemaVersion,
		FrameOrdinal:  p.dev.frameOrdinal - 1,
		SessionNumber: p.SessionNumber,
		Timestamp:     formatTimestamp(frame.Timestamp),
		Command:       command,
		Kind:          kind,
		EntityID:      entityID,
	}
}

func (p *processor) writeSummonOwnerRecord(record summonOwnerRecord) {
	writeResearchRecord(p.dev.summonOwnerOut, record, &p.dev.summonOwnerErrors)
}

// ArchiveSummonOwnerFrame decodes join-scene and entity-add frames for the owner
// output when that output is enabled. It shares the plaintext with the other
// observers.
func (p *processor) ArchiveSummonOwnerFrame(frame capture.GameFrame, plain []byte) {
	if p.dev.summonOwnerOut == nil || p.dev.protocolSchema == nil {
		return
	}
	var message string
	switch frame.CommandID {
	case capture.CmdJoinSceneNotify:
		message = "JoinSceneNotify"
	case capture.CmdEntityAddNotify:
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
