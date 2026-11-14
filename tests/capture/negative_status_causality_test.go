package capture

import (
	"testing"
)

// Causal closure of negative-status damage (internal/capture/negative_status_causes.go, negative_status_credit.go;
// research/runs/2026-11-14/v0.7.0-negative-status-causal-closure). Every expected value follows the mechanism, never the
// code's output:
//
//   - the server reports each application with its applier (creation, stack raise, and an application at the stack limit
//     as a report that keeps the count), its removals with nobody, and a cause before its effect;
//   - a status without a period settles on an application of its moment; an overflow (an application at the limit of a
//     status that clears on overflow: Fusion Burst) sets off a settlement that damages the holder and the enemies around
//     it, so each of its results is caused by the overflowing applier, whoever built the stacks;
//   - an instant status Buff executes its status once, by its instigator;
//   - a result is credited only when every possible cause names one entity, and it is the local team's only when every
//     possible cause is the team's.
//
// All inputs are SYNTHETIC frames through the real decoders (MECHANISM-DERIVED fixtures, not observed server behaviour:
// no capture of the corpus holds a Fusion Burst, a Glacio Chafe the team applied or an instant status Buff).
// Roles: A = entity 100 (role 1409), B = 101 (1505), C = 102 (1608) of local player 77. Enemies 200, 201, 202; enemy 203
// applies statuses too. The scene's combat entity 900 executes every status.

const (
	causalEnemy3      = 202
	causalEnemyActor  = 203       // an enemy that applies statuses to other enemies
	causalUnknown     = 999       // an instigator never announced
	causalInstantBuff = 150721001 // an instant status Buff executing Spectro Frazzle (no instance, no handle)
	fusionLimit       = 10
)

// newCausalityTestProcessor is the settlement test processor with a third enemy, an enemy that applies statuses, the
// stack limits of the statuses without a period and an instant Spectro Frazzle Buff.
func newCausalityTestProcessor() *processor {
	p := newSettlementTestProcessor()
	for _, id := range []uint64{causalEnemy3, causalEnemyActor} {
		id := id
		addTestRuntimeEntity(p, id, func(entity *runtimeEntity) {
			entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
			entity.monsterID, entity.hasMonsterID = 9000+id, true
		})
	}
	if p.masters.buffStatics == nil {
		p.masters.buffStatics = map[uint64]buffStaticParameters{}
	}
	for _, buff := range []uint64{settleFusionBuff, settleGlacioBuff} {
		p.masters.buffStatics[buff] = buffStaticParameters{stackLimit: fusionLimit, hasStackLimit: true}
	}
	p.masters.negativeStatusExecutors[causalInstantBuff] = notifyTestSpectro
	p.masters.negativeStatusSettlements[causalInstantBuff] = negativeStatusSettlement{known: true, executesOnCreation: true, instant: true}
	return p
}

func fusionTick(target uint64, damage int32) []byte { return statusTick(target, settleFusion, damage) }

func spectroTick(target uint64, damage int32) []byte {
	return notifyData(testNotify{attacker: notifyTestScene, target: target, damageID: notifyTestSpectro, damage: damage, changeLife: damage})
}

// buildFusionToLimit puts a Fusion Burst instance at its stack limit on holder: created by creator with nine stacks, the
// tenth applied by tenth.
func buildFusionToLimit(t *testing.T, p *processor, seconds float64, sequence uint32, holder, handle, creator, tenth uint64) {
	t.Helper()
	deliverNotifyFrame(t, p, seconds, sequence, statusApplyItem(holder, handle, settleFusionBuff, creator, 9), statusStackItem(holder, handle, fusionLimit, tenth))
}

// overflow is the server's report of an application at the stack limit (the count stays) followed by the clear.
func overflow(holder, handle, by uint64) [][]byte {
	return [][]byte{statusStackItem(holder, handle, fusionLimit, by), statusRemoveItem(holder, handle)}
}

