package history

import (
	"context"
	"math"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// Career statistics over stored encounters: totals over the records that may count, personal records over complete
// records only, a trial role counted as its parent, and dates in the view's zone.

// withRoles replaces a test encounter's role contributors: each role deals its damage, the encounter totals stay as built.
func withRoles(entries []capture.HistoryEntry, damage map[uint64]int64) []capture.HistoryEntry {
	snapshot := &entries[2].Finished.Snapshot
	snapshot.Roles = nil
	for role, amount := range damage {
		snapshot.Roles = append(snapshot.Roles, capture.RoleDamageSnapshot{PlayerID: 7, RoleID: role, DamageDealt: amount, OutgoingEvents: 1, DirectDamage: amount})
	}
	return entries
}

// hitEntries are accepted local hits of a role assigned to an encounter.
func hitEntries(first uint64, encounter string, at time.Time, role uint64, skill uint64, amounts ...int64) []capture.HistoryEntry {
	var entries []capture.HistoryEntry
	for i, amount := range amounts {
		sequence := first + uint64(i)
		entries = append(entries,
			capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &capture.CombatEventRecord{Kind: capture.CombatEventDamage,
				Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: 1000 + sequence}, ObservedAt: at, Evidence: "damage_rpc_response",
				Flow: capture.CombatFlowLocalOutgoing, Confidence: capture.AttributionConfirmed, Amount: amount, SkillID: skill,
				Source: capture.CombatEndpoint{EntityID: 1, Relation: "LOCAL"}, Target: capture.CombatEndpoint{EntityID: 2, Relation: "UNRELATED"},
				SourceRole: &capture.CombatRole{PlayerID: 7, RoleID: role, Basis: "test"}}},
			capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: sequence, Encounter: encounter})
	}
	return entries
}

func trialParent(id uint64) uint64 {
	if id == 15127004 {
		return 1512
	}
	return id
}

func TestCareerTotalsCountOnlyRecordsThatMayCount(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	complete := withRoles(finishedEncounter(1, 1, base, 10, 1000, capture.IntegrityIntact), map[uint64]int64{1512: 600, 1409: 400})
	degraded := withRoles(finishedEncounter(1, 2, base.Add(time.Hour), 10, 9000, capture.IntegrityDegraded), map[uint64]int64{15127004: 9000})
	lost := withRoles(finishedEncounter(1, 3, base.Add(24*time.Hour), 10, 5000, capture.IntegrityIntact), map[uint64]int64{1409: 5000})
	assigned := capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: 99, Encounter: "1-3"}
	first := append([]capture.HistoryEntry{complete[0], complete[1]}, hitEntries(1, "1-1", base.Add(time.Second), 1512, 15124104, 120, 480)...)
	first = append(first, complete[2])
	record(t, store, liveStart, first, degraded, []capture.HistoryEntry{lost[0], assigned, lost[1], lost[2]})

	view, err := store.Career(ctx, CareerQuery{}, time.UTC, trialParent)
	if err != nil {
		t.Fatal(err)
	}
	totals := view.Totals
	if totals.Encounters != 3 || totals.Counted != 2 || totals.Damage != 10000 || !totals.DamageLowerBound || totals.Seconds != 20 || totals.ActiveDays != 2 {
		t.Fatalf("totals = %+v", totals)
	}
	if totals.Classes[ClassComplete] != 1 || totals.Classes[ClassDegraded] != 1 || totals.Classes[ClassIncomplete] != 1 {
		t.Fatalf("classes = %+v", totals.Classes)
	}
	// The degraded encounter dealt more but holds no record: only the complete one does.
	if view.Records.BestDPS == nil || view.Records.BestDPS.Value != 100 || view.Records.BestDPS.EncounterID != listedID(t, store, base) {
		t.Fatalf("best DPS = %+v", view.Records.BestDPS)
	}
	if hit := view.Records.MaxHit; hit == nil || hit.Value != 480 || hit.RoleID != 1512 || hit.SkillID != 15124104 {
		t.Fatalf("max hit = %+v", hit)
	}
	if view.Records.BestPeak == nil || view.Records.BestPeak.Value != 1000 {
		t.Fatalf("best peak = %+v", view.Records.BestPeak)
	}
	roles := map[uint64]CareerRole{}
	for _, role := range view.Roles {
		roles[role.RoleID] = role
	}
	lily := roles[1512]
	if lily.Encounters != 2 || lily.TrialEncounters != 1 || lily.Counted != 2 || lily.Damage != 9600 {
		t.Fatalf("Lily (with her trial) = %+v", lily)
	}
	if lily.BestDPS == nil || lily.BestDPS.Value != 60 || lily.MaxHit == nil || lily.MaxHit.Value != 480 {
		t.Fatalf("Lily's records = dps %+v hit %+v", lily.BestDPS, lily.MaxHit)
	}
	// Her dates: both encounters on 2026-10-01, her damage there a lower bound, her best DPS from the complete one only.
	if len(lily.Days) != 1 || lily.Days[0].Encounters != 2 || lily.Days[0].Damage != 9600 || !lily.Days[0].DamageLowerBound || lily.Days[0].BestDPS != 60 {
		t.Fatalf("Lily's days = %+v", lily.Days)
	}
	if days := roles[1409].Days; len(days) != 2 || days[1].Damage != 0 || days[1].BestDPS != 0 || days[1].Encounters != 1 {
		t.Fatalf("1409's days = %+v", days)
	}
	if _, trialListed := roles[15127004]; trialListed {
		t.Fatal("the trial role is listed apart from its parent")
	}
	if cartethyia := roles[1409]; cartethyia.Encounters != 2 || cartethyia.Counted != 1 || cartethyia.Damage != 400 {
		t.Fatalf("1409 = %+v", cartethyia)
	}
	if len(view.Combos) != 2 || view.Combos[0].Encounters != 1 {
		t.Fatalf("combos = %+v", view.Combos)
	}
	if len(view.Days) != 2 || view.Days[0].Encounters != 2 || view.Days[0].Counted != 2 || view.Days[1].Counted != 0 || view.Days[0].BestDPS != 100 {
		t.Fatalf("days = %+v", view.Days)
	}
	// The degraded encounter makes every sum it enters a lower bound, and only those.
	if !lily.DamageLowerBound || roles[1409].DamageLowerBound || !view.Days[0].DamageLowerBound || view.Days[1].DamageLowerBound {
		t.Fatalf("lower bounds: Lily %v, 1409 %v, days %v %v", lily.DamageLowerBound, roles[1409].DamageLowerBound, view.Days[0].DamageLowerBound, view.Days[1].DamageLowerBound)
	}
	for _, combo := range view.Combos {
		if combo.DamageLowerBound != (len(combo.Roles) == 1 && combo.Roles[0] == 1512) {
			t.Fatalf("combo lower bound = %+v", combo)
		}
	}
}

