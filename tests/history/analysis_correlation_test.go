package history

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"
)

// Effect coverage counts refreshes, stack changes and the time no interval holds, and damage is placed against an effect's
// intervals by time alone: confirmed, stated, unknown (with the reason) or outside. The fixture's hits land at 1, 2.5, 3,
// 6, 7 and 8 s; effect 1001 is confirmed from 0.5 s to its removal at 3 s, 2002 is stated 4–6 s and unknown after, 3003
// starts unconfirmed at 5 s and 5005 is confirmed throughout.

func effectKeys(t *testing.T, f analysisFixture) map[string]string {
	t.Helper()
	lanes, err := f.store.Effects(context.Background(), f.id, AnalysisQuery{Enemy: -1, Effects: "all"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, row := range lanes.Rows {
		if row.EffectID != nil {
			keys[strconv.FormatInt(*row.EffectID, 10)] = row.Key
		}
	}
	return keys
}

func TestEffectCoverageCountsChangesAndDowntime(t *testing.T) {
	f := newAnalysisFixture(t)
	keys := effectKeys(t, f)
	coverage := map[string]EffectCoverage{}
	for _, row := range f.analyse(t, AnalysisQuery{Enemy: -1, Effects: "all"}).Effects.Rows {
		for effect, key := range keys {
			if key == row.Key {
				coverage[effect] = row
			}
		}
	}
	// 1001: one stack change, never refreshed, absent for 10 s minus its 2.5 s, and the record rules it out then.
	if c := coverage["1001"]; c.StackChanges != 1 || c.Refreshes != 0 || c.InactiveMs != 7500 || !c.InactiveConfirmed || c.UnobservedMs != 0 {
		t.Errorf("1001 = %+v", c)
	}
	if c := coverage["2002"]; c.InactiveMs != 4000 || !c.InactiveConfirmed {
		t.Errorf("2002 = %+v", c)
	}
	// 3003 was first seen in a snapshot at 5 s: before it, it may have been active, which is no downtime.
	if c := coverage["3003"]; c.UnobservedMs != 5000 || c.InactiveMs != 0 {
		t.Errorf("3003 = %+v", c)
	}
	if c := coverage["5005"]; c.InactiveMs != 0 || c.StackChanges != 1 {
		t.Errorf("5005 = %+v", c)
	}
	// Each part of the range is counted once: drawn, unobserved and inactive time make up the whole 10 s.
	for effect, c := range coverage {
		if total := c.ConfirmedMs + c.PartialMs + c.UnknownMs + c.InactiveMs + c.UnobservedMs; effect != "4004" && math.Abs(total-10000) > 1 {
			t.Errorf("%s parts add up to %v ms: %+v", effect, total, c)
		}
	}
	// A capture gap in the range leaves the downtime an upper bound.
	start := f.start.UnixNano()
	if _, err := f.store.db.Exec("INSERT INTO capture_gap (capture_session_id, loss_id, direction, from_ns, to_ns) SELECT capture_session_id, 1, 'in', ?, ? FROM encounter WHERE id = ?",
		start+int64(8*time.Second), start+int64(9*time.Second), f.id); err != nil {
		t.Fatal(err)
	}
	f.store.models.clear()
	for _, row := range f.analyse(t, AnalysisQuery{Enemy: -1, Effects: "all"}).Effects.Rows {
		if row.Key == keys["1001"] && (row.InactiveConfirmed || row.InactiveMs != 6500) {
			t.Fatalf("1001 across a gap = %+v", row)
		}
	}
}

func TestDamageIsPlacedAgainstEffectIntervalsByTime(t *testing.T) {
	f := newAnalysisFixture(t)
	keys := effectKeys(t, f)
	ctx := context.Background()
	correlate := func(effect string, query AnalysisQuery) EffectCorrelation {
		t.Helper()
		out, err := f.store.EffectCorrelation(ctx, f.id, query, keys[effect], nil)
		if err != nil || out.State != AnalysisReady || !out.Available {
			t.Fatalf("%s: %+v (%v)", effect, out, err)
		}
		split := out.Total
		if split.Confirmed.Hits+split.Stated.Hits+split.Unknown.Hits+split.Outside.Hits != split.Hits ||
			split.Confirmed.Damage+split.Stated.Damage+split.Unknown.Damage+split.Outside.Damage != split.Damage {
			t.Fatalf("%s: the classes do not add up to the selected hits: %+v", effect, split)
		}
		return out
	}
	// 1001: the hits at 1 and 2.5 s are inside it; the one at 3 s came at the very instant it was removed, so their order is
	// unknown; the rest came after its removal.
	team := correlate("1001", AnalysisQuery{Enemy: -1})
	if total := team.Total; total.Hits != 6 || total.Damage != 2750 || total.Confirmed.Hits != 2 || total.Confirmed.Damage != 1500 ||
		total.Unknown.Hits != 1 || total.Unknown.Damage != 700 || total.Outside.Hits != 3 || total.Outside.Damage != 550 || total.Stated.Hits != 0 {
		t.Fatalf("1001 team = %+v", total)
	}
	if team.Reasons[reasonSimultaneous] != 1 || len(team.Reasons) != 1 || math.Abs(team.Total.Confirmed.Share-1500.0/2750) > 1e-9 {
		t.Fatalf("1001 reasons = %v shares %+v", team.Reasons, team.Total)
	}
	contributors := map[string]CorrelationRow{}
	for _, row := range team.ByContributor {
		contributors[row.Key] = row
	}
	if a := contributors["role-7-1505"]; a.Confirmed.Hits != 2 || a.Outside.Hits != 1 || a.Damage != 1700 {
		t.Fatalf("role 1505 = %+v", a)
	}
	if u := contributors[UnassignedContributor]; u.Outside.Damage != 50 {
		t.Fatalf("unassigned = %+v", u)
	}
	// A skill row, a range and an enemy narrow the hits the same way the workspace does.
	if skill := correlate("1001", AnalysisQuery{Enemy: -1, SkillRow: "role-7-1505:150501"}); skill.Total.Hits != 2 || skill.Total.Confirmed.Hits != 2 {
		t.Fatalf("1001 skill row = %+v", skill.Total)
	}
	if ranged := correlate("1001", AnalysisQuery{Enemy: -1, ToMs: 2000}); ranged.Total.Hits != 1 || ranged.Total.Confirmed.Damage != 1000 || ranged.Range.ToMs != 2000 {
		t.Fatalf("1001 0–2 s = %+v", ranged.Total)
	}
	// 2002 on enemy 0: before its apply the record rules it out; at 6 s its stated time ends as the hit lands; at 8 s its
	// state is unknown.
	debuff := correlate("2002", AnalysisQuery{Enemy: 0})
	if total := debuff.Total; total.Hits != 4 || total.Outside.Hits != 2 || total.Outside.Damage != 1500 || total.Unknown.Hits != 2 || total.Unknown.Damage != 250 {
		t.Fatalf("2002 on enemy 0 = %+v", total)
	}
	if debuff.Reasons[reasonSimultaneous] != 1 || debuff.Reasons[reasonUnknownInterval] != 1 || debuff.Query.Enemy != 0 {
		t.Fatalf("2002 reasons = %v", debuff.Reasons)
	}
	// 3003 was first seen at 5 s in a snapshot: before that nothing rules it in or out.
	snapshot := correlate("3003", AnalysisQuery{Enemy: -1})
	if total := snapshot.Total; total.Unknown.Hits != 3 || total.Stated.Hits != 3 || total.Outside.Hits != 0 || snapshot.Reasons[reasonUnobserved] != 3 {
		t.Fatalf("3003 = %+v %v", total, snapshot.Reasons)
	}
	// 5005 lasted through the encounter: a hit at its start or end is not at a change of its state.
	if alive := correlate("5005", AnalysisQuery{Enemy: -1}); alive.Total.Confirmed.Hits != 6 {
		t.Fatalf("5005 = %+v %v", alive.Total, alive.Reasons)
	}
	if missing, err := f.store.EffectCorrelation(ctx, f.id, AnalysisQuery{Enemy: -1}, "no-such-row", nil); err != nil || missing.Available || missing.Total.Hits != 0 {
		t.Fatalf("unknown row = %+v (%v)", missing, err)
	}
	// The event browser lists the same hits of one class, each with its place.
	outside, err := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: -1}, CorrelateEffect: keys["1001"], Correlation: CorrelationOutside}, nil)
	if err != nil || outside.Total != 3 || outside.Rows[0].Correlation != CorrelationOutside || outside.Rows[0].Kind != EventDamage {
		t.Fatalf("outside hits = %+v (%v)", outside, err)
	}
	unknown, err := f.store.Events(ctx, f.id, EventQuery{AnalysisQuery: AnalysisQuery{Enemy: -1}, CorrelateEffect: keys["1001"], Correlation: CorrelationUnknown}, nil)
	if err != nil || unknown.Total != 1 || unknown.Rows[0].Amount != 700 || unknown.Rows[0].CorrelationReason != reasonSimultaneous {
		t.Fatalf("unknown hits = %+v (%v)", unknown, err)
	}
}

// BenchmarkCorrelationOfManyHits places 50,000 hits against an effect confirmed for half of every second of ten minutes.
func BenchmarkCorrelationOfManyHits(b *testing.B) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	timeline := rowTimeline{start: start, end: start + int64(10*time.Minute)}
	for i := 0; i < 600; i++ {
		from := start + int64(i)*int64(time.Second)
		timeline.confirmed = append(timeline.confirmed, span{from, from + int64(500*time.Millisecond)})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for hit := 0; hit < 50000; hit++ {
			timeline.classify(start + int64(hit)*int64(12*time.Millisecond))
		}
	}
}