func items(groups ...[][]byte) [][]byte {
	var out [][]byte
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func assertDealt(t *testing.T, p *processor, want int64, why string) {
	t.Helper()
	snapshot := p.damageStatistics.Snapshot()
	if snapshot.DamageDealt != want {
		t.Fatalf("dealt %d, want %d: %s (roles %+v, unassigned %+v)", snapshot.DamageDealt, want, why, snapshot.Roles, snapshot.UnassignedRole)
	}
	assertRoleTotalsReconcile(t, snapshot)
}

func assertStatus(t *testing.T, p *processor, roleID, damageID uint64, want StatusDamageSnapshot, why string) {
	t.Helper()
	if got := statusRow(t, p, roleID, damageID); got != want {
		t.Fatalf("role %d status %+v, want %+v: %s", roleID, got, want, why)
	}
}

func triggered(damageID uint64, damage int64, events uint64) StatusDamageSnapshot {
	return StatusDamageSnapshot{DamageID: damageID, DamageDealt: damage, OutgoingEvents: events, TriggerEvents: events, TriggerDamage: damage}
}

// One overflow damages its holder and the enemies around it; a target without the status takes a result of the same
// settlement. Every result is the team's and A's, each counted once; nothing is added for the overflow itself.
func TestFusionBurstSplashOnEnemiesWithoutTheStatusIsTheTeams(t *testing.T) {
	p := newCausalityTestProcessor()
	buildFusionToLimit(t, p, 1.0, 2, 200, 8, 100, 100)
	deliverNotifyFrame(t, p, 1.5, 3, items(overflow(200, 8, 100), [][]byte{fusionTick(200, 5000), fusionTick(201, 4000), fusionTick(causalEnemy3, 3000)})...)
	assertDealt(t, p, 12000, "the burst on its holder and its splash on two enemies without the status")
	assertStatus(t, p, 1409, settleFusion, triggered(settleFusion, 12000, 3), "A applied every stack and overflowed the instance")
	assertUnassignedStatus(t, p, 0)
	if p.notifyStats.settledAcrossTargets != 2 || p.notifyStats.negativeStatusLocal != 3 {
		t.Fatalf("notify stats %+v; two results came from another enemy's overflow", p.notifyStats)
	}
	// The same frame delivered again (a retransmission) adds nothing: every result keeps its server key.
	deliverNotifyFrame(t, p, 1.5, 3, items(overflow(200, 8, 100), [][]byte{fusionTick(200, 5000), fusionTick(201, 4000), fusionTick(causalEnemy3, 3000)})...)
	assertDealt(t, p, 12000, "a retransmitted settlement frame")
}

// The overflowing applier sets the settlement off: B's application at the limit makes every result of the burst B's,
// though A built all ten stacks, and swapping the entities swaps the credit (no role is preferred).
func TestFusionBurstCreditFollowsTheOverflowingApplier(t *testing.T) {
	cases := []struct {
		name                      string
		creator, tenth, overflows uint64
		credited, other           uint64
	}{
		{"A builds, B overflows", 100, 100, 101, 1505, 1409},
		{"B builds, A overflows", 101, 101, 100, 1409, 1505},
		{"A and B build, C overflows", 100, 101, 102, 1608, 1409},
		{"C builds, B overflows", 102, 102, 101, 1505, 1608},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newCausalityTestProcessor()
			buildFusionToLimit(t, p, 1.0, 2, 200, 8, c.creator, c.tenth)
			deliverNotifyFrame(t, p, 1.5, 3, items(overflow(200, 8, c.overflows), [][]byte{fusionTick(200, 5000), fusionTick(201, 4000)})...)
			assertDealt(t, p, 9000, "the burst and its splash")
			assertStatus(t, p, c.credited, settleFusion, triggered(settleFusion, 9000, 2), "the overflowing applier")
			if row := statusRow(t, p, c.other, settleFusion); row.DamageDealt != 0 {
				t.Fatalf("role %d status %+v; building stacks does not set the burst off", c.other, row)
			}
			assertUnassignedStatus(t, p, 0)
		})
	}
}

