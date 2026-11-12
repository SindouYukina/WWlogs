package history

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// analysisFixture is one finished encounter of ten seconds with its stored results and effect changes, consistent with what
// the engine would have journaled for them:
//
//	t=1.0 s  role 1505 hits enemy A with skill 150501 for 1000     t=2.5 s  the same for 500
//	t=3.0 s  role 1206 hits enemy B with skill 120601 for 700      t=4.0 s  enemy A hits role 1505 for 120
//	t=5.0 s  role 1206 heals role 1505: 90 reported, 40 applied    t=6.0 s  role 1505 hits enemy A without a SkillId for 200
//	t=7.0 s  a summon of role 1206 hits enemy B with 120602 for 300 t=8.0 s  unassigned local damage to enemy A for 50
//	t=9.0 s  unattributed outgoing damage of 7
//
// Effects: 1001 on role 1505 applied at 0.5 s, stacked at 2 s, removed at 3 s; 2002 on enemy A from role 1505 applied at
// 4 s for 2 s and never heard of again; 3003 already active on role 1206 at its snapshot at 5 s, without a duration; an
// instant 4004 at 6 s; 5005 on role 1505 applied 5 s before the encounter for 100 s, stacked at 7 s and removed 2 s after
// it; and 6006 applied 30 s before for 5 s and never heard of again (stale).
type analysisFixture struct {
	store *Store
	id    int64
	start time.Time
}

const (
	fixturePlayer = 7
	roleA         = 1505
	roleB         = 1206
)

