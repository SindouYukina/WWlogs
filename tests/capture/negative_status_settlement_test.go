package capture

import (
	"database/sql"
	"strings"
	"testing"
)

// Adversarial tests of the role credit of negative-status damage (internal/capture/negative_status_credit.go;
// research/runs/2026-11-14/v0.7.0-anomaly-attribution-verification). The expected credits follow the settlement rules
// and the client mechanism, never the code's output:
//
//   - an InvokePeriod forces only the named statuses present when the server applies it (InvokePeriod.OnExecute), and
//     its forced execution is credited to its instigator;
//   - a timed status (buff.period > 0) executes on its own timer and on creation: credited to the instance owner;
//   - a status without a period settles on an application (Glacio Chafe on every application, Fusion Burst on the
//     overflow that also clears it): credited only when every applier is the same entity;
//   - whenever the evidence leaves two entities possible, nothing is credited, and the damage stays the team's.
//
// Roles: A is entity 100 (role 1409), B is entity 101 (role 1505), C is entity 102 (role 1608), all of local player 77.
// Enemies 200 and 201. The scene's combat entity 900 executes every status.

const (
	settleGlacio      = 1003     // negative-status DamageId without a period (applied)
	settleGlacioBuff  = 10011000 // its status Buff
	settleFusion      = 1004
	settleFusionBuff  = 10021000
	settleHavoc       = 1006 // DEF reduction, no damage per the client glossary
	settleHavocBuff   = 10061000
	settleGlacioNew   = 1007     // the FormulaType 9 Glacio Chafe variant
	settleGlacioNewBf = 10011100 // replaces 10011000 for some appliers
)

// newSettlementTestProcessor is the credit test processor with a third local role, the statuses without a period and the
// settlement of every status Buff the tests use.
func newSettlementTestProcessor() *Processor {
	p := newCreditTestProcessor()
	addTestPlayerRoleGroupEntity(p, 102, 77, 1608)
	p.ObserveLocalRoleEntity(102)
	hash := strings.Repeat("a", 64)
	for _, damageID := range []uint64{settleGlacio, settleFusion, settleHavoc, settleGlacioNew} {
		p.Masters.Damage[damageID] = DamageMasterRecord{ID: damageID, CalculateType: 0, SourceHash: hash, SourceTable: "damage"}
		p.Masters.AbnormalDamageIDs[damageID] = struct{}{}
	}
	p.Masters.NegativeStatusExecutors[settleGlacioBuff] = settleGlacio
	p.Masters.NegativeStatusExecutors[settleFusionBuff] = settleFusion
	p.Masters.NegativeStatusExecutors[settleHavocBuff] = settleHavoc
	p.Masters.NegativeStatusExecutors[settleGlacioNewBf] = settleGlacioNew
	p.Masters.NegativeStatusSettlements = map[uint64]NegativeStatusSettlementRule{
		notifyTestAeroBuff:    {Known: true, Periodic: true, ExecutesOnCreation: true},
		notifyTestAeroBuff2:   {Known: true, Periodic: true, ExecutesOnCreation: true},
		notifyTestSpectroBuff: {Known: true, Periodic: true},
		settleGlacioBuff:      {Known: true, ExecutesOnCreation: true, ClearsOnOverflow: true},
		settleFusionBuff:      {Known: true, ExecutesOnCreation: true, ClearsOnOverflow: true},
		settleHavocBuff:       {Known: true, ExecutesOnCreation: true},
		settleGlacioNewBf:     {Known: true, ExecutesOnCreation: true},
	}
	return p
}

// statusApplyItem is the server's ApplyGameplayEffectNotify of a status instance (positive handle) on target.
func statusApplyItem(target, handle, buff, instigator, stack uint64) []byte {
	payload := appendVarintField(nil, FieldApplyGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyID, buff)
	payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyEntityID, target)
	if instigator != 0 {
		payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyInstigatorID, instigator)
	}
	payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyStackCount, stack)
	notify := appendBytesField(nil, FieldCombatNotifyDataCombatCommon, appendVarintField(nil, FieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, FieldCombatNotifyDataApplyGameplayEffectNotify, payload)
	return appendBytesField(nil, FieldCombatReceiveDataCombatNotifyData, notify)
}

