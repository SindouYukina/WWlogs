//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func newPlayerRelevanceTestProcessor() (*processor, *bytes.Buffer, *bytes.Buffer) {
	var stdout, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &bytes.Buffer{}, nil)
	p.entities = newEntityResolver()
	p.combatEventsJSONL = &jsonl
	return p, &stdout, &jsonl
}

func observeTestLocalRole(p *processor, entityID uint64) {
	p.observeRoleReport(gameFrame{}, &decodedMessage{Fields: []decodedField{
		{Name: "CurrentRoleId", Value: int64(1409)},
		{Name: "CurrentEntityId", Value: int64(entityID)},
	}})
}

func TestPlayerRelevanceDoesNotFilterPlayerBuffSnapshots(t *testing.T) {
	p, _, jsonl := newPlayerRelevanceTestProcessor()
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	p.encounterSummary = &bytes.Buffer{}
	entityID, buffID := uint64(100), uint64(3000001001)
	row := buffSnapshotAuditRow{
		SchemaVersion: buffSnapshotSchemaVersion,
		EventType:     "BUFF_SNAPSHOT",
		SessionNumber: 1,
		EntityID:      &entityID,
		Buff:          rawBuffSnapshot{ID: buffID, HasID: true},
	}
	p.observeBuffSnapshot(row)
	p.writeStructuredEventJSONL(row)

	if !strings.Contains(jsonl.String(), `"eventType":"BUFF_SNAPSHOT"`) {
		t.Fatalf("player Buff snapshot was filtered: %q", jsonl.String())
	}
	if len(p.encounterSummaries) != 1 {
		t.Fatalf("player Buff snapshot missing from encounter summary: %+v", p.encounterSummaries)
	}
	for _, summary := range p.encounterSummaries {
		if summary.buffObservations[buffObservationKey{buffID: buffID, hasBuffID: true, targetID: entityID, hasTargetID: true}] != 1 {
			t.Fatalf("player Buff observation missing: %+v", summary.buffObservations)
		}
	}
}

func TestPlayerRelevanceFilterUsesLocalRuntimeIdentity(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*processor)
		sourceID   uint64
		targetID   uint64
		wantOutput bool
		wantOwner  bool
	}{
		{
			name: "local player character attacks an enemy",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, nil)
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "a boss damages the local player character",
			setup: func(p *processor) {
				observeTestLocalRole(p, 200)
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, nil)
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "monster damages another monster",
			setup: func(p *processor) {
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9002, true })
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "monster damages unrelated npc",
			setup: func(p *processor) {
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.templateID, entity.hasTemplateID = 7001, true })
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "unrelated npc damages monster",
			setup: func(p *processor) {
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.templateID, entity.hasTemplateID = 7001, true })
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "local player summon damages an enemy and keeps owner chain",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.roleID, entity.hasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) {
					entity.summonerID, entity.hasSummonerID = 100, true
					entity.summonType, entity.hasSummonType = summonTypeConcomitantVision, true
				})
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true, wantOwner: true,
		},
		{
			name: "local echo skill damages an enemy and keeps owner chain",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.roleID, entity.hasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) {
					entity.summonerID, entity.hasSummonerID = 100, true
					entity.summonType, entity.hasSummonType = summonTypeConcomitantVision, true
					entity.summonSkillID, entity.hasSummonSkillID = 280026010, true
				})
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true, wantOwner: true,
		},
		{
			name: "nearby monster fight is not included",
			setup: func(p *processor) {
				observeTestLocalRole(p, 300)
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9002, true })
				addTestRuntimeEntity(p, 300, nil)
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "unknown source remains visible when the target is local",
			setup: func(p *processor) {
				observeTestLocalRole(p, 200)
				addTestRuntimeEntity(p, 200, nil)
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "unknown target remains visible when the source is local",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, nil)
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "role switch adds the newly reported local entity",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				observeTestLocalRole(p, 101)
				addTestRuntimeEntity(p, 100, nil)
				addTestRuntimeEntity(p, 101, nil)
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true,
		},
		{
			name: "entity removal invalidates the summon owner link",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.roleID, entity.hasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) { entity.summonerID, entity.hasSummonerID = 100, true })
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
				p.playerRelevance.removeEntity(100)
				delete(p.entities.entities, 100)
			},
			sourceID: 101, targetID: 200, wantOutput: true,
		},
		{
			name: "another player remains visible until unrelatedness is established",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.roleID, entity.hasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) { entity.roleID, entity.hasRoleID = 1409, true })
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true,
		},
		{
			name:     "unresolved event remains visible",
			setup:    func(*processor) {},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, stdout, jsonl := newPlayerRelevanceTestProcessor()
			test.setup(p)
			event := testDamageEvent(p, test.sourceID, test.targetID)
			if test.wantOwner {
				event.SummonerCharacter = resolveSummonerCharacter(p.entities, p.masters, event.Source.Runtime)
			}
			if got := p.writeDamageEvent(event); got != test.wantOutput {
				t.Fatalf("player relevance = %v, want %v; stdout=%q", got, test.wantOutput, stdout.String())
			}
			if test.wantOutput != (stdout.Len() > 0) {
				t.Fatalf("normal output presence = %v, want %v: %q", stdout.Len() > 0, test.wantOutput, stdout.String())
			}
			if test.name == "unresolved event remains visible" && !strings.Contains(stdout.String(), "[PlayerTeamSide: UNCONFIRMED]") {
				t.Fatalf("unconfirmed player-team attribution was not identified precisely: %q", stdout.String())
			}
			if jsonl.Len() == 0 {
				t.Fatal("event diagnostics were dropped by the normal-output filter")
			}
			if test.wantOwner && !strings.Contains(stdout.String(), "Summoner:") {
				t.Fatalf("confirmed summoner chain was lost from output: %q", stdout.String())
			}
			if !test.wantOutput && p.stats.suppressedUnrelatedEvents != 1 {
				t.Fatalf("confirmed unrelated event count = %d, want 1", p.stats.suppressedUnrelatedEvents)
			}
		})
	}
}