func TestEmptyCareerHasNoNumbersToInvent(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	view, err := store.Career(context.Background(), CareerQuery{}, time.UTC, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.Totals.Encounters != 0 || view.Totals.Damage != 0 || view.Totals.Seconds != 0 || view.Totals.First != nil || view.Records.BestDPS != nil || view.Records.MaxHit != nil {
		t.Fatalf("empty career = %+v", view)
	}
	if len(view.Days) != 0 || len(view.Roles) != 0 || view.Days == nil || view.Roles == nil || view.Combos == nil || view.Instances == nil || view.Versions == nil {
		t.Fatalf("empty career lists = %+v", view)
	}
	for _, value := range []float64{view.Totals.Seconds} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatal("a non-finite number")
		}
	}
}

func TestCareerDatesFollowTheViewZoneAndSelection(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	late := time.Date(2026, 10, 5, 23, 30, 0, 0, time.UTC)
	record(t, store, liveStart, finishedEncounter(1, 1, late, 10, 1000, capture.IntegrityIntact), finishedEncounter(1, 2, late.Add(48*time.Hour), 10, 2000, capture.IntegrityIntact))
	tokyo := mustZone(t, "Asia/Tokyo")
	view, err := store.Career(ctx, CareerQuery{}, tokyo, nil)
	if err != nil || len(view.Days) != 2 || view.Days[0].Date != "2026-10-06" {
		t.Fatalf("days in Tokyo = %+v (%v)", view.Days, err)
	}
	ranged, err := store.Career(ctx, CareerQuery{From: "2026-10-07"}, tokyo, nil)
	if err != nil || ranged.Totals.Encounters != 1 || ranged.Totals.Damage != 2000 {
		t.Fatalf("from 10-07 = %+v (%v)", ranged.Totals, err)
	}
	other, err := store.Career(ctx, CareerQuery{GameVersions: []string{"9.9"}}, tokyo, nil)
	if err != nil || other.Totals.Encounters != 0 || len(other.Versions) != 1 || other.Versions[0] != capture.CurrentGameVersion() {
		t.Fatalf("other version = %+v versions %v (%v)", other.Totals, other.Versions, err)
	}
}

