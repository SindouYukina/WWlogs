//go:build wwdev

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestEncounterSummarySeparatesCreditedDamageHealingAndOwnerCandidates(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &output
	p.masters = &gameMaster{manifest: masterManifest{GameVersion: "3.7.1", Language: "zh-Hant"}}
	start := time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC)
	p.observeEncounterSummary(combatEvent{
		Timestamp: start, SessionNumber: 1,
		Source:      entityIdentity{Runtime: runtimeEntity{entityID: 101, sceneInstanceID: 9001, hasSceneInstanceID: true}},
		Target:      entityIdentity{Runtime: runtimeEntity{entityID: 201, sceneInstanceID: 9001, hasSceneInstanceID: true}, HasMonster: true, Monster: masterRecord{ID: 71001, Name: "Boss | Candidate"}, MonsterStatus: resolutionCorrelated, MonsterProof: "numeric MonsterId mapping"},
		CharacterID: 1312, CharacterName: "Role", CharacterNameStatus: resolutionVerified, CharacterStatus: resolutionCorrelated, CharacterProof: "two verified SkillIds map to RoleId",
		SkillID: 1312011, SkillName: "Skill", SkillNameStatus: resolutionVerified, SkillProof: "SkillDescription -> Skill",
		EventTypeCandidate: "DAMAGE_CANDIDATE", EventTypeProof: "Damage master CalculateType 0", Damage: 300,
		SummonerCharacter: summonerCharacterIdentity{CharacterID: 1414, HasCharacterID: true, Character: masterRecord{Name: "Owner"}, HasCharacter: true, Status: resolutionCorrelated},
	})
	p.observeEncounterSummary(combatEvent{
		Timestamp: start.Add(time.Second), SessionNumber: 1,
		Source:             entityIdentity{Runtime: runtimeEntity{entityID: 301, sceneInstanceID: 9001, hasSceneInstanceID: true}},
		Target:             entityIdentity{Runtime: runtimeEntity{entityID: 101, sceneInstanceID: 9001, hasSceneInstanceID: true}},
		EventTypeCandidate: "HEALING_CANDIDATE", Damage: 50,
	})
	p.observeEncounterSummary(combatEvent{
		Timestamp: start.Add(2 * time.Second), SessionNumber: 1,
		Source:             entityIdentity{Runtime: runtimeEntity{entityID: 401, sceneInstanceID: 9002, hasSceneInstanceID: true}},
		Target:             entityIdentity{Runtime: runtimeEntity{entityID: 402, sceneInstanceID: 9003, hasSceneInstanceID: true}},
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 7,
	})

	if len(p.encounterSummaries) != 2 {
		t.Fatalf("summary scope count = %d, want identified scene plus one conflict bucket", len(p.encounterSummaries))
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Master version: 3.7.1 (Global, zh-Hant)",
		"no Echo or summon attribution is inferred here",
		"DAMAGE_CANDIDATE: Damage master CalculateType 0",
		"Damage candidates: 300",
		"Healing candidate result magnitude: 50",
		"Events without credited CharacterId: 1 (result magnitude 50)",
		"CharacterId 1312 — Role [CORRELATED; name VERIFIED]",
		"SkillDescription -> Skill",
		"SkillId 1312011 — Skill",
		"MonsterId 71001 / EntityId 201 — Boss \\| Candidate [CORRELATED]",
		"CharacterId 1414 — Owner [CORRELATED]: associated with 1 events",
		"retained separately from Damage Credit",
		"Scene scope CONFLICT (capture-session aggregate)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Effective Healing") && !strings.Contains(text, "not established") {
		t.Fatal("summary implied effective healing without its evidence limitation")
	}
}

func TestEncounterSummaryWithoutEventsReportsNoMatchedEvents(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &output
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No matched combat events were observed.") {
		t.Fatalf("empty summary = %q", output.String())
	}
}

func TestEncounterSummaryCapsDistinctScopesAndKeepsOverflowTotals(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &bytes.Buffer{}
	for sceneID := uint64(1); sceneID <= maxEncounterSummaryGroups+2; sceneID++ {
		p.observeEncounterSummary(combatEvent{
			SessionNumber:      1,
			Source:             entityIdentity{Runtime: runtimeEntity{sceneInstanceID: sceneID, hasSceneInstanceID: true}},
			EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 1,
		})
	}
	if got := len(p.encounterSummaries); got != maxEncounterSummaryGroups+1 {
		t.Fatalf("summary group count = %d, want at most the configured limit plus one overflow bucket", got)
	}
	overflow := p.encounterSummaries[encounterSummaryKey{overflow: true}]
	if overflow == nil || overflow.eventCount != 2 || overflow.damageCandidates != 2 {
		t.Fatalf("overflow summary lost events: %+v", overflow)
	}
}
