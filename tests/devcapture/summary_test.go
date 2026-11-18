package devcapture

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestEncounterSummarySeparatesCreditedDamageHealingAndOwnerCandidates(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	p.Masters = &capture.GameMaster{Manifest: capture.MasterManifest{SourceHash: strings.Repeat("a", 64)}, Locale: "zh-TW", DatabasePath: "data/master_data.db"}
	start := time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC)
	p.ObserveEncounterSummary(capture.CombatEvent{
		Timestamp: start, SessionNumber: 1,
		Source:      capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 101, SceneInstanceID: 9001, HasSceneInstanceID: true}},
		Target:      capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 201, SceneInstanceID: 9001, HasSceneInstanceID: true}, HasMonster: true, Monster: capture.MasterRecord{ID: 71001, Name: "Boss | Candidate"}, MonsterStatus: capture.ResolutionCorrelated, MonsterProof: "numeric MonsterId mapping"},
		CharacterID: 1312, CharacterName: "Role", CharacterNameStatus: capture.ResolutionVerified, CharacterStatus: capture.ResolutionCorrelated, CharacterProof: "two verified SkillIds map to RoleId",
		SkillID: 1312011, SkillName: "Skill", SkillNameStatus: capture.ResolutionVerified, SkillProof: "SkillDescription -> Skill",
		EventTypeCandidate: "DAMAGE_CANDIDATE", EventTypeProof: "Damage master CalculateType 0", Damage: 300,
		SummonerCharacter: capture.SummonerCharacterIdentity{CharacterID: 1414, HasCharacterID: true, Character: capture.MasterRecord{Name: "Owner"}, HasCharacter: true, Status: capture.ResolutionCorrelated},
	})
	p.ObserveEncounterSummary(capture.CombatEvent{
		Timestamp: start.Add(time.Second), SessionNumber: 1,
		Source:             capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 301, SceneInstanceID: 9001, HasSceneInstanceID: true}},
		Target:             capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 101, SceneInstanceID: 9001, HasSceneInstanceID: true}},
		EventTypeCandidate: "HEALING_CANDIDATE", Damage: 50,
	})
	p.ObserveEncounterSummary(capture.CombatEvent{
		Timestamp: start.Add(2 * time.Second), SessionNumber: 1,
		Source:             capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 401, SceneInstanceID: 9002, HasSceneInstanceID: true}},
		Target:             capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 402, SceneInstanceID: 9003, HasSceneInstanceID: true}},
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 7,
	})

	if len(p.dev.encounterSummaries) != 2 {
		t.Fatalf("summary scope count = %d, want identified scene plus one conflict bucket", len(p.dev.encounterSummaries))
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Combat master: external test override: master_data.db (Global, zh-TW, SHA-256 " + strings.Repeat("a", 64) + ")",
		"no Echo or summon attribution is inferred here",
		"DAMAGE_CANDIDATE: Damage master CalculateType 0",
		"Damage candidates: 300",
		"Healing candidate result magnitude: 50",
		"Events without resolved source character identity: 1 (result magnitude 50; this does not imply non-player damage; player-team relation is tracked separately)",
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
	if strings.Contains(text, "Effective Healing") && !strings.Contains(text, "does not compute Effective Healing") {
		t.Fatal("summary implied effective healing without its evidence limitation")
	}
}