func TestDamageEventConsoleLabelsLocalPlayerSide(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*processor)
		sourceID  uint64
		targetID  uint64
		wantLabel string
	}{
		{
			name: "local source",
			setup: func(p *processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, nil)
				addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
			},
			sourceID: 100, targetID: 200, wantLabel: "SOURCE",
		},
		{
			name: "local target",
			setup: func(p *processor) {
				observeTestLocalRole(p, 200)
				addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, nil)
			},
			sourceID: 100, targetID: 200, wantLabel: "TARGET",
		},
		{
			name:      "unresolved endpoints",
			setup:     func(*processor) {},
			sourceID:  100,
			targetID:  200,
			wantLabel: "UNCONFIRMED",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, stdout, _ := newPlayerRelevanceTestProcessor()
			test.setup(p)
			if !p.writeDamageEvent(testDamageEvent(p, test.sourceID, test.targetID)) {
				t.Fatal("player-relevant event was filtered")
			}
			want := "[PlayerTeamSide: " + test.wantLabel + "]"
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("console output %q does not contain %q", stdout.String(), want)
			}
		})
	}
}

func TestPlayerRelevanceKeepsLocallyOwnedMotorcycleDamage(t *testing.T) {
	p, stdout, jsonl := newPlayerRelevanceTestProcessor()
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.roleID, entity.hasRoleID = 1409, true })
	addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) {
		entity.summonerID, entity.hasSummonerID = 100, true
		entity.summonType, entity.hasSummonType = summonTypeConcomitantMotorcycle, true
	})
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })

	event := testDamageEvent(p, 101, 200)
	event.DamageContributorCandidate = &damageContributorCandidate{ActorType: "Vehicle", Status: resolutionCorrelated}
	if !p.writeDamageEvent(event) {
		t.Fatal("locally owned motorcycle damage was filtered")
	}
	if stdout.Len() == 0 || !strings.Contains(jsonl.String(), `"actorType":"Vehicle"`) {
		t.Fatalf("motorcycle contribution missing from output: stdout=%q jsonl=%q", stdout.String(), jsonl.String())
	}
}

