package capture

import (
	"reflect"
	"sync"
	"testing"
)

// assertRoleTotalsReconcile checks the role aggregation invariants for an interval and every retained encounter.
func assertRoleTotalsReconcile(t *testing.T, snapshot DamageStatisticsSnapshot) {
	t.Helper()
	check := func(scope string, roles []RoleDamageSnapshot, unassigned UnassignedRoleTotals, vehicle *VehicleDamageSnapshot, dealt, taken int64, outgoing, incoming uint64) {
		t.Helper()
		sumDealt, sumTaken := unassigned.DamageDealt, unassigned.DamageTaken
		sumOutgoing, sumIncoming := unassigned.OutgoingEvents, unassigned.IncomingEvents
		if vehicle != nil {
			sumDealt += vehicle.DamageDealt
			sumOutgoing += vehicle.OutgoingEvents
		}
		for _, role := range roles {
			if role.DirectDamage+role.SummonDamage != role.DamageDealt {
				t.Fatalf("%s role %d/%d: direct %d + summon %d != dealt %d", scope, role.PlayerID, role.RoleID, role.DirectDamage, role.SummonDamage, role.DamageDealt)
			}
			sumDealt += role.DamageDealt
			sumTaken += role.DamageTaken
			sumOutgoing += role.OutgoingEvents
			sumIncoming += role.IncomingEvents
		}
		if sumDealt != dealt || sumTaken != taken || sumOutgoing != outgoing || sumIncoming != incoming {
			t.Fatalf("%s roles do not reconcile: roles+unassigned dealt=%d taken=%d out=%d in=%d, totals dealt=%d taken=%d out=%d in=%d",
				scope, sumDealt, sumTaken, sumOutgoing, sumIncoming, dealt, taken, outgoing, incoming)
		}
	}
	check("interval", snapshot.Roles, snapshot.UnassignedRole, snapshot.Vehicle, snapshot.DamageDealt, snapshot.DamageTaken, snapshot.OutgoingEvents, snapshot.IncomingEvents)
	encounters := snapshot.Encounters.Completed
	if snapshot.Encounters.Current != nil {
		encounters = append(append([]EncounterSnapshot(nil), encounters...), *snapshot.Encounters.Current)
	}
	for _, e := range encounters {
		check("encounter "+e.EncounterID, e.Roles, e.UnassignedRole, e.Vehicle, e.Totals.DamageDealt, e.Totals.DamageTaken, e.Totals.OutgoingEvents, e.Totals.IncomingEvents)
	}
}

func roleOf(t *testing.T, snapshot DamageStatisticsSnapshot, roleID uint64) RoleDamageSnapshot {
	t.Helper()
	for _, role := range snapshot.Roles {
		if role.RoleID == roleID {
			return role
		}
	}
	t.Fatalf("role %d missing from %+v", roleID, snapshot.Roles)
	return RoleDamageSnapshot{}
}

func writeTestDamage(p *processor, sourceID, targetID, rpcID uint64, amount int64) {
	event := testDamageEvent(p, sourceID, targetID)
	event.EventTypeCandidate, event.Damage, event.RPCID = "DAMAGE_CANDIDATE", amount, rpcID
	p.writeDamageEvent(event)
}

func TestRoleAggregationCreditsTeamRolesByPlayerSceneGroup(t *testing.T) {
	p := newPairingTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	addTestPlayerRoleGroupEntity(p, 102, 88, 1312) // another player's role
	writeTestDamage(p, 100, 200, 1, 100)
	writeTestDamage(p, 101, 200, 2, 40)
	writeTestDamage(p, 100, 200, 3, 25)
	writeTestDamage(p, 200, 101, 4, 30)
	writeTestDamage(p, 102, 200, 5, 999)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if len(got.Roles) != 2 {
		t.Fatalf("roles = %+v, want the two local roles only", got.Roles)
	}
	first, second := roleOf(t, got, 1409), roleOf(t, got, 1508)
	if first.PlayerID != 77 || first.DamageDealt != 125 || first.OutgoingEvents != 2 || first.DirectDamage != 125 || first.SummonDamage != 0 {
		t.Fatalf("role 1409 = %+v", first)
	}
	if second.DamageDealt != 40 || second.DamageTaken != 30 || second.IncomingEvents != 1 {
		t.Fatalf("role 1508 = %+v", second)
	}
	if got.UnassignedRole != (UnassignedRoleTotals{}) {
		t.Fatalf("unassigned = %+v, want empty", got.UnassignedRole)
	}
}

