package capture

import (
	"fmt"
	"time"
)

type CombatEventLabelTextJSONL struct {
	Locale   string `json:"locale"`
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence,omitempty"`
}

type CombatEvent struct {
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
	Source            EntityIdentity
	Target            EntityIdentity
	SummonerCharacter SummonerCharacterIdentity
	// SourceVisionSummonCfgs lists SummonCfg ids of ConcomitantVision entities whose SummonerComponent names the source entity.
	SourceVisionSummonCfgs []uint64
	CharacterID            uint64
	CharacterName          string
	CharacterNameStatus    ResolutionStatus
	CharacterStatus        ResolutionStatus
	CharacterProof         string
	SkillID                uint64
	SkillMessageID         uint64
	SkillName              string
	SkillNameStatus        ResolutionStatus
	SkillNameKind          string
	// SkillDevLabel is the developer-label text of a skill that has no official name (master locale, zh-CN fallback).
	SkillDevLabel              string
	SkillDevLabelKind          string
	SkillStatus                ResolutionStatus
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
	EventTypeStatus            ResolutionStatus
	EventTypeProof             string
	DamageContributorCandidate *DamageContributorCandidate
	// Notify is set on a result read from a server-executed DamageExecuteNotify (damage_notify.go); nil for a response.
	Notify *NotifiedResult
}

func (p *Processor) BuildDamageEvent(rpcID uint64, request PendingDamageRequest, resultRaw, changeLifeRaw int32, hasChangeLife bool, damage int64) CombatEvent {
	event := CombatEvent{
		Timestamp:           p.CurrentFrameTime,
		SessionNumber:       p.SessionNumber,
		RPCID:               rpcID,
		RequestEpoch:        request.requestEpoch,
		DamageID:            request.DamageID,
		SourceEntityID:      request.AttackerEntityID,
		HasSourceEntity:     request.HasAttackerEntityID && request.AttackerEntityID != 0,
		TargetEntityID:      request.TargetEntityID,
		HasTargetEntity:     request.HasTargetEntityID && request.TargetEntityID != 0,
		Source:              ResolveEntityIdentity(p.Entities, p.Masters, request.AttackerEntityID),
		Target:              ResolveEntityIdentity(p.Entities, p.Masters, request.TargetEntityID),
		SkillID:             request.SkillID,
		SkillMessageID:      request.SkillMessageID,
		HasDamageID:         request.HasDamageID,
		DamageSourceType:    request.DamageSourceType,
		HasDamageSourceType: request.HasDamageSourceType,
		BulletID:            request.BulletID,
		HasBulletID:         request.HasBulletID,
		Damage:              damage,
		ServerResultRaw:     resultRaw,
		HasServerResult:     true,
		ChangeLifeRaw:       changeLifeRaw,
		HasChangeLife:       hasChangeLife,
		CharacterStatus:     ResolutionUnknown,
		CharacterNameStatus: ResolutionUnknown,
		SkillStatus:         ResolutionUnknown,
		SkillNameStatus:     ResolutionUnknown,
		EventTypeStatus:     ResolutionUnknown,
	}
	event.SummonerCharacter = ResolveSummonerCharacter(p.Entities, p.Masters, event.Source.Runtime)
	event.SourceVisionSummonCfgs = p.Entities.VisionSummonConfigs(request.AttackerEntityID)
	event.ResolveEventType(p.Masters)
	if event.Source.HasCharacter && (event.Source.CharacterStatus == ResolutionVerified || event.Source.CharacterStatus == ResolutionCorrelated) {
		event.CharacterID = event.Source.Character.ID
		event.CharacterNameStatus = ResolutionStatus(event.Source.Character.NameStatus)
		if event.CharacterNameStatus == ResolutionVerified || event.CharacterNameStatus == ResolutionCorrelated {
			event.CharacterName = event.Source.Character.Name
		}
		event.CharacterStatus = event.Source.CharacterStatus
		event.CharacterProof = event.Source.CharacterProof
	} else if p.Masters != nil {
		if roleID, ok := p.SourceRoles.CorrelatedRole(request.AttackerEntityID); ok {
			if character, exists := p.Masters.Characters[roleID]; exists {
				event.CharacterID = character.ID
				event.CharacterNameStatus = ResolutionStatus(character.NameStatus)
				if event.CharacterNameStatus == ResolutionVerified || event.CharacterNameStatus == ResolutionCorrelated {
					event.CharacterName = character.Name
				}
				event.CharacterStatus = ResolutionCorrelated
				event.CharacterProof = "2+ distinct DamageContext.SkillId values for this source map to RoleId through SkillDescription -> Skill"
			}
		}
	}
	if label := ResolveSkillLabelText(p.Masters, request.SkillID); label != nil {
		event.SkillDevLabel, event.SkillDevLabelKind = label.Text, label.Kind
	}
	if p.Masters != nil {
		if skill, ok := p.Masters.DamageSkill(request.SkillID); ok {
			event.SkillStatus = ResolutionStatus(skill.Status)
			event.SkillNameStatus = ResolutionStatus(skill.NameStatus)
			if event.SkillNameStatus == ResolutionVerified || event.SkillNameStatus == ResolutionCorrelated {
				event.SkillName = skill.Name
				event.SkillNameKind = skill.NameKind
			}
			event.SkillProof = skill.SourceTable
		}
	}
	event.DamageContributorCandidate = ResolveVehicleDamageContributorCandidate(p.Entities, p.Masters, event)
	return event
}

func (event *CombatEvent) ResolveEventType(masters *GameMaster) {
	if masters == nil || !event.HasDamageID {
		return
	}
	record, ok := masters.Damage[event.DamageID]
	if !ok {
		return
	}
	event.DamageCalculateType = record.CalculateType
	event.HasCalculateType = true
	event.EventTypeStatus = ResolutionCorrelated
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

// WriteDamageEvent records one damage result in the core statistics, hands it to the development outputs and returns
// whether the event passes the normal player-relevance filter. The outputs cannot stop the counters that follow them.
func (p *Processor) WriteDamageEvent(event CombatEvent) bool {
	sourceRelation, targetRelation, visible := p.PlayerRelevantDamageEvent(event)
	if record, ok := normalizeCombatEvent(event, sourceRelation, targetRelation); ok && p.DamageStatistics != nil {
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
			if candidate := event.DamageContributorCandidate; candidate != nil && candidate.Kind == VehicleKindMotorcycle && candidate.ActorType == "Vehicle" && candidate.EntityType == "BattleVehicle" && candidate.Status == ResolutionCorrelated {
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
		assignment := p.DamageStatistics.addCombatEvent(record)
		if p.observer != nil {
			p.observer.WriteCombatEventRecord(record, assignment)
		}
		if p.observer != nil {
			p.observer.FlushEncounterTransitions()
		}
	}
	if p.observer != nil {
		p.observer.WriteDamageEventOutputs(event, visible, sourceRelation, targetRelation)
	}
	if !visible {
		p.Stats.SuppressedUnrelatedEvents++
		return false
	}
	if sourceRelation == PlayerRelationLocal || targetRelation == PlayerRelationLocal {
		p.Stats.PlayerRelatedEvents++
	} else {
		p.Stats.IncludedUnresolvedRelations++
	}
	p.Stats.Printed++
	p.Stats.MatchedCombatResultAbs += event.Damage
	return true
}