func newAnalysisFixture(t *testing.T) analysisFixture {
	t.Helper()
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC).Local()
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	key := "1-1"
	enemyA := capture.CombatEndpoint{EntityID: 100, Relation: "UNRELATED"}
	enemyB := capture.CombatEndpoint{EntityID: 101, Relation: "UNRELATED"}
	roleEntity := func(role uint64) capture.CombatEndpoint {
		return capture.CombatEndpoint{EntityID: 200 + role, Relation: "LOCAL"}
	}
	monsterA := &capture.CombatActorType{MonsterID: 340000040, MonsterStatus: "CORRELATED"}
	monsterB := &capture.CombatActorType{MonsterID: 340000041, MonsterStatus: "CORRELATED"}
	role := func(id uint64, basis string) *capture.CombatRole {
		return &capture.CombatRole{PlayerID: fixturePlayer, RoleID: id, Basis: basis}
	}
	events := []capture.CombatEventRecord{
		{Kind: capture.CombatEventDamage, ObservedAt: at(1000), Flow: capture.CombatFlowLocalOutgoing, Amount: 1000, SkillID: 150501, DamageID: 11, Source: roleEntity(roleA), Target: enemyA, SourceRole: role(roleA, "player_scene_group_role"), TargetActor: monsterA},
		{Kind: capture.CombatEventDamage, ObservedAt: at(2500), Flow: capture.CombatFlowLocalOutgoing, Amount: 500, SkillID: 150501, DamageID: 11, Source: roleEntity(roleA), Target: enemyA, SourceRole: role(roleA, "player_scene_group_role"), TargetActor: monsterA},
		{Kind: capture.CombatEventDamage, ObservedAt: at(3000), Flow: capture.CombatFlowLocalOutgoing, Amount: 700, SkillID: 120601, DamageID: 21, Source: roleEntity(roleB), Target: enemyB, SourceRole: role(roleB, "player_scene_group_role"), TargetActor: monsterB},
		{Kind: capture.CombatEventDamage, ObservedAt: at(4000), Flow: capture.CombatFlowLocalIncoming, Amount: 120, Source: enemyA, Target: roleEntity(roleA), TargetRole: role(roleA, "player_scene_group_role"), SourceActor: monsterA},
		{Kind: capture.CombatEventHealing, ObservedAt: at(5000), Flow: capture.CombatFlowLocalInternal, Amount: 90, AppliedHealing: 40, Source: roleEntity(roleB), Target: roleEntity(roleA), SourceRole: role(roleB, "player_scene_group_role"), TargetRole: role(roleA, "player_scene_group_role")},
		{Kind: capture.CombatEventDamage, ObservedAt: at(6000), Flow: capture.CombatFlowLocalOutgoing, Amount: 200, Source: roleEntity(roleA), Target: enemyA, SourceRole: role(roleA, "player_scene_group_role"), TargetActor: monsterA},
		{Kind: capture.CombatEventDamage, ObservedAt: at(7000), Flow: capture.CombatFlowLocalOutgoing, Amount: 300, SkillID: 120602, DamageID: 22, Source: capture.CombatEndpoint{EntityID: 300, Relation: "LOCAL"}, Target: enemyB, SourceRole: role(roleB, "summoner_chain"), TargetActor: monsterB},
		{Kind: capture.CombatEventDamage, ObservedAt: at(8000), Flow: capture.CombatFlowLocalOutgoing, Amount: 50, Source: capture.CombatEndpoint{EntityID: 301, Relation: "LOCAL"}, Target: enemyA, TargetActor: monsterA},
		{Kind: capture.CombatEventDamage, ObservedAt: at(9000), Flow: capture.CombatFlowUnattributedOutgoing, Amount: 7, Source: capture.CombatEndpoint{EntityID: 302, Relation: "UNKNOWN"}, Target: enemyB},
	}
	end := at(10000)
	snapshot := capture.EncounterSnapshot{
		StatisticsIntervalID: 1, Sequence: 1, ProtocolSession: 1, State: capture.EncounterFinished, Outcome: capture.EncounterOutcomeConfirmed,
		StartedAt: start, StartBasis: "battle_state_enter", StartConfirmed: true, BattleEntered: start, EndedAt: end, EndBasis: "battle_state_exit", EndDecidedAt: end.Add(time.Second),
		FirstActivityAt: at(1000), LastActivityAt: at(8000),
		Totals:    capture.EncounterTotals{DamageDealt: 2750, DamageTaken: 120, OutgoingEvents: 6, IncomingEvents: 1, UnattributedOutgoingDamage: 7, UnattributedOutgoingEvents: 1},
		Integrity: capture.DataIntegrity{Status: capture.IntegrityIntact, Risks: []string{}},
		EncounterDPS: capture.DPSResult{Status: capture.DPSStatusAvailable, Damage: 2750, Duration: 10 * time.Second, Value: 275,
			StartObservedAt: start, EndObservedAt: end, Confidence: "confirmed"},
		PeakDPS: capture.DPSResult{Status: capture.DPSStatusAvailable, Damage: 1000, Duration: time.Second, Value: 1000, StartObservedAt: at(1000), EndObservedAt: at(2000), Confidence: "confirmed"},
		Healing: capture.HealingResult{Status: capture.DPSStatusAvailable, HealingTotals: capture.HealingTotals{Effective: 40, Reported: 90, Overheal: 50, Events: 1,
			ByHealer: []capture.HealerTotals{{PlayerID: fixturePlayer, RoleID: roleB, Effective: 40, Reported: 90, Events: 1}}}},
		Roles: []capture.RoleDamageSnapshot{
			{PlayerID: fixturePlayer, RoleID: roleB, DamageDealt: 1000, OutgoingEvents: 2, DirectDamage: 700, SummonDamage: 300,
				Skills: []capture.SkillDamageSnapshot{{SkillID: 120601, DamageDealt: 700, OutgoingEvents: 1, DamageIDs: []uint64{21}}, {SkillID: 120602, DamageDealt: 300, OutgoingEvents: 1, DamageIDs: []uint64{22}}}},
			{PlayerID: fixturePlayer, RoleID: roleA, DamageDealt: 1700, OutgoingEvents: 3, DirectDamage: 1700, DamageTaken: 120, IncomingEvents: 1,
				Skills:          []capture.SkillDamageSnapshot{{SkillID: 150501, DamageDealt: 1500, OutgoingEvents: 2, DamageIDs: []uint64{11}}},
				UnassignedSkill: capture.UnassignedSkillTotals{DamageDealt: 200, OutgoingEvents: 1, MissingSkillEvents: 1, DamageIDsIncomplete: true}},
		},
		UnassignedRole: capture.UnassignedRoleTotals{DamageDealt: 50, OutgoingEvents: 1},
	}
	contributors := []capture.TimelineContributor{{Kind: "role", PlayerID: fixturePlayer, RoleID: roleA}, {Kind: "role", PlayerID: fixturePlayer, RoleID: roleB}, {Kind: "unassigned"}}
	timeline := capture.StoredTimeline{Anchor: start, ScaleSeconds: 1, Contributors: contributors, Buckets: []capture.StoredTimelineBucket{
		{Index: 1, Contributor: 0, Damage: 1000}, {Index: 2, Contributor: 0, Damage: 500}, {Index: 3, Contributor: 1, Damage: 700},
		{Index: 6, Contributor: 0, Damage: 200}, {Index: 7, Contributor: 1, Damage: 300}, {Index: 8, Contributor: 2, Damage: 50},
	}}
	enemies := []capture.EncounterEnemySnapshot{
		{Ordinal: 0, MonsterID: 340000040, MonsterStatus: "CORRELATED", DamageTaken: 1750, HitsTaken: 4, DamageDealt: 120, HitsDealt: 1, FirstSeenAt: at(1000), LastSeenAt: at(8000)},
		{Ordinal: 1, MonsterID: 340000041, MonsterStatus: "CORRELATED", DamageTaken: 1000, HitsTaken: 2, FirstSeenAt: at(3000), LastSeenAt: at(7000)},
	}
	entries := []capture.HistoryEntry{
		{Kind: capture.HistoryEncounterOpened, Encounter: key, Opened: &capture.HistoryEncounterOpening{ProtocolSession: 1, StatisticsInterval: 1, Sequence: 1, StartedAt: start, StartBasis: "battle_state_enter"}},
		{Kind: capture.HistoryEncounterActive, Encounter: key},
	}
	effect := func(sequence, instance, afterCombat uint64, change capture.EffectChange, observation string, ms int, effectID uint64, mutate func(*capture.HistoryEffect)) capture.HistoryEntry {
		e := capture.HistoryEffect{Instance: instance, Sequence: sequence, AfterCombat: afterCombat, Change: change, Observation: observation, Evidence: capture.EffectEvidenceServerNotify,
			ObservedAt: at(ms), SessionNumber: 1, HolderEntityID: 200 + roleA, EffectID: effectID, HasEffectID: true, HolderRelation: capture.EffectRelationLocalRole,
			SourceRelation: capture.EffectRelationUnknown, Origin: capture.EffectOriginApply, FirstObservedAt: at(ms),
			HolderActor: &capture.EffectActorIdentity{Role: role(roleA, "player_scene_group_role")}}
		if mutate != nil {
			mutate(&e)
		}
		return capture.HistoryEntry{Kind: capture.HistoryEffectChanged, Sequence: sequence, Effect: &e}
	}
	timed := func(remaining float64, basisMs int) func(*capture.HistoryEffect) {
		return func(e *capture.HistoryEffect) {
			e.TimeKnown, e.Remaining, e.BasisAt, e.Duration, e.HasDuration = true, remaining, at(basisMs), remaining, true
		}
	}
	both := func(first, second func(*capture.HistoryEffect)) func(*capture.HistoryEffect) {
		return func(e *capture.HistoryEffect) { first(e); second(e) }
	}
	stack := func(n uint64) func(*capture.HistoryEffect) {
		return func(e *capture.HistoryEffect) { e.Stack, e.HasStack = n, true }
	}
	// Effects journaled before the encounter belong to the span; they precede its first result.
	entries = append(entries,
		effect(1, 6, 0, capture.EffectChangeApply, capture.EffectObserved, -30000, 6006, timed(5, -30000)),
		effect(2, 5, 0, capture.EffectChangeApply, capture.EffectObserved, -5000, 5005, both(timed(100, -5000), stack(1))),
		effect(3, 1, 0, capture.EffectChangeApply, capture.EffectObserved, 500, 1001, both(timed(3, 500), stack(1))),
	)
	for i := range events {
		sequence := uint64(i + 1)
		events[i].Key = capture.CombatEventKey{ProtocolSession: 1, RPCID: sequence}
		events[i].Confidence, events[i].Evidence = "confirmed", "rpc"
		entries = append(entries, capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &events[i]},
			capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: sequence, Encounter: key})
		switch i {
		case 0:
			entries = append(entries, effect(4, 1, 1, capture.EffectChangeStack, capture.EffectObserved, 2000, 1001, both(timed(3, 500), stack(2))))
		case 2:
			entries = append(entries, effect(5, 1, 3, capture.EffectChangeRemove, capture.EffectObserved, 3000, 1001, func(e *capture.HistoryEffect) { e.Reason = string(capture.EffectLifecycleConfirmedRemoved) }))
		case 3:
			entries = append(entries, effect(6, 2, 4, capture.EffectChangeApply, capture.EffectObserved, 4000, 2002, func(e *capture.HistoryEffect) {
				timed(2, 4000)(e)
				e.HolderEntityID, e.HolderRelation, e.HolderActor = 100, capture.EffectRelationNonPlayer, &capture.EffectActorIdentity{Type: monsterA}
				e.SourceEntityID, e.HasSource, e.SourceRelation = 200+roleA, true, capture.EffectRelationLocalRole
				e.SourceActor = &capture.EffectActorIdentity{Role: role(roleA, "player_scene_group_role")}
			}))
		case 4:
			entries = append(entries, effect(7, 3, 5, capture.EffectChangeSnapshot, capture.EffectObserved, 5000, 3003, func(e *capture.HistoryEffect) {
				e.Origin, e.Evidence, e.HolderEntityID = capture.EffectOriginSnapshot, capture.EffectEvidenceServerSnapshot, 200+roleB
				e.HolderActor = &capture.EffectActorIdentity{Role: role(roleB, "player_scene_group_role")}
			}))
		case 5:
			entries = append(entries, effect(8, 4, 6, capture.EffectChangeInstant, capture.EffectObserved, 6000, 4004, nil))
		case 6:
			entries = append(entries, effect(9, 5, 7, capture.EffectChangeStack, capture.EffectObserved, 7000, 5005, both(timed(100, -5000), stack(2))))
		}
	}
	entries = append(entries, capture.HistoryEntry{Kind: capture.HistoryEncounterFinished, Encounter: key, Finished: &capture.HistoryEncounter{Snapshot: snapshot, Timeline: timeline, Enemies: enemies}},
		effect(10, 5, 9, capture.EffectChangeRemove, capture.EffectObserved, 12000, 5005, nil))
	record(t, store, liveStart, entries)
	day, err := store.Day(context.Background(), start.UTC().Format(dateLayout), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	return analysisFixture{store: store, id: day.Encounters[0].ID, start: start}
}