// A splash onto an enemy that holds its own Fusion Burst: the result can be that enemy's own settlement or the splash, so
// its own instance's appliers and the overflowing applier are all possible causes.
func TestFusionBurstSplashOntoAnEnemyWithItsOwnStatus(t *testing.T) {
	other := newCausalityTestProcessor()
	deliverNotifyFrame(t, other, 0.5, 2, statusApplyItem(201, 9, settleFusionBuff, 102, 5)) // C's five stacks on 201
	buildFusionToLimit(t, other, 1.0, 3, 200, 8, 100, 100)
	deliverNotifyFrame(t, other, 1.5, 4, items(overflow(200, 8, 100), [][]byte{fusionTick(200, 5000), fusionTick(201, 4000)})...)
	assertDealt(t, other, 9000, "both results are the team's")
	assertStatus(t, other, 1409, settleFusion, triggered(settleFusion, 5000, 1), "the burst on its own holder")
	if row := statusRow(t, other, 1608, settleFusion); row.DamageDealt != 0 {
		t.Fatalf("role 1608 status %+v; C's instance did not overflow", row)
	}
	assertUnassignedStatus(t, other, 4000)

	same := newCausalityTestProcessor()
	deliverNotifyFrame(t, same, 0.5, 2, statusApplyItem(201, 9, settleFusionBuff, 100, 5)) // A's own five stacks on 201
	buildFusionToLimit(t, same, 1.0, 3, 200, 8, 100, 100)
	deliverNotifyFrame(t, same, 1.5, 4, items(overflow(200, 8, 100), [][]byte{fusionTick(200, 5000), fusionTick(201, 4000)})...)
	assertStatus(t, same, 1409, settleFusion, triggered(settleFusion, 9000, 2), "every possible cause is A")
	assertUnassignedStatus(t, same, 0)
}

// Two overflows of different roles in one moment: no result can be told to belong to one of them, also the results on
// the two holders. Every result stays the team's, credited to nobody; the same role's two overflows credit that role.
func TestSimultaneousOverflowsOfDifferentRolesCreditNobody(t *testing.T) {
	p := newCausalityTestProcessor()
	buildFusionToLimit(t, p, 1.0, 2, 200, 8, 100, 100)
	buildFusionToLimit(t, p, 1.0, 3, 201, 9, 101, 101)
	deliverNotifyFrame(t, p, 1.5, 4, items(overflow(200, 8, 100), overflow(201, 9, 101),
		[][]byte{fusionTick(200, 5000), fusionTick(201, 4500), fusionTick(causalEnemy3, 3000)})...)
	assertDealt(t, p, 12500, "every result of the two settlements")
	assertUnassignedStatus(t, p, 12500)

	one := newCausalityTestProcessor()
	buildFusionToLimit(t, one, 1.0, 2, 200, 8, 100, 100)
	buildFusionToLimit(t, one, 1.0, 3, 201, 9, 101, 101)
	deliverNotifyFrame(t, one, 1.5, 4, items(overflow(200, 8, 102), overflow(201, 9, 102),
		[][]byte{fusionTick(200, 5000), fusionTick(201, 4500), fusionTick(causalEnemy3, 3000)})...)
	assertStatus(t, one, 1608, settleFusion, triggered(settleFusion, 12500, 3), "C overflowed both instances")
	assertUnassignedStatus(t, one, 0)
}

