package capture

import (
	"io"
	"reflect"
	"testing"
)

func TestAttributionIDsDoNotDependOnLocalizedMasterNames(t *testing.T) {
	type attributionIDs struct {
		SourceEntityID uint64
		TargetEntityID uint64
		CharacterID    uint64
		CharacterState ResolutionStatus
		SkillID        uint64
		SkillState     ResolutionStatus
		DamageID       uint64
		Damage         int64
		EventType      string
		EventTypeState ResolutionStatus
	}

	labels := []struct {
		locale        string
		characterName string
		skillName     string
	}{
		{locale: "zh-TW", characterName: "角色甲", skillName: "技能甲"},
		{locale: "zh-CN", characterName: "角色乙", skillName: "技能乙"},
		{locale: "en-US", characterName: "Character C", skillName: "Skill C"},
		{locale: "ja-JP", characterName: "キャラクターD", skillName: "スキルD"},
		{locale: "arbitrary", characterName: "UNRELATED_TEXT_4", skillName: "UNRELATED_TEXT_4"},
	}

	var baseline attributionIDs
	for index, label := range labels {
		master := &GameMaster{
			Manifest: MasterManifest{Language: label.locale},
			Characters: map[uint64]MasterRecord{
				1312: {
					ID: 1312, Name: label.characterName, NameStatus: string(ResolutionVerified),
					Status: string(ResolutionVerified),
				},
			},
			Skills: map[uint64]MasterRecord{
				1312011: {
					ID: 1312011, Name: label.skillName, NameStatus: string(ResolutionVerified),
					Status: string(ResolutionVerified), RoleID: 1312, NameKind: "SkillName",
				},
				1312012: {
					ID: 1312012, Name: label.skillName + " second", NameStatus: string(ResolutionVerified),
					Status: string(ResolutionVerified), RoleID: 1312, NameKind: "SkillName",
				},
			},
			Damage: map[uint64]DamageMasterRecord{
				131201101: {
					ID: 131201101, CalculateType: 0, SourceTable: "Damage",
				},
			},
		}
		processor := NewProcessor(nil, io.Discard)
		processor.Masters = master
		processor.SourceRoles.Observe(28006, master.Skills[1312011])
		processor.SourceRoles.Observe(28006, master.Skills[1312012])

		request := PendingDamageRequest{
			AttackerEntityID: 28006, HasAttackerEntityID: true,
			TargetEntityID: 28530, HasTargetEntityID: true,
			SkillID: 1312011, DamageID: 131201101, HasDamageID: true,
		}
		event := processor.BuildDamageEvent(7, request, -404, 0, false, 404)
		got := attributionIDs{
			SourceEntityID: event.SourceEntityID,
			TargetEntityID: event.TargetEntityID,
			CharacterID:    event.CharacterID,
			CharacterState: event.CharacterStatus,
			SkillID:        event.SkillID,
			SkillState:     event.SkillStatus,
			DamageID:       event.DamageID,
			Damage:         event.Damage,
			EventType:      event.EventTypeCandidate,
			EventTypeState: event.EventTypeStatus,
		}
		if event.CharacterName != label.characterName || event.SkillName != label.skillName {
			t.Fatalf("locale %s did not affect display labels as expected: character=%q skill=%q", label.locale, event.CharacterName, event.SkillName)
		}
		if index == 0 {
			baseline = got
			continue
		}
		if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("locale %s changed numeric attribution: got %+v, baseline %+v", label.locale, got, baseline)
		}
	}
}