func TestEncounterSummaryUsesPlayerTeamRelationWithoutCharacterIdentity(t *testing.T) {
	var output bytes.Buffer
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.dev.encounterSummary = &output
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	addTestRuntimeEntity(p, 200, func(entity *capture.RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = capture.EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.SessionNumber = 1
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.Target.HasMonster, event.Target.MonsterStatus = true, capture.ResolutionVerified
	p.ObserveEncounterSummary(event)

	summary := p.dev.encounterSummaries[encounterSummaryKey{session: 1}]
	if summary == nil || summary.partyToEnemyCandidateEvents != 1 || summary.partyToEnemyCandidateAbs != 321 || summary.unknownDirectionDamageEvents != 0 {
		t.Fatalf("known player-team damage lost its direction when CharacterId was unresolved: %+v", summary)
	}
	if summary.unknownCredit != 1 || summary.unknownCreditAbs != 321 {
		t.Fatalf("missing character identity was not kept separate from player-team direction: %+v", summary)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Player team to identified enemy: 1 event(s), reported magnitude 321",
		"Events without resolved source character identity: 1 (result magnitude 321; this does not imply non-player damage; player-team relation is tracked separately)",
		"Missing CharacterId or character names do not break a confirmed player-team relation.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
}

func TestEncounterSummaryListsLocalVehicleAsIndependentPlayerTeamSource(t *testing.T) {
	var output bytes.Buffer
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.dev.encounterSummary = &output
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 200, func(entity *capture.RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = capture.EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 100, 200)
	event.SessionNumber = 1
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 55
	event.Target.HasMonster, event.Target.MonsterStatus = true, capture.ResolutionVerified
	event.CharacterID = 1409
	event.CharacterName = "Role"
	event.CharacterNameStatus = capture.ResolutionVerified
	event.CharacterStatus = capture.ResolutionVerified
	event.DamageContributorCandidate = &capture.DamageContributorCandidate{
		ActorType: "Vehicle", FollowerConfigID: 290101, Status: capture.ResolutionCorrelated,
	}
	p.ObserveEncounterSummary(event)

	summary := p.dev.encounterSummaries[encounterSummaryKey{session: 1}]
	if summary == nil || summary.damageCandidates != 55 || summary.partyToEnemyCandidateAbs != 55 || summary.partyToEnemyCandidateEvents != 1 {
		t.Fatalf("vehicle damage was lost from player-team totals or counted more than once: %+v", summary)
	}
	if len(summary.characters) != 0 || summary.unknownCredit != 0 || len(summary.vehicleContributors) != 1 {
		t.Fatalf("vehicle damage was attributed to a character or omitted as a source: %+v", summary)
	}
	if row := summary.vehicleContributors[290101]; row == nil || row.events != 1 || row.damage != 55 {
		t.Fatalf("vehicle contributor row = %+v", row)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Player-team vehicle contributors (independent damage sources)",
		"included in the player-team damage and direction totals above",
		"FollowerSummonConfigId:290101; CORRELATED]: 1 event(s), CombatResult magnitude 55",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
}

func TestEncounterSummaryUsesPlayerTeamTargetWithoutCharacterIdentity(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.dev.encounterSummary = &bytes.Buffer{}
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	addTestRuntimeEntity(p, 200, func(entity *capture.RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = capture.EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 200, 101)
	event.SessionNumber = 1
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 77
	event.Source.HasMonster, event.Source.MonsterStatus = true, capture.ResolutionVerified
	p.ObserveEncounterSummary(event)

	summary := p.dev.encounterSummaries[encounterSummaryKey{session: 1}]
	if summary == nil || summary.enemyToPartyCandidateEvents != 1 || summary.enemyToPartyCandidateAbs != 77 || summary.unknownDirectionDamageEvents != 0 {
		t.Fatalf("incoming damage to a local player-team role lost its direction when CharacterId was unresolved: %+v", summary)
	}
}

func TestEncounterSummaryGroupsHealingByCreditedSourceTargetAndDamageID(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	base := capture.CombatEvent{
		SessionNumber: 1,
		Source: capture.EntityIdentity{
			Runtime:      capture.RuntimeEntity{EntityID: 12001},
			HasCharacter: true, Character: capture.MasterRecord{ID: 1209, Name: "Healer", NameStatus: string(capture.ResolutionVerified)},
			CharacterStatus: capture.ResolutionVerified, CharacterProof: "source RoleId field",
		},
		Target: capture.EntityIdentity{
			Runtime:      capture.RuntimeEntity{EntityID: 14001},
			HasCharacter: true, Character: capture.MasterRecord{ID: 1409, Name: "Recipient", NameStatus: string(capture.ResolutionVerified)},
			CharacterStatus: capture.ResolutionVerified, CharacterProof: "target RoleId field",
		},
		CharacterID: 1209, CharacterName: "Healer", CharacterNameStatus: capture.ResolutionVerified,
		CharacterStatus: capture.ResolutionVerified, CharacterProof: "source RoleId field",
		SkillID: ^uint64(0), SkillStatus: capture.ResolutionUnknown,
		DamageID: 1100, HasDamageID: true,
		EventTypeCandidate: "HEALING_CANDIDATE", EventTypeStatus: capture.ResolutionCorrelated,
		EventTypeProof: "Damage CalculateType=1", Damage: 200,
	}
	p.ObserveEncounterSummary(base)
	base.Damage = 50
	base.SkillID = 0
	p.ObserveEncounterSummary(base)

	unresolvedTarget := base
	unresolvedTarget.Target = capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 14999}}
	unresolvedTarget.Damage = 25
	p.ObserveEncounterSummary(unresolvedTarget)

	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"### Healing source-to-target attribution candidates",
		"Source CharacterId 1209 — Healer [VERIFIED; name VERIFIED] → Target CharacterId 1409 — Recipient [VERIFIED; name VERIFIED]; SkillId UNKNOWN; DamageId 1100: 2 event(s), reported magnitude 250; classification CORRELATED",
		"source evidence: source RoleId field",
		"target evidence: target RoleId field",
		"Classification evidence: Damage CalculateType=1",
		"Healing events without both source and target Character identities: 1 (reported magnitude 25)",
		"This table does not compute Effective Healing or Overheal; the encounter statistics take the applied HP change from ChangeLife.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("healing summary missing %q:\n%s", want, text)
		}
	}
}

func TestEncounterSummaryHandlesHealingAfterBuffSnapshotCreatesGroup(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	p.observeBuffSnapshot(buffSnapshotAuditRow{SessionNumber: 1, Buff: capture.RawBuffSnapshot{ID: 70001, HasID: true}})
	p.ObserveEncounterSummary(capture.CombatEvent{
		SessionNumber: 1,
		Target: capture.EntityIdentity{
			HasCharacter: true, Character: capture.MasterRecord{ID: 1409, Name: "Recipient", NameStatus: string(capture.ResolutionVerified)},
			CharacterStatus: capture.ResolutionVerified, CharacterProof: "target RoleId field",
		},
		CharacterID: 1209, CharacterStatus: capture.ResolutionVerified, CharacterProof: "source RoleId field",
		SkillID: ^uint64(0), EventTypeCandidate: "HEALING_CANDIDATE", EventTypeStatus: capture.ResolutionCorrelated,
		DamageID: 1100, HasDamageID: true, Damage: 25,
	})
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "reported magnitude 25") {
		t.Fatalf("Healing after Buff snapshot was not summarized:\n%s", output.String())
	}
}