// The server may report the clear before the results, and the results may come in the next frame: within the moment
// (statusTriggerCarry) the overflow still explains them; a result long after it is no splash of it.
func TestFusionBurstResultsAfterTheClearAndAfterADelay(t *testing.T) {
	p := newCausalityTestProcessor()
	buildFusionToLimit(t, p, 1.0, 2, 200, 8, 100, 100)
	deliverNotifyFrame(t, p, 1.5, 3, overflow(200, 8, 101)...)
	deliverNotifyFrame(t, p, 1.62, 4, fusionTick(200, 5000), fusionTick(201, 4000))
	assertStatus(t, p, 1505, settleFusion, triggered(settleFusion, 9000, 2), "results 0.12 s after B's overflow")
	deliverNotifyFrame(t, p, 1.85, 5, fusionTick(causalEnemy3, 3000))
	assertDealt(t, p, 9000, "a result 0.35 s after the overflow has no cause in its moment")
}

// A result that the server sends before any cause of it (no status on the target, no overflow yet) is no evidence of the
// team's damage; the server reports a cause before its effect, so this order is outside the corpus behaviour.
func TestSplashReportedBeforeItsOverflowIsNotTheTeams(t *testing.T) {
	p := newCausalityTestProcessor()
	buildFusionToLimit(t, p, 1.0, 2, 200, 8, 100, 100)
	deliverNotifyFrame(t, p, 1.5, 3, items([][]byte{fusionTick(201, 4000)}, overflow(200, 8, 100), [][]byte{fusionTick(200, 5000)})...)
	assertDealt(t, p, 5000, "only the result after its cause")
	assertStatus(t, p, 1409, settleFusion, triggered(settleFusion, 5000, 1), "the burst after the overflow report")
}

// Missing server frames in the moment of a splash: the observed overflow is the team's, but a lost frame may have carried
// another application, so nothing is credited to a role.
func TestMissingServerFramesLeaveASplashUncredited(t *testing.T) {
	p := newCausalityTestProcessor()
	deliverServerFrame(t, p, 1.0, 10, statusApplyItem(200, 8, settleFusionBuff, 100, 9), statusStackItem(200, 8, fusionLimit, 100))
	// Frame 11 is missing.
	deliverServerFrame(t, p, 1.5, 12, items(overflow(200, 8, 100), [][]byte{fusionTick(200, 5000), fusionTick(201, 4000)})...)
	assertDealt(t, p, 9000, "the team's settlement")
	assertUnassignedStatus(t, p, 9000)
}

// An enemy's Fusion Burst (holder and source unrelated to every player) overflowing in the same moment as A's: a splash on
// an enemy without the status, and either holder's result, can be the enemy's; none of them is the team's damage.
func TestAnEnemysOverflowKeepsTheMomentsResultsOutOfTheTeam(t *testing.T) {
	both := newCausalityTestProcessor()
	buildFusionToLimit(t, both, 1.0, 2, 200, 8, 100, 100)
	buildFusionToLimit(t, both, 1.0, 3, 201, 9, causalEnemyActor, causalEnemyActor)
	deliverNotifyFrame(t, both, 1.5, 4, items(overflow(200, 8, 100), overflow(201, 9, causalEnemyActor),
		[][]byte{fusionTick(200, 5000), fusionTick(201, 4500), fusionTick(causalEnemy3, 3000)})...)
	assertDealt(t, both, 0, "every result of the moment can be the enemy's settlement")
	if both.notifyStats.excludedStatusInstances != 1 {
		t.Fatalf("notify stats %+v; the enemy's status is the one the tracker leaves out", both.notifyStats)
	}

	alone := newCausalityTestProcessor()
	buildFusionToLimit(t, alone, 1.0, 2, 201, 9, causalEnemyActor, causalEnemyActor)
	deliverNotifyFrame(t, alone, 1.5, 3, items(overflow(201, 9, causalEnemyActor), [][]byte{fusionTick(201, 4500), fusionTick(causalEnemy3, 3000)})...)
	assertDealt(t, alone, 0, "an enemy's settlement and its splash")

	// An instigator the processor cannot place overflows a status on an enemy: its splash credits no role and is not
	// the team's.
	unknown := newCausalityTestProcessor()
	buildFusionToLimit(t, unknown, 1.0, 2, 201, 9, causalUnknown, causalUnknown)
	deliverNotifyFrame(t, unknown, 1.5, 3, items(overflow(201, 9, causalUnknown), [][]byte{fusionTick(causalEnemy3, 3000)})...)
	assertDealt(t, unknown, 0, "a splash of a status nobody of the team applied")
}

