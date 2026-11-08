package capture

import (
	"fmt"
	"reflect"
	"testing"
)

// writeTestSkillDamage writes one paired damage result whose request carried DamageContext.SkillId skillID.
func writeTestSkillDamage(p *processor, sourceID, targetID, rpcID, skillID uint64, amount int64) {
	event := testDamageEvent(p, sourceID, targetID)
	event.EventTypeCandidate, event.Damage, event.RPCID, event.SkillID = "DAMAGE_CANDIDATE", amount, rpcID, skillID
	p.writeDamageEvent(event)
}

func skillOf(t *testing.T, skills []SkillDamageSnapshot, skillID uint64) SkillDamageSnapshot {
	t.Helper()
	for _, skill := range skills {
		if skill.SkillID == skillID {
			return skill
		}
	}
	t.Fatalf("skill %d missing from %+v", skillID, skills)
	return SkillDamageSnapshot{}
}

func TestSkillBreakdownGroupsRepeatedHitsOfOneSkill(t *testing.T) {
	p := newPairingTestProcessor()
	// A multi-hit skill: three server-confirmed results with the same SkillId are one row with three events.
	writeTestSkillDamage(p, 100, 200, 1, 1409011, 100)
	writeTestSkillDamage(p, 100, 200, 2, 1409011, 150)
	writeTestSkillDamage(p, 100, 200, 3, 1409011, 50)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	role := roleOf(t, got, 1409)
	if len(role.Skills) != 1 || !reflect.DeepEqual(role.Skills[0], SkillDamageSnapshot{SkillID: 1409011, DamageDealt: 300, OutgoingEvents: 3, DamageIDsIncomplete: true}) {
		t.Fatalf("skills = %+v", role.Skills)
	}
	if !reflect.DeepEqual(role.UnassignedSkill, UnassignedSkillTotals{}) {
		t.Fatalf("unassigned skill = %+v, want empty", role.UnassignedSkill)
	}
}

func TestSkillBreakdownOrdersSkillsByDamageThenSkillID(t *testing.T) {
	p := newPairingTestProcessor()
	writeTestSkillDamage(p, 100, 200, 1, 30, 40)
	writeTestSkillDamage(p, 100, 200, 2, 20, 90)
	writeTestSkillDamage(p, 100, 200, 3, 10, 40)
	writeTestSkillDamage(p, 100, 200, 4, 40, 5)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	var order []uint64
	for _, skill := range roleOf(t, got, 1409).Skills {
		order = append(order, skill.SkillID)
	}
	if !reflect.DeepEqual(order, []uint64{20, 10, 30, 40}) {
		t.Fatalf("skill order = %v, want damage descending with SkillId breaking the tie", order)
	}
}

func TestSkillBreakdownKeepsRolesApartWithInterleavedAndSharedSkillIDs(t *testing.T) {
	p := newPairingTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	// The same SkillId under two roles stays two rows: ownership comes from the role, never from the SkillId.
	writeTestSkillDamage(p, 100, 200, 1, 555, 10)
	writeTestSkillDamage(p, 101, 200, 2, 555, 20)
	writeTestSkillDamage(p, 100, 200, 3, 1409001, 30)
	writeTestSkillDamage(p, 101, 200, 4, 1508001, 40)
	writeTestSkillDamage(p, 100, 200, 5, 555, 1)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	first, second := roleOf(t, got, 1409), roleOf(t, got, 1508)
	if skill := skillOf(t, first.Skills, 555); skill.DamageDealt != 11 || skill.OutgoingEvents != 2 {
		t.Fatalf("role 1409 skill 555 = %+v", skill)
	}
	if skill := skillOf(t, second.Skills, 555); skill.DamageDealt != 20 || skill.OutgoingEvents != 1 {
		t.Fatalf("role 1508 skill 555 = %+v", skill)
	}
	if len(first.Skills) != 2 || len(second.Skills) != 2 {
		t.Fatalf("skills = %+v / %+v", first.Skills, second.Skills)
	}
}

