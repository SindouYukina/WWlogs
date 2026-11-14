package capture

import (
	"strings"
	"testing"
)

// Server-executed damage (DamageExecuteNotify) shares the response's accounting: these tests hold it to counting every
// confirmed local result once, never twice, and never crediting a result whose ownership is not established
// (internal/capture/damage_notify.go; research/runs/2026-11-14/v0.7.0-damage-execute-notify-client-re).

const (
	notifyTestScene    = 900 // SceneInformation.SceneEntityId of the test scene
	notifyTestDamage   = 70010001
	notifyTestHeal     = 70020001
	notifyTestHPLoss   = 70030001
	notifyTestAero     = 1001     // negative-status DamageId (AbnormalDamageConfig Abnormal1001)
	notifyTestAeroBuff = 10041000 // a status Buff whose extra effect type is 1001
)

type testNotify struct {
	attacker, target uint64
	damageID         uint64
	damage           int32
	changeLife       int32
	immune           int32
}

// notifyData is one CombatReceiveData item: CombatNotifyData{CombatCommon{EntityId: target}, DamageExecuteNotify}.
func notifyData(n testNotify) []byte {
	common := appendVarintField(nil, fieldCombatCommonEntityID, n.target)
	efn := appendVarintField(nil, fieldDamageExecuteNotifyAttackerEntityID, n.attacker)
	efn = appendVarintField(efn, fieldDamageExecuteNotifyTargetEntityID, n.target)
	efn = appendVarintField(efn, fieldDamageExecuteNotifyDamageID, n.damageID)
	efn = appendVarintField(efn, fieldDamageExecuteNotifyDamage, uint64(uint32(n.damage)))
	if n.changeLife != 0 {
		efn = appendVarintField(efn, fieldDamageExecuteNotifyChangeLife, uint64(uint32(n.changeLife)))
	}
	if n.immune != 0 {
		efn = appendVarintField(efn, fieldDamageExecuteNotifyImmuneType, uint64(n.immune))
	}
	notify := appendBytesField(nil, fieldCombatNotifyDataCombatCommon, common)
	notify = appendBytesField(notify, fieldCombatNotifyDataDamageExecuteNotify, efn)
	return appendBytesField(nil, fieldCombatReceiveDataCombatNotifyData, notify)
}

// deliverNotifyFrame hands one standalone CombatReceivePackNotify frame with the given server sequence.
func deliverNotifyFrame(t *testing.T, p *processor, seconds float64, sequence uint32, items ...[]byte) {
	t.Helper()
	pairingFrame(p, seconds)
	frame := gameFrame{direction: "in", commandID: cmdCombatReceivePackNotify, sequence: sequence, timestamp: pairingAt(seconds)}
	if err := p.handleCombatReceivePacks(frame, receivePackBody(items...)); err != nil {
		t.Fatal(err)
	}
}

// newNotifyTestProcessor is the pairing processor (local role 100 of player 77, monster 200) with a second local role
// 101, a second monster 201, the scene's combat entity, a single-player scene and the Master rows the tests use.
func newNotifyTestProcessor() *processor {
	p := newPairingTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1505)
	p.observeLocalRoleEntity(101)
	addTestRuntimeEntity(p, 201, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9002, true
	})
	p.entities.sceneCombatEntityID, p.entities.hasSceneCombatEntityID = notifyTestScene, true
	p.scenePlayerIDs = []uint64{77}
	hash := strings.Repeat("a", 64)
	p.masters.damage[notifyTestHeal] = damageMasterRecord{ID: notifyTestHeal, CalculateType: 1, SourceHash: hash, SourceTable: "damage"}
	p.masters.damage[notifyTestHPLoss] = damageMasterRecord{ID: notifyTestHPLoss, CalculateType: 2, SourceHash: hash, SourceTable: "damage"}
	p.masters.damage[notifyTestAero] = damageMasterRecord{ID: notifyTestAero, CalculateType: 0, SourceHash: hash, SourceTable: "damage"}
	p.masters.abnormalDamageIDs = map[uint64]struct{}{notifyTestAero: {}}
	p.masters.negativeStatusExecutors = map[uint64]uint64{notifyTestAeroBuff: notifyTestAero}
	return p
}