// An enemy's status of the same kind on the target keeps a result of it out of the team, also a timed status's tick.
func TestAnEnemysStatusOnTheTargetKeepsItsResultOutOfTheTeam(t *testing.T) {
	p := newCausalityTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1), statusApplyItem(200, 9, notifyTestAeroBuff2, causalEnemyActor, 1))
	deliverNotifyFrame(t, p, 3.5, 3, aeroTick(200, 800))
	assertDealt(t, p, 0, "the enemy's instance can be the one that executed")
}

// A removal at the stack limit without a reported overflowing application: the burst happened, but whose application set
// it off is unknown. The results are the team's (the instance is A's) and credited to nobody; a removal below the limit is
// no overflow and explains no splash.
func TestRemovalAtTheLimitWithoutAnOverflowReportCreditsNobody(t *testing.T) {
	p := newCausalityTestProcessor()
	buildFusionToLimit(t, p, 1.0, 2, 200, 8, 100, 100)
	deliverNotifyFrame(t, p, 1.5, 3, statusRemoveItem(200, 8), fusionTick(200, 5000), fusionTick(201, 4000))
	assertDealt(t, p, 9000, "the burst of A's instance and its splash")
	assertUnassignedStatus(t, p, 9000)

	below := newCausalityTestProcessor()
	deliverNotifyFrame(t, below, 1.0, 2, statusApplyItem(200, 8, settleFusionBuff, 100, 5))
	deliverNotifyFrame(t, below, 1.5, 3, statusRemoveItem(200, 8), fusionTick(201, 4000))
	assertDealt(t, below, 0, "a removal at five stacks is no overflow")
}

// An overflow of an earlier protocol session explains nothing in the new one.
func TestOverflowOfAnEarlierSessionSpreadsNothing(t *testing.T) {
	p := newCausalityTestProcessor()
	buildFusionToLimit(t, p, 1.0, 2, 200, 8, 100, 100)
	deliverNotifyFrame(t, p, 1.5, 3, overflow(200, 8, 100)...)
	p.sessionNumber++ // reconnect
	deliverNotifyFrame(t, p, 1.55, 4, fusionTick(201, 4000))
	assertDealt(t, p, 0, "the new session has no cause for the result")
}

// Glacio Chafe settles on every application: each result is caused by the application of its moment, so each applier is
// credited with its own; two appliers in one moment leave the result to nobody.
func TestGlacioChafeCreditsTheApplicationOfTheMoment(t *testing.T) {
	p := newCausalityTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 8, settleGlacioBuff, 100, 1), statusTick(200, settleGlacio, 100))
	deliverNotifyFrame(t, p, 1.0, 3, statusStackItem(200, 8, 2, 101), statusTick(200, settleGlacio, 200))
	deliverNotifyFrame(t, p, 1.5, 4, statusStackItem(200, 8, 3, 102), statusTick(200, settleGlacio, 300))
	deliverNotifyFrame(t, p, 2.0, 5, statusStackItem(200, 8, 4, 100), statusStackItem(200, 8, 5, 101), statusTick(200, settleGlacio, 400), statusTick(200, settleGlacio, 500))
	assertStatus(t, p, 1409, settleGlacio, triggered(settleGlacio, 100, 1), "A's creation")
	assertStatus(t, p, 1505, settleGlacio, triggered(settleGlacio, 200, 1), "B's application")
	assertStatus(t, p, 1608, settleGlacio, triggered(settleGlacio, 300, 1), "C's application")
	assertUnassignedStatus(t, p, 900)
	assertDealt(t, p, 1500, "every result once")
}

