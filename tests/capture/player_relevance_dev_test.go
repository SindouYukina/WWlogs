//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func newPlayerRelevanceTestProcessor() (*Processor, *bytes.Buffer, *bytes.Buffer) {
	var stdout, jsonl bytes.Buffer
	p := NewProcessor(nil, &stdout, &bytes.Buffer{}, nil)
	p.Entities = NewEntityResolver()
	p.Dev.combatEventsJSONL = &jsonl
	return p, &stdout, &jsonl
}

// observeTestLocalRole reports entityID as the current role through the core role decoder, as every build reads it.
func observeTestLocalRole(p *Processor, entityID uint64) {
	p.ObserveClientPushFrame(GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out"}, localRoleReportBody(1409, entityID))
}

func TestPlayerRelevanceDoesNotFilterPlayerBuffSnapshots(t *testing.T) {
	p, _, jsonl := newPlayerRelevanceTestProcessor()
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	p.Dev.encounterSummary = &bytes.Buffer{}
	entityID, buffID := uint64(100), uint64(3000001001)
	row := buffSnapshotAuditRow{
		SchemaVersion: buffSnapshotSchemaVersion,
		EventType:     "BUFF_SNAPSHOT",
		SessionNumber: 1,
		EntityID:      &entityID,
		Buff:          RawBuffSnapshot{ID: buffID, HasID: true},
	}
	p.observeBuffSnapshot(row)
	p.WriteStructuredEventJSONL(row)

	if !strings.Contains(jsonl.String(), `"eventType":"BUFF_SNAPSHOT"`) {
		t.Fatalf("player Buff snapshot was filtered: %q", jsonl.String())
	}
	if len(p.Dev.encounterSummaries) != 1 {
		t.Fatalf("player Buff snapshot missing from encounter summary: %+v", p.Dev.encounterSummaries)
	}
	for _, summary := range p.Dev.encounterSummaries {
		if summary.buffObservations[buffObservationKey{buffID: buffID, hasBuffID: true, targetID: entityID, hasTargetID: true}] != 1 {
			t.Fatalf("player Buff observation missing: %+v", summary.buffObservations)
		}
	}
}

func TestPlayerRelevanceFilterUsesLocalRuntimeIdentity(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*Processor)
		sourceID   uint64
		targetID   uint64
		wantOutput bool
		wantOwner  bool
	}{
		{
			name: "local player character attacks an enemy",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, nil)
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "a boss damages the local player character",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 200)
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, nil)
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "monster damages another monster",
			setup: func(p *Processor) {
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9002, true })
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "monster damages unrelated npc",
			setup: func(p *Processor) {
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.TemplateID, entity.HasTemplateID = 7001, true })
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "unrelated npc damages monster",
			setup: func(p *Processor) {
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.TemplateID, entity.HasTemplateID = 7001, true })
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "local player summon damages an enemy and keeps owner chain",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.RoleID, entity.HasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
					entity.SummonerID, entity.HasSummonerID = 100, true
					entity.SummonType, entity.HasSummonType = SummonTypeConcomitantVision, true
				})
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true, wantOwner: true,
		},
		{
			name: "local echo skill damages an enemy and keeps owner chain",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.RoleID, entity.HasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
					entity.SummonerID, entity.HasSummonerID = 100, true
					entity.SummonType, entity.HasSummonType = SummonTypeConcomitantVision, true
					entity.SummonSkillID, entity.HasSummonSkillID = 280026010, true
				})
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true, wantOwner: true,
		},
		{
			name: "nearby monster fight is not included",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 300)
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9002, true })
				addTestRuntimeEntity(p, 300, nil)
			},
			sourceID: 100, targetID: 200,
		},
		{
			name: "unknown source remains visible when the target is local",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 200)
				addTestRuntimeEntity(p, 200, nil)
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "unknown target remains visible when the source is local",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, nil)
			},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
		{
			name: "role switch adds the newly reported local entity",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				observeTestLocalRole(p, 101)
				addTestRuntimeEntity(p, 100, nil)
				addTestRuntimeEntity(p, 101, nil)
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true,
		},
		{
			name: "entity removal invalidates the summon owner link",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.RoleID, entity.HasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) { entity.SummonerID, entity.HasSummonerID = 100, true })
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
				p.PlayerRelevance.RemoveEntity(100)
				delete(p.Entities.Entities, 100)
			},
			sourceID: 101, targetID: 200, wantOutput: true,
		},
		{
			name: "another player remains visible until unrelatedness is established",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.RoleID, entity.HasRoleID = 1409, true })
				addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) { entity.RoleID, entity.HasRoleID = 1409, true })
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
			},
			sourceID: 101, targetID: 200, wantOutput: true,
		},
		{
			name:     "unresolved event remains visible",
			setup:    func(*Processor) {},
			sourceID: 100, targetID: 200, wantOutput: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, stdout, jsonl := newPlayerRelevanceTestProcessor()
			test.setup(p)
			event := testDamageEvent(p, test.sourceID, test.targetID)
			if test.wantOwner {
				event.SummonerCharacter = ResolveSummonerCharacter(p.Entities, p.Masters, event.Source.Runtime)
			}
			if got := p.WriteDamageEvent(event); got != test.wantOutput {
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
			if !test.wantOutput && p.Stats.SuppressedUnrelatedEvents != 1 {
				t.Fatalf("confirmed unrelated event count = %d, want 1", p.Stats.SuppressedUnrelatedEvents)
			}
		})
	}
}