func TestRoleAggregationCreditsSummonThroughVerifiedChain(t *testing.T) {
	p := newPairingTestProcessor()
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) {
		entity.summonerID, entity.hasSummonerID = 100, true
	})
	addTestRuntimeEntity(p, 301, func(entity *runtimeEntity) {
		entity.summonerID, entity.hasSummonerID = 300, true
	})
	writeTestDamage(p, 300, 200, 1, 60)
	writeTestDamage(p, 301, 200, 2, 15)
	writeTestDamage(p, 100, 200, 3, 10)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	role := roleOf(t, got, 1409)
	if role.DamageDealt != 85 || role.SummonDamage != 75 || role.DirectDamage != 10 || role.OutgoingEvents != 3 {
		t.Fatalf("summon damage was not credited through the chain: %+v", role)
	}
}

func TestRoleAggregationKeepsRoleReportOnlyEntityUnassigned(t *testing.T) {
	p := newPairingTestProcessor()
	// Local by the client's role report, but without a PlayerSceneGroup RoleId mapping.
	addTestRuntimeEntity(p, 150, nil)
	p.observeLocalRoleEntity(150)
	writeTestDamage(p, 150, 200, 1, 50)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if got.DamageDealt != 50 || got.UnassignedRole.DamageDealt != 50 || got.UnassignedRole.OutgoingEvents != 1 || len(got.Roles) != 0 {
		t.Fatalf("damage without role evidence must stay unassigned: %+v", got)
	}
}

func TestRoleAggregationExcludesUnattributedDamage(t *testing.T) {
	p := newPairingTestProcessor()
	addTestRuntimeEntity(p, 400, func(entity *runtimeEntity) { entity.summonerID, entity.hasSummonerID = 999, true })
	writeTestDamage(p, 400, 200, 1, 70) // unknown summoner
	p.context.invalidate("in", pairingAt(1), "test_gap")
	writeTestDamage(p, 100, 200, 2, 30) // downgraded
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if len(got.Roles) != 0 || got.UnassignedRole.DamageDealt != 0 || got.DamageDealt != 0 {
		t.Fatalf("unattributed damage reached the role aggregation: %+v", got)
	}
}

func TestRoleAggregationFollowsRoleAcrossEntityChanges(t *testing.T) {
	p := newPairingTestProcessor()
	writeTestDamage(p, 100, 200, 1, 10)
	// A new scene gives the same role a new EntityId.
	p.playerRelevance.resetSceneEntities()
	addTestPlayerRoleGroupEntity(p, 500, 77, 1409)
	p.observeLocalRoleEntity(500)
	writeTestDamage(p, 500, 200, 2, 20)
	// The old EntityId comes back for a different role: the role follows the new mapping.
	addTestPlayerRoleGroupEntity(p, 100, 77, 1508)
	writeTestDamage(p, 100, 200, 3, 5)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if role := roleOf(t, got, 1409); role.DamageDealt != 30 || role.OutgoingEvents != 2 {
		t.Fatalf("role 1409 did not follow its new EntityId: %+v", role)
	}
	if role := roleOf(t, got, 1508); role.DamageDealt != 5 {
		t.Fatalf("a remapped EntityId kept its old role: %+v", role)
	}
}

func TestRoleAggregationIsPerEncounterAndResetsWithInterval(t *testing.T) {
	statistics := NewDamageStatistics()
	role := &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}
	record := func(seconds float64, amount int64) CombatEventRecord {
		r := testEncounterRecord(CombatFlowLocalOutgoing, amount, seconds)
		r.SourceRole = role
		return r
	}
	statistics.observeBattleState(true, encounterAt(1), 1)
	statistics.addCombatEvent(record(2, 100))
	statistics.observeBattleState(false, encounterAt(3), 1)
	statistics.observeBattleState(true, encounterAt(20), 1)
	statistics.addCombatEvent(record(21, 40))
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 7, 22))
	got := statistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if len(got.Encounters.Completed) != 1 || got.Encounters.Current == nil {
		t.Fatalf("encounters = %+v", got.Encounters)
	}
	if roles := got.Encounters.Completed[0].Roles; len(roles) != 1 || roles[0].DamageDealt != 100 {
		t.Fatalf("first encounter roles = %+v", roles)
	}
	if current := got.Encounters.Current; len(current.Roles) != 1 || current.Roles[0].DamageDealt != 40 || current.UnassignedRole.DamageDealt != 7 {
		t.Fatalf("current encounter roles = %+v unassigned=%+v", current.Roles, current.UnassignedRole)
	}
	statistics.Reset()
	if after := statistics.Snapshot(); len(after.Roles) != 0 || after.UnassignedRole != (UnassignedRoleTotals{}) {
		t.Fatalf("reset kept role totals: %+v", after)
	}
}

