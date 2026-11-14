package capture

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type combatEventJSONL struct {
	SchemaVersion              int                         `json:"schemaVersion"`
	EventType                  string                      `json:"eventType"`
	Timestamp                  time.Time                   `json:"timestamp"`
	SessionNumber              int                         `json:"sessionNumber"`
	RPCID                      uint64                      `json:"rpcId"`
	RequestEpoch               uint64                      `json:"requestEpoch,omitempty"` // wraps of the client's RequestId counter before the request
	Source                     combatEventEntityJSONL      `json:"source"`
	Target                     combatEventEntityJSONL      `json:"target"`
	Credit                     combatEventCreditJSONL      `json:"credit"`
	Summoner                   combatEventSummonerJSONL    `json:"summoner"`
	Skill                      combatEventSkillJSONL       `json:"skill"`
	CombatResult               combatEventResultJSONL      `json:"combatResult"`
	Healing                    *combatEventHealingJSONL    `json:"healing,omitempty"`
	Actors                     actorsJSONL                 `json:"actors"`
	Master                     combatEventMasterJSONL      `json:"master"`
	Raw                        combatEventRawJSONL         `json:"raw"`
	Bullet                     *combatEventBulletJSONL     `json:"bullet,omitempty"`
	DamageContributorCandidate *damageContributorCandidate `json:"damageContributorCandidate,omitempty"`
	// DamageOwners lists the static skill and Buff owners of the DamageId; it is a master reference, not the runtime attacker.
	DamageOwners *damageOwnersJSONL `json:"damageOwners,omitempty"`
	// Response is set on DAMAGE_RESPONSE_WITHOUT_AMOUNT events, whose server response carried no usable Damage field.
	Response *combatEventNoAmountJSONL `json:"response,omitempty"`
	// Push is set on DAMAGE_PUSH_RESULT events: server-pushed damage that no client request asked for.
	Push *combatEventPushJSONL `json:"push,omitempty"`
}

// combatEventNoAmountJSONL records why a server-confirmed hit has no amount. HasAmount=false means proto3 omitted Damage
// (the default 0) and the amount is therefore zero or not reported; the event never contributes to damage totals.
type combatEventNoAmountJSONL struct {
	HasAmount       bool   `json:"hasAmount"`
	ErrorCode       int32  `json:"errorCode"`
	ImmuneType      int32  `json:"immuneType"`
	ChangeLifeRaw   *int32 `json:"changeLifeRaw,omitempty"`
	Outcome         string `json:"outcome"`
	OutcomeEvidence string `json:"outcomeEvidence"`
}

// combatEventBulletJSONL is the static namespace translation of the event's BulletId.
// It is kept apart from Raw, which only copies wire values.
type combatEventBulletJSONL struct {
	BulletID       uint64 `json:"bulletId"`
	Namespace      string `json:"namespace,omitempty"`
	ActorBlueprint string `json:"actorBlueprint,omitempty"`
	Via            string `json:"via,omitempty"`
	DevLabelZhCn   string `json:"devLabelZhCn,omitempty"`
	// DevLabel is the localized developer note of the bullet row; never an official name.
	DevLabel    *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
	RowDamageID uint64                     `json:"rowDamageId,omitempty"`
	Status      resolutionStatus           `json:"status"`
	Evidence    string                     `json:"evidence,omitempty"`
}

// combatEventRawJSONL holds only values copied from the decoded damage request
// and response. Master lookups and runtime resolution never write here, so a
// consumer can compare this block with the wire without reproducing resolution.
type combatEventRawJSONL struct {
	DamageID         *uint64 `json:"damageId,omitempty"`
	AttackerEntityID *uint64 `json:"attackerEntityId,omitempty"`
	TargetEntityID   *uint64 `json:"targetEntityId,omitempty"`
	SkillID          *uint64 `json:"skillId,omitempty"`
	SkillMessageID   *uint64 `json:"skillMessageId,omitempty"`
	DamageSourceType *uint64 `json:"damageSourceType,omitempty"`
	BulletID         *int64  `json:"bulletId,omitempty"`
	ServerResultRaw  *int32  `json:"serverResultRaw,omitempty"`
	ChangeLifeRaw    *int32  `json:"changeLifeRaw,omitempty"`
}

