package capture

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func roleOutgoing(statistics *DamageStatistics, roleID uint64, amount int64, seconds float64) {
	record := testEncounterRecord(CombatFlowLocalOutgoing, amount, seconds)
	record.SourceRole = &CombatRole{PlayerID: 7, RoleID: roleID, Basis: roleBasisRoleEntity}
	statistics.addCombatEvent(record)
}

func summonOutgoing(statistics *DamageStatistics, roleID uint64, amount int64, seconds float64) {
	record := testEncounterRecord(CombatFlowLocalOutgoing, amount, seconds)
	record.SourceRole = &CombatRole{PlayerID: 7, RoleID: roleID, Basis: roleBasisSummonerChain}
	statistics.addCombatEvent(record)
}

func vehicleOutgoing(statistics *DamageStatistics, amount int64, seconds float64) {
	record := testEncounterRecord(CombatFlowLocalOutgoing, amount, seconds)
	record.VehicleKind = vehicleKindMotorcycle
	statistics.addCombatEvent(record)
}

func timelineNow(statistics *DamageStatistics, seconds float64) *EncounterTimelineSnapshot {
	snapshot := statistics.Snapshot()
	return statistics.TimelineAt(CaptureQueryTime{CaptureSessionID: snapshot.CaptureSessionID, Timestamp: encounterAt(seconds)}, 0)
}

// requireTimelineConservation checks one timeline against its encounter: the points add up to the confirmed local
// damage dealt, each contributor to its role, vehicle, or unassigned total, and the intervals tile the encounter span.
func requireTimelineConservation(t *testing.T, timeline EncounterTimelineSnapshot, encounter EncounterSnapshot) {
	t.Helper()
	if err := timelineConservationError(timeline, encounter); err != nil {
		t.Fatal(err)
	}
}

func timelineConservationError(timeline EncounterTimelineSnapshot, encounter EncounterSnapshot) error {
	if timeline.EncounterID != encounter.EncounterID {
		return fmt.Errorf("timeline %s describes another encounter than %s", timeline.EncounterID, encounter.EncounterID)
	}
	var sum int64
	perContributor := make([]int64, len(timeline.Contributors))
	duration := 0.0
	for i, point := range timeline.Points {
		var parts int64
		for c, amount := range point.ContributorDamage {
			parts += amount
			perContributor[c] += amount
		}
		if parts != point.Damage {
			return fmt.Errorf("point %d: contributors add up to %d, point says %d", i, parts, point.Damage)
		}
		if point.Damage < 0 || point.DurationSeconds < 0 {
			return fmt.Errorf("point %d is negative: %+v", i, point)
		}
		if i > 0 && math.Abs(timeline.Points[i-1].StartSeconds+timeline.Points[i-1].DurationSeconds-point.StartSeconds) > 1e-6 {
			return fmt.Errorf("point %d does not start where point %d ended", i, i-1)
		}
		if point.State == TimelinePointComplete && math.Abs(point.DurationSeconds-timeline.IntervalSeconds) > 1e-9 {
			return fmt.Errorf("complete point %d lasts %.3f s", i, point.DurationSeconds)
		}
		sum += point.Damage
		duration += point.DurationSeconds
	}
	if sum != encounter.Totals.DamageDealt || timeline.DamageDealt != encounter.Totals.DamageDealt {
		return fmt.Errorf("timeline damage %d (reported %d) != encounter damage dealt %d", sum, timeline.DamageDealt, encounter.Totals.DamageDealt)
	}
	if len(timeline.Points) > 0 && math.Abs(duration-encounter.EncounterDPS.Duration.Seconds()) > 1e-6 {
		return fmt.Errorf("timeline spans %.6f s, Encounter DPS uses %.6f s", duration, encounter.EncounterDPS.Duration.Seconds())
	}
	want := map[TimelineContributor]int64{}
	for _, role := range encounter.Roles {
		if role.DamageDealt > 0 {
			want[TimelineContributor{Kind: TimelineContributorRole, PlayerID: role.PlayerID, RoleID: role.RoleID}] = role.DamageDealt
		}
	}
	if encounter.Vehicle != nil && encounter.Vehicle.DamageDealt > 0 {
		want[TimelineContributor{Kind: TimelineContributorVehicle, VehicleKind: encounter.Vehicle.Kind}] = encounter.Vehicle.DamageDealt
	}
	if encounter.UnassignedRole.DamageDealt > 0 {
		want[TimelineContributor{Kind: TimelineContributorUnassigned}] = encounter.UnassignedRole.DamageDealt
	}
	if len(want) != len(timeline.Contributors) {
		return fmt.Errorf("timeline has %d contributors, encounter %d: %+v", len(timeline.Contributors), len(want), timeline.Contributors)
	}
	for c, contributor := range timeline.Contributors {
		if want[contributor] != perContributor[c] {
			return fmt.Errorf("contributor %+v: timeline %d, encounter %d", contributor, perContributor[c], want[contributor])
		}
	}
	return nil
}