func TestDamageEventConsoleLabelsLocalPlayerSide(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Processor)
		sourceID  uint64
		targetID  uint64
		wantLabel string
	}{
		{
			name: "local source",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 100)
				addTestRuntimeEntity(p, 100, nil)
				addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
			},
			sourceID: 100, targetID: 200, wantLabel: "SOURCE",
		},
		{
			name: "local target",
			setup: func(p *Processor) {
				observeTestLocalRole(p, 200)
				addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
				addTestRuntimeEntity(p, 200, nil)
			},
			sourceID: 100, targetID: 200, wantLabel: "TARGET",
		},
		{
			name:      "unresolved endpoints",
			setup:     func(*Processor) {},
			sourceID:  100,
			targetID:  200,
			wantLabel: "UNCONFIRMED",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, stdout, _ := newPlayerRelevanceTestProcessor()
			test.setup(p)
			if !p.WriteDamageEvent(testDamageEvent(p, test.sourceID, test.targetID)) {
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
	addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.RoleID, entity.HasRoleID = 1409, true })
	addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
		entity.SummonerID, entity.HasSummonerID = 100, true
		entity.SummonType, entity.HasSummonType = SummonTypeConcomitantMotorcycle, true
	})
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })

	event := testDamageEvent(p, 101, 200)
	event.DamageContributorCandidate = &DamageContributorCandidate{ActorType: "Vehicle", Status: ResolutionCorrelated}
	if !p.WriteDamageEvent(event) {
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
	addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
		entity.PlayerID, entity.HasPlayerID = 77, true
		entity.EntityTypeCode, entity.HasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.CharacterID = 1409
	event.CharacterName = "Role"
	event.CharacterNameStatus = ResolutionVerified
	event.CharacterStatus = ResolutionVerified
	event.DamageContributorCandidate = &DamageContributorCandidate{ActorType: "Vehicle", Status: ResolutionCorrelated}
	source, target, visible := p.PlayerRelevantDamageEvent(event)
	if source != PlayerRelationLocal || target != PlayerRelationUnrelated || !visible {
		t.Fatalf("local vehicle relations = (%d, %d, %t), want (local, unrelated, visible)", source, target, visible)
	}
	if !p.WriteDamageEvent(event) {
		t.Fatal("local vehicle damage was filtered")
	}
	snapshot := p.DamageStatistics.Snapshot()
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
	if _, ok := p.PlayerRelevance.LocalPlayerIDs[77]; !ok {
		t.Fatal("local PlayerId was not established from the client role report")
	}

	p.PlayerRelevance.ResetSceneEntities()
	p.Entities.Entities = make(map[uint64]RuntimeEntity)
	addTestPlayerRoleGroupEntity(p, 200, 77, 1508)
	addTestPlayerRoleGroupEntity(p, 201, 88, 1508)
	addTestRuntimeEntity(p, 300, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })

	localEvent := testDamageEvent(p, 200, 300)
	sourceRelation, targetRelation, visible := p.PlayerRelevantDamageEvent(localEvent)
	if !visible || sourceRelation != PlayerRelationLocal || targetRelation != PlayerRelationUnrelated {
		t.Fatalf("same-player entity after scene transition = %v/%v/%v, want LOCAL/UNRELATED/true", sourceRelation, targetRelation, visible)
	}

	otherPlayerEvent := testDamageEvent(p, 201, 300)
	sourceRelation, _, visible = p.PlayerRelevantDamageEvent(otherPlayerEvent)
	if !visible || sourceRelation != PlayerRelationUnknown {
		t.Fatalf("other player after scene transition = %v/%v, want UNKNOWN/true", sourceRelation, visible)
	}

	p.PlayerRelevance.Reset()
	if len(p.PlayerRelevance.LocalPlayerIDs) != 0 {
		t.Fatal("new capture session retained the previous local PlayerId")
	}
}