type combatEventEntityJSONL struct {
	EntityID        *uint64                      `json:"entityId,omitempty"`
	RuntimeStatus   resolutionStatus             `json:"runtimeStatus"`
	RuntimeEvidence string                       `json:"runtimeEvidence,omitempty"`
	RuntimeFields   json.RawMessage              `json:"runtimeFields"`
	Lifecycle       entityLifecycleJSONL         `json:"lifecycle"`
	Character       *combatEventMasterIDJSONL    `json:"character,omitempty"`
	Monster         *combatEventMasterIDJSONL    `json:"monster,omitempty"`
	SceneTemplate   *combatEventTemplateJSONL    `json:"sceneTemplate,omitempty"`
	RoleSkin        *combatEventRoleSkinJSONL    `json:"roleSkin,omitempty"`
	SummonSkill     *combatEventSummonSkillJSONL `json:"summonSkill,omitempty"`
}

// combatEventSummonSkillJSONL is the SummonSkillId namespace translation of one entity.
type combatEventSummonSkillJSONL struct {
	SummonSkillID uint64           `json:"summonSkillId"`
	SummonType    *uint64          `json:"summonType,omitempty"`
	Namespace     string           `json:"namespace,omitempty"`
	Status        resolutionStatus `json:"status"`
	Evidence      string           `json:"evidence,omitempty"`
	// DevLabel is the developer note of the resolved Vision row.
	DevLabel *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
}

// combatEventRoleSkinJSONL is the RoleSkinId -> RoleSkin.RoleId translation of one entity.
type combatEventRoleSkinJSONL struct {
	SkinID       uint64           `json:"skinId"`
	MasterRoleID uint64           `json:"masterRoleId,omitempty"`
	Status       resolutionStatus `json:"status"`
	Evidence     string           `json:"evidence,omitempty"`
}

// entityLifecycleJSONL is the normalized lifecycle of one entity. State UNKNOWN
// with status UNKNOWN means no add, remove or scene snapshot named the entity.
type entityLifecycleJSONL struct {
	State             entityLifecycleState `json:"state"`
	Status            resolutionStatus     `json:"status"`
	AddCount          uint64               `json:"addCount"`
	RemoveCount       uint64               `json:"removeCount"`
	ReusedAfterRemove uint64               `json:"reusedAfterRemove"`
	LastSource        string               `json:"lastSource,omitempty"`
	Evidence          string               `json:"evidence,omitempty"`
}

type combatEventMasterIDJSONL struct {
	ID       uint64           `json:"id"`
	Name     string           `json:"name,omitempty"`
	Status   resolutionStatus `json:"status"`
	Evidence string           `json:"evidence,omitempty"`
}

type combatEventTemplateJSONL struct {
	Name         string           `json:"name,omitempty"`
	Subtitle     string           `json:"subtitle,omitempty"`
	IdentityType string           `json:"identityType"`
	Status       resolutionStatus `json:"status"`
	NameStatus   resolutionStatus `json:"nameStatus"`
	// DevLabel is set instead of Name when the single candidate has only a developer note; never an official name.
	DevLabel   *combatEventLabelTextJSONL          `json:"devLabel,omitempty"`
	Candidates []combatEventTemplateCandidateJSONL `json:"candidates"`
	Evidence   string                              `json:"evidence,omitempty"`
}

type combatEventTemplateCandidateJSONL sceneTemplateCandidate

type combatEventCreditJSONL struct {
	CharacterID *uint64          `json:"characterId,omitempty"`
	Name        string           `json:"name,omitempty"`
	Status      resolutionStatus `json:"status"`
	NameStatus  resolutionStatus `json:"nameStatus"`
	Evidence    string           `json:"evidence,omitempty"`
}

type combatEventSummonerJSONL struct {
	EntityIDs   []uint64         `json:"entityIds,omitempty"`
	CharacterID *uint64          `json:"characterId,omitempty"`
	Name        string           `json:"name,omitempty"`
	Status      resolutionStatus `json:"status"`
	Evidence    string           `json:"evidence,omitempty"`
}