func pointDPS(t *testing.T, point TimelinePoint) float64 {
	t.Helper()
	if point.DPS == nil {
		t.Fatalf("point without DPS: %+v", point)
	}
	return *point.DPS
}

func TestTimelineOneSecondIntervalsZeroGapsAndFinalPartial(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(10), 1)
	roleOutgoing(statistics, 1102, 100, 10.2)
	roleOutgoing(statistics, 1102, 50, 10.9) // same second
	roleOutgoing(statistics, 1102, 30, 11.0) // exactly on a boundary: the next interval
	roleOutgoing(statistics, 1102, 70, 13.3)
	statistics.observeBattleState(false, encounterAt(13.4), 1)
	statistics.observeCaptureTime(encounterAt(15))

	encounter := requireOneCompleted(t, statistics.Snapshot())
	timeline := timelineNow(statistics, 15)
	if timeline == nil || !timeline.Final || timeline.IntervalSeconds != 1 || len(timeline.Points) != 4 {
		t.Fatalf("unexpected timeline: %+v", timeline)
	}
	requireTimelineConservation(t, *timeline, encounter)
	want := []struct {
		damage int64
		state  TimelinePointState
		dps    float64
	}{{150, TimelinePointComplete, 150}, {30, TimelinePointComplete, 30}, {0, TimelinePointComplete, 0}, {70, TimelinePointPartial, 70 / 0.4}}
	for i, w := range want {
		point := timeline.Points[i]
		if point.Damage != w.damage || point.State != w.state || math.Abs(pointDPS(t, point)-w.dps) > 1e-6 {
			t.Fatalf("point %d = %+v (dps %v), want %+v", i, point, pointDPS(t, point), w)
		}
	}
	// The zero-damage second stays on the timeline with DPS 0, not as a gap.
	if timeline.Points[2].DPS == nil || *timeline.Points[2].DPS != 0 {
		t.Fatal("a second without damage must be reported as 0 DPS")
	}
}

func TestTimelineSplitsRolesVehicleAndUnassignedLikeTheRoleTotals(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	roleOutgoing(statistics, 1102, 100, 0.5)
	roleOutgoing(statistics, 1409, 200, 0.6)
	summonOutgoing(statistics, 1409, 40, 1.5) // a verified summon is credited to its role
	vehicleOutgoing(statistics, 300, 2.5)
	outgoing(statistics, 25, 3.5) // confirmed local, no role evidence: unassigned
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedOutgoing, 999, 1.2))
	statistics.addCombatEvent(testEncounterRecord(CombatFlowLocalIncoming, 77, 1.3))
	statistics.observeBattleState(false, encounterAt(5), 1)
	statistics.observeCaptureTime(encounterAt(7))

	encounter := requireOneCompleted(t, statistics.Snapshot())
	timeline := timelineNow(statistics, 7)
	requireTimelineConservation(t, *timeline, encounter)
	if timeline.DamageDealt != 665 {
		t.Fatalf("UNKNOWN ownership or damage taken entered the timeline: %d", timeline.DamageDealt)
	}
	kinds := []string{}
	for _, contributor := range timeline.Contributors {
		kinds = append(kinds, fmt.Sprintf("%s:%d", contributor.Kind, contributor.RoleID))
	}
	if fmt.Sprint(kinds) != "[role:1102 role:1409 vehicle:0 unassigned:0]" {
		t.Fatalf("contributor order = %v", kinds)
	}
	if fmt.Sprint(timeline.Points[1].ContributorDamage) != "[0 40 0 0]" || fmt.Sprint(timeline.Points[2].ContributorDamage) != "[0 0 300 0]" {
		t.Fatalf("points = %+v", timeline.Points)
	}
}