// No application in a result's moment: every applier the instance had is a possible cause.
func TestApplicationOutsideTheMomentFallsBackToEveryApplier(t *testing.T) {
	mixed := newCausalityTestProcessor()
	deliverNotifyFrame(t, mixed, 0.5, 2, statusApplyItem(200, 8, settleGlacioBuff, 100, 1))
	deliverNotifyFrame(t, mixed, 1.0, 3, statusStackItem(200, 8, 2, 101))
	deliverNotifyFrame(t, mixed, 2.0, 4, statusTick(200, settleGlacio, 300))
	assertUnassignedStatus(t, mixed, 300)

	single := newCausalityTestProcessor()
	deliverNotifyFrame(t, single, 0.5, 2, statusApplyItem(200, 8, settleGlacioBuff, 101, 1))
	deliverNotifyFrame(t, single, 1.0, 3, statusStackItem(200, 8, 2, 101))
	deliverNotifyFrame(t, single, 2.0, 4, statusTick(200, settleGlacio, 300))
	assertStatus(t, single, 1505, settleGlacio, triggered(settleGlacio, 300, 1), "B applied every stack")
}

// A gap inside a status's life can hide another entity's application: a result no application of its moment explains is
// then credited to nobody, also when the gap is long before the result.
func TestStackReportLostDuringTheInstanceLife(t *testing.T) {
	p := newCausalityTestProcessor()
	deliverServerFrame(t, p, 0.5, 10, statusApplyItem(200, 8, settleGlacioBuff, 100, 1))
	deliverServerFrame(t, p, 0.6, 12, statusStackItem(200, 8, 2, 100)) // frame 11 is missing
	deliverServerFrame(t, p, 3.0, 13, statusTick(200, settleGlacio, 300))
	assertUnassignedStatus(t, p, 300)
	// An application in the moment still proves the cause when no frame of the moment is missing.
	q := newCausalityTestProcessor()
	deliverServerFrame(t, q, 0.5, 10, statusApplyItem(200, 8, settleGlacioBuff, 100, 1))
	deliverServerFrame(t, q, 0.6, 12, statusStackItem(200, 8, 2, 100)) // frame 11 is missing
	deliverServerFrame(t, q, 3.0, 13, statusStackItem(200, 8, 3, 101), statusTick(200, settleGlacio, 300))
	assertStatus(t, q, 1505, settleGlacio, triggered(settleGlacio, 300, 1), "B's application of the moment")
}

// A timed status stacked and kept at its limit by other roles still executes on its own: the owner's, whoever applied the
// stacks; a forcing InvokePeriod credits its own instigator.
func TestCoStackedTimedStatusKeepsItsOwnerAndItsTrigger(t *testing.T) {
	p := newCausalityTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverNotifyFrame(t, p, 0.55, 3, aeroTick(200, 100))
	deliverNotifyFrame(t, p, 1.0, 4, statusStackItem(200, 7, 2, 101), statusStackItem(200, 7, 3, 102), statusStackItem(200, 7, 3, 101))
	deliverNotifyFrame(t, p, 2.0, 5, triggerData(200, notifyTestInvokeAero, 102), aeroTick(200, 700))
	deliverNotifyFrame(t, p, 3.55, 6, aeroTick(200, 900))
	assertStatus(t, p, 1409, notifyTestAero, StatusDamageSnapshot{DamageID: notifyTestAero, DamageDealt: 1000, OutgoingEvents: 2}, "the instance's own executions")
	assertStatus(t, p, 1608, notifyTestAero, triggered(notifyTestAero, 700, 1), "C forced one execution")
	if row := statusRow(t, p, 1505, notifyTestAero); row.DamageDealt != 0 {
		t.Fatalf("role 1505 status %+v; stacking a timed status settles nothing", row)
	}
	assertUnassignedStatus(t, p, 0)
}

