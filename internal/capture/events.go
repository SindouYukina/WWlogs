package capture

import (
	"fmt"
	"time"
)

type combatEventLabelTextJSONL struct {
	Locale   string `json:"locale"`
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence,omitempty"`
}

type combatEvent struct {
	Timestamp     time.Time
	SessionNumber int
	// RPCID is the RequestId on the wire; RequestEpoch the wraps of the client's RequestId counter before the request.
	RPCID             uint64
	RequestEpoch      uint64
	DamageID          uint64
	SourceEntityID    uint64
	HasSourceEntity   bool
	TargetEntityID    uint64
	HasTargetEntity   bool
	Source            entityIdentity
	Target            entityIdentity
	SummonerCharacter summonerCharacterIdentity
	// SourceVisionSummonCfgs lists SummonCfg ids of ConcomitantVision entities whose SummonerComponent names the source entity.
	SourceVisionSummonCfgs []uint64
	CharacterID            uint64
	CharacterName          string
	CharacterNameStatus    resolutionStatus
	CharacterStatus        resolutionStatus
	CharacterProof         string
	SkillID                uint64
	SkillMessageID         uint64
	SkillName              string
	SkillNameStatus        resolutionStatus
	SkillNameKind          string
	// SkillDevLabel is the developer-label text of a skill that has no official name (master locale, zh-CN fallback).
	SkillDevLabel              string
	SkillDevLabelKind          string
	SkillStatus                resolutionStatus
	SkillProof                 string
	HasDamageID                bool
	DamageSourceType           uint64
	HasDamageSourceType        bool
	BulletID                   int64
	HasBulletID                bool
	Damage                     int64
	ServerResultRaw            int32
	HasServerResult            bool
	ChangeLifeRaw              int32
	HasChangeLife              bool
	DamageCalculateType        int32
	HasCalculateType           bool
	EventTypeCandidate         string
	EventTypeStatus            resolutionStatus
	EventTypeProof             string
	DamageContributorCandidate *damageContributorCandidate
	// Notify is set on a result read from a server-executed DamageExecuteNotify (damage_notify.go); nil for a response.
	Notify *notifiedResult
}

func (p *processor) buildDamageEvent(rpcID uint64, request pendingDamageRequest, resultRaw, changeLifeRaw int32, hasChangeLife bool, damage int64) combatEvent {
	event := combatEvent{
		Timestamp:           p.currentFrameTime,
		SessionNumber:       p.sessionNumber,
		RPCID:               rpcID,
		RequestEpoch:        request.requestEpoch,
		DamageID:            request.damageID,
		SourceEntityID:      request.attackerEntityID,
		HasSourceEntity:     request.hasAttackerEntityID && request.attackerEntityID != 0,
		TargetEntityID:      request.targetEntityID,
		HasTargetEntity:     request.hasTargetEntityID && request.targetEntityID != 0,
		Source:              resolveEntityIdentity(p.entities, p.masters, request.attackerEntityID),
		Target:              resolveEntityIdentity(p.entities, p.masters, request.targetEntityID),
		SkillID:             request.skillID,
		SkillMessageID:      request.skillMessageID,
		HasDamageID:         request.hasDamageID,
		DamageSourceType:    request.damageSourceType,
		HasDamageSourceType: request.hasDamageSourceType,
		BulletID:            request.bulletID,
		HasBulletID:         request.hasBulletID,
		Damage:              damage,
		ServerResultRaw:     resultRaw,
		HasServerResult:     true,
		ChangeLifeRaw:       changeLifeRaw,
		HasChangeLife:       hasChangeLife,
		CharacterStatus:     resolutionUnknown,
		CharacterNameStatus: resolutionUnknown,
		SkillStatus:         resolutionUnknown,
		SkillNameStatus:     resolutionUnknown,
		EventTypeStatus:     resolutionUnknown,
	}
	event.SummonerCharacter = resolveSummonerCharacter(p.entities, p.masters, event.Source.Runtime)
	event.SourceVisionSummonCfgs = p.entities.visionSummonConfigs(request.attackerEntityID)
	event.resolveEventType(p.masters)
	if event.Source.HasCharacter && (event.Source.CharacterStatus == resolutionVerified || event.Source.CharacterStatus == resolutionCorrelated) {
		event.CharacterID = event.Source.Character.ID
		event.CharacterNameStatus = resolutionStatus(event.Source.Character.NameStatus)
		if event.CharacterNameStatus == resolutionVerified || event.CharacterNameStatus == resolutionCorrelated {
			event.CharacterName = event.Source.Character.Name
		}
		event.CharacterStatus = event.Source.CharacterStatus
		event.CharacterProof = event.Source.CharacterProof
	} else if p.masters != nil {
		if roleID, ok := p.sourceRoles.correlatedRole(request.attackerEntityID); ok {
			if character, exists := p.masters.characters[roleID]; exists {
				event.CharacterID = character.ID
				event.CharacterNameStatus = resolutionStatus(character.NameStatus)
				if event.CharacterNameStatus == resolutionVerified || event.CharacterNameStatus == resolutionCorrelated {
					event.CharacterName = character.Name
				}
				event.CharacterStatus = resolutionCorrelated
				event.CharacterProof = "2+ distinct DamageContext.SkillId values for this source map to RoleId through SkillDescription -> Skill"
			}
		}
	}
	if label := resolveSkillLabelText(p.masters, request.skillID); label != nil {
		event.SkillDevLabel, event.SkillDevLabelKind = label.Text, label.Kind
	}
	if p.masters != nil {
		if skill, ok := p.masters.damageSkill(request.skillID); ok {
			event.SkillStatus = resolutionStatus(skill.Status)
			event.SkillNameStatus = resolutionStatus(skill.NameStatus)
			if event.SkillNameStatus == resolutionVerified || event.SkillNameStatus == resolutionCorrelated {
				event.SkillName = skill.Name
				event.SkillNameKind = skill.NameKind
			}
			event.SkillProof = skill.SourceTable
		}
	}
	event.DamageContributorCandidate = resolveVehicleDamageContributorCandidate(p.entities, p.masters, event)
	return event
}

