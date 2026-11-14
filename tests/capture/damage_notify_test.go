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

// Role credit of negative-status damage (internal/capture/negative_status_credit.go;
// research/runs/2026-11-14/v0.7.0-causal-damage-attribution). The expected credits follow the rule, not the code: a
// forced execution belongs to the role whose InvokePeriod forced it, any other execution to the role owning the executing
// instance, and nothing is credited without that runtime evidence.

const (
	notifyTestSpectro     = 1005     // negative-status DamageId (AbnormalDamageConfig Abnormal1005)
	notifyTestSpectroBuff = 10051000 // a status Buff whose extra effect type is 1005
	notifyTestAeroBuff2   = 10040000 // a second Aero Erosion status Buff
	notifyTestInvokeAero  = 1409303001
	notifyTestInvokeBoth  = 900266000023
	notifyTestInvokeOther = 287110351 // an InvokePeriod naming no negative status
)

// newCreditTestProcessor adds the Master rows of the InvokePeriod Buffs and a second negative status.
func newCreditTestProcessor() *processor {
	p := newNotifyTestProcessor()
	hash := strings.Repeat("a", 64)
	p.masters.damage[notifyTestSpectro] = damageMasterRecord{ID: notifyTestSpectro, CalculateType: 0, SourceHash: hash, SourceTable: "damage"}
	p.masters.abnormalDamageIDs[notifyTestSpectro] = struct{}{}
	p.masters.negativeStatusExecutors[notifyTestSpectroBuff] = notifyTestSpectro
	p.masters.negativeStatusExecutors[notifyTestAeroBuff2] = notifyTestAero
	p.masters.invokePeriodTargets = map[uint64][]uint64{
		notifyTestInvokeAero: {notifyTestAeroBuff},
		notifyTestInvokeBoth: {notifyTestAeroBuff, notifyTestSpectroBuff},
	}
	return p
}

// triggerData is one CombatReceiveData item: the server's instant ApplyGameplayEffectNotify (handle -2) of a Buff on target.
func triggerData(target, buff, instigator uint64) []byte {
	payload := appendVarintField(nil, fieldApplyGameplayEffectNotifyHandle, uint64(0xFFFFFFFFFFFFFFFE)) // int32 -2
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyID, buff)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyEntityID, target)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyInstigatorID, instigator)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyStackCount, 1)
	notify := appendBytesField(nil, fieldCombatNotifyDataCombatCommon, appendVarintField(nil, fieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, fieldCombatNotifyDataApplyGameplayEffectNotify, payload)
	return appendBytesField(nil, fieldCombatReceiveDataCombatNotifyData, notify)
}

func aeroTick(target uint64, damage int32) []byte {
	return notifyData(testNotify{attacker: notifyTestScene, target: target, damageID: notifyTestAero, damage: damage, changeLife: damage})
}

// creditOf returns the status row of DamageId on role, and the role.
func creditOf(t *testing.T, snapshot DamageStatisticsSnapshot, roleID, damageID uint64) (RoleDamageSnapshot, StatusDamageSnapshot) {
	t.Helper()
	for _, role := range snapshot.Roles {
		if role.RoleID != roleID {
			continue
		}
		for _, row := range role.Statuses {
			if row.DamageID == damageID {
				return role, row
			}
		}
		return role, StatusDamageSnapshot{}
	}
	return RoleDamageSnapshot{}, StatusDamageSnapshot{}
}

