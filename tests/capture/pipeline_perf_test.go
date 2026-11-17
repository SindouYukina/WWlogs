package capture

import (
	"bufio"
	"encoding/json"
	"io"
	"math"
	"os"
	"runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"

	"wwlogs/internal/buildinfo"
)

// Capture pipeline measurement shared by both build profiles: the replay helpers the measurement collectors use, and
// TestReplayPipelineCollector. Every collector is skipped unless its output variable is set, and reads only paths supplied
// through the environment.
//
// TestReplayPipelineCollector replays preloaded captures through the pipeline as it runs in the profile the test binary
// was built for, so a Public and a Development build of one source tree, and builds of two source trees, can be compared
// on one machine (the Development-only diagnostics cost is TestReplayPerformanceCollector's). Per round:
//
//	decode:   packet decoding and direction only (gopacket), the cost before WWlogs code runs;
//	pipeline: decode plus KCP, frames, decryption, combat decoding, statistics, encounters and effects, untimed per call;
//	latency:  the same work with every datagram timed by QueryPerformanceCounter, and a view snapshot built and encoded
//	          every 250 ms of capture time, the way the host's publisher builds one (timed the same way).
//
// It reports wall and process CPU time, allocations, GC cycles and pause, and heap per phase; the combat and effect events
// the pipeline accepted; and the per-datagram and per-snapshot distributions in microseconds, with the encoded snapshot
// sizes. Go's clock resolves only about a millisecond on this platform, so per-call samples use QueryPerformanceCounter.
//
// Environment: WWLOGS_PIPELINE_PERF_OUT (output file), WWLOGS_PERF_PCAPS ("name|path" per line), WWLOGS_PERF_MASTER_DB
// (empty: the embedded Master), WWLOGS_PERF_ROUNDS (default 3).

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

// readSegmentDropList reads a "name|absolute path" capture list; blank lines and # comments are skipped.
func readSegmentDropList(t *testing.T, path string) []equivalencePCAP {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var pcaps []equivalencePCAP
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, found := strings.Cut(line, "|")
		if !found {
			t.Fatalf("malformed PCAP list line %q", line)
		}
		pcaps = append(pcaps, equivalencePCAP{name: name, path: path})
	}
	if len(pcaps) == 0 {
		t.Fatal("PCAP list is empty")
	}
	return pcaps
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
	return udp.Payload, PacketDirection(packet, "192.168.1.2", udp, 13100, 13120), true
}

func replayRawCapture(p *Processor, capture rawCapture, rewrite func([]byte), offset time.Duration, streams map[string]*Stream, stats *CaptureStats) {
	for _, raw := range capture.packets {
		payload, direction, ok := decodeCapturePacket(capture.link, raw)
		if !ok {
			continue
		}
		stats.UDPPackets++
		if rewrite != nil {
			payload = append([]byte(nil), payload...)
			rewrite(payload)
		}
		ConsumeDatagram(payload, direction, raw.info.Timestamp.Add(offset), streams, p, stats)
	}
}

// perfDistribution summarizes samples (microseconds or bytes): count, mean, quantiles and maximum. A quantile is reported
// only when the samples resolve it (at least 1/(1-q) of them); a smaller set reports its maximum instead, never a guess.
func perfDistribution(samples []float64) map[string]any {
	if len(samples) == 0 {
		return map[string]any{"n": 0}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, value := range sorted {
		sum += value
	}
	result := map[string]any{"n": len(sorted), "mean": sum / float64(len(sorted)), "max": sorted[len(sorted)-1]}
	for _, quantile := range []struct {
		name string
		q    float64
	}{{"p50", 0.5}, {"p95", 0.95}, {"p99", 0.99}, {"p999", 0.999}} {
		if float64(len(sorted)) < 1/(1-quantile.q) {
			continue
		}
		index := int(math.Ceil(quantile.q*float64(len(sorted)))) - 1
		result[quantile.name] = sorted[index]
	}
	return result
}

func TestReplayPipelineCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_PIPELINE_PERF_OUT")
	if out == "" {
		t.Skip("WWLOGS_PIPELINE_PERF_OUT is not set")
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
	result := map[string]any{"profile": string(buildinfo.Current().Profile), "captures": len(captures), "packets": total,
		"rounds": rounds, "goVersion": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "numCPU": runtime.NumCPU()}
	phases := map[string][]phaseMeasure{}
	var accepted []map[string]uint64
	var latency []map[string]any
	for round := 0; round < rounds; round++ {
		phases["decode"] = append(phases["decode"], measurePhase(total, func() {
			for _, capture := range captures {
				for _, raw := range capture.packets {
					decodeCapturePacket(capture.link, raw)
				}
			}
		}))
		counts := map[string]uint64{}
		phases["pipeline"] = append(phases["pipeline"], measurePhase(total, func() {
			for _, capture := range captures {
				p := NewProcessor(key, io.Discard, io.Discard, nil)
				p.Masters = masters
				streams, stats := make(map[string]*Stream), CaptureStats{}
				replayRawCapture(p, capture, nil, 0, streams, &stats)
				FinishTransport(streams, p, &stats)
				p.FinishCapture()
				report := p.CurrentStatisticsReport()
				_ = report.String()
				counts["combatEvents"] += report.OutgoingEvents + report.IncomingEvents
				counts["effectApplies"] += report.EffectApplies
				counts["effectRemoves"] += report.EffectRemoves
				counts["encounters"] += uint64(report.Encounters)
			}
		}))
		accepted = append(accepted, counts)
		latency = append(latency, measurePipelineLatency(t, captures, key, masters))
	}
	result["phases"] = phases
	result["acceptedPerRound"] = accepted
	result["latencyPerRound"] = latency
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// measurePipelineLatency replays every capture once with each datagram timed, building and encoding a view snapshot every
// 250 ms of capture time as the publisher does.
func measurePipelineLatency(t *testing.T, captures []rawCapture, key *StaticRSA, masters *GameMaster) map[string]any {
	t.Helper()
	runtime.GC()
	var datagrams, snapshots, snapshotBytes []float64
	for _, capture := range captures {
		p := NewProcessor(key, io.Discard, io.Discard, nil)
		p.Masters = masters
		streams, stats := make(map[string]*Stream), CaptureStats{}
		var nextSnapshot time.Time
		for _, raw := range capture.packets {
			payload, direction, ok := decodeCapturePacket(capture.link, raw)
			if !ok {
				continue
			}
			stats.UDPPackets++
			started := qpcNow()
			ConsumeDatagram(payload, direction, raw.info.Timestamp, streams, p, &stats)
			datagrams = append(datagrams, qpcMicrosSince(started))
			if !raw.info.Timestamp.Before(nextSnapshot) {
				nextSnapshot = raw.info.Timestamp.Add(250 * time.Millisecond)
				started = qpcNow()
				data, err := json.Marshal(p.ViewSnapshot())
				snapshots = append(snapshots, qpcMicrosSince(started))
				if err != nil {
					t.Fatal(err)
				}
				snapshotBytes = append(snapshotBytes, float64(len(data)))
			}
		}
		FinishTransport(streams, p, &stats)
		p.FinishCapture()
	}
	return map[string]any{"datagramMicros": perfDistribution(datagrams), "snapshotMicros": perfDistribution(snapshots),
		"snapshotBytes": perfDistribution(snapshotBytes)}
}
