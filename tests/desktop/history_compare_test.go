package desktop

import (
	"math"
	"testing"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
)

// The Encounter Comparison lines stored encounters up as their History details show them: contributors by the role they
// show as, skills by SkillId, changes only between complete records, and the shared conditions stated as facts.

func float(value float64) *float64 { return &value }

var towerMode = historyMode{Kind: capture.InstanceModeCategory, Name: "Tower", Status: "VERIFIED"}

func compareSummary(id int64, version string, instance uint64, mode historyMode, duration float64, monster uint64) historySummaryJSON {
	summary := history.EncounterSummary{ID: id, GameVersion: version, State: "finished", Complete: true, DurationSeconds: float(duration)}
	if instance != 0 {
		summary.Scene = history.SceneRef{Known: true, InstanceID: instance}
	}
	if monster != 0 {
		summary.MainEnemy = &history.EnemyRef{MonsterID: monster}
	}
	return historySummaryJSON{EncounterSummary: summary, Mode: mode}
}

func compareView(damage int64, dps float64, status capture.DPSStatus, roles ...capture.ViewRole) *capture.ViewEncounter {
	return &capture.ViewEncounter{Totals: capture.EncounterTotals{DamageDealt: damage}, DPS: capture.ViewDPS{Status: status, Value: dps},
		PeakDPS: capture.ViewDPS{Status: status, Value: dps * 2}, Roles: roles}
}

func compareRole(role, static uint64, name string, damage int64, share, dps float64, skills ...capture.ViewSkill) capture.ViewRole {
	return capture.ViewRole{RoleID: role, StaticRoleID: static, Name: name, NameStatus: "VERIFIED", DamageDealt: damage, DamageShare: float(share), DPS: float(dps), Skills: skills}
}

func skill(id uint64, name string, damage int64, hits uint64) capture.ViewSkill {
	status := "VERIFIED"
	if name == "" {
		status = "UNKNOWN"
	}
	return capture.ViewSkill{SkillID: id, Name: name, NameStatus: status, DamageDealt: damage, OutgoingEvents: hits}
}

func near(value *float64, want float64) bool {
	return value != nil && math.Abs(*value-want) < 1e-9
}

func metric(t *testing.T, out comparison, key string) compareMetric {
	t.Helper()
	for _, metric := range out.Metrics {
		if metric.Key == key {
			return metric
		}
	}
	t.Fatalf("no metric %s in %+v", key, out.Metrics)
	return compareMetric{}
}

func contributor(t *testing.T, out comparison, kind string, role uint64) compareContributor {
	t.Helper()
	for _, c := range out.Contributors {
		if c.Kind == kind && c.RoleID == role {
			return c
		}
	}
	t.Fatalf("no %s %d in %+v", kind, role, out.Contributors)
	return compareContributor{}
}

func condition(out comparison, key string) string {
	for _, c := range out.Context.Conditions {
		if c.Key == key {
			return c.State
		}
	}
	return ""
}