// applyStatus puts a live status instance on holder, applied by source with the given relation.
func applyStatus(p *processor, seconds float64, holder, handle, effectID, source uint64, relation EffectEntityRelation) {
	duration := 30.0
	p.effects.ObserveApply(EffectApplyEvent{
		SessionNumber: p.sessionNumber, HolderEntityID: holder, HolderGeneration: p.effectHolderGeneration(holder), HandleID: handle,
		EffectID: effectID, HasEffectID: true, SourceEntityID: source, HasSource: true,
		HolderRelation: EffectRelationNonPlayer, SourceRelation: relation, DurationSeconds: &duration, ObservedAt: pairingAt(seconds),
	})
}

func TestNotifiedLocalRoleDamageCountsOnceLikeAResponse(t *testing.T) {
	p := newNotifyTestProcessor()
	push := notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 500, changeLife: 500})
	deliverNotifyFrame(t, p, 1, 41, push)
	// The same server frame delivered again (a retransmission the transport let through, a resumed reader): same key.
	deliverNotifyFrame(t, p, 1.1, 41, push)
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 500 || snapshot.OutgoingEvents != 1 || snapshot.DuplicateEvents != 1 {
		t.Fatalf("dealt %d in %d events, duplicates %d; want 500 once and one duplicate", snapshot.DamageDealt, snapshot.OutgoingEvents, snapshot.DuplicateEvents)
	}
	if len(snapshot.Roles) != 1 || snapshot.Roles[0].RoleID != 1409 || snapshot.Roles[0].DamageDealt != 500 || snapshot.UnassignedRole.DamageDealt != 0 {
		t.Fatalf("roles %+v unassigned %+v; the notified hit must be credited to its server-named attacker role", snapshot.Roles, snapshot.UnassignedRole)
	}
	if p.notifyStats.notifies != 2 || p.notifyStats.keyless != 0 {
		t.Fatalf("notify stats %+v", p.notifyStats)
	}
}

func TestNotifiedResultEqualToAResponseIsNotADuplicate(t *testing.T) {
	// Equal values on one target close in time are common between real hits (3,854 of 8,438 counted responses in the
	// Build 31 capture); a notified result is a different server message, so value equality never merges it.
	p := newNotifyTestProcessor()
	sendDamageRequestFrom(p, 1, 1, 100, 200)
	deliverReceivePack(t, p, 1.1, inStandaloneNotify, damageResponseData(1, 800),
		notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 800, changeLife: 800}))
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 1600 || snapshot.OutgoingEvents != 2 || snapshot.DuplicateEvents != 0 {
		t.Fatalf("dealt %d in %d events, duplicates %d; want both results", snapshot.DamageDealt, snapshot.OutgoingEvents, snapshot.DuplicateEvents)
	}
}

func TestNotifiedResultsInOneFrameAndAcrossSessionsKeepTheirOwnKeys(t *testing.T) {
	p := newNotifyTestProcessor()
	first := notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 300, changeLife: 300})
	second := notifyData(testNotify{attacker: 101, target: 201, damageID: notifyTestDamage, damage: 300, changeLife: 300})
	deliverNotifyFrame(t, p, 1, 7, first, second, first) // a repeated item inside one frame is a second server result
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 900 || got.OutgoingEvents != 3 {
		t.Fatalf("one frame: dealt %d in %d events, want 900 in 3", got.DamageDealt, got.OutgoingEvents)
	}
	// A reconnect starts a new protocol session whose server sequence starts over: the same frame sequence is a new key.
	p.sessionNumber++
	deliverNotifyFrame(t, p, 2, 7, first)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 1200 || got.DuplicateEvents != 0 {
		t.Fatalf("after a new protocol session: dealt %d, duplicates %d", got.DamageDealt, got.DuplicateEvents)
	}
}

