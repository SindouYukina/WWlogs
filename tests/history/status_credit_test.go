package history

import (
	"context"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// Negative-status damage the engine credited to a role (capture/negative_status_credit.go) is stored as an event with the
// credited source role and is rebuilt from the events on read: the role's status damage and rows, the analysis rows and
// hits, and the unassigned negative-status damage, which is only what no role was credited with.
//
//	t=1 s  role 1409 hits the enemy with skill 140901 for 1000
//	t=2 s  an Aero Erosion tick (DamageId 1001) of the status role 1409 owns, 1500
//	t=3 s  an Aero Erosion tick role 1505 forced, 300
//	t=4 s  an Aero Erosion tick no role could be credited with, 200
const (
	statusPlayer   = 7
	statusOwner    = 1409
	statusTrigger  = 1505
	statusAeroDmg  = 1001
	statusSkill    = 140901
	statusDuration = 10 * time.Second
)

func newStatusCreditFixture(t *testing.T) (*Store, int64) {
	t.Helper()
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC).Local()
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	key := "1-1"
	enemy := capture.CombatEndpoint{EntityID: 100, Relation: "UNRELATED"}
	scene := capture.CombatEndpoint{EntityID: 900, Relation: "LOCAL"}
	monster := &capture.CombatActorType{MonsterID: 340000040, MonsterStatus: "CORRELATED"}
	role := func(id uint64, basis string) *capture.CombatRole {
		return &capture.CombatRole{PlayerID: statusPlayer, RoleID: id, Basis: basis}
	}
	events := []capture.CombatEventRecord{
		{Kind: capture.CombatEventDamage, ObservedAt: at(1000), Flow: capture.CombatFlowLocalOutgoing, Amount: 1000, SkillID: statusSkill, DamageID: 11, Evidence: "rpc",
			Source: capture.CombatEndpoint{EntityID: 201, Relation: "LOCAL"}, Target: enemy, SourceRole: role(statusOwner, "player_scene_group_role"), TargetActor: monster},
		{Kind: capture.CombatEventDamage, ObservedAt: at(2000), Flow: capture.CombatFlowLocalOutgoing, Amount: 1500, DamageID: statusAeroDmg, Evidence: capture.CombatEvidenceNegativeStatusNotify,
			Source: scene, Target: enemy, SourceRole: role(statusOwner, capture.RoleBasisNegativeStatusOwner), TargetActor: monster},
		{Kind: capture.CombatEventDamage, ObservedAt: at(3000), Flow: capture.CombatFlowLocalOutgoing, Amount: 300, DamageID: statusAeroDmg, Evidence: capture.CombatEvidenceNegativeStatusNotify,
			Source: scene, Target: enemy, SourceRole: role(statusTrigger, capture.RoleBasisNegativeStatusTrigger), TargetActor: monster},
		{Kind: capture.CombatEventDamage, ObservedAt: at(4000), Flow: capture.CombatFlowLocalOutgoing, Amount: 200, DamageID: statusAeroDmg, Evidence: capture.CombatEvidenceNegativeStatusNotify,
			Source: scene, Target: enemy, TargetActor: monster},
	}
	end := at(10000)
	snapshot := capture.EncounterSnapshot{
		StatisticsIntervalID: 1, Sequence: 1, ProtocolSession: 1, State: capture.EncounterFinished, Outcome: capture.EncounterOutcomeConfirmed,
		StartedAt: start, StartBasis: "battle_state_enter", StartConfirmed: true, BattleEntered: start, EndedAt: end, EndBasis: "battle_state_exit", EndDecidedAt: end.Add(time.Second),
		FirstActivityAt: at(1000), LastActivityAt: at(4000),
		Totals:    capture.EncounterTotals{DamageDealt: 3000, OutgoingEvents: 4},
		Integrity: capture.DataIntegrity{Status: capture.IntegrityIntact, Risks: []string{}},
		EncounterDPS: capture.DPSResult{Status: capture.DPSStatusAvailable, Damage: 3000, Duration: statusDuration, Value: 300,
			StartObservedAt: start, EndObservedAt: end, Confidence: "confirmed"},
		PeakDPS: capture.DPSResult{Status: capture.DPSStatusAvailable, Damage: 1500, Duration: time.Second, Value: 1500, StartObservedAt: at(2000), EndObservedAt: at(3000), Confidence: "confirmed"},
		Healing: capture.HealingResult{Status: capture.DPSStatusAvailable},
		Roles: []capture.RoleDamageSnapshot{
			{PlayerID: statusPlayer, RoleID: statusOwner, DamageDealt: 2500, OutgoingEvents: 2, DirectDamage: 1000, StatusDamage: 1500,
				Skills:   []capture.SkillDamageSnapshot{{SkillID: statusSkill, DamageDealt: 1000, OutgoingEvents: 1, DamageIDs: []uint64{11}}},
				Statuses: []capture.StatusDamageSnapshot{{DamageID: statusAeroDmg, DamageDealt: 1500, OutgoingEvents: 1}}},
			{PlayerID: statusPlayer, RoleID: statusTrigger, DamageDealt: 300, OutgoingEvents: 1, StatusDamage: 300,
				Skills:   []capture.SkillDamageSnapshot{},
				Statuses: []capture.StatusDamageSnapshot{{DamageID: statusAeroDmg, DamageDealt: 300, OutgoingEvents: 1, TriggerEvents: 1, TriggerDamage: 300}}},
		},
		UnassignedRole: capture.UnassignedRoleTotals{DamageDealt: 200, OutgoingEvents: 1, NegativeStatusDamage: 200, NegativeStatusEvents: 1},
	}
	contributors := []capture.TimelineContributor{{Kind: "role", PlayerID: statusPlayer, RoleID: statusOwner}, {Kind: "role", PlayerID: statusPlayer, RoleID: statusTrigger}, {Kind: "unassigned"}}
	timeline := capture.StoredTimeline{Anchor: start, ScaleSeconds: 1, Contributors: contributors, Buckets: []capture.StoredTimelineBucket{
		{Index: 1, Contributor: 0, Damage: 1000}, {Index: 2, Contributor: 0, Damage: 1500}, {Index: 3, Contributor: 1, Damage: 300}, {Index: 4, Contributor: 2, Damage: 200},
	}}
	enemies := []capture.EncounterEnemySnapshot{{Ordinal: 0, MonsterID: 340000040, MonsterStatus: "CORRELATED", DamageTaken: 3000, HitsTaken: 4, FirstSeenAt: at(1000), LastSeenAt: at(4000)}}
	entries := []capture.HistoryEntry{
		{Kind: capture.HistoryEncounterOpened, Encounter: key, Opened: &capture.HistoryEncounterOpening{ProtocolSession: 1, StatisticsInterval: 1, Sequence: 1, StartedAt: start, StartBasis: "battle_state_enter"}},
		{Kind: capture.HistoryEncounterActive, Encounter: key},
	}
	for i := range events {
		sequence := uint64(i + 1)
		events[i].Key = capture.CombatEventKey{ProtocolSession: 1, Notify: sequence << 16}
		if events[i].Evidence == "rpc" {
			events[i].Key = capture.CombatEventKey{ProtocolSession: 1, RPCID: sequence}
		}
		events[i].Confidence = "confirmed"
		entries = append(entries, capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &events[i]},
			capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: sequence, Encounter: key})
	}
	entries = append(entries, capture.HistoryEntry{Kind: capture.HistoryEncounterFinished, Encounter: key, Finished: &capture.HistoryEncounter{Snapshot: snapshot, Timeline: timeline, Enemies: enemies}})
	record(t, store, liveStart, entries)
	day, err := store.Day(context.Background(), start.UTC().Format(dateLayout), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	return store, day.Encounters[0].ID
}