func TestRoleSnapshotsAreIndependentCopies(t *testing.T) {
	statistics := NewDamageStatistics()
	r := testEncounterRecord(CombatFlowLocalOutgoing, 10, 1)
	r.SourceRole = &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}
	statistics.addCombatEvent(r)
	first := statistics.Snapshot()
	first.Roles[0].DamageDealt = 999
	second := statistics.Snapshot()
	if second.Roles[0].DamageDealt != 10 {
		t.Fatalf("a caller changed the statistics through a snapshot: %+v", second.Roles)
	}
	if !reflect.DeepEqual(statistics.Snapshot(), statistics.Snapshot()) {
		t.Fatal("repeated snapshots differ")
	}
}

func TestRoleAggregationOverflowStaysReconciled(t *testing.T) {
	var aggregate roleAggregate
	for roleID := uint64(1); roleID <= maxAggregatedRoles+1; roleID++ {
		aggregate.add(CombatEventRecord{Flow: CombatFlowLocalOutgoing, Amount: 1, SourceRole: &CombatRole{PlayerID: 1, RoleID: roleID}})
	}
	roles, unassigned := aggregate.snapshot()
	if len(roles) != maxAggregatedRoles || unassigned.OverflowEvents != 1 || unassigned.DamageDealt != 1 {
		t.Fatalf("overflow roles=%d unassigned=%+v", len(roles), unassigned)
	}
}

func TestConcurrentQueriesSeeConsistentRoleTotals(t *testing.T) {
	statistics := NewDamageStatistics()
	role := &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan string, 8)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snapshot := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: statistics.Snapshot().CaptureSessionID, Timestamp: encounterAt(50)})
				var sum int64
				for _, r := range snapshot.Roles {
					sum += r.DamageDealt
				}
				if sum+snapshot.UnassignedRole.DamageDealt != snapshot.DamageDealt {
					errs <- "a concurrent snapshot saw roles and totals from different moments"
					return
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		r := testEncounterRecord(CombatFlowLocalOutgoing, 3, float64(i)/100)
		if i%3 == 0 {
			r.SourceRole = role
		}
		statistics.addCombatEvent(r)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	assertRoleTotalsReconcile(t, statistics.Snapshot())
}

func TestRoleAggregationCreditsRoleEntityTouchedByActiveRequest(t *testing.T) {
	p := newPairingTestProcessor()
	// An EntityActiveRequest update marks the record CORRELATED but keeps its PlayerSceneGroup mapping.
	if err := p.entities.observeActiveRequest(appendVarintField(nil, 4, 100)); err != nil {
		t.Fatal(err)
	}
	if _, status := p.entities.resolve(100); status != resolutionCorrelated {
		t.Fatalf("fixture status = %s, want CORRELATED", status)
	}
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) { entity.summonerID, entity.hasSummonerID = 100, true })
	writeTestDamage(p, 100, 200, 1, 10)
	writeTestDamage(p, 300, 200, 2, 5)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if role := roleOf(t, got, 1409); role.DirectDamage != 10 || role.SummonDamage != 5 {
		t.Fatalf("role entity with an active-request update was not credited: %+v unassigned=%+v", role, got.UnassignedRole)
	}
}

func TestRoleAggregationNeverCreditsUnknownEntity(t *testing.T) {
	p := newPairingTestProcessor()
	if role := p.localRoleForEntity(999, entityIdentity{}); role != nil {
		t.Fatalf("an entity without a record was credited: %+v", role)
	}
}