func TestPlayerRelevanceDoesNotAttributeOtherPlayerVehicleByPlayerID(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
		entity.PlayerID, entity.HasPlayerID = 88, true
		entity.EntityTypeCode, entity.HasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.DamageContributorCandidate = &DamageContributorCandidate{ActorType: "Vehicle", Status: ResolutionCorrelated}
	if !p.WriteDamageEvent(event) {
		t.Fatal("unresolved other-player vehicle event was filtered")
	}
	snapshot := p.DamageStatistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.UnattributedOutgoingDamage != 321 {
		t.Fatalf("other-player vehicle was attributed to the local player: %+v", snapshot)
	}
}

func TestPlayerRelevanceDoesNotInferUnknownSummonFromPlayerID(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
		entity.PlayerID, entity.HasPlayerID = 77, true
		entity.EntityTypeCode, entity.HasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	if !p.WriteDamageEvent(event) {
		t.Fatal("unresolved summon event was filtered")
	}
	snapshot := p.DamageStatistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.UnattributedOutgoingDamage != 321 {
		t.Fatalf("unknown summon was attributed to the local player: %+v", snapshot)
	}
}

func TestPlayerRelevanceKeepsNPCDamageUnrelatedWithLocalPlayerID(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 101, func(entity *RuntimeEntity) {
		entity.PlayerID, entity.HasPlayerID = 77, true
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9002, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.DamageContributorCandidate = &DamageContributorCandidate{ActorType: "Vehicle", Status: ResolutionCorrelated}
	if p.WriteDamageEvent(event) {
		t.Fatal("NPC vs NPC event was classified as player-relevant")
	}
	snapshot := p.DamageStatistics.Snapshot()
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
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.PlayerID, entity.HasPlayerID = 77, true
		entity.OwnerIncID, entity.HasOwnerIncID = 99, true
	})

	event := testDamageEvent(p, 101, 200)
	source, target, _ := p.PlayerRelevantDamageEvent(event)
	if source != PlayerRelationLocal || target != PlayerRelationUnrelated {
		t.Fatalf("team role relations = (%d, %d), want (local, unrelated)", source, target)
	}
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.WriteDamageEvent(event) {
		t.Fatal("damage from another role in the local PlayerSceneGroup was not retained")
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 10 || got.OutgoingEvents != 1 {
		t.Fatalf("team role damage was not counted as confirmed output: %+v", got)
	}

	otherPlayerEvent := testDamageEvent(p, 102, 200)
	otherSource, otherTarget, _ := p.PlayerRelevantDamageEvent(otherPlayerEvent)
	if otherSource != PlayerRelationUnknown || otherTarget != PlayerRelationUnrelated {
		t.Fatalf("another player's role was attributed locally: relations=(%d, %d)", otherSource, otherTarget)
	}
}