func TestNotifiedNegativeStatusIsCreditedToTheRoleOwningTheStatus(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3, aeroTick(200, 1200))
	snapshot := p.damageStatistics.Snapshot()
	role, row := creditOf(t, snapshot, 1409, notifyTestAero)
	if snapshot.DamageDealt != 1200 || snapshot.UnassignedRole.DamageDealt != 0 || snapshot.UnassignedRole.NegativeStatusDamage != 0 {
		t.Fatalf("dealt %d unassigned %+v; the status owner's tick is the team's damage and a role's", snapshot.DamageDealt, snapshot.UnassignedRole)
	}
	if role.DamageDealt != 1200 || role.StatusDamage != 1200 || role.DirectDamage != 0 || role.SummonDamage != 0 || len(role.Skills) != 0 || role.UnassignedSkill.OutgoingEvents != 0 ||
		row != (StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 1200, OutgoingEvents: 1}) {
		t.Fatalf("role %+v; the tick is role 1409's status damage, outside its skill rows", role)
	}
	assertRoleTotalsReconcile(t, snapshot)
	completeness := intervalDamageCompleteness(snapshot)
	if !completeness.Conserved || !completeness.RoleAttributionComplete || !completeness.SkillBreakdownComplete {
		t.Fatalf("completeness %+v", completeness)
	}
	if p.notifyStats.creditedOwner != 1 || p.notifyStats.creditedTrigger != 0 || p.notifyStats.uncredited != 0 {
		t.Fatalf("notify stats %+v", p.notifyStats)
	}
}

// The case the owner named first: role A (1409) applied the status, role B (1505) forced its execution. The forced tick
// is B's; the status's own next tick is A's.
func TestNotifiedNegativeStatusForcedByAnotherRoleIsCreditedToTheTrigger(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 101), aeroTick(200, 900))
	deliverNotifyFrame(t, p, 4, 4, aeroTick(200, 400))
	snapshot := p.damageStatistics.Snapshot()
	_, forced := creditOf(t, snapshot, 1505, notifyTestAero)
	_, own := creditOf(t, snapshot, 1409, notifyTestAero)
	if forced != (StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 900, OutgoingEvents: 1, TriggerEvents: 1, TriggerDamage: 900}) {
		t.Fatalf("role 1505 status %+v; the forced tick belongs to the role whose InvokePeriod forced it", forced)
	}
	if own != (StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 400, OutgoingEvents: 1}) {
		t.Fatalf("role 1409 status %+v; the status's own tick belongs to the role that owns it", own)
	}
	if snapshot.DamageDealt != 1300 || snapshot.UnassignedRole.DamageDealt != 0 {
		t.Fatalf("dealt %d unassigned %+v; each tick counts once", snapshot.DamageDealt, snapshot.UnassignedRole)
	}
	assertRoleTotalsReconcile(t, snapshot)
}

// The server may close a receive pack right after the trigger: its forced result then opens the next pack. A trigger older
// than the carry window explains nothing as forced, but until its doubt has passed (negative_status_credit.go) the result it
// may still have forced late is not the owner's either: B forced, A owns, so the 0.2 s result credits nobody.
func TestNegativeStatusTriggerCarriesIntoTheNextPackOnly(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 101))
	deliverNotifyFrame(t, p, 1.1, 4, aeroTick(200, 900))
	deliverNotifyFrame(t, p, 2, 5, triggerData(200, notifyTestInvokeAero, 101))
	deliverNotifyFrame(t, p, 2.2, 6, aeroTick(200, 300))
	snapshot := p.damageStatistics.Snapshot()
	if _, forced := creditOf(t, snapshot, 1505, notifyTestAero); forced.DamageDealt != 900 || forced.TriggerEvents != 1 {
		t.Fatalf("role 1505 status %+v; a result in the next pack within the window is the forced one", forced)
	}
	if _, own := creditOf(t, snapshot, 1409, notifyTestAero); own.DamageDealt != 0 {
		t.Fatalf("role 1409 status %+v; a result 0.2 s after B's trigger may be B's late forced one", own)
	}
	if snapshot.UnassignedRole.NegativeStatusDamage != 300 {
		t.Fatalf("unassigned %+v; the late result stays the team's", snapshot.UnassignedRole)
	}
}