// statusStackItem is the server's BuffStackCountNotify of one stack application by instigator.
func statusStackItem(target, handle, stack, instigator uint64) []byte {
	payload := appendVarintField(nil, FieldBuffStackCountNotifyHandleID, handle)
	payload = appendVarintField(payload, FieldBuffStackCountNotifyNewStackCount, stack)
	payload = appendVarintField(payload, FieldBuffStackCountNotifyInstigatorID, instigator)
	notify := appendBytesField(nil, FieldCombatNotifyDataCombatCommon, appendVarintField(nil, FieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, FieldCombatNotifyDataBuffStackCountNotify, payload)
	return appendBytesField(nil, FieldCombatReceiveDataCombatNotifyData, notify)
}

// statusRemoveItem is the server's RemoveGameplayEffectNotify of a status instance.
func statusRemoveItem(target, handle uint64) []byte {
	payload := appendVarintField(nil, FieldRemoveGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, FieldRemoveGameplayEffectNotifyEntityID, target)
	notify := appendBytesField(nil, FieldCombatNotifyDataCombatCommon, appendVarintField(nil, FieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, FieldCombatNotifyDataRemoveGameplayEffectNotify, payload)
	return appendBytesField(nil, FieldCombatReceiveDataCombatNotifyData, notify)
}

func statusTick(target, damageID uint64, damage int32) []byte {
	return notifyData(testNotify{attacker: notifyTestScene, target: target, damageID: damageID, damage: damage, changeLife: damage})
}

// deliverServerFrame hands one standalone server frame through the server-sequence check first, as handleFrame does.
func deliverServerFrame(t *testing.T, p *Processor, seconds float64, sequence uint32, items ...[]byte) {
	t.Helper()
	pairingFrame(p, seconds)
	frame := GameFrame{Direction: "in", CommandID: CmdCombatReceivePackNotify, Sequence: sequence, Timestamp: pairingAt(seconds)}
	p.observeServerSequence(frame)
	if err := p.HandleCombatReceivePacks(frame, receivePackBody(items...)); err != nil {
		t.Fatal(err)
	}
}

// statusRow returns the status row of DamageId credited to role, zero when the role has none.
func statusRow(t *testing.T, p *Processor, roleID, damageID uint64) StatusDamageSnapshot {
	t.Helper()
	_, row := creditOf(t, p.DamageStatistics.Snapshot(), roleID, damageID)
	return row
}

// assertUnassignedStatus checks that damage of the local team stays credited to no role.
func assertUnassignedStatus(t *testing.T, p *Processor, want int64) {
	t.Helper()
	snapshot := p.DamageStatistics.Snapshot()
	if snapshot.UnassignedRole.NegativeStatusDamage != want {
		t.Fatalf("unassigned negative-status damage %d, want %d (roles %+v)", snapshot.UnassignedRole.NegativeStatusDamage, want, snapshot.Roles)
	}
	assertRoleTotalsReconcile(t, snapshot)
}

// The corpus case the Build 38 rule got wrong: an InvokePeriod applied before the status existed forces nothing, and the
// new instance's on-creation execution belongs to its creator, also when another role applied the InvokePeriod.
func TestInvokePeriodBeforeTheStatusExistsForcesNothing(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 101), statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, p, 1.06, 4, aeroTick(200, 650))
	if row := statusRow(t, p, 1409, notifyTestAero); row != (StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 650, OutgoingEvents: 1}) {
		t.Fatalf("role 1409 status %+v; the on-creation execution is the creator's own", row)
	}
	if row := statusRow(t, p, 1505, notifyTestAero); row.DamageDealt != 0 {
		t.Fatalf("role 1505 status %+v; an InvokePeriod that found no status forced nothing", row)
	}
	if p.NotifyStats.StatusTriggersForcingNothing != 1 || p.NotifyStats.CreditedTrigger != 0 {
		t.Fatalf("notify stats %+v", p.NotifyStats)
	}
	assertUnassignedStatus(t, p, 0)
}