func TestNotifiedKeysNeverMeetResponseKeys(t *testing.T) {
	p := newNotifyTestProcessor()
	key := notifyEventKey(1, 0) // 65536: also a RequestId continued past one wrap
	sendDamageRequestFrom(p, 1, key, 100, 200)
	deliverReceivePack(t, p, 1.1, inStandaloneNotify, damageResponseData(key, 100))
	deliverNotifyFrame(t, p, 1.2, 1, notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 100, changeLife: 100}))
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 200 || got.DuplicateEvents != 0 {
		t.Fatalf("dealt %d, duplicates %d; a notification and a response never share a key", got.DamageDealt, got.DuplicateEvents)
	}
}

func TestNotifiedNegativeStatusDamageIsTheLocalTeamsWithoutARole(t *testing.T) {
	p := newNotifyTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3, notifyData(testNotify{attacker: notifyTestScene, target: 200, damageID: notifyTestAero, damage: 1200, changeLife: 1200}))
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 1200 || snapshot.UnassignedRole.DamageDealt != 1200 || snapshot.UnassignedRole.NegativeStatusDamage != 1200 || len(snapshot.Roles) != 0 {
		t.Fatalf("dealt %d unassigned %+v roles %+v; negative-status damage is local, unassigned and no role's", snapshot.DamageDealt, snapshot.UnassignedRole, snapshot.Roles)
	}
	completeness := intervalDamageCompleteness(snapshot)
	if !completeness.Conserved || !completeness.RoleAttributionComplete || completeness.NegativeStatusDamage != 1200 {
		t.Fatalf("completeness %+v; negative-status damage must not read as incomplete role attribution", completeness)
	}
}

func TestNotifiedNegativeStatusDamageNeedsLocalStatusEvidence(t *testing.T) {
	aero := testNotify{attacker: notifyTestScene, target: 200, damageID: notifyTestAero, damage: 700, changeLife: 700}
	cases := []struct {
		name  string
		setup func(p *processor)
	}{
		{"no live status on the target", func(p *processor) {}},
		{"status applied by an enemy", func(p *processor) { applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 201, EffectRelationNonPlayer) }},
		{"status of unknown source next to a local one", func(p *processor) {
			applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
			applyStatus(p, 0.6, 200, 2, notifyTestAeroBuff, 555, EffectRelationUnknown)
		}},
		{"another player in the scene", func(p *processor) {
			applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
			p.scenePlayerIDs = []uint64{77, 88}
		}},
		{"status on another enemy", func(p *processor) { applyStatus(p, 0.5, 201, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole) }},
		{"attacker is not the scene's combat entity", func(p *processor) {
			applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
			p.entities.sceneCombatEntityID = 901
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newNotifyTestProcessor()
			c.setup(p)
			deliverNotifyFrame(t, p, 1, 3, notifyData(aero))
			snapshot := p.damageStatistics.Snapshot()
			if snapshot.DamageDealt != 0 || snapshot.UnassignedRole.NegativeStatusDamage != 0 {
				t.Fatalf("dealt %d, unassigned %+v; without local status evidence the damage is not the local team's", snapshot.DamageDealt, snapshot.UnassignedRole)
			}
			if p.notifyStats.negativeStatus != 1 || p.notifyStats.negativeStatusLocal != 0 {
				t.Fatalf("notify stats %+v", p.notifyStats)
			}
		})
	}
}

func TestNotifiedNegativeStatusOfAnExpiredStatusIsNotCredited(t *testing.T) {
	p := newNotifyTestProcessor()
	duration := 3.0
	p.effects.ObserveApply(EffectApplyEvent{
		SessionNumber: p.sessionNumber, HolderEntityID: 200, HandleID: 1, EffectID: notifyTestAeroBuff, HasEffectID: true,
		SourceEntityID: 100, HasSource: true, HolderRelation: EffectRelationNonPlayer, SourceRelation: EffectRelationLocalRole,
		DurationSeconds: &duration, ObservedAt: pairingAt(1),
	})
	deliverNotifyFrame(t, p, 10, 3, notifyData(testNotify{attacker: notifyTestScene, target: 200, damageID: notifyTestAero, damage: 700, changeLife: 700}))
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("dealt %d from a status that ended 6 s earlier", got.DamageDealt)
	}
}