func TestPlayerTeamDamageDoesNotDisplayUnknownCreditWhenRoleIdentityIsUnavailable(t *testing.T) {
	p, stdout, jsonl := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})

	event := testDamageEvent(p, 101, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	if !p.WriteDamageEvent(event) {
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
	if row.Credit.CharacterID == nil || *row.Credit.CharacterID != 1508 || row.Credit.Status != ResolutionCorrelated || row.Credit.Name != "" || row.Credit.NameStatus == ResolutionVerified || row.Credit.NameStatus == ResolutionCorrelated {
		t.Fatalf("the FightRoleInfos role must be kept in the credit block without a resolved name: %+v", row.Credit)
	}
	if row.Actors.ActualSource.ActorType != "Character" || row.Actors.Target.ActorType != "Monster" {
		t.Fatalf("actors = %+v", row.Actors)
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 321 || got.OutgoingEvents != 1 {
		t.Fatalf("team role damage was not counted as player output: %+v", got)
	}
}

func TestPlayerRelevanceUsesExplicitMonsterEntityType(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.PlayerID, entity.HasPlayerID = 77, true
		entity.OwnerIncID, entity.HasOwnerIncID = 99, true
	})

	outgoing := testDamageEvent(p, 100, 200)
	outgoing.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.WriteDamageEvent(outgoing) {
		t.Fatal("damage against an explicit Monster entity was not retained")
	}
	incoming := testDamageEvent(p, 200, 100)
	incoming.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.WriteDamageEvent(incoming) {
		t.Fatal("damage from an explicit Monster entity was not retained")
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 10 || got.DamageTaken != 10 ||
		got.OutgoingEvents != 1 || got.IncomingEvents != 1 {
		t.Fatalf("Monster entity damage was not classified by direction: %+v", got)
	}
}

func TestPlayerRelevanceKeepsUnownedMonsterSummonUnknown(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, func(entity *RuntimeEntity) { entity.RoleID, entity.HasRoleID = 1409, true })
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.SummonType, entity.HasSummonType = SummonTypeConcomitantVision, true
	})
	event := testDamageEvent(p, 200, 100)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	source, target, visible := p.PlayerRelevantDamageEvent(event)
	if source != PlayerRelationUnknown || target != PlayerRelationLocal || !visible {
		t.Fatalf("unowned Monster summon relations = (%d, %d), visible=%t; want (unknown, local), visible=true", source, target, visible)
	}
	if !p.WriteDamageEvent(event) {
		t.Fatal("unknown Monster summon damage was discarded instead of retained as unresolved")
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageTaken != 0 || got.UnattributedIncomingDamage != 10 {
		t.Fatalf("unknown Monster summon was counted as confirmed incoming damage: %+v", got)
	}
}

func addTestMonsterCompanion(p *Processor, entityID, summonerID uint64, extra func(*RuntimeEntity)) {
	addTestRuntimeEntity(p, entityID, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.PlayerID, entity.HasPlayerID = 77, true
		entity.SummonerID, entity.HasSummonerID = summonerID, true
		entity.SummonConfigID, entity.HasSummonConfigID = 3980056, true
		entity.SummonType, entity.HasSummonType = 2, true
		if extra != nil {
			extra(entity)
		}
	})
}

