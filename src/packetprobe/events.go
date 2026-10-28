package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type combatEventJSONL struct {
	SchemaVersion int                      `json:"schemaVersion"`
	EventType     string                   `json:"eventType"`
	Timestamp     time.Time                `json:"timestamp"`
	SessionNumber int                      `json:"sessionNumber"`
	RPCID         uint64                   `json:"rpcId"`
	Source        combatEventEntityJSONL   `json:"source"`
	Target        combatEventEntityJSONL   `json:"target"`
	Credit        combatEventCreditJSONL   `json:"credit"`
	Summoner      combatEventSummonerJSONL `json:"summoner"`
	Skill         combatEventSkillJSONL    `json:"skill"`
	CombatResult  combatEventResultJSONL   `json:"combatResult"`
	Healing       *combatEventHealingJSONL `json:"healing,omitempty"`
	Actors        actorsJSONL              `json:"actors"`
	Master        combatEventMasterJSONL   `json:"master"`
}

type combatEventEntityJSONL struct {
	EntityID        *uint64                   `json:"entityId,omitempty"`
	RuntimeStatus   resolutionStatus          `json:"runtimeStatus"`
	RuntimeEvidence string                    `json:"runtimeEvidence,omitempty"`
	RuntimeFields   json.RawMessage           `json:"runtimeFields"`
	Character       *combatEventMasterIDJSONL `json:"character,omitempty"`
	Monster         *combatEventMasterIDJSONL `json:"monster,omitempty"`
	SceneTemplate   *combatEventTemplateJSONL `json:"sceneTemplate,omitempty"`
}

type combatEventMasterIDJSONL struct {
	ID       uint64           `json:"id"`
	Name     string           `json:"name,omitempty"`
	Status   resolutionStatus `json:"status"`
	Evidence string           `json:"evidence,omitempty"`
}

type combatEventTemplateJSONL struct {
	IdentityType string                              `json:"identityType"`
	Status       resolutionStatus                    `json:"status"`
	NameStatus   resolutionStatus                    `json:"nameStatus"`
	Candidates   []combatEventTemplateCandidateJSONL `json:"candidates"`
	Evidence     string                              `json:"evidence,omitempty"`
}

type combatEventTemplateCandidateJSONL struct {
	TemplateID    uint64 `json:"templateId"`
	BlueprintType string `json:"blueprintType"`
	RawName       string `json:"rawName,omitempty"`
}

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
	ID         uint64           `json:"id"`
	MessageID  uint64           `json:"messageId"`
	Name       string           `json:"name,omitempty"`
	NameKind   string           `json:"nameKind,omitempty"`
	Status     resolutionStatus `json:"status"`
	NameStatus resolutionStatus `json:"nameStatus"`
	Evidence   string           `json:"evidence,omitempty"`
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
	SourceCommit    string `json:"sourceCommit,omitempty"`
	SourceHash      string `json:"sourceHash,omitempty"`
}

type combatEvent struct {
	Timestamp           time.Time
	SessionNumber       int
	RPCID               uint64
	DamageID            uint64
	SourceEntityID      uint64
	HasSourceEntity     bool
	TargetEntityID      uint64
	HasTargetEntity     bool
	Source              entityIdentity
	Target              entityIdentity
	SummonerCharacter   summonerCharacterIdentity
	CharacterID         uint64
	CharacterName       string
	CharacterNameStatus resolutionStatus
	CharacterStatus     resolutionStatus
	CharacterProof      string
	SkillID             uint64
	SkillMessageID      uint64
	SkillName           string
	SkillNameStatus     resolutionStatus
	SkillNameKind       string
	SkillStatus         resolutionStatus
	SkillProof          string
	HasDamageID         bool
	DamageSourceType    uint64
	HasDamageSourceType bool
	BulletID            int64
	HasBulletID         bool
	Damage              int64
	ServerResultRaw     int32
	HasServerResult     bool
	ChangeLifeRaw       int32
	HasChangeLife       bool
	DamageCalculateType int32
	HasCalculateType    bool
	EventTypeCandidate  string
	EventTypeStatus     resolutionStatus
	EventTypeProof      string
}

func (p *processor) buildDamageEvent(rpcID uint64, request pendingDamageRequest, resultRaw, changeLifeRaw int32, hasChangeLife bool, damage int64) combatEvent {
	event := combatEvent{
		Timestamp:           p.currentFrameTime,
		SessionNumber:       p.sessionNumber,
		RPCID:               rpcID,
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
	if p.masters != nil {
		if skill, ok := p.masters.skills[request.skillID]; ok {
			event.SkillStatus = resolutionStatus(skill.Status)
			event.SkillNameStatus = resolutionStatus(skill.NameStatus)
			if event.SkillNameStatus == resolutionVerified || event.SkillNameStatus == resolutionCorrelated {
				event.SkillName = skill.Name
				event.SkillNameKind = skill.NameKind
			}
			event.SkillProof = skill.SourceTable
		}
	}
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
		event.EventTypeProof = source + "; client Global 3.7.1 DamageUiController CalculateType 0 uses ordinary damage text"
	case 1:
		event.EventTypeCandidate = "HEALING_CANDIDATE"
		event.EventTypeProof = source + "; client Global 3.7.1 DamageUiController CalculateType 1 uses cure/heal display"
	default:
		event.EventTypeCandidate = "OTHER_COMBAT_RESULT_CANDIDATE"
		event.EventTypeProof = source + "; nonzero type other than 1 has no verified event semantics"
	}
}

func (p *processor) writeDamageEvent(event combatEvent) {
	p.recordDamageForOverlap(event)
	p.writeStructuredEventJSONL(event.jsonlView(p.masters))
	if _, err := fmt.Fprintln(p.stdout, event.formatNormal()); err != nil {
		fmt.Fprintf(p.diagnostic, "stdout_write_error=%v\n", err)
		return
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
		if err := flusher.Flush(); err != nil {
			fmt.Fprintf(p.diagnostic, "stdout_flush_error=%v\n", err)
			return
		}
	}
	p.stats.printed++
	p.stats.matchedCombatResultAbs += event.Damage
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