func TestSkillBreakdownKeepsDamageWithoutSkillIDOnItsRole(t *testing.T) {
	p := newPairingTestProcessor()
	writeTestSkillDamage(p, 100, 200, 1, 1409011, 100)
	writeTestSkillDamage(p, 100, 200, 2, 0, 25)
	// A BulletId alone does not prove a skill.
	event := testDamageEvent(p, 100, 200)
	event.EventTypeCandidate, event.Damage, event.RPCID, event.BulletID, event.HasBulletID = "DAMAGE_CANDIDATE", 7, 3, 1409011001, true
	p.writeDamageEvent(event)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	role := roleOf(t, got, 1409)
	if role.DamageDealt != 132 || role.OutgoingEvents != 3 {
		t.Fatalf("role total changed when its skill was unknown: %+v", role)
	}
	if !reflect.DeepEqual(role.UnassignedSkill, UnassignedSkillTotals{DamageDealt: 32, OutgoingEvents: 2, MissingSkillEvents: 2, DamageIDsIncomplete: true}) {
		t.Fatalf("unassigned skill = %+v", role.UnassignedSkill)
	}
	if got.UnassignedRole.DamageDealt != 0 {
		t.Fatalf("an unknown skill moved confirmed role damage to the unassigned role: %+v", got.UnassignedRole)
	}
}

func TestSkillBreakdownCreditsVerifiedSummonAndExcludesUnverifiedSummon(t *testing.T) {
	p := newPairingTestProcessor()
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) { entity.summonerID, entity.hasSummonerID = 100, true })
	addTestRuntimeEntity(p, 400, func(entity *runtimeEntity) { entity.summonerID, entity.hasSummonerID = 999, true })
	writeTestSkillDamage(p, 300, 200, 1, 1409050, 60)
	writeTestSkillDamage(p, 400, 200, 2, 1409050, 70)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	role := roleOf(t, got, 1409)
	if role.SummonDamage != 60 || len(role.Skills) != 1 || !reflect.DeepEqual(role.Skills[0], SkillDamageSnapshot{SkillID: 1409050, DamageDealt: 60, OutgoingEvents: 1, DamageIDsIncomplete: true}) {
		t.Fatalf("verified summon skill = %+v", role)
	}
	if got.UnattributedOutgoingDamage != 70 {
		t.Fatalf("unverified summon damage = %d, want it kept unattributed", got.UnattributedOutgoingDamage)
	}
}

func TestSkillBreakdownIgnoresDuplicateResponses(t *testing.T) {
	p := newPairingTestProcessor()
	writeTestSkillDamage(p, 100, 200, 1, 1409011, 100)
	writeTestSkillDamage(p, 100, 200, 1, 1409011, 100)
	statistics := NewDamageStatistics()
	record := testEncounterRecord(CombatFlowLocalOutgoing, 40, 1)
	record.SourceRole, record.SkillID = &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}, 1409011
	statistics.addCombatEvent(record)
	if assignment := statistics.addCombatEvent(record); assignment != statisticsAssignmentDuplicate {
		t.Fatalf("replayed record assignment = %q", assignment)
	}
	for _, got := range []DamageStatisticsSnapshot{p.damageStatistics.Snapshot(), statistics.Snapshot()} {
		assertRoleTotalsReconcile(t, got)
		if role := roleOf(t, got, 1409); len(role.Skills) != 1 || role.Skills[0].OutgoingEvents != 1 {
			t.Fatalf("a duplicate response was counted twice: %+v", role.Skills)
		}
	}
}

func TestSkillBreakdownExcludesIncomingAndNonLocalDamage(t *testing.T) {
	p := newPairingTestProcessor()
	addTestRuntimeEntity(p, 201, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9002, true
	})
	writeTestSkillDamage(p, 200, 100, 1, 9001001, 30) // the role takes damage
	writeTestSkillDamage(p, 200, 201, 2, 9001001, 50) // monster versus monster
	writeTestSkillDamage(p, 100, 200, 3, 1409011, 10)
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	role := roleOf(t, got, 1409)
	if role.DamageTaken != 30 || len(role.Skills) != 1 || role.Skills[0].SkillID != 1409011 {
		t.Fatalf("incoming or excluded damage reached the skill split: %+v", role)
	}
}

