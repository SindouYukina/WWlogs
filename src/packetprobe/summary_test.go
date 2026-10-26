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

func TestEncounterSummaryBackfillsEarlierSourceEventsAsCorrelated(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &output
	p.masters = &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Name: "Role", NameStatus: string(resolutionVerified)},
	}}
	p.observeEncounterSummary(combatEvent{
		SessionNumber: 1, HasSourceEntity: true, SourceEntityID: 28006,
		Source:  entityIdentity{Runtime: runtimeEntity{entityID: 28006, sceneInstanceID: 901, hasSceneInstanceID: true}},
		SkillID: 1312011, SkillName: "Skill A", SkillNameStatus: resolutionVerified, SkillProof: "SkillDescription -> Skill",
		CharacterStatus: resolutionUnknown, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 120,
	})
	if got := p.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}].unknownCredit; got != 1 {
		t.Fatalf("unknown credit before correlation = %d, want 1", got)
	}
	p.sourceRoles.observe(28006, masterRecord{ID: 1312011, RoleID: 1312, Status: string(resolutionVerified), NameKind: "SkillName"})
	p.sourceRoles.observe(28006, masterRecord{ID: 1312012, RoleID: 1312, Status: string(resolutionVerified), NameKind: "SkillName"})
	roleID, ok := p.sourceRoles.correlatedRole(28006)
	if !ok {
		t.Fatal("two distinct verified skills did not establish correlated role")
	}
	p.backfillEncounterSummary(28006, roleID)

	summary := p.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}]
	if summary.unknownCredit != 0 || summary.unknownCreditAbs != 0 || summary.identityBackfilledEvents != 1 {
		t.Fatalf("backfill counters = unknown %d/%d, backfilled %d", summary.unknownCredit, summary.unknownCreditAbs, summary.identityBackfilledEvents)
	}
	character := summary.characters[1312]
	if character == nil || character.status != resolutionCorrelated || character.damage != 120 || character.eventCount != 1 {
		t.Fatalf("backfilled character summary = %+v", character)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Events without credited CharacterId: 0 (result magnitude 0)",
		"Earlier events backfilled from later same-lifetime Skill evidence: 1 [CORRELATED]",
		"CharacterId 1312 — Role [CORRELATED; name VERIFIED]",
		"backfilled within observed session/scene/entity lifetime",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("summary missing %q:\n%s", want, output.String())
		}
	}
}

func TestEncounterSummaryBackfillDoesNotCrossObservedLifecycleReset(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &bytes.Buffer{}
	p.masters = &gameMaster{characters: map[uint64]masterRecord{1312: {ID: 1312}}}
	p.observeEncounterSummary(combatEvent{
		SessionNumber: 1, HasSourceEntity: true, SourceEntityID: 28006,
		Source:          entityIdentity{Runtime: runtimeEntity{entityID: 28006, sceneInstanceID: 901, hasSceneInstanceID: true}},
		CharacterStatus: resolutionUnknown, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 120,
	})
	p.resetPendingSummaryCredits()
	p.sourceRoles.observe(28006, masterRecord{ID: 1312011, RoleID: 1312, Status: string(resolutionVerified), NameKind: "SkillName"})
	p.sourceRoles.observe(28006, masterRecord{ID: 1312012, RoleID: 1312, Status: string(resolutionVerified), NameKind: "SkillName"})
	roleID, ok := p.sourceRoles.correlatedRole(28006)
	if !ok {
		t.Fatal("test setup did not establish correlated role")
	}
	p.backfillEncounterSummary(28006, roleID)

	summary := p.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}]
	if summary.unknownCredit != 1 || summary.unknownCreditAbs != 120 || summary.identityBackfilledEvents != 0 || len(summary.characters) != 0 {
		t.Fatalf("lifecycle reset allowed an earlier event backfill: %+v", summary)
	}
}

func TestEncounterSummaryBackfillRejectsConflictingSkillRoleEvidence(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &bytes.Buffer{}
	p.masters = &gameMaster{characters: map[uint64]masterRecord{1312: {ID: 1312}, 1414: {ID: 1414}}}
	p.observeEncounterSummary(combatEvent{
		SessionNumber: 1, HasSourceEntity: true, SourceEntityID: 28006,
		Source:          entityIdentity{Runtime: runtimeEntity{entityID: 28006, sceneInstanceID: 901, hasSceneInstanceID: true}},
		CharacterStatus: resolutionUnknown, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 120,
	})
	p.sourceRoles.observe(28006, masterRecord{ID: 1312011, RoleID: 1312, Status: string(resolutionVerified), NameKind: "SkillName"})
	p.sourceRoles.observe(28006, masterRecord{ID: 1312012, RoleID: 1312, Status: string(resolutionVerified), NameKind: "SkillName"})
	p.sourceRoles.observe(28006, masterRecord{ID: 1414001, RoleID: 1414, Status: string(resolutionVerified), NameKind: "SkillName"})
	p.backfillAvailableSummaryCredits()

	summary := p.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}]
	if summary.unknownCredit != 1 || summary.unknownCreditAbs != 120 || summary.identityBackfilledEvents != 0 || len(summary.characters) != 0 {
		t.Fatalf("conflicting SkillId evidence was backfilled: %+v", summary)
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