func TestPlayerRelevanceUsesLocalPlayerIDForVehicleEntity(t *testing.T) {
	p, stdout, jsonl := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) {
		entity.playerID, entity.hasPlayerID = 77, true
		entity.entityTypeCode, entity.hasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.CharacterID = 1409
	event.CharacterName = "Role"
	event.CharacterNameStatus = resolutionVerified
	event.CharacterStatus = resolutionVerified
	event.DamageContributorCandidate = &damageContributorCandidate{ActorType: "Vehicle", Status: resolutionCorrelated}
	source, target, visible := p.playerRelevantDamageEvent(event)
	if source != playerRelationLocal || target != playerRelationUnrelated || !visible {
		t.Fatalf("local vehicle relations = (%d, %d, %t), want (local, unrelated, visible)", source, target, visible)
	}
	if !p.writeDamageEvent(event) {
		t.Fatal("local vehicle damage was filtered")
	}
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 321 || snapshot.OutgoingEvents != 1 {
		t.Fatalf("local vehicle damage was not included in confirmed output: %+v", snapshot)
	}
	if !strings.Contains(stdout.String(), "Credit:[Vehicle]") {
		t.Fatalf("vehicle contributor was not visible in console output: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "Credit:Role") || !strings.Contains(jsonl.String(), `"characterId":1409`) || !strings.Contains(jsonl.String(), `"actorType":"Vehicle"`) {
		t.Fatalf("vehicle source was merged into character credit or raw identity was lost: stdout=%q jsonl=%q", stdout.String(), jsonl.String())
	}
	if !strings.Contains(stdout.String(), "[PlayerTeamSide: SOURCE]") {
		t.Fatalf("local motorcycle damage was not labeled as player-team output: %q", stdout.String())
	}
}

func TestPlayerRelevanceKeepsLocalPlayerIDAcrossSceneTransition(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	if _, ok := p.playerRelevance.localPlayerIDs[77]; !ok {
		t.Fatal("local PlayerId was not established from the client role report")
	}

	p.playerRelevance.resetSceneEntities()
	p.entities.entities = make(map[uint64]runtimeEntity)
	addTestPlayerRoleGroupEntity(p, 200, 77, 1508)
	addTestPlayerRoleGroupEntity(p, 201, 88, 1508)
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })

	localEvent := testDamageEvent(p, 200, 300)
	sourceRelation, targetRelation, visible := p.playerRelevantDamageEvent(localEvent)
	if !visible || sourceRelation != playerRelationLocal || targetRelation != playerRelationUnrelated {
		t.Fatalf("same-player entity after scene transition = %v/%v/%v, want LOCAL/UNRELATED/true", sourceRelation, targetRelation, visible)
	}

	otherPlayerEvent := testDamageEvent(p, 201, 300)
	sourceRelation, _, visible = p.playerRelevantDamageEvent(otherPlayerEvent)
	if !visible || sourceRelation != playerRelationUnknown {
		t.Fatalf("other player after scene transition = %v/%v, want UNKNOWN/true", sourceRelation, visible)
	}

	p.playerRelevance.reset()
	if len(p.playerRelevance.localPlayerIDs) != 0 {
		t.Fatal("new capture session retained the previous local PlayerId")
	}
}

func TestPlayerRelevanceDoesNotAttributeOtherPlayerVehicleByPlayerID(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) {
		entity.playerID, entity.hasPlayerID = 88, true
		entity.entityTypeCode, entity.hasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.DamageContributorCandidate = &damageContributorCandidate{ActorType: "Vehicle", Status: resolutionCorrelated}
	if !p.writeDamageEvent(event) {
		t.Fatal("unresolved other-player vehicle event was filtered")
	}
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.UnattributedOutgoingDamage != 321 {
		t.Fatalf("other-player vehicle was attributed to the local player: %+v", snapshot)
	}
}

func TestPlayerRelevanceDoesNotInferUnknownSummonFromPlayerID(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) {
		entity.playerID, entity.hasPlayerID = 77, true
		entity.entityTypeCode, entity.hasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	if !p.writeDamageEvent(event) {
		t.Fatal("unresolved summon event was filtered")
	}
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.UnattributedOutgoingDamage != 321 {
		t.Fatalf("unknown summon was attributed to the local player: %+v", snapshot)
	}
}