func TestComparisonOfCompleteRecordsGivesChangesAndContributors(t *testing.T) {
	first := compareInput{summary: compareSummary(1, "3.8", 20016, towerMode, 100, 900), class: history.ClassComplete,
		view: compareView(10000, 100, capture.DPSStatusAvailable,
			compareRole(1512, 1512, "Lily", 6000, 0.6, 60, skill(15124104, "Basic", 4000, 10), skill(15124105, "", 2000, 2)),
			compareRole(1409, 1409, "Cartethyia", 4000, 0.4, 40, skill(14091001, "Skill", 4000, 4)))}
	// The second fight: Lily as her trial role, a role that was absent before, a vehicle and unassigned damage.
	second := compareInput{summary: compareSummary(2, "3.8", 20016, towerMode, 80, 900), class: history.ClassComplete,
		view: compareView(9600, 120, capture.DPSStatusAvailable,
			compareRole(15127004, 1512, "Lily (Trial)", 6000, 0.625, 75, skill(15124104, "Basic", 6000, 12)),
			compareRole(1205, 1205, "Changli", 3000, 0.3125, 37.5))}
	second.view.Vehicle = &capture.ViewVehicle{VehicleDamageSnapshot: capture.VehicleDamageSnapshot{DamageDealt: 400}, DamageShare: float(400.0 / 9600)}
	second.view.UnassignedRole = capture.UnassignedRoleTotals{DamageDealt: 200}
	out := compareEncounters([]compareInput{first, second})

	if !out.Context.Reliable || len(out.Encounters) != 2 || !out.Encounters[1].Reliable {
		t.Fatalf("encounters = %+v context %+v", out.Encounters, out.Context)
	}
	dps := metric(t, out, "dps")
	if dps.Values[0].Delta != nil || !near(dps.Values[1].Delta, 0.2) || dps.Values[1].Status != compareAvailable {
		t.Fatalf("dps = %+v", dps.Values)
	}
	if duration := metric(t, out, "duration"); !near(duration.Values[1].Delta, -0.2) {
		t.Fatalf("duration = %+v", duration.Values)
	}
	if damage := metric(t, out, "damage"); !near(damage.Values[1].Value, 9600) || !near(damage.Values[1].Delta, -0.04) {
		t.Fatalf("damage = %+v", damage.Values)
	}
	// Lily is one row across both fights: her trial role shows as her, and her DPS change is measured between them.
	lily := contributor(t, out, "role", 1512)
	if lily.Name.Name != "Lily" || lily.Values[0] == nil || lily.Values[1] == nil || !lily.Values[1].Trial || !near(lily.Values[1].DPSDelta, 0.25) {
		t.Fatalf("Lily = %+v %+v %+v", lily, lily.Values[0], lily.Values[1])
	}
	if len(lily.Skills) != 2 || lily.Skills[0].SkillID != 15124104 || !near(lily.Skills[0].Values[0].Share, 4000.0/6000) || !near(lily.Skills[0].Values[1].Share, 1) {
		t.Fatalf("Lily's skills = %+v", lily.Skills)
	}
	if unnamed := lily.Skills[1]; unnamed.Name.Status != "UNKNOWN" || unnamed.Name.Name != "" || unnamed.Values[1] != nil {
		t.Fatalf("an unnamed skill must stay UNKNOWN and be absent where unused: %+v", unnamed)
	}
	// A role that dealt no damage in a fight has no value there, never a zero.
	if changli := contributor(t, out, "role", 1205); changli.Values[0] != nil || changli.Values[1] == nil || changli.Values[1].DPSDelta != nil {
		t.Fatalf("Changli = %+v", changli.Values)
	}
	if cartethyia := contributor(t, out, "role", 1409); cartethyia.Values[1] != nil {
		t.Fatalf("Cartethyia = %+v", cartethyia.Values)
	}
	// Roles come first, then the vehicle and the unassigned damage, which keep the totals reconcilable.
	kinds := []string{}
	for _, c := range out.Contributors {
		kinds = append(kinds, c.Kind)
	}
	if len(kinds) != 5 || kinds[0] != "role" || kinds[3] != "vehicle" || kinds[4] != "unassigned" {
		t.Fatalf("contributor order = %v", kinds)
	}
	var sum int64
	for _, c := range out.Contributors {
		if c.Values[1] != nil {
			sum += c.Values[1].Damage
		}
	}
	if sum != 9600 {
		t.Fatalf("the second fight's contributors add up to %d, not its confirmed damage", sum)
	}
	for _, key := range []string{"game_version", "mode", "stage", "main_enemy"} {
		if state := condition(out, key); state != "same" {
			t.Errorf("%s = %s", key, state)
		}
	}
	if !near(out.Context.DurationRatio, 1.25) || out.Context.OpenWorld {
		t.Fatalf("context = %+v", out.Context)
	}
}