func TestHealingAttributionRowsAreBounded(t *testing.T) {
	summary := &encounterSummary{healingAttributions: make(map[healingAttributionKey]*healingAttributionSummary)}
	for targetID := uint64(1); targetID <= maxSummaryHealingAttributions+1; targetID++ {
		addHealingAttribution(summary, capture.CombatEvent{
			CharacterID: 1209, CharacterStatus: capture.ResolutionVerified,
			Target: capture.EntityIdentity{
				HasCharacter: true, Character: capture.MasterRecord{ID: targetID}, CharacterStatus: capture.ResolutionVerified,
			},
			EventTypeCandidate: "HEALING_CANDIDATE", Damage: 1,
		})
	}
	if got := len(summary.healingAttributions); got != maxSummaryHealingAttributions {
		t.Fatalf("healing attribution rows = %d, want bounded limit %d", got, maxSummaryHealingAttributions)
	}
	if summary.healingAttributionsOmitted != 1 {
		t.Fatalf("omitted healing rows = %d, want 1", summary.healingAttributionsOmitted)
	}
}

func TestEncounterSummaryWithoutEventsReportsNoMatchedEvents(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	key := combatActionRequestKey{sessionNumber: 1, messageID: 9}
	p.dev.combatActionRequests[key] = combatActionRequest{SessionNumber: 1, MessageID: 9, SkillID: 1001}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No matched combat events were observed.") {
		t.Fatalf("empty summary = %q", output.String())
	}
	if !strings.Contains(output.String(), "NO_CORRELATED_DAMAGE_RESULT") || !strings.Contains(output.String(), "not sufficient to label a miss") {
		t.Fatalf("skill request without a damage result was not preserved: %q", output.String())
	}
}