// The owner's cross-character table: who owns the status and who forced it decide the credit.
func TestNegativeStatusCreditFollowsOwnerAndTrigger(t *testing.T) {
	cases := []struct {
		name           string
		owner, trigger uint64 // entities; 0 owner names no instigator, 0 trigger is no InvokePeriod
		anonymous      bool   // the InvokePeriod names no instigator
		wantRole       uint64
		wantTrigger    bool
	}{
		{name: "A owns, A forces", owner: 100, trigger: 100, wantRole: 1409, wantTrigger: true},
		{name: "A owns, B forces", owner: 100, trigger: 101, wantRole: 1505, wantTrigger: true},
		{name: "A owns, own tick", owner: 100, wantRole: 1409},
		{name: "B owns, A forces", owner: 101, trigger: 100, wantRole: 1409, wantTrigger: true},
		{name: "unknown owner, B forces", owner: 0, trigger: 101, wantRole: 1505, wantTrigger: true},
		{name: "A owns, an InvokePeriod without instigator forces", owner: 100, trigger: 0, anonymous: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newSettlementTestProcessor()
			deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, c.owner, 1))
			deliverNotifyFrame(t, p, 0.55, 3, aeroTick(200, 100)) // the on-creation execution
			items := [][]byte{}
			switch {
			case c.anonymous:
				items = append(items, triggerData(200, notifyTestInvokeAero, 0))
			case c.trigger != 0:
				items = append(items, triggerData(200, notifyTestInvokeAero, c.trigger))
			}
			deliverNotifyFrame(t, p, 2, 4, append(items, aeroTick(200, 900))...)
			snapshot := p.DamageStatistics.Snapshot()
			var credited int64
			for _, role := range snapshot.Roles {
				for _, row := range role.Statuses {
					if row.DamageID == notifyTestAero && row.DamageDealt >= 900 {
						if role.RoleID != c.wantRole {
							t.Fatalf("the 900 execution was credited to role %d, want %d", role.RoleID, c.wantRole)
						}
						credited = row.DamageDealt
						if c.wantTrigger && (row.TriggerEvents != 1 || row.TriggerDamage != 900) {
							t.Fatalf("role %d status %+v; the forced execution is its trigger part", role.RoleID, row)
						}
					}
				}
			}
			if c.wantRole == 0 {
				if snapshot.UnassignedRole.NegativeStatusDamage < 900 {
					t.Fatalf("unassigned %+v; a forced execution of no known instigator credits nobody", snapshot.UnassignedRole)
				}
			} else if credited < 900 {
				t.Fatalf("roles %+v unassigned %+v; want the 900 execution on role %d", snapshot.Roles, snapshot.UnassignedRole, c.wantRole)
			}
			assertRoleTotalsReconcile(t, snapshot)
		})
	}
}

// Swapping the entities of A and B swaps the credits: nothing in the rule depends on a role id, the active role or a
// fixed entity.
func TestNegativeStatusCreditFollowsTheEntitiesWhenTheyAreSwapped(t *testing.T) {
	run := func(owner, forcer uint64) (ownerRole, forcerRole StatusDamageSnapshot, p *Processor) {
		p = newSettlementTestProcessor()
		deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, owner, 1))
		deliverNotifyFrame(t, p, 0.55, 3, aeroTick(200, 111))
		deliverNotifyFrame(t, p, 2, 4, triggerData(200, notifyTestInvokeAero, forcer), aeroTick(200, 900))
		deliverNotifyFrame(t, p, 3.55, 5, aeroTick(200, 333))
		roleOf := map[uint64]uint64{100: 1409, 101: 1505}
		return statusRow(t, p, roleOf[owner], notifyTestAero), statusRow(t, p, roleOf[forcer], notifyTestAero), p
	}
	for _, order := range [][2]uint64{{100, 101}, {101, 100}} {
		ownerRow, forcerRow, _ := run(order[0], order[1])
		if ownerRow != (StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 444, OutgoingEvents: 2}) {
			t.Fatalf("owner %d status %+v; its own executions are its", order[0], ownerRow)
		}
		if forcerRow != (StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 900, OutgoingEvents: 1, TriggerEvents: 1, TriggerDamage: 900}) {
			t.Fatalf("forcer %d status %+v; the forced execution is its", order[1], forcerRow)
		}
	}
}