// One InvokePeriod forces one execution of each status it names: a second result on the same instance is the instance's
// own, and a trigger naming two statuses explains one result of each.
func TestNegativeStatusTriggerForcesEachNamedInstanceOnce(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	applyStatus(p, 0.5, 200, 2, notifyTestSpectroBuff, 100, EffectRelationLocalRole)
	spectro := notifyData(testNotify{attacker: notifyTestScene, target: 200, damageID: notifyTestSpectro, damage: 250, changeLife: 250})
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeBoth, 101), aeroTick(200, 900), spectro, aeroTick(200, 500))
	snapshot := p.damageStatistics.Snapshot()
	role, aero := creditOf(t, snapshot, 1505, notifyTestAero)
	_, spectroRow := creditOf(t, snapshot, 1505, notifyTestSpectro)
	if aero.DamageDealt != 900 || aero.TriggerEvents != 1 || spectroRow.DamageDealt != 250 || spectroRow.TriggerEvents != 1 || role.StatusDamage != 1150 {
		t.Fatalf("role 1505 %+v; one trigger naming two statuses forces one result of each", role)
	}
	if _, own := creditOf(t, snapshot, 1409, notifyTestAero); own.DamageDealt != 500 || own.TriggerEvents != 0 {
		t.Fatalf("role 1409 status %+v; the trigger is used up for that instance", own)
	}
	assertRoleTotalsReconcile(t, snapshot)
}

// A forced execution that deals nothing still uses up its trigger, so the next result of the instance is its own.
func TestNegativeStatusTriggerIsUsedByAResultWithoutAnAmount(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 101), aeroTick(200, 0))
	deliverNotifyFrame(t, p, 1.05, 4, aeroTick(200, 600))
	snapshot := p.damageStatistics.Snapshot()
	if _, own := creditOf(t, snapshot, 1409, notifyTestAero); own.DamageDealt != 600 {
		t.Fatalf("role 1409 status %+v; the zero result used the trigger", own)
	}
	if role, _ := creditOf(t, snapshot, 1505, notifyTestAero); role.StatusDamage != 0 {
		t.Fatalf("role 1505 %+v; nothing was forced that dealt damage", role)
	}
}

// Without one owner and without a trigger, or with a trigger of no local role, a tick stays the local team's but no role's.
func TestNegativeStatusWithoutOneLocalCreditStaysUnassigned(t *testing.T) {
	cases := []struct {
		name  string
		setup func(p *processor) [][]byte
		want  string
	}{
		{"two live statuses of different owners", func(p *processor) [][]byte {
			applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
			applyStatus(p, 0.6, 200, 2, notifyTestAeroBuff2, 101, EffectRelationLocalRole)
			return [][]byte{aeroTick(200, 700)}
		}, "the live status instances on the target have different owners"},
		{"a trigger of an enemy", func(p *processor) [][]byte {
			applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
			return [][]byte{triggerData(200, notifyTestInvokeAero, 201), aeroTick(200, 700)}
		}, "the credited entity is no local role or summon of one"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newCreditTestProcessor()
			deliverNotifyFrame(t, p, 1, 3, c.setup(p)...)
			snapshot := p.damageStatistics.Snapshot()
			if snapshot.DamageDealt != 700 || snapshot.UnassignedRole.NegativeStatusDamage != 700 || snapshot.UnassignedRole.DamageDealt != 700 {
				t.Fatalf("dealt %d unassigned %+v; the tick is the team's and no role's", snapshot.DamageDealt, snapshot.UnassignedRole)
			}
			for _, role := range snapshot.Roles {
				if role.StatusDamage != 0 {
					t.Fatalf("role %d credited %d without evidence", role.RoleID, role.StatusDamage)
				}
			}
			if completeness := intervalDamageCompleteness(snapshot); completeness.RoleAttributionComplete || !completeness.Conserved {
				t.Fatalf("completeness %+v; unassigned negative-status damage is an attribution gap", completeness)
			}
			if p.notifyStats.uncredited != 1 {
				t.Fatalf("notify stats %+v", p.notifyStats)
			}
		})
	}
}