// A record that is not complete is shown with what its values are, and nothing is measured against it.
func TestComparisonWithAnIncompleteRecordGivesNoChanges(t *testing.T) {
	complete := compareInput{summary: compareSummary(1, "3.8", 8, historyMode{Kind: capture.InstanceModeOpenWorld, Status: "VERIFIED"}, 30, 0), class: history.ClassComplete,
		view: compareView(3000, 100, capture.DPSStatusAvailable, compareRole(1512, 1512, "Lily", 3000, 1, 100))}
	degraded := compareInput{summary: compareSummary(2, "3.9", 20016, towerMode, 60, 900), class: history.ClassDegraded,
		view: compareView(9000, 150, capture.DPSStatusLowerBound, compareRole(1512, 1512, "Lily", 9000, 1, 150))}
	affected := compareInput{summary: compareSummary(3, "", 0, historyMode{Status: "UNKNOWN"}, 60, 0), class: history.ClassKnownAffected,
		view: compareView(9000, 150, capture.DPSStatusAvailable, compareRole(1512, 1512, "Lily", 9000, 1, 150))}
	abandoned := compareInput{summary: compareSummary(4, "3.8", 8, historyMode{Kind: capture.InstanceModeOpenWorld, Status: "VERIFIED"}, 20, 0), class: history.ClassUnfinished}
	abandoned.summary.DurationSeconds = nil
	out := compareEncounters([]compareInput{complete, degraded, affected, abandoned})

	if out.Context.Reliable || out.Encounters[1].Reliable || out.Encounters[3].Results {
		t.Fatalf("encounters = %+v", out.Encounters)
	}
	dps := metric(t, out, "dps")
	want := []string{compareAvailable, compareLowerBound, compareLowerBound, compareUnavailable}
	for i, value := range dps.Values {
		if value.Status != want[i] || value.Delta != nil {
			t.Errorf("dps %d = %+v, want status %s and no change", i, value, want[i])
		}
	}
	if dps.Values[3].Value != nil {
		t.Fatalf("an encounter without results has a DPS: %+v", dps.Values[3])
	}
	// A known undercount keeps its stored value, marked as a lower bound even where the engine called it available.
	if !near(dps.Values[2].Value, 150) {
		t.Fatalf("known affected dps = %+v", dps.Values[2])
	}
	lily := contributor(t, out, "role", 1512)
	if lily.Values[1].DPSDelta != nil || lily.Values[1].Status != compareLowerBound || lily.Values[3] != nil {
		t.Fatalf("Lily = %+v %+v", lily.Values[1], lily.Values[3])
	}
	if condition(out, "game_version") != "unknown" || condition(out, "mode") != "unknown" || condition(out, "stage") != "unknown" || condition(out, "main_enemy") != "unknown" {
		t.Fatalf("conditions = %+v", out.Context.Conditions)
	}
	if !out.Context.OpenWorld || out.Context.DurationRatio != nil {
		t.Fatalf("context = %+v", out.Context)
	}
	// Two encounters with different known facts are different, never unknown.
	versions := compareEncounters([]compareInput{complete, degraded})
	if condition(versions, "game_version") != "different" || condition(versions, "mode") != "different" || condition(versions, "stage") != "different" || condition(versions, "main_enemy") != "unknown" {
		t.Fatalf("conditions = %+v", versions.Context.Conditions)
	}
}

func TestComparisonNeedsTwoToFourDistinctEncounters(t *testing.T) {
	for _, ids := range [][]int64{nil, {1}, {1, 1}, {1, 2, 3, 4, 5}} {
		if compareIDs(ids) == nil {
			t.Errorf("%v was accepted", ids)
		}
	}
	for _, ids := range [][]int64{{1, 2}, {4, 3, 2, 1}} {
		if err := compareIDs(ids); err != nil {
			t.Errorf("%v: %v", ids, err)
		}
	}
}