// InvokePeriods of two roles pending for one instance at once: which forced which execution cannot be told, so neither
// execution is credited; InvokePeriods of one role are.
func TestConcurrentInvokePeriodsOfDifferentRolesCreditNobody(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 101), triggerData(200, notifyTestInvokeAero, 102))
	deliverNotifyFrame(t, p, 1.03, 4, aeroTick(200, 900))
	deliverNotifyFrame(t, p, 1.05, 5, aeroTick(200, 800))
	for _, role := range []uint64{1505, 1608} {
		if row := statusRow(t, p, role, notifyTestAero); row.DamageDealt != 0 {
			t.Fatalf("role %d status %+v; neither forced execution can be told apart", role, row)
		}
	}
	assertUnassignedStatus(t, p, 1700)

	same := newSettlementTestProcessor()
	deliverNotifyFrame(t, same, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, same, 1, 3, triggerData(200, notifyTestInvokeAero, 101), triggerData(200, notifyTestInvokeAero, 101))
	deliverNotifyFrame(t, same, 1.03, 4, aeroTick(200, 900))
	deliverNotifyFrame(t, same, 1.05, 5, aeroTick(200, 800))
	if row := statusRow(t, same, 1505, notifyTestAero); row.DamageDealt != 1700 || row.TriggerEvents != 2 {
		t.Fatalf("role 1505 status %+v; two InvokePeriods of one role force two executions of that role", row)
	}
	assertUnassignedStatus(t, same, 0)
}

// A forced execution that arrives after the carry window may still be the forced one: until the InvokePeriod's doubt has
// passed, the instance's executions credit nobody when the forcer and the owner differ.
func TestLateForcedResultIsNotGivenToTheOwner(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, p, 2, 3, triggerData(200, notifyTestInvokeAero, 101))
	deliverNotifyFrame(t, p, 2.3, 4, aeroTick(200, 900)) // 0.3 s later: late forced result or the owner's tick
	deliverNotifyFrame(t, p, 3.2, 5, aeroTick(200, 500)) // the InvokePeriod's doubt has passed
	if row := statusRow(t, p, 1505, notifyTestAero); row.DamageDealt != 0 {
		t.Fatalf("role 1505 status %+v; the late result is not proven forced", row)
	}
	if row := statusRow(t, p, 1409, notifyTestAero); row != (StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 500, OutgoingEvents: 1}) {
		t.Fatalf("role 1409 status %+v; only the execution after the doubt is the owner's", row)
	}
	assertUnassignedStatus(t, p, 900)

	// The same with the owner forcing its own status: every possible actor is A, so A is credited at once.
	own := newSettlementTestProcessor()
	deliverNotifyFrame(t, own, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, own, 2, 3, triggerData(200, notifyTestInvokeAero, 100))
	deliverNotifyFrame(t, own, 2.3, 4, aeroTick(200, 900))
	if row := statusRow(t, own, 1409, notifyTestAero); row.DamageDealt != 900 {
		t.Fatalf("role 1409 status %+v; A is the only possible actor", row)
	}
}

