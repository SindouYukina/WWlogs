package history

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"wwlogs/internal/capture"
)

// TestHistoryPerformanceCollector measures the History the same way in any build of this package, so two builds can be
// compared on one machine (A/B): opening a new history and an existing one (also as the first open of a new process), the
// write path (commit throughput and per-commit latency of the recorder's transactions), and the queries the History views
// make (year, day, library, one encounter, its analysis) on a history fixture. It runs only with WWLOGS_HISTORY_PERF_OUT
// set; WWLOGS_HISTORY_PERF_FIXTURE names a history directory to query (a copy is used), and WWLOGS_HISTORY_PERF_MASTER the
// Master for the analysis. Times come from QueryPerformanceCounter: Go's clock is too coarse here for microsecond samples.
// WWLOGS_HISTORY_PERF_QUERY_SCALE (default 1) multiplies the number of samples of every query, so a tail quantile has
// enough samples to resolve it; WWLOGS_HISTORY_PERF_SAMPLES=1 also writes every sample to history-samples.json, so the
// samples of several runs can be pooled.
func TestHistoryPerformanceCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_HISTORY_PERF_OUT")
	if os.Getenv("WWLOGS_HISTORY_PERF_COLD") != "" {
		perfColdOpen(t)
		return
	}
	if out == "" {
		t.Skip("WWLOGS_HISTORY_PERF_OUT is not set")
	}
	scale := 1
	if value := os.Getenv("WWLOGS_HISTORY_PERF_QUERY_SCALE"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("invalid WWLOGS_HISTORY_PERF_QUERY_SCALE %q", value)
		}
		scale = parsed
	}
	rawSamples := map[string][]float64{}
	report := map[string]any{"goVersion": runtime.Version(), "driver": driverVersion(), "gomaxprocs": runtime.GOMAXPROCS(0), "querySampleScale": scale}
	ctx := context.Background()

	// Opening: a new history, then the same history again with 200 encounters in it.
	var created, reopened []float64
	for i := 0; i < 40; i++ {
		path := filepath.Join(t.TempDir(), "history", "combat-history.db")
		started := perfNow()
		store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master"})
		created = append(created, perfSince(started))
		if err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
	path := filepath.Join(t.TempDir(), "history", "combat-history.db")
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master"})
	if err != nil {
		t.Fatal(err)
	}
	perfRecord(t, store, 200)
	store.Close()
	for i := 0; i < 60; i++ {
		started := perfNow()
		store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master"})
		reopened = append(reopened, perfSince(started))
		if err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
	report["openNew"] = perfSummary(created)
	report["openExisting"] = perfSummary(reopened)
	rawSamples["openNew"], rawSamples["openExisting"] = created, reopened

	// The first open of a new process, which pays every one-time cost (DPAPI service, firmware table, key parsing).
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var cold []float64
	for i := 0; i < 15; i++ {
		helper := exec.Command(executable, "-test.run=^TestHistoryPerformanceCollector$", "-test.count=1")
		helper.Env = append(os.Environ(), "WWLOGS_HISTORY_PERF_COLD="+path)
		output, err := helper.CombinedOutput()
		if err != nil {
			t.Fatalf("cold open: %v %s", err, output)
		}
		for _, line := range strings.Split(string(output), "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "cold-open-us "); ok {
				number, _ := strconv.ParseFloat(value, 64)
				cold = append(cold, number)
			}
		}
	}
	report["openExistingColdProcess"] = perfSummary(cold)
	rawSamples["openExistingColdProcess"] = cold

	// Writing: the recorder's transactions (BEGIN IMMEDIATE, one insert per combat result, COMMIT) for a fixed time.
	writePath := filepath.Join(t.TempDir(), "history", "combat-history.db")
	writer, err := Open(Options{Path: writePath, Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master"})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := writer.BeginSession(SessionStart{Source: SourceLive})
	if err != nil {
		t.Fatal(err)
	}
	span := recorder.SessionID()
	var commits []float64
	rows := int64(0)
	deadline := time.Now().Add(8 * time.Second)
	started := perfNow()
	var cpuBefore, cpuAfter windows.Rusage
	_ = windows.GetProcessTimes(windows.CurrentProcess(), &cpuBefore.CreationTime, &cpuBefore.ExitTime, &cpuBefore.KernelTime, &cpuBefore.UserTime)
	var last int64
	err = stressWriteTimed(ctx, writer.db, span, func() bool { return time.Now().After(deadline) }, func(n, lastSequence int64, micros float64) {
		commits = append(commits, micros)
		rows += lastSequence - last
		last = lastSequence
	})
	elapsed := perfSince(started) / 1e6
	_ = windows.GetProcessTimes(windows.CurrentProcess(), &cpuAfter.CreationTime, &cpuAfter.ExitTime, &cpuAfter.KernelTime, &cpuAfter.UserTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	walBefore := fileSizeOf(writePath + "-wal")
	checkpointStarted := perfNow()
	if _, err := writer.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	checkpointMicros := perfSince(checkpointStarted)
	report["walCheckpoint"] = map[string]any{"walBytes": walBefore, "micros": checkpointMicros}
	rawSamples["walCheckpoint"] = []float64{checkpointMicros}
	rawSamples["write.commitMicros"] = commits
	cpu := float64(filetimeTicks(cpuAfter.UserTime)+filetimeTicks(cpuAfter.KernelTime)-filetimeTicks(cpuBefore.UserTime)-filetimeTicks(cpuBefore.KernelTime)) / 1e7
	writer.Close()
	report["write"] = map[string]any{"seconds": elapsed, "commits": len(commits), "rows": rows, "rowsPerSecond": float64(rows) / elapsed,
		"commitsPerSecond": float64(len(commits)) / elapsed, "commitMicros": perfSummary(commits), "cpuSeconds": cpu, "fileBytes": fileSizeOf(writePath)}

	// Queries on a fixture history, as the History views make them.
	if fixture := os.Getenv("WWLOGS_HISTORY_PERF_FIXTURE"); fixture != "" {
		queryPath := copyFixture(t, fixture)
		started := perfNow()
		store, err := Open(Options{Path: queryPath, Location: time.UTC, Build: &testBuild})
		report["fixtureOpenMicros"] = perfSince(started)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		heapBefore := memory.HeapAlloc
		masters, err := capture.LoadMasterData(os.Getenv("WWLOGS_HISTORY_PERF_MASTER"), "zh-TW")
		if err != nil {
			t.Fatal(err)
		}
		defer masters.Close()
		library, err := store.Library(ctx, LibraryQuery{Limit: MaxLibraryPage}, time.UTC)
		if err != nil || len(library.Encounters) == 0 {
			t.Fatalf("library: %d (%v)", len(library.Encounters), err)
		}
		measure := func(name string, times int, run func(i int) error) {
			var samples []float64
			for i := 0; i < times; i++ {
				started := perfNow()
				if err := run(i); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				samples = append(samples, perfSince(started))
			}
			report["query."+name] = perfSummary(samples)
			rawSamples["query."+name] = samples
		}
		first := library.Encounters[len(library.Encounters)-1].StartedAt
		measure("year", 60*scale, func(int) error { _, err := store.Year(ctx, first.Year(), time.UTC); return err })
		measure("day", 60*scale, func(int) error {
			_, err := store.Day(ctx, first.UTC().Format("2006-01-02"), 0, MaxDayPage, time.UTC)
			return err
		})
		measure("library", 60*scale, func(int) error {
			_, err := store.Library(ctx, LibraryQuery{Limit: MaxLibraryPage}, time.UTC)
			return err
		})
		measure("encounter", 3*len(library.Encounters)*scale, func(i int) error {
			_, err := store.Encounter(ctx, library.Encounters[i%len(library.Encounters)].ID, time.UTC)
			return err
		})
		measure("analysis", 2*len(library.Encounters)*scale, func(i int) error {
			_, err := store.Analysis(ctx, library.Encounters[i%len(library.Encounters)].ID, AnalysisQuery{Enemy: -1, Effects: "hud"}, masters)
			return err
		})
		// A comparison loads every compared encounter's stored result and analysis.
		measure("compare5", 30*scale, func(i int) error {
			for j := 0; j < 5; j++ {
				id := library.Encounters[(i+j)%len(library.Encounters)].ID
				if _, err := store.Encounter(ctx, id, time.UTC); err != nil {
					return err
				}
				if _, err := store.Analysis(ctx, id, AnalysisQuery{Enemy: -1, Effects: "hud"}, masters); err != nil {
					return err
				}
			}
			return nil
		})
		runtime.ReadMemStats(&memory)
		report["queryHeapGrowthBytes"] = int64(memory.HeapAlloc) - int64(heapBefore)
		report["fixtureEncounters"] = len(library.Encounters)
		report["fixtureStatus"] = fmt.Sprintf("%s/%s", store.Status().State, store.Status().Reason)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "history-performance.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("WWLOGS_HISTORY_PERF_SAMPLES") != "" {
		data, _ := json.Marshal(rawSamples)
		if err := os.WriteFile(filepath.Join(out, "history-samples.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%s", data)
}

// perfColdOpen opens the history WWLOGS_HISTORY_PERF_COLD names once, as the first thing this process does with a history,
// and prints the time.
func perfColdOpen(t *testing.T) {
	started := perfNow()
	store, err := Open(Options{Path: os.Getenv("WWLOGS_HISTORY_PERF_COLD"), Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master"})
	micros := perfSince(started)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	fmt.Printf("cold-open-us %.1f\n", micros)
}

// perfRecord stores n finished encounters through a recorder, waiting for each batch.
func perfRecord(t *testing.T, store *Store, n int) {
	t.Helper()
	recorder, err := store.BeginSession(SessionStart{Source: SourceReplay, SourceSHA256: fmt.Sprintf("%064d", n)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := recorder.SubmitWait(ctx, []capture.HistoryEntry{protocolOne}, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		if err := recorder.SubmitWait(ctx, finishedEncounter(1, uint64(i+1), start.Add(time.Duration(i)*time.Minute), 30, int64(1000+i), capture.IntegrityIntact), 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Finish(ctx, capture.StopReasonReplayEnd); err != nil {
		t.Fatal(err)
	}
}

// stressWriteTimed is stressWrite with each commit timed by QueryPerformanceCounter.
func stressWriteTimed(ctx context.Context, db beginner, span int64, stop func() bool, committed func(tx, last int64, micros float64)) error {
	var started int64
	return stressWrite(ctx, db, span, func(n int64) int64 {
		started = perfNow()
		return stressTxSize(n)
	}, stop, func(tx, last int64, _ time.Duration) {
		committed(tx, last, perfSince(started))
	})
}

var (
	perfKernel32  = windows.NewLazySystemDLL("kernel32.dll")
	perfCounter   = perfKernel32.NewProc("QueryPerformanceCounter")
	perfFrequency = perfKernel32.NewProc("QueryPerformanceFrequency")
)

func perfNow() int64 {
	var value int64
	perfCounter.Call(uintptr(unsafe.Pointer(&value)))
	return value
}

// perfSince is the time since started in microseconds.
func perfSince(started int64) float64 {
	var frequency int64
	perfFrequency.Call(uintptr(unsafe.Pointer(&frequency)))
	return float64(perfNow()-started) * 1e6 / float64(frequency)
}

func perfSummary(samples []float64) map[string]float64 {
	if len(samples) == 0 {
		return map[string]float64{"n": 0}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	at := func(q float64) float64 {
		return sorted[int(math.Min(float64(len(sorted)-1), math.Ceil(q*float64(len(sorted)))-1))]
	}
	sum := 0.0
	for _, value := range sorted {
		sum += value
	}
	return map[string]float64{"n": float64(len(sorted)), "p50": at(0.5), "p95": at(0.95), "p99": at(0.99), "max": sorted[len(sorted)-1], "mean": sum / float64(len(sorted))}
}

func fileSizeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func filetimeTicks(value windows.Filetime) int64 {
	return int64(value.HighDateTime)<<32 | int64(value.LowDateTime)
}