func TestEncounterSummaryListsUncreditedRawIDGroups(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	event := capture.CombatEvent{
		SessionNumber: 2, SourceEntityID: 501, HasSourceEntity: true, TargetEntityID: 602, HasTargetEntity: true,
		SkillID: 13111001, DamageID: 13111002, HasDamageID: true, BulletID: 13111003, HasBulletID: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 40,
	}
	p.ObserveEncounterSummary(event)
	event.Damage = 60
	p.ObserveEncounterSummary(event)

	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Events without resolved source character identity: 2 (result magnitude 100; this does not imply non-player damage; player-team relation is tracked separately)",
		"Unresolved source character identity breakdown (raw IDs)",
		"Session 2; source Scene UNKNOWN; target Scene UNKNOWN; DAMAGE_CANDIDATE; SkillId 13111001; DamageId 13111002; BulletId 13111003; Source EntityId 501; Target EntityId 602: 2 event(s), magnitude 100; CharacterId UNKNOWN",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "CharacterId 1311 —") {
		t.Fatal("uncredited raw-ID grouping inferred a CharacterId")
	}
}

func TestEncounterSummaryBoundsUncreditedRawIDGroups(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	for skillID := uint64(1); skillID <= maxSummaryUncreditedRows+1; skillID++ {
		p.ObserveEncounterSummary(capture.CombatEvent{
			SessionNumber: 1, SkillID: skillID, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 1,
		})
	}
	if got := len(p.dev.encounterSummaries[encounterSummaryKey{session: 1}].uncreditedEvents); got != maxSummaryUncreditedRows {
		t.Fatalf("uncredited group count = %d, want cap %d", got, maxSummaryUncreditedRows)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Additional uncredited events omitted by the safety limit: 1") {
		t.Fatalf("summary did not report the omitted group count: %s", output.String())
	}
}

func TestEncounterSummaryOverflowKeepsUncreditedSessionsSeparate(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	for session := 1; session <= maxEncounterSummaryGroups+2; session++ {
		p.ObserveEncounterSummary(capture.CombatEvent{
			SessionNumber: session, SourceEntityID: 501, HasSourceEntity: true, TargetEntityID: 602, HasTargetEntity: true,
			SkillID: 700, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 1,
		})
	}
	if got := len(p.dev.encounterSummaries[encounterSummaryKey{overflow: true}].uncreditedEvents); got != 2 {
		t.Fatalf("overflow uncredited groups = %d, want one per session", got)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	overflowAt := strings.Index(text, "Additional session/scene groups")
	if overflowAt < 0 {
		t.Fatalf("overflow summary was not emitted: %s", text)
	}
	overflowText := text[overflowAt:]
	unknownRows := strings.Count(overflowText, "CharacterId UNKNOWN")
	hasSession513, hasSession514 := strings.Contains(text, "Session 513;"), strings.Contains(text, "Session 514;")
	if unknownRows != 2 || !hasSession513 || !hasSession514 {
		t.Fatalf("overflow summary merged raw IDs across sessions: rows=%d session513=%t session514=%t", unknownRows, hasSession513, hasSession514)
	}
}

func TestEncounterSummaryReportsBuffSnapshotObservationsWithoutLifecycleClaims(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	p.Entities.Entities[8801] = capture.RuntimeEntity{EntityID: 8801, SceneInstanceID: 9001, HasSceneInstanceID: true, SourceMessageID: capture.CmdEntityActiveResponse}
	entityID := uint64(8801)
	p.observeBuffSnapshot(buffSnapshotAuditRow{
		SessionNumber: 4,
		Buff:          capture.RawBuffSnapshot{ID: 70001, HasID: true},
		EntityID:      &entityID,
	})
	p.observeBuffSnapshot(buffSnapshotAuditRow{
		SessionNumber: 4,
		Buff:          capture.RawBuffSnapshot{ID: 70001, HasID: true},
		EntityID:      &entityID,
	})

	if len(p.dev.encounterSummaries) != 1 {
		t.Fatalf("summary scopes = %d, want 1", len(p.dev.encounterSummaries))
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Matched result events: 0",
		"Buff snapshot observations",
		"BuffId 70001 / EntityId 8801: 2 snapshot observation(s)",
		"do not establish apply, refresh, removal, uptime, caster, or ownership semantics",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"BuffId 70001 was applied", "caster EntityId 8801"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("summary made an unsupported lifecycle claim %q", forbidden)
		}
	}
}