func TestSkillBreakdownKeepsVehicleAndUnassignedLocalDamageOffRoles(t *testing.T) {
	statistics := NewDamageStatistics()
	vehicle := testEncounterRecord(CombatFlowLocalOutgoing, 80, 1)
	vehicle.VehicleKind, vehicle.SkillID = vehicleKindMotorcycle, 10001009
	unassigned := testEncounterRecord(CombatFlowLocalOutgoing, 15, 2)
	unassigned.SkillID = 1409011
	role := testEncounterRecord(CombatFlowLocalOutgoing, 5, 3)
	role.SourceRole, role.SkillID = &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}, 1409011
	for _, record := range []CombatEventRecord{vehicle, unassigned, role} {
		statistics.addCombatEvent(record)
	}
	got := statistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if got.Vehicle == nil || len(got.Vehicle.Skills) != 1 || !reflect.DeepEqual(got.Vehicle.Skills[0], SkillDamageSnapshot{SkillID: 10001009, DamageDealt: 80, OutgoingEvents: 1, DamageIDsIncomplete: true}) {
		t.Fatalf("vehicle skills = %+v", got.Vehicle)
	}
	if r := roleOf(t, got, 1409); r.DamageDealt != 5 || r.Skills[0].DamageDealt != 5 {
		t.Fatalf("vehicle or unassigned damage reached a role skill: %+v", r)
	}
	if got.UnassignedRole.DamageDealt != 15 || got.DamageDealt != 100 {
		t.Fatalf("confirmed local denominator changed: dealt=%d unassigned=%+v", got.DamageDealt, got.UnassignedRole)
	}
}

func TestSkillBreakdownIsPerEncounterAndResets(t *testing.T) {
	statistics := NewDamageStatistics()
	role := &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}
	record := func(seconds float64, amount int64, skillID uint64) CombatEventRecord {
		r := testEncounterRecord(CombatFlowLocalOutgoing, amount, seconds)
		r.SourceRole, r.SkillID = role, skillID
		return r
	}
	statistics.observeBattleState(true, encounterAt(1), 1)
	statistics.addCombatEvent(record(2, 100, 11))
	statistics.observeBattleState(false, encounterAt(3), 1)
	statistics.observeBattleState(true, encounterAt(20), 1)
	statistics.addCombatEvent(record(21, 40, 22))
	statistics.observeSessionChange(encounterAt(25))
	statistics.observeBattleState(true, encounterAt(30), 2)
	second := record(31, 9, 33)
	second.Key.ProtocolSession = 2
	statistics.addCombatEvent(second)
	got := statistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if len(got.Encounters.Completed) != 2 || got.Encounters.Current == nil {
		t.Fatalf("encounters = %+v", got.Encounters)
	}
	want := [][]SkillDamageSnapshot{{{SkillID: 11, DamageDealt: 100, OutgoingEvents: 1, DamageIDsIncomplete: true}}, {{SkillID: 22, DamageDealt: 40, OutgoingEvents: 1, DamageIDsIncomplete: true}}}
	for i, encounter := range got.Encounters.Completed {
		if !reflect.DeepEqual(encounter.Roles[0].Skills, want[i]) {
			t.Fatalf("encounter %d skills = %+v, want %+v", i, encounter.Roles[0].Skills, want[i])
		}
	}
	if skills := got.Encounters.Current.Roles[0].Skills; len(skills) != 1 || skills[0].SkillID != 33 {
		t.Fatalf("a skill leaked across the session change: %+v", skills)
	}
	if len(roleOf(t, got, 1409).Skills) != 3 {
		t.Fatalf("interval skills = %+v", roleOf(t, got, 1409).Skills)
	}
	statistics.Reset()
	if after := statistics.Snapshot(); len(after.Roles) != 0 {
		t.Fatalf("reset kept skills: %+v", after.Roles)
	}
}

func TestSkillBreakdownOverflowStaysReconciled(t *testing.T) {
	statistics := NewDamageStatistics()
	role := &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}
	for i := 0; i < maxSkillsPerContributor+20; i++ {
		r := testEncounterRecord(CombatFlowLocalOutgoing, int64(i+1), float64(i)*0.01)
		r.SourceRole, r.SkillID = role, uint64(i+1)
		statistics.addCombatEvent(r)
	}
	got := statistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	r := roleOf(t, got, 1409)
	if len(r.Skills) != maxSkillsPerContributor || r.UnassignedSkill.OverflowEvents != 20 || r.UnassignedSkill.MissingSkillEvents != 0 {
		t.Fatalf("overflow: %d rows, unassigned %+v", len(r.Skills), r.UnassignedSkill)
	}
}

