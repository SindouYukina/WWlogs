//go:build wwdev

package main

import (
	"encoding/json"
	"fmt"
)

func (event combatEvent) formatNormal() string {
	timestamp := "--:--:--.---"
	if !event.Timestamp.IsZero() {
		timestamp = event.Timestamp.Local().Format("15:04:05.000")
	}
	sourceID := "?"
	if event.HasSourceEntity {
		sourceID = fmt.Sprintf("%d", event.SourceEntityID)
	}
	source := fmt.Sprintf("Unknown [Entity:%s]", sourceID)
	if event.Source.HasMonster && (event.Source.MonsterStatus == resolutionVerified || event.Source.MonsterStatus == resolutionCorrelated) {
		if event.Source.Monster.Name != "" && (event.Source.Monster.NameStatus == string(resolutionVerified) || event.Source.Monster.NameStatus == string(resolutionCorrelated)) {
			source = fmt.Sprintf("%s [Monster:%d] [Entity:%s]", event.Source.Monster.Name, event.Source.Monster.ID, sourceID)
		} else {
			source = fmt.Sprintf("Monster:%d [name %s] [Entity:%s]", event.Source.Monster.ID, event.Source.Monster.NameStatus, sourceID)
		}
	}
	credit := "Credit:?"
	if event.CharacterID != 0 && (event.CharacterStatus == resolutionVerified || event.CharacterStatus == resolutionCorrelated) {
		if event.CharacterName != "" {
			credit = fmt.Sprintf("Credit:%s [%s]", event.CharacterName, event.CharacterStatus)
			if event.CharacterNameStatus != "" && event.CharacterNameStatus != event.CharacterStatus {
				credit = fmt.Sprintf("Credit:%s [%s; name %s]", event.CharacterName, event.CharacterStatus, event.CharacterNameStatus)
			}
		} else {
			credit = fmt.Sprintf("Credit:Character:%d [%s; name %s]", event.CharacterID, event.CharacterStatus, event.CharacterNameStatus)
		}
	}
	skillID := "?"
	if event.SkillID != 0 {
		skillID = fmt.Sprintf("%d", event.SkillID)
	}
	skill := fmt.Sprintf("SkillId:%s", skillID)
	if event.SkillName != "" && (event.SkillNameStatus == resolutionVerified || event.SkillNameStatus == resolutionCorrelated) {
		skill = fmt.Sprintf("%s [SkillId:%s, %s]", event.SkillName, skillID, event.SkillStatus)
		if event.SkillNameStatus != "" && event.SkillNameStatus != event.SkillStatus {
			skill = fmt.Sprintf("%s [SkillId:%s, %s; name %s]", event.SkillName, skillID, event.SkillStatus, event.SkillNameStatus)
		}
		if event.SkillNameKind == "SkillButtonLabel" {
			skill = fmt.Sprintf("%s [UI label, SkillId:%s, %s]", event.SkillName, skillID, event.SkillStatus)
			if event.SkillNameStatus != "" && event.SkillNameStatus != event.SkillStatus {
				skill = fmt.Sprintf("%s [UI label, SkillId:%s, %s; name %s]", event.SkillName, skillID, event.SkillStatus, event.SkillNameStatus)
			}
		}
	}
	damageSource := "Unknown"
	if event.HasDamageSourceType {
		switch event.DamageSourceType {
		case 0:
			damageSource = "Bullet"
		case 1:
			damageSource = "Effect"
		default:
			damageSource = fmt.Sprintf("Unknown(%d)", event.DamageSourceType)
		}
	}
	provenance := fmt.Sprintf("DamageId:%s, Source:%s", formatOptionalUint(event.DamageID, event.HasDamageID), damageSource)
	if event.HasBulletID {
		provenance += fmt.Sprintf(", BulletId:%d", event.BulletID)
	}
	targetID := "?"
	if event.HasTargetEntity {
		targetID = fmt.Sprintf("%d", event.TargetEntityID)
	}
	target := fmt.Sprintf("Unknown [Entity:%s]", targetID)
	if event.Target.HasMonster && (event.Target.MonsterStatus == resolutionVerified || event.Target.MonsterStatus == resolutionCorrelated) {
		if event.Target.Monster.Name != "" && (event.Target.Monster.NameStatus == string(resolutionVerified) || event.Target.Monster.NameStatus == string(resolutionCorrelated)) {
			target = fmt.Sprintf("%s [Monster:%d] [Entity:%s]", event.Target.Monster.Name, event.Target.Monster.ID, targetID)
		} else {
			target = fmt.Sprintf("Monster:%d [name %s] [Entity:%s]", event.Target.Monster.ID, event.Target.Monster.NameStatus, targetID)
		}
	} else if event.Target.HasCharacter && (event.Target.CharacterStatus == resolutionVerified || event.Target.CharacterStatus == resolutionCorrelated) {
		if event.Target.Character.Name != "" && (event.Target.Character.NameStatus == string(resolutionVerified) || event.Target.Character.NameStatus == string(resolutionCorrelated)) {
			target = fmt.Sprintf("%s [Char:%d] [Entity:%s]", event.Target.Character.Name, event.Target.Character.ID, targetID)
		} else {
			target = fmt.Sprintf("Character:%d [name %s] [Entity:%s]", event.Target.Character.ID, event.Target.Character.NameStatus, targetID)
		}
	}
	line := fmt.Sprintf("%s  %s  %s  [%s]  %s  %s  %d", timestamp, source, skill, provenance, credit, target, event.Damage)
	if event.SummonerCharacter.HasCharacterID {
		label := fmt.Sprintf("Character:%d", event.SummonerCharacter.CharacterID)
		if event.SummonerCharacter.HasCharacter && event.SummonerCharacter.Character.Name != "" && (event.SummonerCharacter.NameStatus == resolutionVerified || event.SummonerCharacter.NameStatus == resolutionCorrelated) {
			label = event.SummonerCharacter.Character.Name
		}
		line += fmt.Sprintf("  Summoner:%s [%s]", label, event.SummonerCharacter.Status)
	} else if len(event.SummonerCharacter.EntityIDs) > 0 {
		line += fmt.Sprintf("  SummonerEntityChain:%v [UNKNOWN]", event.SummonerCharacter.EntityIDs)
	}
	return line
}

