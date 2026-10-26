package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"
)

func appendFixed32TestField(dst []byte, number uint64, value uint32) []byte {
	dst = appendTestVarint(dst, number<<3|5)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	return append(dst, raw[:]...)
}

func TestEntityActiveResponseWithoutSnapshotOutputStillParsesBuff(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	buffRecord := appendVarintField(nil, 2, 70001)
	response := appendBytesField(nil, 11, appendBytesField(nil, 15, appendBytesField(nil, 1, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: 7}, response); err != nil {
		t.Fatalf("handle ActiveResponse without a snapshot writer: %v", err)
	}
	if p.stats.buffSnapshotResponses != 1 || p.stats.observedBuffEvents != 1 {
		t.Fatalf("snapshot responses/events = %d/%d, want 1/1", p.stats.buffSnapshotResponses, p.stats.observedBuffEvents)
	}
}

func TestEntityActiveResponseCorrelatesIdentityComponentsToRequestEntity(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	const token = 55
	const entityID = 8801
	if err := p.handleActiveRequest(token, appendVarintField(nil, 4, entityID)); err != nil {
		t.Fatal(err)
	}

	summoner := appendVarintField(nil, 1, 771)
	summoner = appendVarintField(summoner, 2, 9901)
	summoner = appendVarintField(summoner, 3, 220)
	summoner = appendVarintField(summoner, 4, 772)
	summoner = appendVarintField(summoner, 5, 2)
	monster := appendVarintField(nil, 1, 123456)
	monster = appendVarintField(monster, 2, 654321)
	monster = appendVarintField(monster, 3, 456789)
	follower := appendVarintField(nil, 1, 773)
	follower = appendVarintField(follower, 2, 9902)
	var response []byte
	response = appendBytesField(response, 11, appendBytesField(nil, 4, summoner))
	response = appendBytesField(response, 11, appendBytesField(nil, 18, monster))
	response = appendBytesField(response, 11, appendBytesField(nil, 42, follower))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: token}, response); err != nil {
		t.Fatal(err)
	}

	entity := p.entities.entities[entityID]
	if !entity.hasMonsterID || entity.monsterID != 456789 || !entity.hasTemplateID || entity.templateID != 123456 || !entity.hasMonsterCaptureEntityID || entity.monsterCaptureEntityID != 654321 {
		t.Fatalf("monster component identity = %+v", entity)
	}
	if !entity.hasSummonerID || entity.summonerID != 771 || !entity.hasSummonerPlayerID || entity.summonerPlayerID != 772 {
		t.Fatalf("summoner component identity = %+v", entity)
	}
	if !entity.hasPlayerEntityID || entity.playerEntityID != 773 || !entity.hasFollowerSummonConfigID || entity.followerSummonConfigID != 9902 {
		t.Fatalf("follower component identity = %+v", entity)
	}
	if entity.sourceMessageID != 28481 || entity.componentSourceMessageID != 26702 || !strings.Contains(entity.componentSourceFieldPath, "frame token match + field4 EntityId") {
		t.Fatalf("identity evidence provenance = %+v", entity)
	}
	if status := func() resolutionStatus { _, status := p.entities.resolve(entityID); return status }(); status != resolutionCorrelated {
		t.Fatalf("runtime entity status = %s, want CORRELATED", status)
	}
	if p.stats.activeResponseIdentityComponents != 3 || p.stats.activeResponseSummonerComponents != 1 || p.stats.activeResponseMonsterComponents != 1 || p.stats.activeResponseFollowerComponents != 1 {
		t.Fatalf("identity component counts = %+v", p.stats)
	}

	masters := &gameMaster{monsters: map[uint64]masterRecord{456789: {ID: 456789, Status: string(resolutionVerified)}}}
	identity := resolveEntityIdentity(p.entities, masters, entityID)
	if !identity.HasMonster || identity.Monster.ID != 456789 || identity.MonsterStatus != resolutionCorrelated {
		t.Fatalf("static Monster mapping confidence = %+v", identity)
	}
}

func TestApplyGameplayEffectNotifyPreservesRawIdentityFields(t *testing.T) {
	body := appendVarintField(nil, 2, 501)    // Handle
	body = appendVarintField(body, 11, 71001) // Id
	body = appendVarintField(body, 5, 2)      // Level
	body = appendVarintField(body, 7, 8801)   // EntityId
	body = appendVarintField(body, 8, 99002)  // InstigatorId
	body = appendVarintField(body, 9, 1)      // ApplyType
	body = appendFixed32TestField(body, 15, mathFloat32bits(12.5))
	body = appendVarintField(body, 6, 1)    // IsActive
	body = appendVarintField(body, 10, 601) // ServerId
	body = appendVarintField(body, 1, 3)    // StackCount
	body = appendFixed32TestField(body, 12, mathFloat32bits(4.25))
	body = appendVarintField(body, 14, 72002) // ConfBuffId
	frame := gameFrame{commandID: 25830, direction: "in", timestamp: time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC)}
	row, err := decodeBuffLifecycleMessage(frame, body)
	if err != nil {
		t.Fatal(err)
	}
	if row.MessageName != "ApplyGameplayEffectNotify" || row.EventKind != "GAMEPLAY_EFFECT_APPLY" || row.EventTypeStatus != resolutionCorrelated {
		t.Fatalf("event classification = %+v", row)
	}
	if row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.InstigatorID == nil || *row.InstigatorID != 99002 || row.InstigatorStatus != resolutionCorrelated || !strings.Contains(row.InstigatorEvidence, "CreatureModel.GetEntity") {
		t.Fatalf("target/source evidence was lost or overstated: %+v", row)
	}
	if row.HandleID == nil || *row.HandleID != 501 || row.ID == nil || *row.ID != 71001 || row.ConfBuffID == nil || *row.ConfBuffID != 72002 || row.BuffID != nil {
		t.Fatalf("Handle, Id, ConfBuffId, and BuffId were conflated: %+v", row)
	}
	if row.DurationSeconds == nil || *row.DurationSeconds != 12.5 || row.RemainingDuration == nil || *row.RemainingDuration != 4.25 || row.StackCount == nil || *row.StackCount != 3 || row.IsActive == nil || !*row.IsActive {
		t.Fatalf("duration/stack/active values = %+v", row)
	}
}

