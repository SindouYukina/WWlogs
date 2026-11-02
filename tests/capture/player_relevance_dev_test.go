//go:build wwdev

package capture

import (
	"bytes"
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
	p, stdout, _ := newPlayerRelevanceTestProcessor()
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
	response := appendVarintField(nil, 4, 0)
	response = appendVarintField(response, 5, 0)
	response = appendVarintField(response, 10, 9001)
	p.handleOneDamageResult(42, response)
	p.handleOneDamageResult(42, response)
	if strings.Count(stdout.String(), "\n") != 1 || p.stats.printed != 1 || p.stats.matchedCombatResultAbs != 9001 {
		t.Fatalf("player output duplicated or missing: stdout=%q stats=%+v", stdout.String(), p.stats)
	}
	if p.stats.decodedMatchedEvents != 1 || p.stats.decodedCombatResultAbs != 9001 || p.stats.duplicateResponses != 1 {
		t.Fatalf("raw response accounting changed: %+v", p.stats)
	}
}