// A forcing InvokePeriod lost with missing server frames must not turn its forced result into the owner's: after a gap
// of the server's frame sequence a result of a forcible status credits nobody until the doubt has passed.
func TestMissingServerFramesKeepTheOwnerFromTakingAPossiblyForcedResult(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverServerFrame(t, p, 0.5, 10, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverServerFrame(t, p, 0.55, 11, aeroTick(200, 100))
	// Frames 12 and 13 are missing: one of them may have carried B's InvokePeriod.
	deliverServerFrame(t, p, 2.0, 14, aeroTick(200, 900))
	deliverServerFrame(t, p, 3.5, 15, aeroTick(200, 500))
	if row := statusRow(t, p, 1409, notifyTestAero); row.DamageDealt != 600 || row.OutgoingEvents != 2 {
		t.Fatalf("role 1409 status %+v; the result right after the gap is not proven the owner's", row)
	}
	assertUnassignedStatus(t, p, 900)
	if p.ServerSequence.Total != 1 {
		t.Fatalf("server sequence gaps %d, want 1", p.ServerSequence.Total)
	}

	complete := newSettlementTestProcessor()
	deliverServerFrame(t, complete, 0.5, 10, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverServerFrame(t, complete, 0.55, 11, aeroTick(200, 100))
	deliverServerFrame(t, complete, 2.0, 12, aeroTick(200, 900))
	if row := statusRow(t, complete, 1409, notifyTestAero); row.DamageDealt != 1000 {
		t.Fatalf("role 1409 status %+v; with every server frame present the tick is the owner's", row)
	}
	// A status without a period settles on an application. When no application of the result's moment explains it, every
	// applier of the instance is a possible cause, and a gap since the instance's creation may have carried the
	// application of another entity: the result stays the team's, credited to nobody.
	glacio := newSettlementTestProcessor()
	deliverServerFrame(t, glacio, 0.5, 10, statusApplyItem(200, 8, settleGlacioBuff, 100, 1))
	deliverServerFrame(t, glacio, 2.0, 14, statusTick(200, settleGlacio, 300))
	if row := statusRow(t, glacio, 1409, settleGlacio); row.DamageDealt != 0 {
		t.Fatalf("role 1409 Glacio Chafe %+v; a lost frame may have carried another entity's application", row)
	}
	assertUnassignedStatus(t, glacio, 300)
	// With every server frame present, A is the only applier the instance ever had.
	glacioComplete := newSettlementTestProcessor()
	deliverServerFrame(t, glacioComplete, 0.5, 10, statusApplyItem(200, 8, settleGlacioBuff, 100, 1))
	deliverServerFrame(t, glacioComplete, 2.0, 11, statusTick(200, settleGlacio, 300))
	if row := statusRow(t, glacioComplete, 1409, settleGlacio); row.DamageDealt != 300 {
		t.Fatalf("role 1409 Glacio Chafe %+v; A applied every stack and no frame is missing", row)
	}
}

// A new protocol session starts over: an InvokePeriod of the earlier one forces nothing now.
func TestInvokePeriodOfAnEarlierSessionForcesNothing(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 101))
	p.SessionNumber++ // reconnect: a new protocol session
	deliverNotifyFrame(t, p, 1.05, 4, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, p, 1.08, 5, aeroTick(200, 400))
	if row := statusRow(t, p, 1505, notifyTestAero); row.DamageDealt != 0 {
		t.Fatalf("role 1505 status %+v; an InvokePeriod of an earlier session forces nothing", row)
	}
	if row := statusRow(t, p, 1409, notifyTestAero); row.DamageDealt != 400 {
		t.Fatalf("role 1409 status %+v", row)
	}
}

