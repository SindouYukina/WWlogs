//go:build wwdev

package capture

import "strings"

const skillRefSchemaVersion = 1

// skillRefRecord is one SkillId carried by a combat message with its namespace and actor-owner evidence.
type skillRefRecord struct {
	SchemaVersion  int              `json:"schemaVersion"`
	FrameOrdinal   uint64           `json:"frameOrdinal"`
	SessionNumber  int              `json:"sessionNumber"`
	Timestamp      string           `json:"timestamp"`
	Direction      string           `json:"direction"`
	FieldPath      string           `json:"fieldPath"`
	Holder         string           `json:"holder"`
	SkillID        int64            `json:"skillId"`
	SourceEntityID *int64           `json:"sourceEntityId,omitempty"`
	SourceBasis    string           `json:"sourceBasis,omitempty"`
	Status         resolutionStatus `json:"status"`
	Namespace      string           `json:"namespace,omitempty"`
	ActorBlueprint string           `json:"actorBlueprint,omitempty"`
	DevLabelZhCn   string           `json:"devLabelZhCn,omitempty"`
	Evidence       string           `json:"evidence"`
}

// resolveSkillRef validates a SkillId against an available source chain or a unique static owner.
// The skill-derived echo link remains excluded because it cannot independently validate itself.
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
		if actor, ok := masters.uniqueActorForSkill(skillID); ok {
			return resolutionCorrelated, "EntitySkillPreload.SkillId", masters.assetPath[actor],
				"SkillId -> exactly one distinct EntitySkillPreload.ActorAssetId -> AssetReference.Path in the loaded master; client build observed, master compatibility unverified"
		}
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
		FrameOrdinal:  p.dev.frameOrdinal - 1,
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
	candidates := skillSourceCandidates(message, parent, common)
	if len(candidates) != 0 {
		candidate := candidates[0]
		id := candidate.id
		record.SourceEntityID, record.SourceBasis = &id, candidate.basis
		resolved := resolveEntityIdentity(p.entities, p.masters, uint64(candidate.id))
		if resolved.RuntimeStatus != resolutionUnknown {
			identity, found = resolved, true
		}
	}
	source := bulletSource{}
	if found {
		source = newBulletSource(identity, 0, p.entities.visionSummonConfigs(uint64(*record.SourceEntityID)))
	}
	record.Status, record.Namespace, record.ActorBlueprint, record.Evidence = resolveSkillRef(p.masters, uint64(skillID), source)
	if !found && record.Status == resolutionUnknown && p.masters != nil {
		record.Evidence = "no candidate source entity is known to the runtime resolver; " + record.Evidence
	}
	if label := resolveSkillLabel(p.masters, uint64(skillID)); label != nil && label.Status == resolutionCorrelated {
		record.DevLabelZhCn = label.Label
	}
	writeResearchRecord(p.dev.skillRefOut, record, &p.dev.skillRefErrors)
}