// A trigger naming the other of two differently owned statuses forces that one, but the other instance can have executed
// this result on its own timer just as well (the two executions could swap), so with different owners nothing is credited
// (corrected 2026-11-14; Build 38 credited the forcer).
func TestNegativeStatusTriggerNamesTheInstanceAmongDifferentOwners(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	applyStatus(p, 0.6, 200, 2, notifyTestAeroBuff2, 101, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 100), aeroTick(200, 800))
	snapshot := p.damageStatistics.Snapshot()
	if _, row := creditOf(t, snapshot, 1409, notifyTestAero); row.DamageDealt != 0 || snapshot.UnassignedRole.NegativeStatusDamage != 800 {
		t.Fatalf("role 1409 status %+v unassigned %+v", row, snapshot.UnassignedRole)
	}
}

// An InvokePeriod that names no negative status, or names one not on the target, forces nothing.
func TestNegativeStatusTriggerOfAnotherStatusIsNotUsed(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeOther, 101), triggerData(201, notifyTestInvokeAero, 101), aeroTick(200, 500))
	snapshot := p.damageStatistics.Snapshot()
	if _, own := creditOf(t, snapshot, 1409, notifyTestAero); own.DamageDealt != 500 || own.TriggerEvents != 0 {
		t.Fatalf("role 1409 status %+v; neither trigger forces this status on this target", own)
	}
	if p.notifyStats.statusTriggers != 1 {
		t.Fatalf("notify stats %+v; only the InvokePeriod that names a negative status is remembered", p.notifyStats)
	}
}

// A status a summon applied is its owning role's, as the summon's own hits are, but as status damage, not summon damage.
func TestNegativeStatusOfASummonIsCreditedToItsRole(t *testing.T) {
	p := newCreditTestProcessor()
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) {
		entity.summonerID, entity.hasSummonerID = 100, true
		entity.summonConfigID, entity.hasSummonConfigID = 77001, true
	})
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 300, EffectRelationLocalOwned)
	deliverNotifyFrame(t, p, 1, 3, aeroTick(200, 650))
	snapshot := p.damageStatistics.Snapshot()
	role, row := creditOf(t, snapshot, 1409, notifyTestAero)
	if row.DamageDealt != 650 || role.StatusDamage != 650 || role.SummonDamage != 0 || role.DirectDamage != 0 {
		t.Fatalf("role %+v; the summon's status is role 1409's status damage", role)
	}
}

// A status keeps ticking while another role fights: who is acting when the tick lands is no evidence, so the tick stays
// with the role that owns the status.
func TestNegativeStatusTickWhileAnotherRoleFightsStaysWithItsOwner(t *testing.T) {
	p := newCreditTestProcessor()
	applyStatus(p, 0.5, 200, 1, notifyTestAeroBuff, 100, EffectRelationLocalRole)
	deliverNotifyFrame(t, p, 1, 3,
		notifyData(testNotify{attacker: 101, target: 200, damageID: notifyTestDamage, damage: 2000, changeLife: 2000}), aeroTick(200, 450))
	deliverNotifyFrame(t, p, 4, 4, notifyData(testNotify{attacker: 101, target: 200, damageID: notifyTestDamage, damage: 2000, changeLife: 2000}))
	deliverNotifyFrame(t, p, 4.01, 5, aeroTick(200, 450))
	snapshot := p.damageStatistics.Snapshot()
	if _, own := creditOf(t, snapshot, 1409, notifyTestAero); own.DamageDealt != 900 || own.OutgoingEvents != 2 {
		t.Fatalf("role 1409 status %+v; the owner keeps its ticks", own)
	}
	if role, _ := creditOf(t, snapshot, 1505, notifyTestAero); role.StatusDamage != 0 || role.DirectDamage != 4000 {
		t.Fatalf("role 1505 %+v; fighting next to a tick is not causing it", role)
	}
	assertRoleTotalsReconcile(t, snapshot)
}