// The lifecycle of the executing instance decides its owner: a removed instance's owner does not pass to the instance
// another role creates after it, and an InvokePeriod that found the old instance does not force the new one.
func TestNegativeStatusOwnerFollowsTheInstanceLifecycle(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, p, 0.55, 3, aeroTick(200, 100))
	deliverNotifyFrame(t, p, 1, 4, triggerData(200, notifyTestInvokeAero, 102), aeroTick(200, 200)) // forced by C
	deliverNotifyFrame(t, p, 2, 5, statusRemoveItem(200, 7))
	deliverNotifyFrame(t, p, 3, 6, statusApplyItem(200, 9, notifyTestAeroBuff, 101, 1)) // B reapplies: a new instance
	deliverNotifyFrame(t, p, 3.05, 7, aeroTick(200, 300))
	deliverNotifyFrame(t, p, 6.05, 8, aeroTick(200, 400))
	if row := statusRow(t, p, 1409, notifyTestAero); row.DamageDealt != 100 {
		t.Fatalf("role 1409 status %+v; A owns only the first instance", row)
	}
	if row := statusRow(t, p, 1608, notifyTestAero); row.DamageDealt != 200 || row.TriggerEvents != 1 {
		t.Fatalf("role 1608 status %+v; C forced one execution of the first instance", row)
	}
	if row := statusRow(t, p, 1505, notifyTestAero); row.DamageDealt != 700 || row.TriggerEvents != 0 {
		t.Fatalf("role 1505 status %+v; the second instance is B's", row)
	}
	assertUnassignedStatus(t, p, 0)
}

// Several roles stacking a timed status: the executions are still the instance's own, so the owner's. Several roles
// applying a status without a period: the result is caused by the application the server reported in its moment (the
// server reports a cause before its effect, and Glacio Chafe settles on every application), so each result is credited
// to the role whose application set it off, as an action of that role.
func TestStackingByAnotherRoleDependsOnTheSettlement(t *testing.T) {
	timed := newSettlementTestProcessor()
	deliverNotifyFrame(t, timed, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, timed, 0.55, 3, aeroTick(200, 100))
	deliverNotifyFrame(t, timed, 1, 4, statusStackItem(200, 7, 2, 101), statusStackItem(200, 7, 3, 102))
	deliverNotifyFrame(t, timed, 3.55, 5, aeroTick(200, 900))
	if row := statusRow(t, timed, 1409, notifyTestAero); row.DamageDealt != 1000 {
		t.Fatalf("role 1409 status %+v; the timed status's own ticks are its owner's whoever stacked it", row)
	}
	assertUnassignedStatus(t, timed, 0)

	applied := newSettlementTestProcessor()
	deliverNotifyFrame(t, applied, 0.5, 2, statusApplyItem(200, 8, settleGlacioBuff, 100, 1), statusTick(200, settleGlacio, 120))
	deliverNotifyFrame(t, applied, 1, 3, statusStackItem(200, 8, 2, 100), statusTick(200, settleGlacio, 150))
	deliverNotifyFrame(t, applied, 1.5, 4, statusStackItem(200, 8, 3, 101), statusTick(200, settleGlacio, 180))
	if row := statusRow(t, applied, 1409, settleGlacio); row != (StatusDamageSnapshot{DamageID: settleGlacio, DamageDealt: 270, OutgoingEvents: 2, TriggerEvents: 2, TriggerDamage: 270}) {
		t.Fatalf("role 1409 Glacio Chafe %+v; A's two applications set off the first two results", row)
	}
	if row := statusRow(t, applied, 1505, settleGlacio); row != (StatusDamageSnapshot{DamageID: settleGlacio, DamageDealt: 180, OutgoingEvents: 1, TriggerEvents: 1, TriggerDamage: 180}) {
		t.Fatalf("role 1505 Glacio Chafe %+v; B's application is the only one in the third result's moment", row)
	}
	assertUnassignedStatus(t, applied, 0)
	if applied.NotifyStats.SettledByApplication != 3 {
		t.Fatalf("notify stats %+v", applied.NotifyStats)
	}
}