func TestNotifiedHitOnTheLocalTeamFromTheSceneIsDamageTaken(t *testing.T) {
	p := newNotifyTestProcessor()
	deliverNotifyFrame(t, p, 1, 5, notifyData(testNotify{attacker: notifyTestScene, target: 100, damageID: notifyTestAero, damage: 2085, changeLife: 2085}))
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageTaken != 2085 || snapshot.IncomingEvents != 1 || snapshot.DamageDealt != 0 {
		t.Fatalf("taken %d in %d, dealt %d; the scene entity is a non-player source", snapshot.DamageTaken, snapshot.IncomingEvents, snapshot.DamageDealt)
	}
	if len(snapshot.Roles) != 1 || snapshot.Roles[0].DamageTaken != 2085 {
		t.Fatalf("roles %+v; the hit must be taken by role 1409", snapshot.Roles)
	}
}

func TestNotifiedTeamInternalResultsFollowTheResponseRules(t *testing.T) {
	// The Build 31 capture's role 1505 lowers its own Life by 23,764 (CalculateType 2) and heals role 1508 by the same
	// amount (CalculateType 1, ChangeLife -15,334 applied): HP the team moves inside itself.
	p := newNotifyTestProcessor()
	deliverNotifyFrame(t, p, 1, 9,
		notifyData(testNotify{attacker: 101, target: 101, damageID: notifyTestHPLoss, damage: 23764, changeLife: 23764}),
		notifyData(testNotify{attacker: 101, target: 100, damageID: notifyTestHeal, damage: -23764, changeLife: -15334}))
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageTaken != 0 || snapshot.DamageDealt != 0 {
		t.Fatalf("taken %d dealt %d; an HP loss the team causes itself is neither", snapshot.DamageTaken, snapshot.DamageDealt)
	}
	if snapshot.Healing.Reported != 23764 || snapshot.Healing.Effective != 15334 || snapshot.Healing.Events != 1 {
		t.Fatalf("healing %+v; the notified heal is the team's healing", snapshot.Healing)
	}
}

func TestNotifiedResultsWithoutAnAmountAreNotCounted(t *testing.T) {
	p := newNotifyTestProcessor()
	deliverNotifyFrame(t, p, 1, 11,
		notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 0}),
		notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 400, changeLife: 400, immune: 1}),
		notifyData(testNotify{attacker: 200, target: 201, damageID: notifyTestHeal, damage: -471620, changeLife: -471620}))
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.DamageTaken != 0 || snapshot.Healing.Events != 0 {
		t.Fatalf("snapshot %+v; zero, immune and enemy-internal results count nothing", snapshot)
	}
	if p.notifyStats.noAmount != 2 {
		t.Fatalf("notify stats %+v; want two results without an amount", p.notifyStats)
	}
}

// A notified hit of the local role's summon is credited to the role through the summoner chain, as a response would be,
// and a notified hit whose attacker no message named stays of unknown ownership, outside the totals.
func TestNotifiedSummonAndUnknownAttackersFollowTheResponseRules(t *testing.T) {
	p := newNotifyTestProcessor()
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) {
		entity.summonerID, entity.hasSummonerID = 100, true
		entity.summonConfigID, entity.hasSummonConfigID = 77001, true
	})
	deliverNotifyFrame(t, p, 1, 13,
		notifyData(testNotify{attacker: 300, target: 200, damageID: notifyTestDamage, damage: 640, changeLife: 640}),
		notifyData(testNotify{attacker: 999, target: 200, damageID: notifyTestDamage, damage: 90, changeLife: 90}))
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != 640 || len(snapshot.Roles) != 1 || snapshot.Roles[0].SummonDamage != 640 || snapshot.Roles[0].RoleID != 1409 {
		t.Fatalf("dealt %d roles %+v; the summon's hit belongs to role 1409 as summon damage", snapshot.DamageDealt, snapshot.Roles)
	}
	if snapshot.UnattributedOutgoingDamage != 90 || snapshot.UnattributedOutgoingEvents != 1 {
		t.Fatalf("unattributed %d in %d; an attacker no message named is of unknown ownership", snapshot.UnattributedOutgoingDamage, snapshot.UnattributedOutgoingEvents)
	}
}
