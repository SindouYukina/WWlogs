package capture

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func rankingRole(playerID, roleID uint64, damage int64) ViewRole {
	return ViewRole{PlayerID: playerID, RoleID: roleID, DamageDealt: damage}
}

func rankingOrder(roles []ViewRole, vehicle *ViewVehicle) map[string]int {
	order := make(map[string]int, len(roles)+1)
	for _, role := range roles {
		order["role-"+rankingNumber(role.PlayerID)+"-"+rankingNumber(role.RoleID)] = role.Rank
	}
	if vehicle != nil {
		order["vehicle-"+vehicle.Kind] = vehicle.Rank
	}
	return order
}

func rankingNumber(value uint64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestRankRolesAndVehicleTogetherByDamage(t *testing.T) {
	roles := []ViewRole{rankingRole(1, 1205, 100), rankingRole(1, 1506, 300)}
	vehicle := &ViewVehicle{VehicleDamageSnapshot: VehicleDamageSnapshot{Kind: vehicleKindMotorcycle, DamageDealt: 200}}
	rankViewContributors(roles, vehicle, nil)
	want := map[string]int{"role-1-1506": 0, "vehicle-" + vehicleKindMotorcycle: 1, "role-1-1205": 2}
	got := rankingOrder(roles, vehicle)
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for key, rank := range want {
		if got[key] != rank {
			t.Errorf("%s rank = %d, want %d (all: %v)", key, got[key], rank, got)
		}
	}
	// Ranking is a place in the list only: it never moves damage between contributors or gives the vehicle a role.
	if roles[0].DamageDealt != 100 || roles[1].DamageDealt != 300 || vehicle.DamageDealt != 200 || len(roles) != 2 {
		t.Fatalf("ranking changed a contributor: %+v %+v", roles, vehicle)
	}
}

func TestRankKeepsEqualDamageInOneOrderWhateverTheInput(t *testing.T) {
	first := []ViewRole{rankingRole(2, 1003, 700), rankingRole(1, 1001, 700), rankingRole(1, 999, 700)}
	second := []ViewRole{rankingRole(1, 999, 700), rankingRole(1, 1001, 700), rankingRole(2, 1003, 700)}
	vehicleA := &ViewVehicle{VehicleDamageSnapshot: VehicleDamageSnapshot{Kind: vehicleKindMotorcycle, DamageDealt: 700}}
	vehicleB := &ViewVehicle{VehicleDamageSnapshot: VehicleDamageSnapshot{Kind: vehicleKindMotorcycle, DamageDealt: 700}}
	rankViewContributors(first, vehicleA, nil)
	rankViewContributors(second, vehicleB, nil)
	a, b := rankingOrder(first, vehicleA), rankingOrder(second, vehicleB)
	for key, rank := range a {
		if b[key] != rank {
			t.Fatalf("%s: %d against %d", key, rank, b[key])
		}
	}
	// The tie order is the row identity read as text, the same order the rows have always had.
	want := map[string]int{"role-1-1001": 0, "role-1-999": 1, "role-2-1003": 2, "vehicle-" + vehicleKindMotorcycle: 3}
	for key, rank := range want {
		if a[key] != rank {
			t.Errorf("%s rank = %d, want %d", key, a[key], rank)
		}
	}
}

func TestContributorDPSUsesTheScopesOwnDuration(t *testing.T) {
	build := func() ([]ViewRole, *ViewVehicle) {
		return []ViewRole{rankingRole(1, 1205, 100), rankingRole(1, 1506, 300)}, &ViewVehicle{VehicleDamageSnapshot: VehicleDamageSnapshot{Kind: vehicleKindMotorcycle, DamageDealt: 200}}
	}
	for _, status := range []DPSStatus{DPSStatusAvailable, DPSStatusLowerBound} {
		roles, vehicle := build()
		rankViewContributors(roles, vehicle, &ViewDPS{Status: status, DurationSeconds: 10})
		if roles[0].DPS == nil || *roles[0].DPS != 10 || *roles[1].DPS != 30 || vehicle.DPS == nil || *vehicle.DPS != 20 {
			t.Fatalf("%s: %+v %+v", status, roles, vehicle)
		}
	}
	for _, status := range []DPSStatus{DPSStatusUnreliable, DPSStatusUnavailable, DPSStatusInsufficientData, DPSStatusInvalidDuration} {
		roles, vehicle := build()
		rankViewContributors(roles, vehicle, &ViewDPS{Status: status, DurationSeconds: 10})
		if roles[0].DPS != nil || roles[1].DPS != nil || vehicle.DPS != nil {
			t.Fatalf("%s must leave no per-contributor DPS", status)
		}
		if roles[1].Rank != 0 {
			t.Fatalf("%s: the order does not depend on the DPS status", status)
		}
	}
	roles, vehicle := build()
	rankViewContributors(roles, vehicle, &ViewDPS{Status: DPSStatusAvailable, DurationSeconds: 0})
	if roles[0].DPS != nil || vehicle.DPS != nil {
		t.Fatal("a zero duration has no rate")
	}
	roles, vehicle = build()
	rankViewContributors(roles, vehicle, nil)
	if roles[0].DPS != nil || vehicle.DPS != nil {
		t.Fatal("a capture interval has no encounter duration, so no per-contributor DPS")
	}
}

// An encounter's rows must share one numerator source and one denominator: confirmed local damage, vehicle and unassigned
// included. Checking that the percentages add up is not enough, so each row's numerator and denominator are checked.
func TestEncounterRowsShareOneScopeAndOneDenominator(t *testing.T) {
	p := newViewTestProcessor()
	encounter := EncounterSnapshot{
		EncounterID: "1-1-1", Sequence: 1,
		Totals: EncounterTotals{DamageDealt: 1000, OutgoingEvents: 10},
		Roles: []RoleDamageSnapshot{
			{PlayerID: 77, RoleID: 1409, DamageDealt: 300, OutgoingEvents: 3},
			{PlayerID: 77, RoleID: 1508, DamageDealt: 400, OutgoingEvents: 4},
		},
		Vehicle:        &VehicleDamageSnapshot{Kind: vehicleKindMotorcycle, DamageDealt: 200, OutgoingEvents: 2},
		UnassignedRole: UnassignedRoleTotals{DamageDealt: 100, OutgoingEvents: 1},
		EncounterDPS:   DPSResult{Status: DPSStatusAvailable, Damage: 1000, Duration: 8 * time.Second, Value: 125},
	}
	view := p.viewEncounter(encounter)
	const total = 1000.0
	var sharedDamage int64
	var sharedDPS, sharedPercent float64
	check := func(name string, damage int64, share, dps *float64, rank, wantRank int) {
		t.Helper()
		if share == nil || *share != float64(damage)/total {
			t.Errorf("%s share = %v, want %d over the confirmed total", name, share, damage)
		}
		if dps == nil || *dps != float64(damage)/8 {
			t.Errorf("%s dps = %v, want %d over the encounter's 8 s", name, dps, damage)
		}
		if rank != wantRank {
			t.Errorf("%s rank = %d, want %d", name, rank, wantRank)
		}
		sharedDamage += damage
		sharedDPS += *dps
		sharedPercent += *share
	}
	check("role 1409", view.Roles[0].DamageDealt, view.Roles[0].DamageShare, view.Roles[0].DPS, view.Roles[0].Rank, 1)
	check("role 1508", view.Roles[1].DamageDealt, view.Roles[1].DamageShare, view.Roles[1].DPS, view.Roles[1].Rank, 0)
	check("vehicle", view.Vehicle.DamageDealt, view.Vehicle.DamageShare, view.Vehicle.DPS, view.Vehicle.Rank, 2)
	// Unassigned local damage has no row but stays in the shared denominator, so the rows alone add up to less than 100%.
	if view.UnassignedRoleShare == nil || *view.UnassignedRoleShare != 0.1 {
		t.Fatalf("unassigned share = %v", view.UnassignedRoleShare)
	}
	if sharedDamage+view.UnassignedRole.DamageDealt != view.Totals.DamageDealt {
		t.Fatalf("rows %d + unassigned %d != total %d", sharedDamage, view.UnassignedRole.DamageDealt, view.Totals.DamageDealt)
	}
	if math.Abs(sharedPercent-0.9) > 1e-12 {
		t.Fatalf("row shares = %v, want 0.9 beside 10%% unassigned", sharedPercent)
	}
	if math.Abs(sharedDPS+float64(view.UnassignedRole.DamageDealt)/8-view.DPS.Value) > 1e-9 {
		t.Fatalf("row DPS %v + unassigned does not add up to the encounter's %v", sharedDPS, view.DPS.Value)
	}
	if !view.Completeness.Conserved || view.Completeness.RoleAttributionComplete {
		t.Fatalf("completeness = %+v", view.Completeness)
	}
}

func TestEmptyAndZeroDurationEncountersHaveNoInventedNumbers(t *testing.T) {
	p := newViewTestProcessor()
	empty := p.viewEncounter(EncounterSnapshot{EncounterID: "1-1-1", EncounterDPS: DPSResult{Status: DPSStatusInsufficientData}})
	if len(empty.Roles) != 0 || empty.Vehicle != nil || empty.UnassignedRoleShare != nil {
		t.Fatalf("empty encounter = %+v", empty)
	}
	zero := p.viewEncounter(EncounterSnapshot{
		EncounterID: "1-1-2", Totals: EncounterTotals{DamageDealt: 50, OutgoingEvents: 1},
		Roles:        []RoleDamageSnapshot{{PlayerID: 77, RoleID: 1409, DamageDealt: 50, OutgoingEvents: 1}},
		EncounterDPS: DPSResult{Status: DPSStatusInvalidDuration, Damage: 50},
	})
	if zero.Roles[0].DPS != nil || zero.Roles[0].Rank != 0 || zero.Roles[0].DamageShare == nil || *zero.Roles[0].DamageShare != 1 {
		t.Fatalf("zero duration row = %+v", zero.Roles[0])
	}
}

func TestIntervalAndEncounterRowsNeverMix(t *testing.T) {
	p := newViewTestProcessor()
	statistics := p.damageStatistics
	// A finished encounter of one role, then a running one of another: the interval holds both.
	roleOutgoing(statistics, 1508, 200, 10)
	roleOutgoing(statistics, 1508, 300, 14)
	statistics.observeCaptureTime(encounterAt(14 + encounterInactivityTimeout.Seconds() + 5))
	roleOutgoing(statistics, 1409, 300, 100)
	roleOutgoing(statistics, 1409, 100, 110)
	p.currentFrameTime = encounterAt(110)
	view := p.viewSnapshot()
	if view.Encounters.Current == nil || len(view.Encounters.Recent) != 1 {
		t.Fatalf("encounters = %+v", view.Encounters)
	}
	current := view.Encounters.Current
	byRole := func(roles []ViewRole) map[uint64]ViewRole {
		out := map[uint64]ViewRole{}
		for _, role := range roles {
			out[role.RoleID] = role
		}
		return out
	}
	interval, encounter := byRole(view.Roles), byRole(current.Roles)
	if interval[1508].Rank != 0 || interval[1409].Rank != 1 {
		t.Fatalf("interval ranks: %+v", interval)
	}
	if len(encounter) != 1 || encounter[1409].Rank != 0 {
		t.Fatalf("encounter ranks: %+v", encounter)
	}
	finished := view.Encounters.Recent[0]
	if len(finished.Roles) != 1 || finished.Roles[0].RoleID != 1508 || finished.Roles[0].DPS == nil || *finished.Roles[0].DPS != float64(finished.Roles[0].DamageDealt)/finished.DPS.DurationSeconds {
		t.Fatalf("finished encounter = %+v", finished)
	}
	if interval[1409].DPS != nil || interval[1508].DPS != nil {
		t.Fatal("the capture interval carries no per-contributor DPS")
	}
	row := encounter[1409]
	if current.DPS.Status != DPSStatusAvailable || row.DPS == nil || *row.DPS != float64(row.DamageDealt)/current.DPS.DurationSeconds {
		t.Fatalf("encounter row dps = %v, encounter = %+v", row.DPS, current.DPS)
	}
	if row.DamageDealt != 400 || row.DamageShare == nil || *row.DamageShare != 1 || current.Totals.DamageDealt != 400 {
		t.Fatalf("the encounter must not include the 500 of the one before it: %+v, total %d", row, current.Totals.DamageDealt)
	}
	if interval[1409].DamageShare == nil || *interval[1409].DamageShare != 400.0/900 {
		t.Fatalf("interval share = %v", interval[1409].DamageShare)
	}
}

// The field names and the presence of rank 0 are the contract the frontend reads.
func TestRankingContractFields(t *testing.T) {
	dps := 12.5
	data, err := json.Marshal(ViewRole{RoleID: 1, Rank: 0, DPS: &dps})
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.Contains(text, `"rank":0`) || !strings.Contains(text, `"dps":12.5`) {
		t.Fatalf("role = %s", text)
	}
	data, _ = json.Marshal(ViewVehicle{VehicleDamageSnapshot: VehicleDamageSnapshot{Kind: "x"}, Rank: 1})
	if text := string(data); !strings.Contains(text, `"rank":1`) || strings.Contains(text, `"dps"`) {
		t.Fatalf("vehicle = %s", text)
	}
}