// Fusion Burst bursts on the application that overflows its stack limit, which also clears the instance: the server may
// report the removal before the burst. The burst is still the local team's damage, credited to the role whose
// application overflowed the instance (a stack report that keeps the count at the limit), whoever applied the rest.
func TestOverflowBurstAfterItsInstanceWasRemoved(t *testing.T) {
	burst := func(overflowBy uint64) *Processor {
		p := newSettlementTestProcessor()
		deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 8, settleFusionBuff, 100, 9))
		deliverNotifyFrame(t, p, 1, 3, statusStackItem(200, 8, 10, 100))
		deliverNotifyFrame(t, p, 1.5, 4, statusStackItem(200, 8, 10, overflowBy), statusRemoveItem(200, 8))
		deliverNotifyFrame(t, p, 1.52, 5, statusTick(200, settleFusion, 5000))
		return p
	}
	single := burst(100)
	if row := statusRow(t, single, 1409, settleFusion); row.DamageDealt != 5000 {
		t.Fatalf("role 1409 Fusion Burst %+v; A applied every stack, so the burst is A's", row)
	}
	if got := single.DamageStatistics.Snapshot().DamageDealt; got != 5000 {
		t.Fatalf("dealt %d; the burst after the instance's removal is the team's damage", got)
	}
	other := burst(101)
	if got := other.DamageStatistics.Snapshot().DamageDealt; got != 5000 {
		t.Fatalf("dealt %d; the burst is the team's damage whoever caused it", got)
	}
	if row := statusRow(t, other, 1505, settleFusion); row != (StatusDamageSnapshot{DamageID: settleFusion, DamageDealt: 5000, OutgoingEvents: 1, TriggerEvents: 1, TriggerDamage: 5000}) {
		t.Fatalf("role 1505 Fusion Burst %+v; B's application overflowed the instance A built", row)
	}
	assertUnassignedStatus(t, other, 0)
	// Long after the removal the instance can no longer be the executing one.
	late := newSettlementTestProcessor()
	deliverNotifyFrame(t, late, 0.5, 2, statusApplyItem(200, 8, settleFusionBuff, 100, 9), statusRemoveItem(200, 8))
	deliverNotifyFrame(t, late, 1.5, 3, statusTick(200, settleFusion, 5000))
	if got := late.DamageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("dealt %d; a status removed 1 s earlier is no evidence", got.DamageDealt)
	}
}

// A status's presence is not damage: Havoc Bane lowers DEF and has no damage settlement, and a result of another status
// is never explained by it. Nothing is counted without a server result.
func TestANonDamageStatusIsNeverDamage(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 8, settleHavocBuff, 100, 3))
	deliverNotifyFrame(t, p, 1, 3, aeroTick(200, 900)) // an Aero Erosion result with no Aero Erosion on the target
	if snapshot := p.DamageStatistics.Snapshot(); snapshot.DamageDealt != 0 || snapshot.UnassignedRole.NegativeStatusDamage != 0 {
		t.Fatalf("dealt %d unassigned %+v; Havoc Bane does not execute Aero Erosion", snapshot.DamageDealt, snapshot.UnassignedRole)
	}
	if p.NotifyStats.NegativeStatusLocal != 0 {
		t.Fatalf("notify stats %+v", p.NotifyStats)
	}
}

// The Glacio Chafe variant 10011100 executes DamageId 1007, a FormulaType 9 row AbnormalDamageConfig has no column for.
func TestTheGlacioChafeVariantIsANegativeStatus(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 8, settleGlacioNewBf, 100, 1), statusTick(200, settleGlacioNew, 210))
	if row := statusRow(t, p, 1409, settleGlacioNew); row.DamageDealt != 210 {
		t.Fatalf("role 1409 status %+v; 1007 is the variant's negative-status damage", row)
	}
}

// Another instance on the target that can have executed the result on its own timer keeps a forced execution from being
// credited when its owner is not the forcer: the two executions could have swapped.
func TestForcedExecutionNextToAnotherOwnersInstance(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1), statusApplyItem(200, 8, notifyTestAeroBuff2, 101, 1))
	deliverNotifyFrame(t, p, 1, 3, triggerData(200, notifyTestInvokeAero, 100), aeroTick(200, 800))
	assertUnassignedStatus(t, p, 800)

	same := newSettlementTestProcessor()
	deliverNotifyFrame(t, same, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1), statusApplyItem(200, 8, notifyTestAeroBuff2, 100, 1))
	deliverNotifyFrame(t, same, 1, 3, triggerData(200, notifyTestInvokeAero, 100), aeroTick(200, 800))
	if row := statusRow(t, same, 1409, notifyTestAero); row.DamageDealt != 800 || row.TriggerEvents != 1 {
		t.Fatalf("role 1409 status %+v; every possible actor is A", row)
	}
}