func (f analysisFixture) analyse(t *testing.T, query AnalysisQuery) EncounterAnalysis {
	t.Helper()
	analysis, err := f.store.Analysis(context.Background(), f.id, query, nil)
	if err != nil || analysis.State != AnalysisReady {
		t.Fatalf("analysis %+v = %s (%v)", query, analysis.State, err)
	}
	return analysis
}

func roleRow(snapshot *capture.EncounterSnapshot, id uint64) capture.RoleDamageSnapshot {
	for _, role := range snapshot.Roles {
		if role.RoleID == id {
			return role
		}
	}
	return capture.RoleDamageSnapshot{}
}

func TestFullRangeRebuildsTheStoredResult(t *testing.T) {
	f := newAnalysisFixture(t)
	analysis := f.analyse(t, AnalysisQuery{Enemy: -1})
	if !analysis.Consistent || analysis.Mismatch != "" || !analysis.Range.Full || analysis.Range.DurationMs != 10000 {
		t.Fatalf("full range: consistent=%v mismatch=%q range=%+v", analysis.Consistent, analysis.Mismatch, analysis.Range)
	}
	detail, err := f.store.Encounter(context.Background(), f.id, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// The full range projects to exactly the encounter the History view and the Dashboard showed.
	var masters *capture.MasterData
	full, stored := masters.ProjectEncounter(*analysis.Snapshot), masters.ProjectEncounter(*detail.Snapshot)
	if full.Totals != stored.Totals || full.DPS != stored.DPS || full.PeakDPS != stored.PeakDPS || full.Healing != stored.Healing || len(full.Roles) != len(stored.Roles) {
		t.Fatalf("full range view differs:\n got %+v\nwant %+v", full, stored)
	}
	for i := range full.Roles {
		if full.Roles[i].DamageDealt != stored.Roles[i].DamageDealt || *full.Roles[i].DPS != *stored.Roles[i].DPS || full.Roles[i].Rank != stored.Roles[i].Rank || len(full.Roles[i].Skills) != len(stored.Roles[i].Skills) {
			t.Fatalf("role %d differs:\n got %+v\nwant %+v", i, full.Roles[i], stored.Roles[i])
		}
	}
	if analysis.Selection.Damage != 2750 || analysis.Selection.DPS.Value != 275 || analysis.Selection.Healing.Reported != 90 {
		t.Fatalf("team selection = %+v", analysis.Selection)
	}
}

func TestRangesPartitionTheEncounter(t *testing.T) {
	f := newAnalysisFixture(t)
	first := f.analyse(t, AnalysisQuery{FromMs: 0, ToMs: 3000, Enemy: -1})
	second := f.analyse(t, AnalysisQuery{FromMs: 3000, ToMs: 10000, Enemy: -1})
	if first.Range.Full || second.Range.Full {
		t.Fatal("a part reads as the full range")
	}
	if first.Snapshot.Totals.DamageDealt != 1500 || second.Snapshot.Totals.DamageDealt != 1250 || first.Snapshot.Totals.DamageTaken != 0 || second.Snapshot.Totals.DamageTaken != 120 {
		t.Fatalf("parts = %+v / %+v", first.Snapshot.Totals, second.Snapshot.Totals)
	}
	if dps := first.Snapshot.EncounterDPS; dps.Status != capture.DPSStatusAvailable || dps.Value != 500 || dps.Duration != 3*time.Second {
		t.Fatalf("first part DPS = %+v", dps)
	}
	if peak := second.Snapshot.PeakDPS; peak.Damage != 700 || peak.Value != 700 {
		t.Fatalf("second part peak = %+v", peak)
	}
	a, b := roleRow(first.Snapshot, roleA), roleRow(second.Snapshot, roleA)
	if a.DamageDealt+b.DamageDealt != 1700 || a.Skills[0].DamageDealt != 1500 || b.UnassignedSkill.DamageDealt != 200 || b.UnassignedSkill.MissingSkillEvents != 1 {
		t.Fatalf("role A split = %+v / %+v", a, b)
	}
	if roleRow(second.Snapshot, roleB).SummonDamage != 300 || roleRow(first.Snapshot, roleB).DamageDealt != 0 {
		t.Fatal("role B's summon damage is not in the second part only")
	}
	// Healing belongs to the range it was observed in; the skill rows keep the encounter's naming evidence.
	if first.Snapshot.Healing.Events != 0 || second.Snapshot.Healing.Effective != 40 || len(a.Skills[0].DamageIDs) != 1 {
		t.Fatalf("healing %+v / %+v", first.Snapshot.Healing, second.Snapshot.Healing)
	}
	// The selected contributor's DPS uses the range's duration, as the ranking rows do.
	selected := f.analyse(t, AnalysisQuery{FromMs: 0, ToMs: 3000, Contributor: roleKey(fixturePlayer, roleA), Enemy: -1})
	if selected.Selection.Damage != 1500 || selected.Selection.DPS.Value != 500 || selected.Selection.Contributor != "role-7-1505" {
		t.Fatalf("selection = %+v", selected.Selection)
	}
	// The last part keeps results at the very end; an empty range is never a negative one.
	if empty := f.analyse(t, AnalysisQuery{FromMs: 9500, ToMs: 9000, Enemy: -1}); empty.Range.FromMs != 9500 || empty.Range.ToMs != 9500 || empty.Snapshot.EncounterDPS.Status != capture.DPSStatusInvalidDuration {
		t.Fatalf("reversed range = %+v %+v", empty.Range, empty.Snapshot.EncounterDPS)
	}
}

func TestEnemyFilterFollowsEachInstance(t *testing.T) {
	f := newAnalysisFixture(t)
	a := f.analyse(t, AnalysisQuery{Enemy: 0})
	b := f.analyse(t, AnalysisQuery{Enemy: 1})
	if a.Query.Enemy != 0 || b.Query.Enemy != 1 {
		t.Fatalf("enemy filters were dropped: %d %d", a.Query.Enemy, b.Query.Enemy)
	}
	if a.Snapshot.Totals.DamageDealt != 1750 || a.Snapshot.Totals.DamageTaken != 120 || b.Snapshot.Totals.DamageDealt != 1000 || b.Snapshot.Totals.UnattributedOutgoingDamage != 0 {
		t.Fatalf("enemy totals = %+v / %+v", a.Snapshot.Totals, b.Snapshot.Totals)
	}
	if a.Snapshot.Healing.Status != capture.DPSStatusUnavailable || a.Snapshot.Healing.Reason != "enemy_filter" {
		t.Fatalf("healing under an enemy filter = %+v", a.Snapshot.Healing)
	}
	// The enemy-filtered timeline is drawn from the same grid: enemy B took damage in seconds 3 and 7 only.
	if !b.TimelineValid || b.Timeline.DamageDealt != 1000 {
		t.Fatalf("enemy B timeline = %+v", b.Timeline)
	}
	for _, point := range b.Timeline.Points {
		if point.Damage != 0 && point.StartSeconds != 3 && point.StartSeconds != 7 {
			t.Fatalf("enemy B damage at %v s", point.StartSeconds)
		}
	}
	if unknown := f.analyse(t, AnalysisQuery{Enemy: 7}); unknown.Query.Enemy != -1 || unknown.Snapshot.Totals.DamageDealt != 2750 {
		t.Fatal("an unknown enemy was not dropped from the query")
	}
	for _, enemy := range f.analyse(t, AnalysisQuery{Enemy: -1}).Enemies {
		if !enemy.Mapped {
			t.Fatalf("enemy %d was not tied to its results", enemy.Ordinal)
		}
	}
}

func TestSkillHitsAreHitsWithTheirLargestAndAverage(t *testing.T) {
	f := newAnalysisFixture(t)
	analysis := f.analyse(t, AnalysisQuery{FromMs: 0, ToMs: 2000, Enemy: -1, SkillRow: "role-7-1505:150501"})
	var row *SkillStats
	for i := range analysis.Skills {
		if analysis.Skills[i].Contributor == "role-7-1505" && analysis.Skills[i].SkillID == 150501 {
			row = &analysis.Skills[i]
		}
	}
	if row == nil || row.Hits != 1 || row.Damage != 1000 || row.MaxHit != 1000 || row.AverageHit != 1000 || len(row.Bins) != analysisSkillBins {
		t.Fatalf("skill row = %+v", row)
	}
	// The distribution covers the whole encounter whatever the range: hits at 1 s and 2.5 s.
	total := int64(0)
	for _, bin := range row.Bins {
		total += bin
	}
	if total != 1500 || row.Bins[1000*analysisSkillBins/10000] != 1000 {
		t.Fatalf("bins = %v", row.Bins)
	}
	if len(analysis.Hits) != 2 || analysis.Hits[0].AtMs != 1000 || analysis.Hits[1].AtMs != 2500 {
		t.Fatalf("hits = %+v", analysis.Hits)
	}
}

func TestHitTimelineListsEveryConfirmedHitInItsSkillRow(t *testing.T) {
	f := newAnalysisFixture(t)
	hits, err := f.store.Hits(context.Background(), f.id, AnalysisQuery{FromMs: 2000, ToMs: 4000, Enemy: -1, SkillRow: "role-7-1505:150501"}, nil)
	if err != nil || hits.State != AnalysisReady || hits.DurationMs != 10000 {
		t.Fatalf("hits = %+v (%v)", hits, err)
	}
	// The whole encounter whatever the range, and a skill row only marks hits: neither narrows the timeline.
	if hits.Query.FromMs != 0 || hits.Query.ToMs != 0 || hits.Query.SkillRow != "" {
		t.Fatalf("query = %+v", hits.Query)
	}
	type row struct {
		contributor, skillRow string
		skill                 uint64
		at                    []float64
		amount                []int64
	}
	got := []row{}
	for _, r := range hits.Rows {
		if len(r.AtMs) != len(r.Amount) || len(r.AtMs) != len(r.Sequence) {
			t.Fatalf("row %+v: lists of different lengths", r)
		}
		got = append(got, row{r.Contributor, r.SkillRow, r.SkillID, r.AtMs, r.Amount})
	}
	// Contributors in the encounter's order (role 1206 first, as stored), SkillIds in order with the row without a skill
	// last, a summon's hit in its owner's row, and damage credited to no contributor apart; unattributed damage is no hit.
	want := []row{
		{"role-7-1206", "role-7-1206:120601", 120601, []float64{3000}, []int64{700}},
		{"role-7-1206", "role-7-1206:120602", 120602, []float64{7000}, []int64{300}},
		{"role-7-1505", "role-7-1505:150501", 150501, []float64{1000, 2500}, []int64{1000, 500}},
		{"role-7-1505", "role-7-1505:unassigned", 0, []float64{6000}, []int64{200}},
		{UnassignedContributor, "", 0, []float64{8000}, []int64{50}},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v", got)
	}
	for i := range want {
		if got[i].contributor != want[i].contributor || got[i].skillRow != want[i].skillRow || got[i].skill != want[i].skill || len(got[i].at) != len(want[i].at) {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
		}
		for j := range want[i].at {
			if got[i].at[j] != want[i].at[j] || got[i].amount[j] != want[i].amount[j] {
				t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	}
	// Every listed contributor hit is one of the stored results the skill table counts: the same count, the same damage.
	analysis := f.analyse(t, AnalysisQuery{Enemy: -1})
	statHits, statDamage := uint64(0), int64(0)
	for _, stat := range analysis.Skills {
		statHits += stat.Hits
		statDamage += stat.Damage
	}
	listedHits, listedDamage := uint64(0), int64(0)
	for _, r := range hits.Rows {
		if r.Contributor == UnassignedContributor {
			continue
		}
		for _, amount := range r.Amount {
			listedHits++
			listedDamage += amount
		}
	}
	if hits.Total != 6 || hits.Listed != 6 || hits.Untimed != 0 || hits.Truncated || listedHits != statHits || listedDamage != statDamage {
		t.Fatalf("total %d listed %d untimed %d; skill table %d hits %d damage, listed %d hits %d damage", hits.Total, hits.Listed, hits.Untimed, statHits, statDamage, listedHits, listedDamage)
	}
	// The event browser lists the same results under the same sequences.
	page, err := f.store.Events(context.Background(), f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: -1, SkillRow: "role-7-1505:150501"}, Kinds: []string{EventDamage}}, nil)
	if err != nil || len(page.Rows) != 2 || page.Rows[0].Key != "c"+strconv.FormatInt(hits.Rows[2].Sequence[0], 10) || page.Rows[1].Key != "c"+strconv.FormatInt(hits.Rows[2].Sequence[1], 10) {
		t.Fatalf("event browser = %+v (%v), hits %+v", page.Rows, err, hits.Rows[2])
	}
}

func TestHitTimelineFollowsTheContributorAndEnemyFilters(t *testing.T) {
	f := newAnalysisFixture(t)
	byContributor, err := f.store.Hits(context.Background(), f.id, AnalysisQuery{Contributor: "role-7-1505", Enemy: -1}, nil)
	if err != nil || len(byContributor.Rows) != 2 || byContributor.Total != 3 || byContributor.Query.Contributor != "role-7-1505" {
		t.Fatalf("contributor = %+v (%v)", byContributor, err)
	}
	for _, row := range byContributor.Rows {
		if row.Contributor != "role-7-1505" {
			t.Fatalf("another contributor's row under the filter: %+v", row)
		}
	}
	analysis := f.analyse(t, AnalysisQuery{Enemy: -1})
	enemyB := -1
	for _, enemy := range analysis.Enemies {
		if enemy.Mapped && enemy.DamageTaken == 1000 {
			enemyB = enemy.Ordinal
		}
	}
	byEnemy, err := f.store.Hits(context.Background(), f.id, AnalysisQuery{Enemy: enemyB}, nil)
	if err != nil || enemyB < 0 || byEnemy.Total != 2 || len(byEnemy.Rows) != 2 || byEnemy.Rows[0].SkillID != 120601 || byEnemy.Rows[1].SkillID != 120602 {
		t.Fatalf("enemy %d = %+v (%v)", enemyB, byEnemy, err)
	}
	// An unknown contributor is dropped, not guessed.
	unknown, err := f.store.Hits(context.Background(), f.id, AnalysisQuery{Contributor: "role-7-9999", Enemy: -1}, nil)
	if err != nil || unknown.Query.Contributor != "" || unknown.Total != 6 {
		t.Fatalf("unknown contributor = %+v (%v)", unknown, err)
	}
}

func TestEffectIntervalsSayHowEachPartIsKnown(t *testing.T) {
	f := newAnalysisFixture(t)
	lanes, err := f.store.Effects(context.Background(), f.id, AnalysisQuery{Enemy: -1, Effects: "all"}, nil)
	if err != nil || !lanes.Available || lanes.Stale != 1 {
		t.Fatalf("lanes = %+v (%v)", lanes, err)
	}
	rows := map[string]EffectRow{}
	for _, row := range lanes.Rows {
		if row.EffectID != nil {
			rows[strconv.FormatInt(*row.EffectID, 10)] = row
		}
	}
	if _, stale := rows["6006"]; stale || len(rows) != 5 {
		t.Fatalf("rows = %v", rows)
	}
	segments := func(row EffectRow) string {
		out := ""
		for _, s := range row.Segments {
			out += s.Evidence + ":" + strconv.FormatFloat(s.FromMs, 'f', 0, 64) + "-" + strconv.FormatFloat(s.ToMs, 'f', 0, 64) + " "
		}
		return out
	}
	for effect, want := range map[string]string{
		"1001": "confirmed:500-2000 confirmed:2000-3000 ",
		"2002": "stated:4000-6000 unknown:6000-10000 ",
		"3003": "stated:5000-10000 ",
		"4004": "",
		// Alive before the encounter, confirmed by a change in it and its removal after it.
		"5005": "confirmed:0-7000 confirmed:7000-10000 ",
	} {
		if got := segments(rows[effect]); got != want {
			t.Errorf("effect %s segments %q, want %q", effect, got, want)
		}
	}
	if !rows["3003"].Unconfirmed || rows["1001"].Unconfirmed || rows["2002"].Holder.Kind != ActorEnemy || *rows["2002"].Holder.Enemy != 0 || rows["2002"].Sources[0].Contributor != "role-7-1505" {
		t.Fatalf("rows = %+v", rows)
	}
	markers := map[string]bool{}
	for _, marker := range rows["4004"].Markers {
		markers[marker.Kind] = true
	}
	if !markers[markerInstant] || markers[markerApplied] {
		t.Fatalf("instant markers = %+v", rows["4004"].Markers)
	}
	coverage := func(query AnalysisQuery) map[string]EffectCoverage {
		query.Effects, query.Enemy = "all", -1
		out := map[string]EffectCoverage{}
		for _, row := range f.analyse(t, query).Effects.Rows {
			for effect, lane := range rows {
				if lane.Key == row.Key {
					out[effect] = row
				}
			}
		}
		return out
	}
	full := coverage(AnalysisQuery{})
	if c := full["1001"]; !c.Exact || c.Ratio == nil || math.Abs(*c.Ratio-0.25) > 1e-9 || c.ConfirmedMs != 2500 || c.MaxStack == nil || *c.MaxStack != 2 || c.Applies != 1 {
		t.Errorf("1001 = %+v", c)
	}
	if c := full["2002"]; c.Exact || c.Lower != 0 || c.Upper == nil || math.Abs(*c.Upper-0.6) > 1e-9 || c.PartialMs != 2000 || c.UnknownMs != 4000 {
		t.Errorf("2002 = %+v", c)
	}
	if c := full["3003"]; c.Exact || !c.StartUnconfirmed || c.Upper != nil || c.PartialMs != 5000 {
		t.Errorf("3003 = %+v", c)
	}
	if c := full["5005"]; !c.Exact || c.Ratio == nil || *c.Ratio != 1 {
		t.Errorf("5005 = %+v", c)
	}
	// Half of 2–4 s holds 1001; the share of a range is of that range only.
	if c := coverage(AnalysisQuery{FromMs: 2000, ToMs: 4000})["1001"]; !c.Exact || c.Ratio == nil || *c.Ratio != 0.5 {
		t.Errorf("1001 in 2–4 s = %+v", c)
	}
	// A snapshot before the range is no unconfirmed start inside it.
	if c := coverage(AnalysisQuery{FromMs: 6000, ToMs: 8000})["3003"]; c.StartUnconfirmed || c.Upper == nil {
		t.Errorf("3003 in 6–8 s = %+v", c)
	}
	// The HUD scope needs the Master's HUD evidence; without a Master no row is a HUD row.
	if hud, err := f.store.Effects(context.Background(), f.id, AnalysisQuery{Enemy: -1}, nil); err != nil || len(hud.Rows) != 0 || hud.Hidden != 5 || hud.Scope != "hud" {
		t.Fatalf("HUD scope = %+v (%v)", hud, err)
	}
	// Contributor and enemy filters keep the rows the role or the enemy holds or applied.
	if byRole, _ := f.store.Effects(context.Background(), f.id, AnalysisQuery{Enemy: -1, Effects: "all", Contributor: "role-7-1206"}, nil); len(byRole.Rows) != 1 || *byRole.Rows[0].EffectID != 3003 {
		t.Fatalf("role 1206 rows = %+v", byRole.Rows)
	}
	if byEnemy, _ := f.store.Effects(context.Background(), f.id, AnalysisQuery{Enemy: 0, Effects: "all"}, nil); len(byEnemy.Rows) != 1 || *byEnemy.Rows[0].EffectID != 2002 {
		t.Fatalf("enemy 0 rows = %+v", byEnemy.Rows)
	}
}

func TestCaptureGapsMakeCoverageInexact(t *testing.T) {
	f := newAnalysisFixture(t)
	start := f.start.UnixNano()
	if _, err := f.store.db.Exec("INSERT INTO capture_gap (capture_session_id, loss_id, direction, from_ns, to_ns) SELECT capture_session_id, 1, 'in', ?, ? FROM encounter WHERE id = ?",
		start+int64(time.Second), start+int64(2*time.Second), f.id); err != nil {
		t.Fatal(err)
	}
	f.store.models.clear()
	analysis := f.analyse(t, AnalysisQuery{Enemy: -1, Effects: "all"})
	if len(analysis.Gaps) != 1 || analysis.Gaps[0].FromMs != 1000 || analysis.Gaps[0].ToMs != 2000 {
		t.Fatalf("gaps = %+v", analysis.Gaps)
	}
	for _, row := range analysis.Effects.Rows {
		if row.Exact || row.GapMs != 1000 {
			t.Fatalf("a row is exact across a capture gap: %+v", row)
		}
		if row.ConfirmedMs > 0 && row.PartialMs < 1000 && row.Lower >= 0.95 {
			t.Fatalf("confirmed time inside the gap was not moved to partial: %+v", row)
		}
	}
}

func TestEventBrowserPagesProductRecordsInProcessingOrder(t *testing.T) {
	f := newAnalysisFixture(t)
	ctx := context.Background()
	page, err := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: -1, Effects: "all"}, Limit: 3}, nil)
	if err != nil || page.State != AnalysisReady || !page.EffectsAvailable {
		t.Fatalf("page = %+v (%v)", page, err)
	}
	// 9 results and the 7 effect changes inside the encounter: 1001's apply, stack and removal, 2002, 3003, 4004 and the stack
	// of 5005. What happened before or after the encounter is not listed.
	if page.Total != 9+7 || len(page.Rows) != 3 || page.Rows[0].Kind != EventEffect || page.Rows[1].Kind != EventDamage || page.Rows[1].SkillRow != "role-7-1505:150501" {
		t.Fatalf("page = %+v", page)
	}
	all, _ := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: -1, Effects: "all"}}, nil)
	for i, row := range all.Rows {
		// A confirmed local result always names both ends in product terms; only unattributed ones stay unknown.
		if (row.Flow == string(capture.CombatFlowLocalOutgoing) || row.Flow == string(capture.CombatFlowLocalIncoming)) && (row.Source.Kind == ActorUnknown || row.Target.Kind == ActorUnknown) {
			t.Fatalf("row %d names no endpoint: %+v", i, row)
		}
		if i > 0 && row.AtMs != nil && all.Rows[i-1].AtMs != nil && *row.AtMs < *all.Rows[i-1].AtMs-1 {
			t.Fatalf("row %d goes back in time", i)
		}
	}
	damage, _ := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: 1}, Kinds: []string{EventDamage}}, nil)
	if damage.Total != 2 || damage.Rows[0].Target.Kind != ActorEnemy || *damage.Rows[0].Target.Enemy != 1 || damage.Rows[0].Target.MonsterID != 340000041 {
		t.Fatalf("enemy 1 damage = %+v", damage)
	}
	healing, _ := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: -1, Contributor: "role-7-1206"}, Kinds: []string{EventHealing}}, nil)
	if healing.Total != 1 || *healing.Rows[0].Effective != 40 || healing.Rows[0].Source.Contributor != "role-7-1206" || healing.Rows[0].Target.RoleID != roleA {
		t.Fatalf("healing = %+v", healing)
	}
	ranged, _ := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{FromMs: 2000, ToMs: 4000, Enemy: -1}, Kinds: []string{EventDamage, EventHealing}}, nil)
	if ranged.Total != 2 {
		t.Fatalf("2–4 s = %+v", ranged)
	}
	skill, _ := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: -1, SkillRow: "role-7-1505:unassigned"}}, nil)
	if skill.Total != 1 || skill.Rows[0].Amount != 200 {
		t.Fatalf("unassigned skill row = %+v", skill)
	}
}

func TestOnlyFinishedEncountersWithEventsAreAnalysed(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 11, 11, 4, 0, 0, 0, time.UTC)
	entries := finishedEncounter(1, 1, start, 30, 500, capture.IntegrityIntact)
	// The engine stopped before the encounter finished.
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		t.Fatal(err)
	}
	recorder.Submit(entries[:2], 0)
	if err := recorder.Finish(context.Background(), capture.StopReasonReplayEnd); err != nil {
		t.Fatal(err)
	}
	day, err := store.Day(context.Background(), start.Format(dateLayout), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	analysis, err := store.Analysis(context.Background(), day.Encounters[0].ID, AnalysisQuery{Enemy: -1}, nil)
	if err != nil || analysis.State != AnalysisNotFinished || analysis.Snapshot != nil {
		t.Fatalf("an unfinished encounter = %+v (%v)", analysis.State, err)
	}
	if _, err := store.Analysis(context.Background(), 999, AnalysisQuery{Enemy: -1}, nil); err != ErrNotFound {
		t.Fatalf("a missing encounter = %v", err)
	}
}