type combatEventSkillJSONL struct {
	ID         uint64                    `json:"id"`
	MessageID  uint64                    `json:"messageId"`
	Name       string                    `json:"name,omitempty"`
	NameKind   string                    `json:"nameKind,omitempty"`
	Status     resolutionStatus          `json:"status"`
	NameStatus resolutionStatus          `json:"nameStatus"`
	Evidence   string                    `json:"evidence,omitempty"`
	Request    *combatActionRequestJSONL `json:"request,omitempty"`
	// DevLabelZhCn is the SkillInfo developer label; it is not an official localized name and never fills Name.
	DevLabelZhCn   string           `json:"devLabelZhCn,omitempty"`
	DevLabelStatus resolutionStatus `json:"devLabelStatus,omitempty"`
	DevLabelProof  string           `json:"devLabelEvidence,omitempty"`
	// DevLabel is the developer label as display text in the master locale (zh-CN original as fallback).
	DevLabel *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
}

type combatEventLabelTextJSONL struct {
	Locale   string `json:"locale"`
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence,omitempty"`
}

type combatEventElementJSONL struct {
	ID       int32            `json:"id"`
	Name     string           `json:"name,omitempty"`
	Status   resolutionStatus `json:"status"`
	Evidence string           `json:"evidence,omitempty"`
}

type combatActionRequestJSONL struct {
	SessionNumber      int              `json:"sessionNumber"`
	MessageID          uint64           `json:"messageId"`
	EntityID           uint64           `json:"entityId,omitempty"`
	SkillID            uint64           `json:"skillId"`
	TargetID           uint64           `json:"targetId,omitempty"`
	RequestType        string           `json:"requestType"`
	Timestamp          string           `json:"timestamp"`
	LinkedDamageEvents int              `json:"linkedDamageEvents"`
	LinkStatus         resolutionStatus `json:"linkStatus"`
	Evidence           string           `json:"evidence"`
}

type combatEventResultJSONL struct {
	DamageID        *uint64          `json:"damageId,omitempty"`
	SourceType      *uint64          `json:"sourceType,omitempty"`
	BulletID        *int64           `json:"bulletId,omitempty"`
	CombatResultAbs int64            `json:"combatResultAbs"`
	ServerResultRaw *int32           `json:"serverResultRaw,omitempty"`
	ChangeLifeRaw   *int32           `json:"changeLifeRaw,omitempty"`
	CalculateType   *int32           `json:"calculateType,omitempty"`
	ClassCandidate  string           `json:"classCandidate,omitempty"`
	ClassStatus     resolutionStatus `json:"classStatus"`
	ClassEvidence   string           `json:"classEvidence,omitempty"`
	// Element names damage.element of the DamageId row; absent when the row is unknown or has no element.
	Element *combatEventElementJSONL `json:"element,omitempty"`
}

type combatEventHealingJSONL struct {
	ReportedMagnitudeAbs int64            `json:"reportedMagnitudeAbs"`
	MagnitudeStatus      resolutionStatus `json:"magnitudeStatus"`
	MagnitudeEvidence    string           `json:"magnitudeEvidence,omitempty"`
	EffectiveAmountAbs   *int64           `json:"effectiveAmountAbs"`
	EffectiveStatus      resolutionStatus `json:"effectiveStatus"`
	HPDeltaStatus        resolutionStatus `json:"hpDeltaStatus"`
	OverhealAmountAbs    *int64           `json:"overhealAmountAbs"`
	OverhealStatus       resolutionStatus `json:"overhealStatus"`
}

