//go:build wwdev

package main

import (
	"encoding/json"
	"strings"
)

// resolveSkillRef checks a SkillId against the actors reachable from the firing entity. The chain
// deliberately omits the skill-derived echo link, so the skill never helps choose the actor that
// is then used to validate it.
func resolveSkillRef(masters *gameMaster, skillID uint64, source bulletSource) (resolutionStatus, string, string, string) {
	if masters == nil {
		return resolutionUnknown, "", "", "no master database loaded"
	}
	source.SkillID = 0
	chains := masters.bulletChains(source)
	for _, chain := range chains {
		if _, ok := masters.actorSkills[chain.actor][skillID]; ok {
			return resolutionCorrelated, "EntitySkillPreload.SkillId", masters.assetPath[chain.actor],
				chain.via + "; the actor's EntitySkillPreload rows list this SkillId; client build observed, master compatibility unverified"
		}
	}
	// Common skills are loaded for every actor, so they are not listed per actor.
	if _, ok := masters.commonSkills[skillID]; ok {
		return resolutionCorrelated, "CommonSkillPreload.Id", "",
			"the SkillId is a CommonSkillPreload row, loaded for every actor rather than listed per actor; client build observed, master compatibility unverified"
	}
	if len(chains) == 0 {
		return resolutionUnknown, "", "", "the source entity has no actor chain in the loaded master"
	}
	return resolutionUnknown, "", "", "the SkillId is not listed by any source-chain actor or by CommonSkillPreload"
}

// skillSourceCandidates returns the ordered entity-id sources for a message that holds a SkillId.
// Bullet-carrying holders reuse the bullet rules; UseSkillInformation names its own CombatCommon
// sender; other holders fall back to the enclosing CombatCommon and say so.
func skillSourceCandidates(message, parent, common *decodedMessage) []bulletSourceCandidate {
	switch message.Type {
	case "CreateBulletPush", "CreateBulletRequest", "CreateBulletNotify", "DamageContext", "HitInformation", "InterruptSkillInfo", "DamageRecordNotify":
		return bulletSourceCandidates(message, parent, common)
	case "UseSkillInformation":
		if value, ok := scalarValue(common, "EntityId"); ok && value > 0 {
			return []bulletSourceCandidate{{"UseSkillInformation.CombatCommon.EntityId", value}}
		}
		return nil
	}
	if value, ok := scalarValue(common, "EntityId"); ok && value > 0 {
		return []bulletSourceCandidate{{"CombatCommon.EntityId (default)", value}}
	}
	return nil
}

func (p *processor) observeSkillReferences(frame gameFrame, data *decodedMessage, direction string) {
	p.walkSkillReferences(frame, data, "Data", nil, nil, direction)
}

func (p *processor) walkSkillReferences(frame gameFrame, message *decodedMessage, path string, parent, common *decodedMessage, direction string) {
	if message == nil {
		return
	}
	if own := firstChildMessage(message, "CombatCommon"); own != nil {
		common = own
	}
	if skillID, ok := scalarValue(message, "SkillId"); ok && skillID > 0 {
		p.writeSkillReference(frame, message, parent, path+".SkillId", skillID, common, direction)
	}
	for _, field := range message.Fields {
		if field.Message != nil {
			p.walkSkillReferences(frame, field.Message, path+"."+field.Name, message, common, direction)
		}
	}
}

func (p *processor) writeSkillReference(frame gameFrame, message, parent *decodedMessage, fieldPath string, skillID int64, common *decodedMessage, direction string) {
	record := skillRefRecord{
		SchemaVersion: skillRefSchemaVersion,
		FrameOrdinal:  p.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Timestamp:     formatTimestamp(frame.timestamp),
		Direction:     direction,
		FieldPath:     strings.TrimPrefix(fieldPath, "Data."),
		Holder:        message.Type,
		SkillID:       skillID,
		Status:        resolutionUnknown,
	}
	var identity entityIdentity
	found := false
	for _, candidate := range skillSourceCandidates(message, parent, common) {
		resolved := resolveEntityIdentity(p.entities, p.masters, uint64(candidate.id))
		if resolved.RuntimeStatus == resolutionUnknown {
			continue
		}
		id := candidate.id
		record.SourceEntityID, record.SourceBasis = &id, candidate.basis
		identity, found = resolved, true
		break
	}
	if !found {
		record.Evidence = "no candidate source entity is known to the runtime resolver"
		if p.masters == nil {
			record.Evidence = "no master database loaded"
		}
	} else {
		source := newBulletSource(identity, 0, p.entities.visionSummonConfigs(uint64(*record.SourceEntityID)))
		record.Status, record.Namespace, record.ActorBlueprint, record.Evidence = resolveSkillRef(p.masters, uint64(skillID), source)
	}
	if label := resolveSkillLabel(p.masters, uint64(skillID)); label != nil && label.Status == resolutionCorrelated {
		record.DevLabelZhCn = label.Label
	}
	line, err := json.Marshal(record)
	if err != nil {
		p.skillRefErrors++
		return
	}
	if _, err := p.skillRefOut.Write(append(line, '\n')); err != nil {
		p.skillRefErrors++
	}
}