func (event *combatEvent) resolveEventType(masters *gameMaster) {
	if masters == nil || !event.HasDamageID {
		return
	}
	record, ok := masters.damage[event.DamageID]
	if !ok {
		return
	}
	event.DamageCalculateType = record.CalculateType
	event.HasCalculateType = true
	event.EventTypeStatus = resolutionCorrelated
	source := fmt.Sprintf("%s CalculateType=%d (combat master SQLite SHA-256 %s)", record.SourceTable, record.CalculateType, record.SourceHash)
	switch record.CalculateType {
	case 0:
		event.EventTypeCandidate = "DAMAGE_CANDIDATE"
		event.EventTypeProof = source + "; client code (static research): DamageUiController shows CalculateType 0 as ordinary damage text"
	case 1:
		event.EventTypeCandidate = "HEALING_CANDIDATE"
		event.EventTypeProof = source + "; client code (static research): DamageUiController shows CalculateType 1 as a cure/heal number"
	default:
		event.EventTypeCandidate = "OTHER_COMBAT_RESULT_CANDIDATE"
		event.EventTypeProof = source + "; nonzero type other than 1 has no verified event semantics"
	}
}

// writeDamageEvent records one damage result in the core statistics, hands it to the development outputs and returns
// whether the event passes the normal player-relevance filter. The outputs cannot stop the counters that follow them.
func (p *processor) writeDamageEvent(event combatEvent) bool {
	sourceRelation, targetRelation, visible := p.playerRelevantDamageEvent(event)
	if record, ok := normalizeCombatEvent(event, sourceRelation, targetRelation); ok && p.damageStatistics != nil {
		record = p.gateAttribution(record)
		record.SourceActor, record.TargetActor = combatActorTypeOf(event.Source), combatActorTypeOf(event.Target)
		switch record.Flow {
		case CombatFlowLocalOutgoing:
			record.SourceRole = p.localRoleForEntity(event.SourceEntityID, event.Source)
			if event.Notify != nil && event.Notify.NegativeStatusLocal && event.Notify.Credit != nil {
				// The scene combat entity stays the source; the role is the one whose trigger or status instance the server
				// executed (negative_status_credit.go).
				credit := *event.Notify.Credit
				record.SourceRole = &credit
			}
			if candidate := event.DamageContributorCandidate; candidate != nil && candidate.Kind == vehicleKindMotorcycle && candidate.ActorType == "Vehicle" && candidate.EntityType == "BattleVehicle" && candidate.Status == resolutionCorrelated {
				record.VehicleKind = candidate.Kind
			}
		case CombatFlowLocalIncoming:
			record.TargetRole = p.localRoleForEntity(event.TargetEntityID, event.Target)
		case CombatFlowLocalInternal:
			if record.Kind == CombatEventHealing {
				record = p.gateHealing(record)
				record.SourceRole = p.localRoleForEntity(event.SourceEntityID, event.Source)
				record.TargetRole = p.localRoleForEntity(event.TargetEntityID, event.Target)
			}
		}
		assignment := p.damageStatistics.addCombatEvent(record)
		p.writeCombatEventRecord(record, assignment)
		p.flushEncounterTransitions()
	}
	p.writeDamageEventOutputs(&event, visible, sourceRelation, targetRelation)
	if !visible {
		p.stats.suppressedUnrelatedEvents++
		return false
	}
	if sourceRelation == playerRelationLocal || targetRelation == playerRelationLocal {
		p.stats.playerRelatedEvents++
	} else {
		p.stats.includedUnresolvedRelations++
	}
	p.stats.printed++
	p.stats.matchedCombatResultAbs += event.Damage
	return true
}