type combatEventMasterJSONL struct {
	GameVersion     string `json:"gameVersion,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	ClientRegion    string `json:"clientRegion,omitempty"`
	Locale          string `json:"locale,omitempty"`
	SourceHash      string `json:"sourceHash,omitempty"`
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
	SkillRequest               *combatActionRequest
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
	if request.skillMessageID != 0 {
		key := combatActionRequestKey{sessionNumber: p.sessionNumber, messageID: request.skillMessageID}
		if action, ok := p.combatActionRequests[key]; ok {
			copy := action
			copy.LinkedDamageEvents++
			p.combatActionRequests[key] = copy
			event.SkillRequest = &copy
		}
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

// writeDamageEvent writes all diagnostic forms and returns whether the event
// passes the normal player-relevance filter.
func (p *processor) writeDamageEvent(event combatEvent) bool {
	sourceRelation, targetRelation, visible := p.playerRelevantDamageEvent(event)
	if record, ok := normalizeCombatEvent(event, sourceRelation, targetRelation); ok && p.damageStatistics != nil {
		record = p.gateAttribution(record)
		record.SourceActor, record.TargetActor = combatActorTypeOf(event.Source), combatActorTypeOf(event.Target)
		switch record.Flow {
		case CombatFlowLocalOutgoing:
			record.SourceRole = p.localRoleForEntity(event.SourceEntityID, event.Source)
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
	if visible {
		p.recordDamageForOverlap(event)
	}
	p.writeDamageEventJSONL(event)
	if visible {
		normalOutput := event.formatNormalForSourceRelation(sourceRelation)
		switch {
		case sourceRelation == playerRelationLocal && targetRelation == playerRelationLocal:
			normalOutput += " [PlayerTeamSide: BOTH]"
		case sourceRelation == playerRelationLocal:
			normalOutput += " [PlayerTeamSide: SOURCE]"
		case targetRelation == playerRelationLocal:
			normalOutput += " [PlayerTeamSide: TARGET]"
		default:
			normalOutput += " [PlayerTeamSide: UNCONFIRMED]"
		}
		if _, err := fmt.Fprintln(p.stdout, normalOutput); err != nil {
			fmt.Fprintf(p.diagnostic, "stdout_write_error=%v\n", err)
			return true
		}
	}
	if p.audit != nil {
		summonerID := "?"
		if event.Source.Runtime.hasSummonerID {
			summonerID = fmt.Sprintf("%d", event.Source.Runtime.summonerID)
		}
		playerEntityID := "?"
		if event.Source.Runtime.hasPlayerEntityID {
			playerEntityID = fmt.Sprintf("%d", event.Source.Runtime.playerEntityID)
		}
		monsterID := "?"
		if event.Target.HasMonster {
			monsterID = fmt.Sprintf("%d", event.Target.Monster.ID)
		}
		characterID := "?"
		if event.CharacterID != 0 {
			characterID = fmt.Sprintf("%d", event.CharacterID)
		}
		sourceEntityID := "?"
		if event.HasSourceEntity {
			sourceEntityID = fmt.Sprintf("%d", event.SourceEntityID)
		}
		targetEntityID := "?"
		if event.HasTargetEntity {
			targetEntityID = fmt.Sprintf("%d", event.TargetEntityID)
		}
		fields := []string{
			formatTimestamp(event.Timestamp), fmt.Sprintf("%d", event.RPCID), sourceEntityID, targetEntityID,
			string(event.Source.RuntimeStatus), event.Source.RuntimeProof,
			string(event.Target.RuntimeStatus), event.Target.RuntimeProof, characterID,
			event.CharacterName, string(event.CharacterStatus), event.CharacterProof,
			fmt.Sprintf("%d", event.SkillID), event.SkillName, string(event.SkillStatus), event.SkillProof,
			fmt.Sprintf("%d", event.SkillMessageID), formatOptionalUint(event.DamageID, event.HasDamageID), monsterID,
			event.Target.Monster.Name, string(event.Target.MonsterStatus), event.Target.MonsterProof, summonerID,
			formatOptionalUint(event.SummonerCharacter.CharacterID, event.SummonerCharacter.HasCharacterID),
			event.SummonerCharacter.Character.Name, string(event.SummonerCharacter.Status), event.SummonerCharacter.Proof,
			fmt.Sprint(event.SummonerCharacter.EntityIDs), playerEntityID,
			fmt.Sprintf("%d", event.Damage),
			formatOptionalUint(event.DamageSourceType, event.HasDamageSourceType), formatOptionalInt(event.BulletID, event.HasBulletID),
			marshalRuntimeIdentity(event.Source.Runtime), marshalRuntimeIdentity(event.Target.Runtime),
			formatOptionalInt(int64(event.ServerResultRaw), event.HasServerResult),
			formatOptionalInt(int64(event.ChangeLifeRaw), event.HasChangeLife),
			formatOptionalInt(int64(event.DamageCalculateType), event.HasCalculateType), event.EventTypeCandidate,
			string(event.EventTypeStatus), event.EventTypeProof,
			string(event.CharacterNameStatus), string(event.SkillNameStatus),
		}
		build, versionFields, versionCommands := p.clientVersion.auditFields()
		fields = append(fields, build, versionFields, versionCommands)
		for i := range fields {
			fields[i] = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(fields[i])
		}
		if _, err := fmt.Fprintln(p.audit, strings.Join(fields, "\t")); err != nil {
			fmt.Fprintf(p.diagnostic, "event_audit_write_error=%v\n", err)
		}
	}
	if p.research {
		fmt.Fprintf(p.diagnostic, "[DAMAGE] rpc=%d source_entity=%d target_entity=%d skill_id=%d damage_id=%d source_type=%s bullet_id=%s damage=%d\n", event.RPCID, event.SourceEntityID, event.TargetEntityID, event.SkillID, event.DamageID, optionalDamageSource(event.DamageSourceType, event.HasDamageSourceType), formatOptionalInt(event.BulletID, event.HasBulletID), event.Damage)
		summonerID := "?"
		if event.Source.Runtime.hasSummonerID {
			summonerID = fmt.Sprintf("%d", event.Source.Runtime.summonerID)
		}
		playerEntityID := "?"
		if event.Source.Runtime.hasPlayerEntityID {
			playerEntityID = fmt.Sprintf("%d", event.Source.Runtime.playerEntityID)
		}
		fmt.Fprintf(p.diagnostic, "[ENTITY_RESOLVE] source_status=%s source_evidence=%q source_type=%d source_summoner_id=%s source_summoner_chain=%v source_summoner_character_id=%d source_summoner_character_name=%q source_summoner_character_status=%s source_summoner_evidence=%q source_player_entity_id=%s character_id=%d character_name=%q character_status=%s character_name_status=%s character_evidence=%q target_status=%s target_evidence=%q monster_id=%d monster_name=%q monster_status=%s monster_name_status=%s monster_evidence=%q\n",
			event.Source.RuntimeStatus, event.Source.RuntimeProof, event.Source.Runtime.entityTypeCode, summonerID,
			event.SummonerCharacter.EntityIDs, event.SummonerCharacter.CharacterID, event.SummonerCharacter.Character.Name, event.SummonerCharacter.Status, event.SummonerCharacter.Proof, playerEntityID,
			event.CharacterID, event.CharacterName, event.CharacterStatus, event.CharacterNameStatus, event.CharacterProof,
			event.Target.RuntimeStatus, event.Target.RuntimeProof, event.Target.Monster.ID, event.Target.Monster.Name, event.Target.MonsterStatus, event.Target.Monster.NameStatus, event.Target.MonsterProof)
		fmt.Fprintf(p.diagnostic, "[SKILL_RESOLVE] skill_id=%d name=%q kind=%q status=%s name_status=%s master_locale=%q source=%q\n",
			event.SkillID, event.SkillName, event.SkillNameKind, event.SkillStatus, event.SkillNameStatus,
			masterLocale(p.masters), event.SkillProof)
	}
	if flusher, ok := p.stdout.(interface{ Flush() error }); ok {
		if visible {
			if err := flusher.Flush(); err != nil {
				fmt.Fprintf(p.diagnostic, "stdout_flush_error=%v\n", err)
				return true
			}
		}
	}
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

type optionalRuntimeID struct {
	Value   uint64 `json:"value"`
	Present bool   `json:"present"`
}

type runtimePlayerFollowerAudit struct {
	Type     optionalRuntimeID `json:"type"`
	EntityID optionalRuntimeID `json:"entityId"`
}

type runtimeIdentityAudit struct {
	EntityID                 optionalRuntimeID            `json:"entityId"`
	SceneInstanceID          optionalRuntimeID            `json:"sceneInstanceId"`
	EntityType               optionalRuntimeID            `json:"entityType"`
	ConfigID                 optionalRuntimeID            `json:"configId"`
	ConfigType               optionalRuntimeID            `json:"configType"`
	PlayerID                 optionalRuntimeID            `json:"playerId"`
	OwnerID                  optionalRuntimeID            `json:"ownerId"`
	OwnerIncID               optionalRuntimeID            `json:"ownerIncId"`
	SummonerID               optionalRuntimeID            `json:"summonerId"`
	SummonerPlayerID         optionalRuntimeID            `json:"summonerPlayerId"`
	SummonConfigID           optionalRuntimeID            `json:"summonConfigId"`
	SummonSkillID            optionalRuntimeID            `json:"summonSkillId"`
	SummonType               optionalRuntimeID            `json:"summonType"`
	SceneSummonerID          optionalRuntimeID            `json:"sceneSummonerId"`
	SceneSummonConfigID      optionalRuntimeID            `json:"sceneSummonConfigId"`
	SceneSummonSkillID       optionalRuntimeID            `json:"sceneSummonSkillId"`
	PlayerEntityID           optionalRuntimeID            `json:"playerEntityId"`
	FollowerSummonConfigID   optionalRuntimeID            `json:"followerSummonConfigId"`
	PhantomRoleEntityID      optionalRuntimeID            `json:"phantomRoleEntityId"`
	ConcomitantVisionIDs     []uint64                     `json:"concomitantVisionEntityIds,omitempty"`
	ConcomitantCustomIDs     []uint64                     `json:"concomitantCustomEntityIds,omitempty"`
	WeakVisionEntityID       optionalRuntimeID            `json:"weakVisionEntityId"`
	FollowEntityID           optionalRuntimeID            `json:"followEntityId"`
	PlayerFollowers          []runtimePlayerFollowerAudit `json:"playerFollowers,omitempty"`
	MonsterID                optionalRuntimeID            `json:"monsterId"`
	MonsterCaptureEntityID   optionalRuntimeID            `json:"monsterCaptureEntityId"`
	TemplateID               optionalRuntimeID            `json:"templateId"`
	RoleSkinID               optionalRuntimeID            `json:"roleSkinId"`
	RoleID                   optionalRuntimeID            `json:"roleId"`
	RoleConflict             bool                         `json:"roleConflict"`
	RoleSourceFieldPath      string                       `json:"roleSourceFieldPath,omitempty"`
	SourceMessageID          uint16                       `json:"sourceMessageId"`
	SourceFieldPath          string                       `json:"sourceFieldPath,omitempty"`
	ComponentSourceMessageID uint16                       `json:"componentSourceMessageId,omitempty"`
	ComponentSourceFieldPath string                       `json:"componentSourceFieldPath,omitempty"`
}

func formatOptionalUint(value uint64, present bool) string {
	if !present {
		return "?"
	}
	return fmt.Sprintf("%d", value)
}

func formatOptionalInt(value int64, present bool) string {
	if !present {
		return "?"
	}
	return fmt.Sprintf("%d", value)
}

func formatTimestamp(timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	return timestamp.UTC().Format(time.RFC3339Nano)
}

func masterLocale(masters *gameMaster) string {
	if masters == nil {
		return ""
	}
	return masters.locale
}

func sceneTemplateEventLabel(identity entityIdentity, entityID string) (string, bool) {
	if len(identity.TemplateCandidates) != 1 || identity.TemplateStatus == resolutionUnknown {
		return "", false
	}
	candidate := identity.TemplateCandidates[0]
	if candidate.Name == "" || candidate.NameStatus != string(resolutionVerified) {
		if candidate.DevLabel != nil && candidate.DevLabel.Text != "" {
			return fmt.Sprintf("[internal] %s [Template:%d; %s] [Entity:%s]", candidate.DevLabel.Text, candidate.TemplateID, identity.TemplateStatus, entityID), true
		}
		return "", false
	}
	return fmt.Sprintf("%s [Template:%d; %s] [Entity:%s]", candidate.Name, candidate.TemplateID, identity.TemplateStatus, entityID), true
}