func TestPlayerRelevanceKeepsNPCDamageUnrelatedWithLocalPlayerID(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 101, func(entity *runtimeEntity) {
		entity.playerID, entity.hasPlayerID = 77, true
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9001, true
	})
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9002, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.DamageContributorCandidate = &damageContributorCandidate{ActorType: "Vehicle", Status: resolutionCorrelated}
	if p.writeDamageEvent(event) {
		t.Fatal("NPC vs NPC event was classified as player-relevant")
	}
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.DamageTaken != 0 {
		t.Fatalf("NPC vs NPC event changed player totals: %+v", snapshot)
	}
}

func TestPlayerRelevanceUsesLocalPlayerRoleGroup(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	addTestPlayerRoleGroupEntity(p, 102, 88, 1312)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.playerID, entity.hasPlayerID = 77, true
		entity.ownerIncID, entity.hasOwnerIncID = 99, true
	})

	event := testDamageEvent(p, 101, 200)
	source, target, _ := p.playerRelevantDamageEvent(event)
	if source != playerRelationLocal || target != playerRelationUnrelated {
		t.Fatalf("team role relations = (%d, %d), want (local, unrelated)", source, target)
	}
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.writeDamageEvent(event) {
		t.Fatal("damage from another role in the local PlayerSceneGroup was not retained")
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 10 || got.OutgoingEvents != 1 {
		t.Fatalf("team role damage was not counted as confirmed output: %+v", got)
	}

	otherPlayerEvent := testDamageEvent(p, 102, 200)
	otherSource, otherTarget, _ := p.playerRelevantDamageEvent(otherPlayerEvent)
	if otherSource != playerRelationUnknown || otherTarget != playerRelationUnrelated {
		t.Fatalf("another player's role was attributed locally: relations=(%d, %d)", otherSource, otherTarget)
	}
}

func TestPlayerTeamDamageDoesNotDisplayUnknownCreditWhenRoleIdentityIsUnavailable(t *testing.T) {
	p, stdout, jsonl := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	if !p.writeDamageEvent(event) {
		t.Fatal("damage from another role in the local player group was filtered")
	}
	output := stdout.String()
	if !strings.Contains(output, "Credit:PlayerTeam [role not resolved]") || strings.Contains(output, "Credit:?") {
		t.Fatalf("confirmed player-team damage was presented as unknown credit: %q", output)
	}
	if !strings.Contains(output, "[PlayerTeamSide: SOURCE]") {
		t.Fatalf("confirmed player-team source label missing: %q", output)
	}
	var row struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Credit        combatEventCreditJSONL `json:"credit"`
		Actors        actorsJSONL            `json:"actors"`
	}
	if err := json.NewDecoder(jsonl).Decode(&row); err != nil {
		t.Fatalf("decode player-team event JSONL: %v", err)
	}
	if row.SchemaVersion != 5 {
		t.Fatalf("event schemaVersion = %d, want the unchanged 5", row.SchemaVersion)
	}
	if row.Credit.CharacterID == nil || *row.Credit.CharacterID != 1508 || row.Credit.Status != resolutionCorrelated || row.Credit.Name != "" || row.Credit.NameStatus == resolutionVerified || row.Credit.NameStatus == resolutionCorrelated {
		t.Fatalf("the FightRoleInfos role must be kept in the credit block without a resolved name: %+v", row.Credit)
	}
	if row.Actors.ActualSource.ActorType != "Character" || row.Actors.Target.ActorType != "Monster" {
		t.Fatalf("actors = %+v", row.Actors)
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 321 || got.OutgoingEvents != 1 {
		t.Fatalf("team role damage was not counted as player output: %+v", got)
	}
}

func TestPlayerRelevanceUsesExplicitMonsterEntityType(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.playerID, entity.hasPlayerID = 77, true
		entity.ownerIncID, entity.hasOwnerIncID = 99, true
	})

	outgoing := testDamageEvent(p, 100, 200)
	outgoing.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.writeDamageEvent(outgoing) {
		t.Fatal("damage against an explicit Monster entity was not retained")
	}
	incoming := testDamageEvent(p, 200, 100)
	incoming.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.writeDamageEvent(incoming) {
		t.Fatal("damage from an explicit Monster entity was not retained")
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 10 || got.DamageTaken != 10 ||
		got.OutgoingEvents != 1 || got.IncomingEvents != 1 {
		t.Fatalf("Monster entity damage was not classified by direction: %+v", got)
	}
}