func TestStoredStatusCreditIsReadBackFromTheEvents(t *testing.T) {
	store, id := newStatusCreditFixture(t)
	detail, err := store.Encounter(context.Background(), id, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	owner, trigger := roleRow(detail.Snapshot, statusOwner), roleRow(detail.Snapshot, statusTrigger)
	if owner.DamageDealt != 2500 || owner.DirectDamage != 1000 || owner.StatusDamage != 1500 || len(owner.Statuses) != 1 ||
		owner.Statuses[0] != (capture.StatusDamageSnapshot{DamageID: statusAeroDmg, DamageDealt: 1500, OutgoingEvents: 1}) {
		t.Fatalf("status owner = %+v", owner)
	}
	if trigger.DamageDealt != 300 || trigger.StatusDamage != 300 || len(trigger.Statuses) != 1 ||
		trigger.Statuses[0] != (capture.StatusDamageSnapshot{DamageID: statusAeroDmg, DamageDealt: 300, OutgoingEvents: 1, TriggerEvents: 1, TriggerDamage: 300}) {
		t.Fatalf("triggering role = %+v", trigger)
	}
	if unassigned := detail.Snapshot.UnassignedRole; unassigned.DamageDealt != 200 || unassigned.NegativeStatusDamage != 200 || unassigned.NegativeStatusEvents != 1 {
		t.Fatalf("unassigned = %+v; only the tick no role was credited with is unassigned", unassigned)
	}
	// The projected view names each status row by the status's glossary term (none in this nil Master) and keeps the split.
	var masters *capture.MasterData
	view := masters.ProjectEncounter(*detail.Snapshot)
	for _, role := range view.Roles {
		if role.RoleID == statusOwner && (role.StatusDamage != 1500 || len(role.Statuses) != 1 || role.Statuses[0].DamageID != statusAeroDmg || role.Statuses[0].NameStatus != "UNKNOWN") {
			t.Fatalf("projected owner = %+v", role)
		}
	}
	if completeness := view.Completeness; completeness.RoleAttributionComplete || !completeness.Conserved {
		t.Fatalf("completeness %+v; the unassigned tick leaves role attribution incomplete", completeness)
	}
}

func TestAnalysisRebuildsTheStatusSplit(t *testing.T) {
	store, id := newStatusCreditFixture(t)
	analysis, err := store.Analysis(context.Background(), id, AnalysisQuery{Enemy: -1}, nil)
	if err != nil || analysis.State != AnalysisReady {
		t.Fatalf("analysis = %s (%v)", analysis.State, err)
	}
	if !analysis.Consistent || analysis.Mismatch != "" {
		t.Fatalf("the full range does not rebuild the stored result: %q", analysis.Mismatch)
	}
	owner := roleRow(analysis.Snapshot, statusOwner)
	if owner.StatusDamage != 1500 || len(owner.Statuses) != 1 || owner.DirectDamage != 1000 || len(owner.Skills) != 1 || owner.UnassignedSkill.OutgoingEvents != 0 {
		t.Fatalf("rebuilt owner = %+v", owner)
	}
	stats := map[string]SkillStats{}
	for _, stat := range analysis.Skills {
		key := stat.Contributor
		if stat.StatusDamageID != 0 {
			key += ":status"
		}
		stats[key] = stat
	}
	if stat := stats["role-7-1409:status"]; stat.StatusDamageID != statusAeroDmg || stat.SkillID != 0 || stat.Hits != 1 || stat.MaxHit != 1500 {
		t.Fatalf("owner status stats = %+v", stat)
	}
	if stat := stats["role-7-1505:status"]; stat.Hits != 1 || stat.MaxHit != 300 {
		t.Fatalf("trigger status stats = %+v", stat)
	}
	// The hit timeline lists the status rows by their own key, before any row without a skill.
	hits, err := store.Hits(context.Background(), id, AnalysisQuery{Enemy: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]HitRow{}
	for _, row := range hits.Rows {
		rows[row.SkillRow] = row
	}
	if row, ok := rows["role-7-1409:status:1001"]; !ok || row.StatusDamageID != statusAeroDmg || len(row.Amount) != 1 || row.Amount[0] != 1500 {
		t.Fatalf("hit rows = %+v", hits.Rows)
	}
	if hits.Total != 4 {
		t.Fatalf("hits total = %d", hits.Total)
	}
	// A range holding only the forced tick credits it to the triggering role alone.
	part, err := store.Analysis(context.Background(), id, AnalysisQuery{FromMs: 2500, ToMs: 3500, Enemy: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if role := roleRow(part.Snapshot, statusTrigger); role.StatusDamage != 300 || role.Statuses[0].TriggerDamage != 300 {
		t.Fatalf("part = %+v", part.Snapshot.Roles)
	}
	if role := roleRow(part.Snapshot, statusOwner); role.DamageDealt != 0 {
		t.Fatalf("part owner = %+v", role)
	}
}

// A negative-status tick is no hit of a role: the largest-hit record keeps naming a skill row.
func TestLargestHitLeavesStatusTicksOut(t *testing.T) {
	store, id := newStatusCreditFixture(t)
	hits, err := store.maxHits(context.Background(), []int64{id})
	if err != nil {
		t.Fatal(err)
	}
	if hit := hits[id][statusOwner]; hit.Amount != 1000 || hit.SkillID != statusSkill {
		t.Fatalf("largest hit of role 1409 = %+v; the 1500 status tick is not a hit", hit)
	}
	if _, ok := hits[id][statusTrigger]; ok {
		t.Fatalf("largest hits = %+v; role 1505 has only a status tick", hits[id])
	}
}