func TestEncounterSummaryBackfillsEarlierSourceEventsAsCorrelated(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &output
	p.Masters = &capture.GameMaster{Characters: map[uint64]capture.MasterRecord{
		1312: {ID: 1312, Name: "Role", NameStatus: string(capture.ResolutionVerified)},
	}}
	p.ObserveEncounterSummary(capture.CombatEvent{
		SessionNumber: 1, HasSourceEntity: true, SourceEntityID: 28006,
		Source:  capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 28006, SceneInstanceID: 901, HasSceneInstanceID: true}},
		SkillID: 1312011, SkillName: "Skill A", SkillNameStatus: capture.ResolutionVerified, SkillProof: "SkillDescription -> Skill",
		CharacterStatus: capture.ResolutionUnknown, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 120,
	})
	if got := p.dev.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}].unknownCredit; got != 1 {
		t.Fatalf("unknown credit before correlation = %d, want 1", got)
	}
	p.SourceRoles.Observe(28006, capture.MasterRecord{ID: 1312011, RoleID: 1312, Status: string(capture.ResolutionVerified), NameKind: "SkillName"})
	p.SourceRoles.Observe(28006, capture.MasterRecord{ID: 1312012, RoleID: 1312, Status: string(capture.ResolutionVerified), NameKind: "SkillName"})
	roleID, ok := p.SourceRoles.CorrelatedRole(28006)
	if !ok {
		t.Fatal("two distinct verified skills did not establish correlated role")
	}
	p.BackfillEncounterSummary(28006, roleID)

	summary := p.dev.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}]
	if summary.unknownCredit != 0 || summary.unknownCreditAbs != 0 || summary.identityBackfilledEvents != 1 {
		t.Fatalf("backfill counters = unknown %d/%d, backfilled %d", summary.unknownCredit, summary.unknownCreditAbs, summary.identityBackfilledEvents)
	}
	character := summary.characters[1312]
	if character == nil || character.status != capture.ResolutionCorrelated || character.damage != 120 || character.eventCount != 1 {
		t.Fatalf("backfilled character summary = %+v", character)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Events without resolved source character identity: 0 (result magnitude 0; this does not imply non-player damage; player-team relation is tracked separately)",
		"Earlier events backfilled from later same-lifetime Skill evidence: 1 [CORRELATED]",
		"CharacterId 1312 — Role [CORRELATED; name VERIFIED]",
		"backfilled within observed session/scene/entity lifetime",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("summary missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "CharacterId UNKNOWN") {
		t.Fatal("backfilled event remained in the uncredited breakdown")
	}
}

func TestEncounterSummaryBackfillDoesNotCrossObservedLifecycleReset(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &bytes.Buffer{}
	p.Masters = &capture.GameMaster{Characters: map[uint64]capture.MasterRecord{1312: {ID: 1312}}}
	p.ObserveEncounterSummary(capture.CombatEvent{
		SessionNumber: 1, HasSourceEntity: true, SourceEntityID: 28006,
		Source:          capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 28006, SceneInstanceID: 901, HasSceneInstanceID: true}},
		CharacterStatus: capture.ResolutionUnknown, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 120,
	})
	p.ResetPendingSummaryCredits()
	p.SourceRoles.Observe(28006, capture.MasterRecord{ID: 1312011, RoleID: 1312, Status: string(capture.ResolutionVerified), NameKind: "SkillName"})
	p.SourceRoles.Observe(28006, capture.MasterRecord{ID: 1312012, RoleID: 1312, Status: string(capture.ResolutionVerified), NameKind: "SkillName"})
	roleID, ok := p.SourceRoles.CorrelatedRole(28006)
	if !ok {
		t.Fatal("test setup did not establish correlated role")
	}
	p.BackfillEncounterSummary(28006, roleID)

	summary := p.dev.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}]
	if summary.unknownCredit != 1 || summary.unknownCreditAbs != 120 || summary.identityBackfilledEvents != 0 || len(summary.characters) != 0 {
		t.Fatalf("lifecycle reset allowed an earlier event backfill: %+v", summary)
	}
}

