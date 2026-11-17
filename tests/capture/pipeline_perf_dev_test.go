//go:build wwdev

package capture

import (
	"bufio"
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// Development-profile, test-only measurement collectors for the capture pipeline. Both are skipped unless their
// output variable is set and read only paths supplied through the environment. The replay helpers they share with the
// profile-independent TestReplayPipelineCollector are in pipeline_perf_test.go.
//
// TestReplayPerformanceCollector replays preloaded captures in three phases per round:
//   decode:      packet decoding and direction only (gopacket), the cost before WWlogs code runs;
//   pipeline:    decode plus KCP, frames, decryption, combat decoding, statistics, encounters, effects;
//   diagnostics: pipeline plus the development per-event sinks writing to real files (perfWrapDiagnostics).
// It reports wall time, process CPU time, allocations, GC cycles and pause, and heap, plus per-call costs of the
// snapshot paths and the worst-case resynchronization pass.
//
// TestCaptureSoakCollector runs a continuous synthetic workload for WWLOGS_SOAK_DURATION: archived captures replayed
// back to back with rewritten KCP conversations and shifted timestamps (a capture that keeps reconnecting), restarted
// every few captures, with the development pipeline in live drop mode behind a deliberately slow writer. Every minute
// it samples the heap after a collection, goroutines, KCP streams, pending data, and diagnostic drops.
//
// Environment: WWLOGS_PERF_OUT or WWLOGS_SOAK_OUT (output file), WWLOGS_PERF_PCAPS ("name|path" per line),
// WWLOGS_PERF_MASTER_DB, WWLOGS_PERF_ROUNDS (default 3), WWLOGS_SOAK_DURATION (default 60m).

func TestReplayPerformanceCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_PERF_OUT")
	if out == "" {
		t.Skip("WWLOGS_PERF_OUT is not set")
	}
	rounds := 3
	if value := os.Getenv("WWLOGS_PERF_ROUNDS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("invalid WWLOGS_PERF_ROUNDS %q", value)
		}
		rounds = parsed
	}
	captures := loadRawCaptures(t, os.Getenv("WWLOGS_PERF_PCAPS"))
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := LoadGameMasterDatabase(os.Getenv("WWLOGS_PERF_MASTER_DB"), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, capture := range captures {
		total += len(capture.packets)
	}
	result := map[string]any{"captures": len(captures), "packets": total, "rounds": rounds, "goVersion": runtime.Version(),
		"gomaxprocs": runtime.GOMAXPROCS(0), "numCPU": runtime.NumCPU()}
	phases := map[string][]phaseMeasure{}
	for round := 0; round < rounds; round++ {
		phases["decode"] = append(phases["decode"], measurePhase(total, func() {
			for _, capture := range captures {
				for _, raw := range capture.packets {
					decodeCapturePacket(capture.link, raw)
				}
			}
		}))
		phases["pipeline"] = append(phases["pipeline"], measurePhase(total, func() {
			for _, capture := range captures {
				p := NewProcessor(key, io.Discard, io.Discard, nil)
				p.Masters = masters
				streams, stats := make(map[string]*Stream), CaptureStats{}
				replayRawCapture(p, capture, nil, 0, streams, &stats)
				FinishTransport(streams, p, &stats)
				p.FinishCapture()
				_ = p.CurrentStatisticsReport().String()
			}
		}))
		directory := t.TempDir()
		phases["diagnostics"] = append(phases["diagnostics"], measurePhase(total, func() {
			for i, capture := range captures {
				p := NewProcessor(key, io.Discard, io.Discard, nil)
				p.Masters = masters
				files, finish := perfDiagnosticFiles(t, filepath.Join(directory, strconv.Itoa(i)), p)
				streams, stats := make(map[string]*Stream), CaptureStats{}
				replayRawCapture(p, capture, nil, 0, streams, &stats)
				FinishTransport(streams, p, &stats)
				p.FinishCapture()
				p.writeEncounterFinal()
				finish()
				for _, file := range files {
					file.Close()
				}
			}
		}))
	}
	result["phases"] = phases

	// Per-call costs of the snapshot paths a console, status file, or UI publisher would call.
	largest := captures[0]
	for _, capture := range captures {
		if len(capture.packets) > len(largest.packets) {
			largest = capture
		}
	}
	p := NewProcessor(key, io.Discard, io.Discard, nil)
	p.Masters = masters
	streams, stats := make(map[string]*Stream), CaptureStats{}
	replayRawCapture(p, largest, nil, 0, streams, &stats)
	const calls = 2000
	started := time.Now()
	for i := 0; i < calls; i++ {
		_ = p.CurrentStatisticsReport().String()
		_ = p.TransportHealthSnapshot()
	}
	result["statisticsReportAndHealthMicrosPerCall"] = float64(time.Since(started).Microseconds()) / calls
	started = time.Now()
	for i := 0; i < 200; i++ {
		snapshot := p.liveDiagnosticSnapshot(time.Now())
		data, _ := json.Marshal(snapshot)
		_ = data
	}
	result["liveDiagnosticSnapshotMillisPerCall"] = float64(time.Since(started).Microseconds()) / 200 / 1000
	snapshotData, _ := json.Marshal(p.liveDiagnosticSnapshot(time.Now()))
	result["liveDiagnosticSnapshotBytes"] = len(snapshotData)
	result["largestCapture"] = largest.name

	// Worst-case resynchronization: one search pass over a full retained buffer of noise.
	random := rand.New(rand.NewSource(1))
	noise := make([]byte, MaxResyncRetainedBytes)
	random.Read(noise)
	var worst time.Duration
	started = time.Now()
	const passes = 50
	for i := 0; i < passes; i++ {
		passStart := time.Now()
		FindResyncFrame(noise, nil, &CaptureStats{})
		if elapsed := time.Since(passStart); elapsed > worst {
			worst = elapsed
		}
	}
	result["resyncPassMeanMillis"] = float64(time.Since(started).Microseconds()) / passes / 1000
	result["resyncPassWorstMillis"] = float64(worst.Microseconds()) / 1000
	result["resyncPassBufferBytes"] = len(noise)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// perfDiagnosticFiles points the development per-event sinks at real files and returns a function that finishes them.
func perfDiagnosticFiles(t *testing.T, directory string, p *Processor) ([]*os.File, func()) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	open := func(name string) *os.File {
		file, err := os.Create(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	events, buffEvents, buffSnapshots, archive, encounters, console := open("combat-events.jsonl"), open("buff-events.jsonl"),
		open("buff-snapshots.jsonl"), open("frames.jsonl"), open("encounters.jsonl"), open("console.log")
	consoleWriter := bufio.NewWriter(console)
	p.Dev.combatEventsJSONL, p.Dev.buffLifecycle, p.Dev.buffSnapshots = events, buffEvents, buffSnapshots
	p.Dev.frameArchive, p.Dev.encounterOut, p.Dev.stdout = archive, encounters, consoleWriter
	p.recordEncounterTransitions()
	finish := perfWrapDiagnostics(p, consoleWriter)
	return []*os.File{events, buffEvents, buffSnapshots, archive, encounters, console}, func() {
		finish()
		consoleWriter.Flush()
	}
}
