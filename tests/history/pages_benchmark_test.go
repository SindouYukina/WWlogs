package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// Query costs of the History pages over stored histories of different sizes: the library and its filters, the calendar,
// one encounter, the reads of a comparison, the career, a deletion and an update. Every store is encrypted as a real one.
//
//	go test -overlay=tests/go-overlay.json -run XXX -bench HistoryPages -benchtime 20x ./internal/history

var benchmarkRoles = []uint64{1512, 1409, 1205, 1506, 1107, 1304}

// benchmarkEncounter is one finished encounter with three role contributors and four hits; every 17th is degraded.
func benchmarkEncounter(i int, start time.Time, sequence uint64) []capture.HistoryEntry {
	key := "1-" + strconv.Itoa(i+1)
	integrity := capture.IntegrityIntact
	if i%17 == 0 {
		integrity = capture.IntegrityDegraded
	}
	damage := map[uint64]int64{benchmarkRoles[i%6]: 6000 + int64(i), benchmarkRoles[(i+1)%6]: 4000, benchmarkRoles[(i+2)%6]: 2000}
	entries := withRoles(finishedEncounter(1, uint64(i+1), start, 60+i%120, int64(12000+i), integrity), damage)
	batch := append([]capture.HistoryEntry{entries[0], entries[1]}, hitEntries(sequence, key, start.Add(time.Second), benchmarkRoles[i%6], 15124104, 100, 200, 300, 400)...)
	return append(batch, entries[2])
}

// historyOfSize stores n encounters about 97 minutes apart (5,000 cover most of a year) and returns their IDs in order.
func historyOfSize(b *testing.B, n int) (*Store, []int64, time.Time) {
	b.Helper()
	store := openTestStore(b, filepath.Join(b.TempDir(), "history", "combat-history.db"), nil)
	base := time.Date(2025, 12, 1, 9, 0, 0, 0, time.UTC)
	batches := make([][]capture.HistoryEntry, 0, n)
	var last time.Time
	for i := 0; i < n; i++ {
		last = base.Add(time.Duration(i) * 97 * time.Minute)
		batches = append(batches, benchmarkEncounter(i, last, uint64(1+4*i)))
	}
	// Submitted with the replay's bounded wait: a live submit of this many entries at once would overflow the bounded queue,
	// which the live path records as a loss rather than block.
	recorder := begin(b, store, liveStart)
	for _, batch := range batches {
		if err := recorder.SubmitWait(context.Background(), batch, 0); err != nil {
			b.Fatal(err)
		}
	}
	if err := recorder.Finish(context.Background(), capture.StopReasonReplayEnd); err != nil {
		b.Fatal(err)
	}
	rows, err := store.db.QueryContext(context.Background(), "SELECT id FROM encounter ORDER BY started_at_ns")
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			b.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != n {
		b.Fatalf("stored %d of %d encounters", len(ids), n)
	}
	return store, ids, last
}

func BenchmarkHistoryPages(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{100, 1000, 5000} {
		store, ids, last := historyOfSize(b, n)
		store.now = func() time.Time { return last.Add(time.Hour) }
		size := "n=" + strconv.Itoa(n)
		run := func(name string, query func(b *testing.B)) {
			b.Run(name+"/"+size, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					query(b)
				}
			})
		}
		must := func(b *testing.B, err error) {
			if err != nil {
				b.Fatal(err)
			}
		}
		run("library-first", func(b *testing.B) {
			store.library.clear()
			_, err := store.Library(ctx, LibraryQuery{Limit: 50}, time.UTC)
			must(b, err)
		})
		run("library-page", func(b *testing.B) {
			view, err := store.Library(ctx, LibraryQuery{Limit: 50}, time.UTC)
			must(b, err)
			if raw, _ := json.Marshal(view); b.N == 1 || len(raw) > 0 {
				b.ReportMetric(float64(len(raw)), "json-B")
			}
		})
		run("library-filtered", func(b *testing.B) {
			_, err := store.Library(ctx, LibraryQuery{GameVersions: []string{capture.CurrentGameVersion()}, Classes: []string{ClassComplete}, MinSeconds: 90,
				RoleIDs: []uint64{1512}, Sort: "dps_desc", Limit: 50}, time.UTC)
			must(b, err)
		})
		run("year", func(b *testing.B) {
			_, err := store.Year(ctx, last.Year(), time.UTC)
			must(b, err)
		})
		run("day", func(b *testing.B) {
			_, err := store.Day(ctx, last.Format(dateLayout), 0, 50, time.UTC)
			must(b, err)
		})
		run("encounter", func(b *testing.B) {
			_, err := store.Encounter(ctx, ids[len(ids)/2], time.UTC)
			must(b, err)
		})
		run("compare-reads", func(b *testing.B) {
			chosen := ids[len(ids)-4:]
			_, err := store.RecordClasses(ctx, chosen)
			must(b, err)
			for _, id := range chosen {
				_, err := store.Encounter(ctx, id, time.UTC)
				must(b, err)
			}
		})
		run("career-first", func(b *testing.B) {
			store.library.clear()
			_, err := store.Career(ctx, CareerQuery{}, time.UTC, nil)
			must(b, err)
		})
		run("career", func(b *testing.B) {
			view, err := store.Career(ctx, CareerQuery{}, time.UTC, nil)
			must(b, err)
			raw, _ := json.Marshal(view)
			b.ReportMetric(float64(len(raw)), "json-B")
		})
		run("career-30d", func(b *testing.B) {
			_, err := store.Career(ctx, CareerQuery{LastDays: 30}, time.UTC, nil)
			must(b, err)
		})
		// An update: one more stored encounter, then the library again (the index notices the change and is read again).
		next := len(ids)
		run("update-then-library", func(b *testing.B) {
			b.StopTimer()
			start := last.Add(time.Duration(next-len(ids)+1) * time.Hour)
			record(b, store, liveStart, benchmarkEncounter(next, start, uint64(1+4*next)))
			next++
			b.StartTimer()
			_, err := store.Library(ctx, LibraryQuery{Limit: 50}, time.UTC)
			must(b, err)
		})
		// A deletion alone, then a deletion followed by the career (the deleted encounter's index entry and largest hits go).
		deleted := 0
		run("delete", func(b *testing.B) {
			_, err := store.DeleteEncounter(ctx, ids[deleted%len(ids)])
			must(b, err)
			deleted++
		})
		run("delete-then-career", func(b *testing.B) {
			_, err := store.DeleteEncounter(ctx, ids[deleted%len(ids)])
			must(b, err)
			deleted++
			_, err = store.Career(ctx, CareerQuery{}, time.UTC, nil)
			must(b, err)
		})
	}
}