func TestEncounterSummaryBackfillRejectsConflictingSkillRoleEvidence(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &bytes.Buffer{}
	p.Masters = &capture.GameMaster{Characters: map[uint64]capture.MasterRecord{1312: {ID: 1312}, 1414: {ID: 1414}}}
	p.ObserveEncounterSummary(capture.CombatEvent{
		SessionNumber: 1, HasSourceEntity: true, SourceEntityID: 28006,
		Source:          capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 28006, SceneInstanceID: 901, HasSceneInstanceID: true}},
		CharacterStatus: capture.ResolutionUnknown, EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 120,
	})
	p.SourceRoles.Observe(28006, capture.MasterRecord{ID: 1312011, RoleID: 1312, Status: string(capture.ResolutionVerified), NameKind: "SkillName"})
	p.SourceRoles.Observe(28006, capture.MasterRecord{ID: 1312012, RoleID: 1312, Status: string(capture.ResolutionVerified), NameKind: "SkillName"})
	p.SourceRoles.Observe(28006, capture.MasterRecord{ID: 1414001, RoleID: 1414, Status: string(capture.ResolutionVerified), NameKind: "SkillName"})
	p.BackfillAvailableSummaryCredits()

	summary := p.dev.encounterSummaries[encounterSummaryKey{session: 1, sceneInstance: 901, hasScene: true}]
	if summary.unknownCredit != 1 || summary.unknownCreditAbs != 120 || summary.identityBackfilledEvents != 0 || len(summary.characters) != 0 {
		t.Fatalf("conflicting SkillId evidence was backfilled: %+v", summary)
	}
}

func TestEncounterSummaryCapsDistinctScopesAndKeepsOverflowTotals(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.dev.encounterSummary = &bytes.Buffer{}
	for sceneID := uint64(1); sceneID <= maxEncounterSummaryGroups+2; sceneID++ {
		p.ObserveEncounterSummary(capture.CombatEvent{
			SessionNumber:      1,
			Source:             capture.EntityIdentity{Runtime: capture.RuntimeEntity{SceneInstanceID: sceneID, HasSceneInstanceID: true}},
			EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 1,
		})
	}
	if got := len(p.dev.encounterSummaries); got != maxEncounterSummaryGroups+1 {
		t.Fatalf("summary group count = %d, want at most the configured limit plus one overflow bucket", got)
	}
	overflow := p.dev.encounterSummaries[encounterSummaryKey{overflow: true}]
	if overflow == nil || overflow.eventCount != 2 || overflow.damageCandidates != 2 {
		t.Fatalf("overflow summary lost events: %+v", overflow)
	}
}

// Uncredited rows that tie on magnitude, class, session, skill, damage and entities (several BulletIds of one hit
// pattern) are written in one order, the order of their remaining key fields, not in map order.
func TestUncreditedEventRowsHaveOneOrder(t *testing.T) {
	summary := &encounterSummary{uncreditedEvents: map[uncreditedEventKey]*uncreditedEventSummary{}}
	for _, bulletID := range []int64{8003207108, 8003207105, 8003207106, 8003207107} {
		key := uncreditedEventKey{sessionNumber: 1, eventType: "DAMAGE_CANDIDATE", skillID: 80032071, damageID: 8003207105,
			hasDamageID: true, bulletID: bulletID, hasBulletID: true, sourceEntityID: 71392, hasSourceEntity: true,
			targetEntityID: 71354, hasTargetEntity: true}
		summary.uncreditedEvents[key] = &uncreditedEventSummary{key: key, events: 1, magnitude: 847}
	}
	var first string
	for run := 0; run < 50; run++ {
		var out strings.Builder
		if err := writeUncreditedEvents(&out, summary); err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			first = out.String()
			continue
		}
		if out.String() != first {
			t.Fatalf("run %d wrote another order:\n%s\nfirst:\n%s", run, out.String(), first)
		}
	}
	last := -1
	for _, bulletID := range []string{"8003207105", "8003207106", "8003207107", "8003207108"} {
		at := strings.Index(first, "BulletId "+bulletID+";")
		if at <= last {
			t.Fatalf("BulletId %s is not after the previous row:\n%s", bulletID, first)
		}
		last = at
	}
}
