package capture

import (
	"runtime"
	"testing"
	"time"
)

// Timeline costs: one damage update (encounter path with and without the timeline is measured by the whole
// addCombatEvent call), a bounded view render, and the retained memory of long encounters.

func timelineBenchStatistics(b testing.TB, seconds int) *DamageStatistics {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	for i := 0; i < seconds; i++ {
		for hit := 0; hit < 4; hit++ {
			roleOutgoing(statistics, uint64(1102+hit), 100, float64(i)+float64(hit)*0.2)
		}
	}
	return statistics
}

func BenchmarkTimelineDamageUpdate(b *testing.B) {
	statistics := NewDamageStatistics()
	statistics.observeBattleState(true, encounterAt(0), 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		roleOutgoing(statistics, uint64(1102+i%4), 100, float64(i)*0.05)
	}
}

func benchmarkTimelineView(b *testing.B, seconds int) {
	statistics := timelineBenchStatistics(b, seconds)
	query := CaptureQueryTime{CaptureSessionID: statistics.Snapshot().CaptureSessionID, Timestamp: encounterAt(float64(seconds))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if statistics.TimelineAt(query, maxViewTimelinePoints) == nil {
			b.Fatal("no timeline")
		}
	}
}

func BenchmarkTimelineView10Minutes(b *testing.B) { benchmarkTimelineView(b, 600) }
func BenchmarkTimelineView1Hour(b *testing.B)     { benchmarkTimelineView(b, 3600) }

func TestTimelineRetainedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory measurement")
	}
	measure := func(seconds int) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		statistics := timelineBenchStatistics(t, seconds)
		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(statistics)
		return after.HeapAlloc - min(after.HeapAlloc, before.HeapAlloc)
	}
	start := time.Now()
	short, long := measure(60), measure(3660)
	t.Logf("heap for a 4-role encounter: 60 s %d B, 3660 s %d B, about %d B per second of timeline (%.1f s)", short, long, (long-min(long, short))/3600, time.Since(start).Seconds())
}

// BenchmarkTimelineAddOnly is the timeline's own share of one damage update.
func BenchmarkTimelineAddOnly(b *testing.B) {
	timeline := newEncounterTimeline(encounterAt(0))
	contributors := []TimelineContributor{{Kind: TimelineContributorRole, PlayerID: 7, RoleID: 1102}, {Kind: TimelineContributorRole, PlayerID: 7, RoleID: 1409}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		timeline.add(encounterAt(float64(i%3600)+0.5), 100, contributors[i%2])
	}
}