// Two instances of one timed status with different owners on one target: an own tick can be either's.
func TestDifferentOwnersInstancesOfOneStatusCreditNobody(t *testing.T) {
	p := newCausalityTestProcessor()
	deliverNotifyFrame(t, p, 0.5, 2, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1), statusApplyItem(200, 8, notifyTestAeroBuff2, 101, 1))
	deliverNotifyFrame(t, p, 3.5, 3, aeroTick(200, 800))
	assertUnassignedStatus(t, p, 800)
	assertDealt(t, p, 800, "both instances are the team's")
}

// An instant status Buff executes its status by itself: its instigator is the cause, also on an enemy without the status;
// another role's timed instance of the status could tick in the same moment, so then nobody is credited; an enemy's instant
// Buff is no team damage; one instant Buff explains one result.
func TestInstantStatusBuffCreditsItsInstigator(t *testing.T) {
	alone := newCausalityTestProcessor()
	deliverNotifyFrame(t, alone, 1.0, 2, triggerData(200, causalInstantBuff, 101), spectroTick(200, 2500))
	assertStatus(t, alone, 1505, notifyTestSpectro, triggered(notifyTestSpectro, 2500, 1), "B's instant Buff")
	deliverNotifyFrame(t, alone, 1.05, 3, spectroTick(200, 2500))
	assertDealt(t, alone, 2500, "the instant Buff explains one result only")

	owned := newCausalityTestProcessor()
	deliverNotifyFrame(t, owned, 0.5, 2, statusApplyItem(200, 7, notifyTestSpectroBuff, 101, 3))
	deliverNotifyFrame(t, owned, 1.0, 3, triggerData(200, causalInstantBuff, 101), spectroTick(200, 2500))
	assertStatus(t, owned, 1505, notifyTestSpectro, triggered(notifyTestSpectro, 2500, 1), "B's instant Buff, next to B's own instance")

	mixed := newCausalityTestProcessor()
	deliverNotifyFrame(t, mixed, 0.5, 2, statusApplyItem(200, 7, notifyTestSpectroBuff, 100, 3))
	deliverNotifyFrame(t, mixed, 1.0, 3, triggerData(200, causalInstantBuff, 101), spectroTick(200, 2500))
	assertUnassignedStatus(t, mixed, 2500)

	enemy := newCausalityTestProcessor()
	deliverNotifyFrame(t, enemy, 1.0, 2, triggerData(200, causalInstantBuff, causalEnemyActor), spectroTick(200, 2500))
	assertDealt(t, enemy, 0, "an enemy's instant status Buff")
}

// Missing server frames in the moment of a forced or instant result may have carried a trigger of another role: the result
// stays the team's, credited to nobody; with the moment complete the trigger is credited.
func TestMissingFramesInTheMomentOfATriggerCreditNobody(t *testing.T) {
	forced := newCausalityTestProcessor()
	deliverServerFrame(t, forced, 0.5, 10, statusApplyItem(200, 7, notifyTestAeroBuff, 100, 1))
	deliverServerFrame(t, forced, 0.55, 11, aeroTick(200, 100))
	deliverServerFrame(t, forced, 1.0, 12, triggerData(200, notifyTestInvokeAero, 101))
	// Frame 13 is missing; it may have carried C's InvokePeriod.
	deliverServerFrame(t, forced, 1.05, 14, aeroTick(200, 700))
	assertUnassignedStatus(t, forced, 700)

	instant := newCausalityTestProcessor()
	deliverServerFrame(t, instant, 1.0, 10, triggerData(200, causalInstantBuff, 101))
	deliverServerFrame(t, instant, 1.05, 12, spectroTick(200, 2500)) // frame 11 is missing
	assertUnassignedStatus(t, instant, 2500)

	complete := newCausalityTestProcessor()
	deliverServerFrame(t, complete, 1.0, 10, triggerData(200, causalInstantBuff, 101))
	deliverServerFrame(t, complete, 1.05, 11, spectroTick(200, 2500))
	assertStatus(t, complete, 1505, notifyTestSpectro, triggered(notifyTestSpectro, 2500, 1), "every frame of the moment is present")
}