// A result whose status instance the server reports only after it is no evidence of ownership: it stays out of the local
// totals (unknown ownership), and nothing is credited by guess.
func TestResultBeforeItsStatusInstanceIsNotCredited(t *testing.T) {
	p := newSettlementTestProcessor()
	deliverNotifyFrame(t, p, 1, 2, aeroTick(200, 400), statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	if snapshot := p.DamageStatistics.Snapshot(); snapshot.DamageDealt != 0 || len(snapshot.Roles) != 0 {
		t.Fatalf("dealt %d roles %+v; without a status instance at the result nothing is the team's", snapshot.DamageDealt, snapshot.Roles)
	}
}

// The Master gives the negative-status DamageIds (AbnormalDamageConfig columns and FormulaType 9 rows), their status Buffs
// with how each settles, and the InvokePeriods that force them.
func TestLoadGameMasterDatabaseReadsNegativeStatusSettlement(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"ALTER TABLE damage ADD COLUMN formula_type INTEGER",
		"INSERT INTO damage VALUES('1001',0,9),('1007',0,9),('1508007001',0,0)",
		"CREATE TABLE abnormaldamageconfig(level INTEGER PRIMARY KEY,abnormal1001 INTEGER)",
		"INSERT INTO abnormaldamageconfig VALUES(1,11)",
		"CREATE TABLE buff(id TEXT PRIMARY KEY,extra_effect_id TEXT,period INTEGER,b_execute_periodic_effect_on_application INTEGER,b_clear_stack_on_overflow INTEGER)",
		// 1008 has no AbnormalDamageConfig column (the Master's columns are Abnormal1001 to Abnormal1006) and no FormulaType 9.
		"INSERT INTO buff VALUES('10041000','1001',3,1,0),('10011100','1007',0,1,0),('10021000','1008',0,1,1),('1409303001','67',0,1,0),('1508300100','0',0,1,0)",
		"CREATE TABLE buff_extra_effect_parameters(owner_id TEXT,ordinal INTEGER,value TEXT)",
		"INSERT INTO buff_extra_effect_parameters VALUES('1409303001',0,'10041000#10011100#abc')",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	completeMasterFixture(t, path)
	master, err := LoadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1001, 1007} {
		if !master.isNegativeStatusDamage(id) {
			t.Fatalf("DamageId %d is a negative status", id)
		}
	}
	if master.isNegativeStatusDamage(1008) || master.isNegativeStatusDamage(1508007001) {
		t.Fatal("a DamageId with neither an AbnormalDamageConfig column nor FormulaType 9 is no negative status")
	}
	if !master.negativeStatusExecutes(10041000, 1001) || !master.negativeStatusExecutes(10011100, 1007) || master.negativeStatusExecutes(10021000, 1008) {
		t.Fatal("status executors are the Buffs whose extra effect is a negative-status DamageId")
	}
	if got := master.negativeStatusSettlementOf(10041000); got != (NegativeStatusSettlementRule{Known: true, Periodic: true, ExecutesOnCreation: true}) {
		t.Fatalf("Aero Erosion settlement %+v", got)
	}
	if got := master.negativeStatusSettlementOf(10011100); got != (NegativeStatusSettlementRule{Known: true, ExecutesOnCreation: true}) {
		t.Fatalf("Glacio Chafe variant settlement %+v", got)
	}
	if got := master.invokePeriodStatuses(1409303001); len(got) != 2 || got[0] != 10011100 || got[1] != 10041000 {
		t.Fatalf("InvokePeriod 1409303001 forces %v", got)
	}
	if !master.statusCanBeForced(10041000) || master.statusCanBeForced(10021000) {
		t.Fatal("statusCanBeForced follows the InvokePeriod parameters")
	}
}