func TestPlayerRelevanceKeepsUnownedMonsterSummonUnknown(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, func(entity *runtimeEntity) { entity.roleID, entity.hasRoleID = 1409, true })
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.summonType, entity.hasSummonType = summonTypeConcomitantVision, true
	})
	event := testDamageEvent(p, 200, 100)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	source, target, visible := p.playerRelevantDamageEvent(event)
	if source != playerRelationUnknown || target != playerRelationLocal || !visible {
		t.Fatalf("unowned Monster summon relations = (%d, %d), visible=%t; want (unknown, local), visible=true", source, target, visible)
	}
	if !p.writeDamageEvent(event) {
		t.Fatal("unknown Monster summon damage was discarded instead of retained as unresolved")
	}
	if got := p.damageStatistics.Snapshot(); got.DamageTaken != 0 || got.UnattributedIncomingDamage != 10 {
		t.Fatalf("unknown Monster summon was counted as confirmed incoming damage: %+v", got)
	}
}

func addTestMonsterCompanion(p *processor, entityID, summonerID uint64, extra func(*runtimeEntity)) {
	addTestRuntimeEntity(p, entityID, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.playerID, entity.hasPlayerID = 77, true
		entity.summonerID, entity.hasSummonerID = summonerID, true
		entity.summonConfigID, entity.hasSummonConfigID = 3980056, true
		entity.summonType, entity.hasSummonType = 2, true
		if extra != nil {
			extra(entity)
		}
	})
}

func addTestPlainMonster(p *processor, entityID uint64) {
	addTestRuntimeEntity(p, entityID, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.playerID, entity.hasPlayerID = 77, true
	})
}