func TestMotorcycleContributionIsIndependentOfCharacterCredit(t *testing.T) {
	p := newPairingTestProcessor()
	event := testDamageEvent(p, 100, 200)
	event.EventTypeCandidate, event.Damage, event.RPCID = "DAMAGE_CANDIDATE", 100, 1
	event.DamageContributorCandidate = &damageContributorCandidate{Kind: vehicleKindMotorcycle, ActorType: "Vehicle", EntityType: "BattleVehicle", Status: resolutionCorrelated}
	p.writeDamageEvent(event)
	p.writeDamageEvent(event)
	writeTestDamage(p, 100, 200, 2, 50)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	role := roleOf(t, got, 1409)
	if got.DamageDealt != 150 || got.OutgoingEvents != 2 || got.DuplicateEvents != 1 || got.Vehicle == nil || got.Vehicle.DamageDealt != 100 || got.Vehicle.OutgoingEvents != 1 || role.DamageDealt != 50 || got.UnassignedRole.DamageDealt != 0 {
		t.Fatal("vehicle duplicated or replaced character damage")
	}
	if event.SourceEntityID != 100 || event.DamageContributorCandidate.Status != resolutionCorrelated {
		t.Fatal("vehicle classification changed raw source or evidence")
	}
	view := p.viewSnapshot()
	if view.Vehicle == nil || view.Vehicle.DamageShare == nil || *view.Vehicle.DamageShare != 100.0/150.0 || *view.Roles[0].DamageShare != 50.0/150.0 {
		t.Fatal("vehicle changed the share denominator")
	}
	got.Vehicle.DamageDealt = 999
	if p.damageStatistics.Snapshot().Vehicle.DamageDealt != 100 {
		t.Fatal("vehicle snapshot aliases mutable state")
	}
	p.damageStatistics.Reset()
	if p.damageStatistics.Snapshot().Vehicle != nil {
		t.Fatal("vehicle survived interval reset")
	}
}

func TestMotorcycleContributionRequiresLocalFlowAndRecognizedEvidence(t *testing.T) {
	for _, scenario := range []string{"unknown_class", "unknown_evidence", "suspect_context", "other_player"} {
		t.Run(scenario, func(t *testing.T) {
			p := newPairingTestProcessor()
			addTestRuntimeEntity(p, 150, nil)
			p.observeLocalRoleEntity(150)
			event := testDamageEvent(p, 150, 200)
			event.EventTypeCandidate, event.Damage, event.RPCID = "DAMAGE_CANDIDATE", 70, 1
			event.DamageContributorCandidate = &damageContributorCandidate{Kind: vehicleKindMotorcycle, ActorType: "Vehicle", EntityType: "BattleVehicle", Status: resolutionCorrelated}
			switch scenario {
			case "unknown_class":
				event.DamageContributorCandidate.Kind = "unresolved"
			case "unknown_evidence":
				event.DamageContributorCandidate.Status = resolutionUnknown
			case "suspect_context":
				p.context.invalidate("in", pairingAt(1), "test_gap")
			case "other_player":
				addTestPlayerRoleGroupEntity(p, 102, 88, 1312)
				event = testDamageEvent(p, 102, 200)
				event.EventTypeCandidate, event.Damage, event.RPCID = "DAMAGE_CANDIDATE", 70, 1
				event.DamageContributorCandidate = &damageContributorCandidate{Kind: vehicleKindMotorcycle, ActorType: "Vehicle", EntityType: "BattleVehicle", Status: resolutionCorrelated}
			}
			p.writeDamageEvent(event)
			got := p.damageStatistics.Snapshot()
			assertRoleTotalsReconcile(t, got)
			if got.Vehicle != nil {
				t.Fatal("unsupported or nonlocal event entered motorcycle totals")
			}
			if scenario == "unknown_class" || scenario == "unknown_evidence" {
				if got.UnassignedRole.DamageDealt != 70 || got.DamageDealt != 70 {
					t.Fatal("unassigned local damage was hidden")
				}
			} else if got.DamageDealt != 0 {
				t.Fatal("unconfirmed event entered confirmed totals")
			}
		})
	}
}

func TestMotorcycleContributionIsScopedToEncounters(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	record := testEncounterRecord(CombatFlowLocalOutgoing, 40, 1)
	record.VehicleKind = vehicleKindMotorcycle
	statistics.addCombatEvent(record)
	statistics.observeBattleState(false, encounterAt(2), 1)
	statistics.observeBattleState(true, encounterAt(20), 1)
	record = testEncounterRecord(CombatFlowLocalOutgoing, 60, 21)
	record.VehicleKind = vehicleKindMotorcycle
	record.ObservedAt = encounterAt(21)
	statistics.addCombatEvent(record)
	got := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: statistics.captureSessionID, Timestamp: encounterAt(21)})
	assertRoleTotalsReconcile(t, got)
	if got.Vehicle == nil || got.Vehicle.DamageDealt != 100 || len(got.Encounters.Completed) != 1 || got.Encounters.Completed[0].Vehicle == nil || got.Encounters.Completed[0].Vehicle.DamageDealt != 40 || got.Encounters.Current == nil || got.Encounters.Current.Vehicle == nil || got.Encounters.Current.Vehicle.DamageDealt != 60 {
		t.Fatal("motorcycle totals leaked across encounters")
	}
}