func TestSkillSnapshotsAreIndependentCopies(t *testing.T) {
	statistics := NewDamageStatistics()
	r := testEncounterRecord(CombatFlowLocalOutgoing, 10, 1)
	r.SourceRole, r.SkillID = &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}, 5
	statistics.addCombatEvent(r)
	first := statistics.Snapshot()
	first.Roles[0].Skills[0].DamageDealt = 999
	if second := statistics.Snapshot(); second.Roles[0].Skills[0].DamageDealt != 10 {
		t.Fatalf("a caller changed the skill split through a snapshot: %+v", second.Roles[0].Skills)
	}
	if !reflect.DeepEqual(statistics.Snapshot(), statistics.Snapshot()) {
		t.Fatal("repeated snapshots differ")
	}
}

func TestViewSkillsUseRoleDenominatorAndOfficialNamesOnly(t *testing.T) {
	p := newPairingTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	p.masters.skills = map[uint64]masterRecord{
		1409011: {ID: 1409011, Name: "Official", NameStatus: string(resolutionVerified), Status: string(resolutionVerified)},
		1409012: {ID: 1409012, NameStatus: string(resolutionUnknown), Status: string(resolutionVerified)},
	}
	writeTestSkillDamage(p, 100, 200, 1, 1409011, 300)
	writeTestSkillDamage(p, 100, 200, 2, 1409012, 100)
	writeTestSkillDamage(p, 100, 200, 3, 0, 100)
	writeTestSkillDamage(p, 101, 200, 4, 1508001, 500)
	view := p.viewSnapshot()
	var role ViewRole
	for _, candidate := range view.Roles {
		if candidate.RoleID == 1409 {
			role = candidate
		}
	}
	if len(role.Skills) != 2 {
		t.Fatalf("view skills = %+v", role.Skills)
	}
	named, unnamed := role.Skills[0], role.Skills[1]
	if named.Name != "Official" || named.NameStatus != "VERIFIED" || *named.DamageShare != 0.6 || named.OutgoingEvents != 1 {
		t.Fatalf("named skill = %+v", named)
	}
	if unnamed.SkillID != 1409012 || unnamed.Name != "" || unnamed.NameStatus != "UNKNOWN" || *unnamed.DamageShare != 0.2 {
		t.Fatalf("unnamed skill = %+v", unnamed)
	}
	if role.UnassignedSkill.DamageDealt != 100 || *role.UnassignedSkill.DamageShare != 0.2 {
		t.Fatalf("unassigned skill = %+v", role.UnassignedSkill)
	}
	if *role.DamageShare != 0.5 {
		t.Fatalf("role share = %v; the team share must stay separate from skill shares", *role.DamageShare)
	}
}

func BenchmarkSkillBreakdownAdd(b *testing.B) {
	var aggregate roleAggregate
	role := &CombatRole{PlayerID: 77, RoleID: 1409, Basis: roleBasisRoleEntity}
	record := CombatEventRecord{Flow: CombatFlowLocalOutgoing, Amount: 1234, SourceRole: role}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		record.SkillID = uint64(1409000 + i%24)
		aggregate.add(record)
	}
}

func BenchmarkSkillBreakdownSnapshot(b *testing.B) {
	var aggregate roleAggregate
	for roleIndex := 0; roleIndex < 4; roleIndex++ {
		role := &CombatRole{PlayerID: 77, RoleID: uint64(1400 + roleIndex), Basis: roleBasisRoleEntity}
		for skill := 0; skill < 24; skill++ {
			aggregate.add(CombatEventRecord{Flow: CombatFlowLocalOutgoing, Amount: int64(skill + 1), SourceRole: role, SkillID: uint64(skill + 1)})
		}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		roles, _ := aggregate.snapshot()
		if len(roles) != 4 {
			b.Fatal(fmt.Sprint(len(roles)))
		}
	}
}