// The last N days are the view zone's dates up to today, decided by Go.
func TestCareerLastDaysAreDatesOfTheViewZone(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	now := time.Date(2026, 10, 20, 16, 0, 0, 0, time.UTC) // 2026-10-21 01:00 in Tokyo
	store.now = func() time.Time { return now }
	record(t, store, liveStart, finishedEncounter(1, 1, now.Add(-7*24*time.Hour), 10, 1000, capture.IntegrityIntact), finishedEncounter(1, 2, now.Add(-time.Hour), 10, 2000, capture.IntegrityIntact))
	view, err := store.Career(context.Background(), CareerQuery{LastDays: 7}, mustZone(t, "Asia/Tokyo"), nil)
	if err != nil || view.From != "2026-10-15" || view.To != "2026-10-21" || view.Today != "2026-10-21" || view.Totals.Encounters != 1 {
		t.Fatalf("last 7 days = %s..%s today %s, %d encounters (%v)", view.From, view.To, view.Today, view.Totals.Encounters, err)
	}
	// The host resolves the same dates for the view's filters; a month boundary and an explicit range stay calendar dates.
	if dates := store.CareerDates(CareerQuery{LastDays: 30, GameVersions: []string{"3.8"}}, mustZone(t, "Asia/Tokyo")); dates.From != "2026-09-22" || dates.To != "2026-10-21" || dates.LastDays != 0 || len(dates.GameVersions) != 1 {
		t.Fatalf("last 30 days = %+v", dates)
	}
	if dates := store.CareerDates(CareerQuery{From: "2026-01-01"}, time.UTC); dates.From != "2026-01-01" || dates.To != "" {
		t.Fatalf("explicit range = %+v", dates)
	}
}

// A deletion changes the career at once.
func TestCareerFollowsADeletion(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	base := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	record(t, store, liveStart, finishedEncounter(1, 1, base, 10, 1000, capture.IntegrityIntact), finishedEncounter(1, 2, base.Add(time.Hour), 10, 3000, capture.IntegrityIntact))
	before, err := store.Career(ctx, CareerQuery{}, time.UTC, nil)
	if err != nil || before.Records.BestDamage == nil || before.Records.BestDamage.Value != 3000 {
		t.Fatalf("before = %+v (%v)", before.Records, err)
	}
	if _, err := store.DeleteEncounter(ctx, listedID(t, store, base.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	after, err := store.Career(ctx, CareerQuery{}, time.UTC, nil)
	if err != nil || after.Totals.Encounters != 1 || after.Records.BestDamage == nil || after.Records.BestDamage.Value != 1000 {
		t.Fatalf("after = %+v %+v (%v)", after.Totals, after.Records, err)
	}
}

// BenchmarkCareerOverManyEncounters measures one Career answer over a thousand stored encounters with role contributors
// and hits: the first answer reads the index and the largest hits, later ones reuse them until the history changes.
func BenchmarkCareerOverManyEncounters(b *testing.B) {
	store := openTestStore(b, filepath.Join(b.TempDir(), "history", "combat-history.db"), nil)
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	var batches [][]capture.HistoryEntry
	sequence := uint64(1)
	for i := 0; i < 1000; i++ {
		start := base.Add(time.Duration(i) * 6 * time.Hour)
		key := "1-" + strconv.Itoa(i+1)
		entries := withRoles(finishedEncounter(1, uint64(i+1), start, 60, int64(10000+i), capture.IntegrityIntact), map[uint64]int64{1512: 6000, 1409: int64(4000 + i)})
		batch := append([]capture.HistoryEntry{entries[0], entries[1]}, hitEntries(sequence, key, start.Add(time.Second), 1512, 15124104, 100, 200, 300)...)
		sequence += 3
		batches = append(batches, append(batch, entries[2]))
	}
	record(b, store, liveStart, batches...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Career(context.Background(), CareerQuery{}, time.UTC, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// A deletion drops the deleted encounter's largest hits and keeps the others', which never change once finished.
func TestCareerKeepsTheOtherLargestHitsAfterADeletion(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	ctx := context.Background()
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	first := finishedEncounter(1, 1, base, 10, 1000, capture.IntegrityIntact)
	second := finishedEncounter(1, 2, base.Add(time.Hour), 10, 2000, capture.IntegrityIntact)
	withHits := func(entries []capture.HistoryEntry, sequence uint64, key string, at time.Time, amounts ...int64) []capture.HistoryEntry {
		batch := append([]capture.HistoryEntry{entries[0], entries[1]}, hitEntries(sequence, key, at.Add(time.Second), 1512, 15124104, amounts...)...)
		return append(batch, entries[2])
	}
	record(t, store, liveStart, withHits(first, 1, "1-1", base, 100, 500), withHits(second, 10, "1-2", base.Add(time.Hour), 300))
	before, err := store.Career(ctx, CareerQuery{}, time.UTC, nil)
	if err != nil || before.Records.MaxHit == nil || before.Records.MaxHit.Value != 500 {
		t.Fatalf("before = %+v (%v)", before.Records.MaxHit, err)
	}
	gone, kept := listedID(t, store, base), listedID(t, store, base.Add(time.Hour))
	if _, err := store.DeleteEncounter(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if _, cached := store.library.maxHits[gone]; cached {
		t.Fatal("the deleted encounter's largest hits are still cached")
	}
	if _, cached := store.library.maxHits[kept]; !cached {
		t.Fatal("the remaining encounter's largest hits were dropped")
	}
	after, err := store.Career(ctx, CareerQuery{}, time.UTC, nil)
	if err != nil || after.Records.MaxHit == nil || after.Records.MaxHit.Value != 300 || after.Records.MaxHit.EncounterID != kept {
		t.Fatalf("after = %+v (%v)", after.Records.MaxHit, err)
	}
}