func TestPlayerRelevanceCountsTheEnemysOwnSummon(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestPlainMonster(p, 300)
	addTestMonsterCompanion(p, 200, 300, nil)

	outgoing := testDamageEvent(p, 100, 200)
	outgoing.EventTypeCandidate = "DAMAGE_CANDIDATE"
	source, target, _ := p.playerRelevantDamageEvent(outgoing)
	if source != playerRelationLocal || target != playerRelationUnrelated {
		t.Fatalf("relations of a hit on a Monster summoned by a Monster = (%d, %d)", source, target)
	}
	if !p.writeDamageEvent(outgoing) {
		t.Fatal("hit on the enemy's summon was not retained")
	}
	incoming := testDamageEvent(p, 200, 100)
	incoming.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.writeDamageEvent(incoming) {
		t.Fatal("hit from the enemy's summon was not retained")
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 10 || got.DamageTaken != 10 || got.UnattributedOutgoingDamage != 0 || got.UnattributedIncomingDamage != 0 {
		t.Fatalf("the enemy's summon was not classified by direction: %+v", got)
	}
}

func TestPlayerRelevanceKeepsMonsterSummonUnknownWithoutAClosedChain(t *testing.T) {
	cases := []struct {
		name  string
		setup func(p *processor)
	}{
		{"the summoner was never observed", func(p *processor) { addTestMonsterCompanion(p, 200, 300, nil) }},
		{"the summoner is another player's character", func(p *processor) {
			addTestPlayerRoleGroupEntity(p, 300, 88, 1209)
			addTestMonsterCompanion(p, 200, 300, nil)
		}},
		{"the summoner chain ends at an unclassified entity", func(p *processor) {
			addTestRuntimeEntity(p, 300, nil)
			addTestMonsterCompanion(p, 200, 300, nil)
		}},
		{"a second owner link is unresolved", func(p *processor) {
			addTestPlainMonster(p, 300)
			addTestMonsterCompanion(p, 200, 300, func(entity *runtimeEntity) { entity.ownerID, entity.hasOwnerID = 900, true })
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p, _, _ := newPlayerRelevanceTestProcessor()
			addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
			observeTestLocalRole(p, 100)
			test.setup(p)
			event := testDamageEvent(p, 100, 200)
			event.EventTypeCandidate = "DAMAGE_CANDIDATE"
			if source, target, _ := p.playerRelevantDamageEvent(event); source != playerRelationLocal || target != playerRelationUnknown {
				t.Fatalf("relations = (%d, %d), want (local, unknown)", source, target)
			}
			p.writeDamageEvent(event)
			if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 || got.UnattributedOutgoingDamage != 10 {
				t.Fatalf("an unproven summon entered the confirmed totals: %+v", got)
			}
		})
	}
}

func TestPlayerRelevanceCreditsAMonsterTypedSummonOfALocalRole(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestMonsterCompanion(p, 101, 100, nil)
	addTestPlainMonster(p, 300)
	event := testDamageEvent(p, 101, 300)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if source, target, _ := p.playerRelevantDamageEvent(event); source != playerRelationLocal || target != playerRelationUnrelated {
		t.Fatalf("relations = (%d, %d), want (local, unrelated)", source, target)
	}
}

func TestPlayerRelevanceDoesNotCountMonsterVersusMonster(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	for _, entityID := range []uint64{200, 201} {
		addTestRuntimeEntity(p, entityID, func(entity *runtimeEntity) {
			entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
			entity.playerID, entity.hasPlayerID = 77, true
			entity.ownerIncID, entity.hasOwnerIncID = entityID+1000, true
		})
	}
	event := testDamageEvent(p, 200, 201)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if p.writeDamageEvent(event) {
		t.Fatal("Monster-versus-Monster damage passed the player relevance filter")
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 || got.DamageTaken != 0 || got.UnattributedOutgoingDamage != 0 {
		t.Fatalf("Monster-versus-Monster damage changed player statistics: %+v", got)
	}
}

func TestPlayerRelevanceAttributesSummonOfAnotherLocalTeamRole(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 102, func(entity *runtimeEntity) {
		entity.summonerID, entity.hasSummonerID = 101, true
		entity.summonType, entity.hasSummonType = summonTypeConcomitantVision, true
	})
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
	})

	event := testDamageEvent(p, 102, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.writeDamageEvent(event) {
		t.Fatal("summon damage linked to another local team role was not retained")
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 10 || got.OutgoingEvents != 1 {
		t.Fatalf("summon damage linked to the local team was not counted: %+v", got)
	}
}

func TestPlayerRelevanceIsSessionAndSceneScoped(t *testing.T) {
	p, stdout, _ := newPlayerRelevanceTestProcessor()
	observeTestLocalRole(p, 100)
	if !p.playerRelevance.isRoleEntity(100) {
		t.Fatal("client current-role report did not establish the local entity")
	}
	p.playerRelevance.reset()
	if p.playerRelevance.isRoleEntity(100) {
		t.Fatal("scene/session reset retained the previous local entity")
	}
	if stdout.Len() != 0 {
		t.Fatalf("identity tracking wrote unexpected normal output: %q", stdout.String())
	}
}

func TestDamageResponseDeduplicatesPlayerOutputAndKeepsRawCounts(t *testing.T) {
	var stdout, diagnostic bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
	p.pending[42] = pendingDamageRequest{
		attackerEntityID: 100, hasAttackerEntityID: true,
		targetEntityID: 200, hasTargetEntityID: true,
	}
	response := appendVarintField(nil, fieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, fieldDamageExecuteResponseImmuneType, 0)
	response = appendVarintField(response, fieldDamageExecuteResponseDamage, 9001)
	p.handleOneDamageResult(42, response)
	p.handleOneDamageResult(42, response)
	if strings.Count(stdout.String(), "\n") != 1 || p.stats.printed != 1 || p.stats.matchedCombatResultAbs != 9001 {
		t.Fatalf("player output duplicated or missing: stdout=%q stats=%+v", stdout.String(), p.stats)
	}
	if p.stats.decodedMatchedEvents != 1 || p.stats.decodedCombatResultAbs != 9001 || p.stats.duplicateResponses != 1 {
		t.Fatalf("raw response accounting changed: %+v", p.stats)
	}
}