func TestBuffLifecycleMessagesPreserveCommandSpecificFields(t *testing.T) {
	tests := []struct {
		name      string
		commandID uint16
		body      []byte
		check     func(*testing.T, buffLifecycleAuditRow)
	}{
		{
			name: "remove gameplay effect notify", commandID: 18345,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 13, 501), 5, 8801), 14, 99002),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.EventKind != "GAMEPLAY_EFFECT_REMOVE" || row.TargetEntityID == nil || *row.TargetEntityID != 8801 || row.InstigatorID == nil || *row.InstigatorID != 99002 || row.HandleID == nil || *row.HandleID != 501 {
					t.Fatalf("remove notify fields = %+v", row)
				}
			},
		},
		{
			name: "remove gameplay effect push", commandID: 20358,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, 14, 502), 15, 8802), 10, 1), 4, 99003),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8802 || row.HandleID == nil || *row.HandleID != 502 || row.IsPrematureRemoval == nil || !*row.IsPrematureRemoval || row.InstigatorID == nil || *row.InstigatorID != 99003 {
					t.Fatalf("remove push fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff add", commandID: 17893,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 12, 8803), 1, 1), 3, 70001),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8803 || row.BuffID == nil || *row.BuffID != 70001 || row.IsBuffAdd == nil || !*row.IsBuffAdd || row.InstigatorStatus != resolutionUnknown {
					t.Fatalf("simple buff fields = %+v", row)
				}
			},
		},
		{
			name: "simple buff layer count", commandID: 22102,
			body: appendVarintField(appendVarintField(appendVarintField(nil, 6, 8804), 7, 4), 2, 70002),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.TargetEntityID == nil || *row.TargetEntityID != 8804 || row.BuffID == nil || *row.BuffID != 70002 || row.StackCount == nil || *row.StackCount != 4 {
					t.Fatalf("layer count fields = %+v", row)
				}
			},
		},
		{
			name: "stack count by handle", commandID: 29363,
			body: appendVarintField(appendVarintField(appendVarintField(appendVarintField(nil, 9, 503), 4, 5), 6, 1), 1, 1),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.HandleID == nil || *row.HandleID != 503 || row.StackCount == nil || *row.StackCount != 5 || row.TargetEntityID != nil || row.NotRefreshDuration == nil || !*row.NotRefreshDuration || row.NotRefreshPeriod == nil || !*row.NotRefreshPeriod {
					t.Fatalf("handle-based stack fields were conflated with target ID: %+v", row)
				}
			},
		},
		{
			name: "duration by handle", commandID: 16974,
			body: appendFixed32TestField(appendFixed32TestField(appendVarintField(nil, 12, 504), 2, mathFloat32bits(9.5)), 5, mathFloat32bits(2.25)),
			check: func(t *testing.T, row buffLifecycleAuditRow) {
				if row.HandleID == nil || *row.HandleID != 504 || row.DurationSeconds == nil || *row.DurationSeconds != 9.5 || row.RemainingDuration == nil || *row.RemainingDuration != 2.25 || row.TargetEntityID != nil {
					t.Fatalf("handle-based duration fields were conflated with target ID: %+v", row)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row, err := decodeBuffLifecycleMessage(gameFrame{commandID: test.commandID, direction: "in"}, test.body)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, row)
		})
	}
}

func TestEntityActiveResponseDoesNotAttachComponentsWithoutLiveRequestEntity(t *testing.T) {
	t.Run("unmatched token", func(t *testing.T) {
		p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
		monster := appendVarintField(nil, 3, 456789)
		response := appendBytesField(nil, 11, appendBytesField(nil, 18, monster))
		if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: 77}, response); err != nil {
			t.Fatal(err)
		}
		if len(p.entities.entities) != 0 {
			t.Fatalf("unmatched response created runtime entities: %+v", p.entities.entities)
		}
	})

	t.Run("request entity removed before response", func(t *testing.T) {
		p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
		const token = 78
		const entityID = 8802
		if err := p.handleActiveRequest(token, appendVarintField(nil, 4, entityID)); err != nil {
			t.Fatal(err)
		}
		remove := appendVarintField(nil, 4, 1)
		remove = appendBytesField(remove, 5, appendVarintField(nil, 1, entityID))
		if _, _, err := p.entities.applyEntityRemoveNotify(remove); err != nil {
			t.Fatal(err)
		}
		monster := appendVarintField(nil, 3, 456789)
		response := appendBytesField(nil, 11, appendBytesField(nil, 18, monster))
		if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: token}, response); err != nil {
			t.Fatal(err)
		}
		if _, ok := p.entities.entities[entityID]; ok {
			t.Fatal("late response resurrected a removed entity")
		}
	})
}

func mathFloat32bits(value float32) uint32 {
	return math.Float32bits(value)
}