func TestTimelineOfAnOpenEncounterAdvancesWithTheCaptureClock(t *testing.T) {
	statistics := NewDamageStatistics()
	roleOutgoing(statistics, 1102, 100, 20.0) // opens by first confirmed activity
	statistics.observeCaptureTime(encounterAt(22.5))

	timeline := timelineNow(statistics, 22.5)
	if timeline.Final || len(timeline.Points) != 3 || timeline.Points[2].State != TimelinePointInProgress ||
		timeline.Points[0].State != TimelinePointComplete || timeline.Points[1].Damage != 0 {
		t.Fatalf("open timeline = %+v", timeline)
	}
	// Quiet time keeps adding zero intervals; the live Encounter DPS uses the same end.
	later := timelineNow(statistics, 25)
	current := statistics.SnapshotAt(CaptureQueryTime{CaptureSessionID: statistics.Snapshot().CaptureSessionID, Timestamp: encounterAt(25)}).Encounters.Current
	if len(later.Points) != 5 || later.Points[4].State != TimelinePointComplete || current == nil {
		t.Fatalf("timeline at 25 s = %+v", later)
	}
	requireTimelineConservation(t, *later, *current)
	// A query older than the newest observed capture time never shortens it.
	if older := timelineNow(statistics, 21); len(older.Points) != 3 {
		t.Fatalf("an older query shortened the open timeline: %d points", len(older.Points))
	}
}

func TestTimelineIgnoresDuplicatesAndKeepsEncountersApart(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	record := testEncounterRecord(CombatFlowLocalOutgoing, 500, 0.5)
	statistics.addCombatEvent(record)
	statistics.addCombatEvent(record) // a repeated response
	statistics.observeBattleState(false, encounterAt(2), 1)
	statistics.observeCaptureTime(encounterAt(5))
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 80, 10.5)

	all := statistics.encounterTimelines()
	if len(all) != 2 || all[0].DamageDealt != 500 || all[1].DamageDealt != 80 || all[0].EncounterID == all[1].EncounterID || !all[0].Final || all[1].Final {
		t.Fatalf("timelines = %+v", all)
	}
	if focus := timelineNow(statistics, 11); focus.EncounterID != all[1].EncounterID {
		t.Fatalf("the shown timeline is not the open encounter: %s", focus.EncounterID)
	}

	// A new statistics interval starts with no timeline at all.
	statistics.Reset()
	if focus := timelineNow(statistics, 12); focus != nil {
		t.Fatalf("a reset interval kept a timeline: %+v", focus)
	}
	statistics.observeBattleState(true, encounterAt(13), 1)
	outgoing(statistics, 9, 13.2)
	if focus := timelineNow(statistics, 14); focus == nil || focus.DamageDealt != 9 || focus.EncounterID == all[1].EncounterID {
		t.Fatalf("timeline after reset = %+v", focus)
	}
}

func TestTimelineEndsWithAProtocolSessionChange(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	outgoing(statistics, 100, 1.5)
	statistics.observeSessionChange(encounterAt(3.25))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	timeline := statistics.encounterTimelines()[0]
	requireTimelineConservation(t, timeline, encounter)
	if !timeline.Final || len(timeline.Points) != 4 || timeline.Points[3].State != TimelinePointPartial {
		t.Fatalf("interrupted timeline = %+v", timeline)
	}
}

func TestTimelineAbsorbsAStartMovedBackWithoutLosingDamage(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(10), 1)
	outgoing(statistics, 100, 10.5)
	outgoing(statistics, 60, 9.4) // a late response observed shortly before the enter signal moves the start back
	statistics.observeBattleState(false, encounterAt(12), 1)
	statistics.observeCaptureTime(encounterAt(14))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	timeline := statistics.encounterTimelines()[0]
	requireTimelineConservation(t, timeline, encounter)
	if timeline.Points[0].State != TimelinePointPartial || timeline.Points[0].Damage != 60 || math.Abs(timeline.Points[0].DurationSeconds-0.6) > 1e-9 {
		t.Fatalf("first point after a moved start = %+v", timeline.Points[0])
	}
}

func TestTimelineLongEncounterStaysBoundedAndExact(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	seconds := maxTimelineBuckets + 50
	var total int64
	for i := 0; i < seconds; i++ {
		amount := int64(i%7 + 1)
		roleOutgoing(statistics, 1102, amount, float64(i)+0.5)
		total += amount
	}
	statistics.observeBattleState(false, encounterAt(float64(seconds)), 1)
	statistics.observeCaptureTime(encounterAt(float64(seconds) + 2))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	full := statistics.encounterTimelines()[0]
	requireTimelineConservation(t, full, encounter)
	if full.IntervalSeconds != 2 || full.DamageDealt != total {
		t.Fatalf("long timeline interval %.0f s, damage %d of %d", full.IntervalSeconds, full.DamageDealt, total)
	}
	view := statistics.TimelineAt(CaptureQueryTime{}, 120)
	if len(view.Points) > 120 || view.IntervalSeconds != 300 {
		t.Fatalf("display timeline: %d points of %.0f s", len(view.Points), view.IntervalSeconds)
	}
	requireTimelineConservation(t, *view, encounter)
}

