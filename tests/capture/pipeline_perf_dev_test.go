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
	"runtime/metrics"
	"strconv"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// Development-profile, test-only measurement collectors for the capture pipeline. Both are skipped unless their
// output variable is set and read only paths supplied through the environment.
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

type rawCapturePacket struct {
	info gopacket.CaptureInfo
	data []byte
}

type rawCapture struct {
	name     string
	link     layers.LinkType
	packets  []rawCapturePacket
	duration time.Duration
}

func loadRawCaptures(t *testing.T, listPath string) []rawCapture {
	t.Helper()
	var captures []rawCapture
	for _, entry := range readSegmentDropList(t, listPath) {
		handle, err := pcap.OpenOffline(entry.path)
		if err != nil {
			t.Fatalf("open %s: %v", entry.path, err)
		}
		if err := handle.SetBPFFilter(equivalenceFilter); err != nil {
			t.Fatal(err)
		}
		capture := rawCapture{name: entry.name, link: handle.LinkType()}
		for {
			data, info, err := handle.ReadPacketData()
			if err != nil {
				break
			}
			capture.packets = append(capture.packets, rawCapturePacket{info: info, data: append([]byte(nil), data...)})
		}
		handle.Close()
		if n := len(capture.packets); n > 1 {
			capture.duration = capture.packets[n-1].info.Timestamp.Sub(capture.packets[0].info.Timestamp)
		}
		captures = append(captures, capture)
	}
	return captures
}

// cpuSeconds is the process CPU time the Go runtime has used: available time minus idle time.
func cpuSeconds() float64 {
	samples := []metrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}, {Name: "/cpu/classes/idle:cpu-seconds"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindFloat64 || samples[1].Value.Kind() != metrics.KindFloat64 {
		return 0
	}
	return samples[0].Value.Float64() - samples[1].Value.Float64()
}

type phaseMeasure struct {
	Packets       int     `json:"packets"`
	WallSeconds   float64 `json:"wallSeconds"`
	CPUSeconds    float64 `json:"cpuSeconds"`
	PacketsPerSec float64 `json:"packetsPerSecond"`
	NsPerPacket   float64 `json:"nsPerPacket"`
	AllocsPerPkt  float64 `json:"allocsPerPacket"`
	BytesPerPkt   float64 `json:"bytesPerPacket"`
	GCCycles      uint32  `json:"gcCycles"`
	GCPauseMs     float64 `json:"gcPauseMs"`
	HeapInuseMB   float64 `json:"heapInuseMBAtEnd"`
}

func measurePhase(packets int, run func()) phaseMeasure {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpu := cpuSeconds()
	started := time.Now()
	run()
	wall := time.Since(started)
	cpuUsed := cpuSeconds() - cpu
	runtime.ReadMemStats(&after)
	m := phaseMeasure{Packets: packets, WallSeconds: wall.Seconds(), CPUSeconds: cpuUsed,
		GCCycles: after.NumGC - before.NumGC, GCPauseMs: float64(after.PauseTotalNs-before.PauseTotalNs) / 1e6,
		HeapInuseMB: float64(after.HeapInuse) / (1 << 20)}
	if packets > 0 {
		m.PacketsPerSec = float64(packets) / wall.Seconds()
		m.NsPerPacket = float64(wall.Nanoseconds()) / float64(packets)
		m.AllocsPerPkt = float64(after.Mallocs-before.Mallocs) / float64(packets)
		m.BytesPerPkt = float64(after.TotalAlloc-before.TotalAlloc) / float64(packets)
	}
	return m
}

// decodeCapturePacket mirrors the executables: decode the packet, find UDP, decide the direction.
func decodeCapturePacket(link layers.LinkType, raw rawCapturePacket) ([]byte, string, bool) {
	packet := gopacket.NewPacket(raw.data, link, gopacket.Default)
	udpLayer := packet.Layer(layers.LayerTypeUDP)
	if udpLayer == nil {
		return nil, "", false
	}
	udp := udpLayer.(*layers.UDP)
	return udp.Payload, packetDirection(packet, "192.168.1.2", udp, 13100, 13120), true
}

func replayRawCapture(p *processor, capture rawCapture, rewrite func([]byte), offset time.Duration, streams map[string]*stream, stats *captureStats) {
	for _, raw := range capture.packets {
		payload, direction, ok := decodeCapturePacket(capture.link, raw)
		if !ok {
			continue
		}
		stats.udpPackets++
		if rewrite != nil {
			payload = append([]byte(nil), payload...)
			rewrite(payload)
		}
		consumeDatagram(payload, direction, raw.info.Timestamp.Add(offset), streams, p, stats)
	}
}

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
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := loadGameMasterDatabase(os.Getenv("WWLOGS_PERF_MASTER_DB"), "zh-TW")
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
				p := newProcessor(key, io.Discard, io.Discard, nil)
				p.masters = masters
				streams, stats := make(map[string]*stream), captureStats{}
				replayRawCapture(p, capture, nil, 0, streams, &stats)
				finishTransport(streams, p, &stats)
				p.finishCapture()
				_ = p.currentStatisticsReport().String()
			}
		}))
		directory := t.TempDir()
		phases["diagnostics"] = append(phases["diagnostics"], measurePhase(total, func() {
			for i, capture := range captures {
				p := newProcessor(key, io.Discard, io.Discard, nil)
				p.masters = masters
				files, finish := perfDiagnosticFiles(t, filepath.Join(directory, strconv.Itoa(i)), p)
				streams, stats := make(map[string]*stream), captureStats{}
				replayRawCapture(p, capture, nil, 0, streams, &stats)
				finishTransport(streams, p, &stats)
				p.finishCapture()
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
	p := newProcessor(key, io.Discard, io.Discard, nil)
	p.masters = masters
	streams, stats := make(map[string]*stream), captureStats{}
	replayRawCapture(p, largest, nil, 0, streams, &stats)
	const calls = 2000
	started := time.Now()
	for i := 0; i < calls; i++ {
		_ = p.currentStatisticsReport().String()
		_ = p.transportHealthSnapshot()
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
	noise := make([]byte, maxResyncRetainedBytes)
	random.Read(noise)
	var worst time.Duration
	started = time.Now()
	const passes = 50
	for i := 0; i < passes; i++ {
		passStart := time.Now()
		findResyncFrame(noise, nil, &captureStats{})
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
func perfDiagnosticFiles(t *testing.T, directory string, p *processor) ([]*os.File, func()) {
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
	p.combatEventsJSONL, p.buffLifecycle, p.buffSnapshots = events, buffEvents, buffSnapshots
	p.frameArchive, p.encounterOut, p.stdout = archive, encounters, consoleWriter
	p.damageStatistics.recordEncounterTransitions(true)
	finish := perfWrapDiagnostics(p, consoleWriter)
	return []*os.File{events, buffEvents, buffSnapshots, archive, encounters, console}, func() {
		finish()
		consoleWriter.Flush()
	}
}