func marshalRuntimeIdentity(entity runtimeEntity) string {
	view := runtimeIdentityAudit{
		EntityID:                 optionalRuntimeID{entity.entityID, entity.entityID != 0},
		SceneInstanceID:          optionalRuntimeID{entity.sceneInstanceID, entity.hasSceneInstanceID},
		EntityType:               optionalRuntimeID{entity.entityTypeCode, entity.hasEntityType},
		ConfigID:                 optionalRuntimeID{entity.configID, entity.hasConfigID},
		ConfigType:               optionalRuntimeID{entity.configType, entity.hasConfigType},
		PlayerID:                 optionalRuntimeID{entity.playerID, entity.hasPlayerID},
		OwnerID:                  optionalRuntimeID{entity.ownerID, entity.hasOwnerID},
		OwnerIncID:               optionalRuntimeID{entity.ownerIncID, entity.hasOwnerIncID},
		SummonerID:               optionalRuntimeID{entity.summonerID, entity.hasSummonerID},
		SummonerPlayerID:         optionalRuntimeID{entity.summonerPlayerID, entity.hasSummonerPlayerID},
		SummonConfigID:           optionalRuntimeID{entity.summonConfigID, entity.hasSummonConfigID},
		SummonSkillID:            optionalRuntimeID{entity.summonSkillID, entity.hasSummonSkillID},
		SummonType:               optionalRuntimeID{entity.summonType, entity.hasSummonType},
		SceneSummonerID:          optionalRuntimeID{entity.sceneSummonerID, entity.hasSceneSummonerID},
		SceneSummonConfigID:      optionalRuntimeID{entity.sceneSummonConfigID, entity.hasSceneSummonConfigID},
		SceneSummonSkillID:       optionalRuntimeID{entity.sceneSummonSkillID, entity.hasSceneSummonSkillID},
		PlayerEntityID:           optionalRuntimeID{entity.playerEntityID, entity.hasPlayerEntityID},
		FollowerSummonConfigID:   optionalRuntimeID{entity.followerSummonConfigID, entity.hasFollowerSummonConfigID},
		MonsterID:                optionalRuntimeID{entity.monsterID, entity.hasMonsterID},
		MonsterCaptureEntityID:   optionalRuntimeID{entity.monsterCaptureEntityID, entity.hasMonsterCaptureEntityID},
		TemplateID:               optionalRuntimeID{entity.templateID, entity.hasTemplateID},
		RoleSkinID:               optionalRuntimeID{entity.roleSkinID, entity.hasRoleSkinID},
		RoleID:                   optionalRuntimeID{entity.roleID, entity.hasRoleID},
		RoleConflict:             entity.roleConflict,
		RoleSourceFieldPath:      entity.roleSourceFieldPath,
		SourceMessageID:          entity.sourceMessageID,
		SourceFieldPath:          entity.sourceFieldPath,
		ComponentSourceMessageID: entity.componentSourceMessageID,
		ComponentSourceFieldPath: entity.componentSourceFieldPath,
	}
	data, err := json.Marshal(view)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func optionalDamageSource(value uint64, present bool) string {
	if !present {
		return "?"
	}
	switch value {
	case 0:
		return "Bullet(0)"
	case 1:
		return "Effect(1)"
	default:
		return fmt.Sprintf("Unknown(%d)", value)
	}
}