func TestTimelineDisplayBoundUsesRoundExactIntervals(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	for i := 0; i < 600; i++ {
		roleOutgoing(statistics, 1102, 10, float64(i)+0.25)
	}
	statistics.observeBattleState(false, encounterAt(600), 1)
	statistics.observeCaptureTime(encounterAt(602))
	encounter := requireOneCompleted(t, statistics.Snapshot())
	view := statistics.TimelineAt(CaptureQueryTime{}, 120)
	if view.IntervalSeconds != 5 || len(view.Points) != 120 {
		t.Fatalf("display = %.0f s x %d", view.IntervalSeconds, len(view.Points))
	}
	for _, point := range view.Points {
		if point.Damage != 50 || pointDPS(t, point) != 10 || point.State != TimelinePointComplete {
			t.Fatalf("display point = %+v", point)
		}
	}
	requireTimelineConservation(t, *view, encounter)
}

func TestTimelineSnapshotsAreIndependentCopies(t *testing.T) {
	statistics := NewDamageStatistics()
	roleOutgoing(statistics, 1102, 100, 1.2)
	first := timelineNow(statistics, 2.5)
	first.Points[0].Damage = -1
	first.Points[0].ContributorDamage[0] = -1
	first.Contributors[0].RoleID = 0
	again := timelineNow(statistics, 2.5)
	if again.Points[0].Damage != 100 || again.Points[0].ContributorDamage[0] != 100 || again.Contributors[0].RoleID != 1102 {
		t.Fatalf("a caller changed engine state through a snapshot: %+v", again)
	}
}

func TestTimelineIsTheSameForEveryArrivalPacing(t *testing.T) {
	// The same events applied with different capture-clock observations in between (as different replay speeds would
	// interleave snapshot queries) must give the same final timeline.
	build := func(queries bool) []EncounterTimelineSnapshot {
		encounterTestRPC = 1000
		statistics := NewDamageStatistics()
		statistics.observeBattleState(true, encounterAt(0), 1)
		for i := 0; i < 40; i++ {
			roleOutgoing(statistics, uint64(1102+i%3), int64(10+i), float64(i)*0.37)
			if queries {
				_ = timelineNow(statistics, float64(i)*0.37+0.1)
			}
		}
		statistics.observeBattleState(false, encounterAt(16), 1)
		statistics.observeCaptureTime(encounterAt(20))
		return statistics.encounterTimelines()
	}
	a, b := build(false), build(true)
	if left, right := timelineText(stripTimelineIDs(a)), timelineText(stripTimelineIDs(b)); left != right {
		t.Fatalf("snapshot queries changed the timeline:\n%s\n%s", left, right)
	}
}

func stripTimelineIDs(timelines []EncounterTimelineSnapshot) []EncounterTimelineSnapshot {
	out := append([]EncounterTimelineSnapshot{}, timelines...)
	for i := range out {
		out[i].EncounterID = ""
	}
	return out
}

func TestTimelineWithoutEncounterIsAbsent(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.addCombatEvent(testEncounterRecord(CombatFlowUnattributedOutgoing, 10, 1))
	if timelineNow(statistics, 2) != nil {
		t.Fatal("a timeline appeared without an encounter")
	}
	var nilStatistics *DamageStatistics
	if nilStatistics.TimelineAt(CaptureQueryTime{}, 0) != nil {
		t.Fatal("nil statistics returned a timeline")
	}
}

// timelineText prints values, not pointers, so two equal timelines print the same.
func timelineText(timelines []EncounterTimelineSnapshot) string {
	text := ""
	for _, timeline := range timelines {
		text += fmt.Sprintf("%s %t %v %v %v %d %v|", timeline.EncounterID, timeline.Final, timeline.IntervalSeconds, timeline.StartedAt.Format(time.RFC3339Nano), timeline.EndAt.Format(time.RFC3339Nano), timeline.DamageDealt, timeline.Contributors)
		for _, point := range timeline.Points {
			dps := "nil"
			if point.DPS != nil {
				dps = fmt.Sprint(*point.DPS)
			}
			text += fmt.Sprintf("%v/%v/%s/%d/%s/%v;", point.StartSeconds, point.DurationSeconds, point.State, point.Damage, dps, point.ContributorDamage)
		}
	}
	return text
}