func addTestPlainMonster(p *Processor, entityID uint64) {
	addTestRuntimeEntity(p, entityID, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.PlayerID, entity.HasPlayerID = 77, true
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
	source, target, _ := p.PlayerRelevantDamageEvent(outgoing)
	if source != PlayerRelationLocal || target != PlayerRelationUnrelated {
		t.Fatalf("relations of a hit on a Monster summoned by a Monster = (%d, %d)", source, target)
	}
	if !p.WriteDamageEvent(outgoing) {
		t.Fatal("hit on the enemy's summon was not retained")
	}
	incoming := testDamageEvent(p, 200, 100)
	incoming.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.WriteDamageEvent(incoming) {
		t.Fatal("hit from the enemy's summon was not retained")
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 10 || got.DamageTaken != 10 || got.UnattributedOutgoingDamage != 0 || got.UnattributedIncomingDamage != 0 {
		t.Fatalf("the enemy's summon was not classified by direction: %+v", got)
	}
}

func TestPlayerRelevanceKeepsMonsterSummonUnknownWithoutAClosedChain(t *testing.T) {
	cases := []struct {
		name  string
		setup func(p *Processor)
	}{
		{"the summoner was never observed", func(p *Processor) { addTestMonsterCompanion(p, 200, 300, nil) }},
		{"the summoner is another player's character", func(p *Processor) {
			addTestPlayerRoleGroupEntity(p, 300, 88, 1209)
			addTestMonsterCompanion(p, 200, 300, nil)
		}},
		{"the summoner chain ends at an unclassified entity", func(p *Processor) {
			addTestRuntimeEntity(p, 300, nil)
			addTestMonsterCompanion(p, 200, 300, nil)
		}},
		{"a second owner link is unresolved", func(p *Processor) {
			addTestPlainMonster(p, 300)
			addTestMonsterCompanion(p, 200, 300, func(entity *RuntimeEntity) { entity.OwnerID, entity.HasOwnerID = 900, true })
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
			if source, target, _ := p.PlayerRelevantDamageEvent(event); source != PlayerRelationLocal || target != PlayerRelationUnknown {
				t.Fatalf("relations = (%d, %d), want (local, unknown)", source, target)
			}
			p.WriteDamageEvent(event)
			if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 0 || got.UnattributedOutgoingDamage != 10 {
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
	if source, target, _ := p.PlayerRelevantDamageEvent(event); source != PlayerRelationLocal || target != PlayerRelationUnrelated {
		t.Fatalf("relations = (%d, %d), want (local, unrelated)", source, target)
	}
}

func TestPlayerRelevanceDoesNotCountMonsterVersusMonster(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	for _, entityID := range []uint64{200, 201} {
		addTestRuntimeEntity(p, entityID, func(entity *RuntimeEntity) {
			entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
			entity.PlayerID, entity.HasPlayerID = 77, true
			entity.OwnerIncID, entity.HasOwnerIncID = entityID+1000, true
		})
	}
	event := testDamageEvent(p, 200, 201)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if p.WriteDamageEvent(event) {
		t.Fatal("Monster-versus-Monster damage passed the player relevance filter")
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 0 || got.DamageTaken != 0 || got.UnattributedOutgoingDamage != 0 {
		t.Fatalf("Monster-versus-Monster damage changed player statistics: %+v", got)
	}
}

func TestPlayerRelevanceAttributesSummonOfAnotherLocalTeamRole(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 102, func(entity *RuntimeEntity) {
		entity.SummonerID, entity.HasSummonerID = 101, true
		entity.SummonType, entity.HasSummonType = SummonTypeConcomitantVision, true
	})
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
	})

	event := testDamageEvent(p, 102, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	if !p.WriteDamageEvent(event) {
		t.Fatal("summon damage linked to another local team role was not retained")
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 10 || got.OutgoingEvents != 1 {
		t.Fatalf("summon damage linked to the local team was not counted: %+v", got)
	}
}

func TestPlayerRelevanceIsSessionAndSceneScoped(t *testing.T) {
	p, stdout, _ := newPlayerRelevanceTestProcessor()
	observeTestLocalRole(p, 100)
	if !p.PlayerRelevance.IsRoleEntity(100) {
		t.Fatal("client current-role report did not establish the local entity")
	}
	p.PlayerRelevance.Reset()
	if p.PlayerRelevance.IsRoleEntity(100) {
		t.Fatal("scene/session reset retained the previous local entity")
	}
	if stdout.Len() != 0 {
		t.Fatalf("identity tracking wrote unexpected normal output: %q", stdout.String())
	}
}

func TestDamageResponseDeduplicatesPlayerOutputAndKeepsRawCounts(t *testing.T) {
	var stdout, diagnostic bytes.Buffer
	p := NewProcessor(nil, &stdout, &diagnostic, nil)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 200, func(entity *RuntimeEntity) { entity.MonsterID, entity.HasMonsterID = 9001, true })
	p.Pending[42] = PendingDamageRequest{
		AttackerEntityID: 100, HasAttackerEntityID: true,
		TargetEntityID: 200, HasTargetEntityID: true,
	}
	response := appendVarintField(nil, FieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, FieldDamageExecuteResponseImmuneType, 0)
	response = appendVarintField(response, FieldDamageExecuteResponseDamage, 9001)
	p.HandleOneDamageResult(42, response)
	p.HandleOneDamageResult(42, response)
	if strings.Count(stdout.String(), "\n") != 1 || p.Stats.Printed != 1 || p.Stats.MatchedCombatResultAbs != 9001 {
		t.Fatalf("player output duplicated or missing: stdout=%q stats=%+v", stdout.String(), p.Stats)
	}
	if p.Stats.DecodedMatchedEvents != 1 || p.Stats.DecodedCombatResultAbs != 9001 || p.Stats.DuplicateResponses != 1 {
		t.Fatalf("raw response accounting changed: %+v", p.Stats)
	}
}
