//go:build wwdev

package capture

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

// soakSample is one minute of the soak run.
type soakSample struct {
	Minute            int     `json:"minute"`
	HeapAllocMB       float64 `json:"heapAllocMBAfterGC"`
	HeapInuseMB       float64 `json:"heapInuseMB"`
	Goroutines        int     `json:"goroutines"`
	Streams           int     `json:"kcpStreams"`
	PendingSegments   int     `json:"pendingSegments"`
	Captures          int     `json:"capturesReplayed"`
	Restarts          int     `json:"restarts"`
	Packets           int     `json:"packetsProcessed"`
	DiagnosticDropped uint64  `json:"diagnosticDropped"`
	PeakQueueBytes    int     `json:"peakDiagnosticQueueBytes"`
	TransportState    string  `json:"transportState"`
	Encounters        int     `json:"completedEncountersHeld"`
	ActiveEffects     int     `json:"activeEffects"`
}

func TestCaptureSoakCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_SOAK_OUT")
	if out == "" {
		t.Skip("WWLOGS_SOAK_OUT is not set")
	}
	duration := 60 * time.Minute
	if value := os.Getenv("WWLOGS_SOAK_DURATION"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			t.Fatal(err)
		}
		duration = parsed
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
	runtime.GC()
	baselineGoroutines := runtime.NumGoroutine()
	const capturesPerSession = 8
	var samples []soakSample
	started := time.Now()
	nextSample := started
	capturesReplayed, restarts, packets, iteration := 0, 0, 0, 0
	var dropped uint64
	peakQueue := 0
	for time.Since(started) < duration {
		restarts++
		p := newProcessor(key, io.Discard, io.Discard, nil)
		p.masters = masters
		pipeline := newDiagnosticPipeline(true)
		slow := &slowWriter{delay: 200 * time.Microsecond}
		p.combatEventsJSONL = pipeline.wrap("combat-events", slow, nil, 1<<20)
		p.frameArchive = pipeline.wrap("frame-archive", io.Discard, nil, diagnosticSinkCapacityBytes)
		p.captureLayer.diagnosticDropCount = pipeline.droppedRecords
		streams, stats := make(map[string]*stream), captureStats{}
		var offset time.Duration
		for i := 0; i < capturesPerSession && time.Since(started) < duration; i++ {
			capture := captures[iteration%len(captures)]
			iteration++
			mask := uint32(iteration) << 24
			replayRawCapture(p, capture, func(payload []byte) { rewriteKCPConversations(payload, mask) }, offset, streams, &stats)
			offset += capture.duration + 10*time.Second
			capturesReplayed++
			packets += len(capture.packets)
			slow.mu.Lock()
			slow.buf.Reset() // the slow writer stands for a disk; its content is not kept
			slow.mu.Unlock()
			if time.Now().After(nextSample) {
				runtime.GC()
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				health := summarizeKCPTransport(streams)
				snapshot := p.damageStatisticsSnapshot()
				sinks := pipeline.stats()
				queue := 0
				for _, sink := range sinks {
					if sink.PeakQueue > queue {
						queue = sink.PeakQueue
					}
				}
				if queue > peakQueue {
					peakQueue = queue
				}
				samples = append(samples, soakSample{
					Minute: int(time.Since(started).Minutes()), HeapAllocMB: float64(memory.HeapAlloc) / (1 << 20),
					HeapInuseMB: float64(memory.HeapInuse) / (1 << 20), Goroutines: runtime.NumGoroutine(), Streams: health.Streams,
					PendingSegments: health.PendingSegments, Captures: capturesReplayed, Restarts: restarts, Packets: packets,
					DiagnosticDropped: dropped + pipeline.droppedRecords(), PeakQueueBytes: queue,
					TransportState: string(p.transportHealthSnapshot().State), Encounters: len(snapshot.Encounters.Completed),
					ActiveEffects: len(p.effects.Snapshot().Effects),
				})
				nextSample = nextSample.Add(time.Minute)
			}
			if len(streams) > maxKCPStreams {
				t.Fatalf("stream table grew past its bound: %d", len(streams))
			}
		}
		finishTransport(streams, p, &stats)
		p.finishCapture()
		pipeline.finishCapture()
		for _, sink := range pipeline.close(diagnosticFlushTimeout) {
			if sink.FlushTimedOut {
				t.Fatalf("sink %s did not drain at restart", sink.Name)
			}
		}
		dropped += pipeline.droppedRecords()
	}
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()
	result := map[string]any{
		"durationMinutes": time.Since(started).Minutes(), "captures": len(captures), "capturesReplayed": capturesReplayed,
		"restarts": restarts, "packets": packets, "baselineGoroutines": baselineGoroutines, "finalGoroutines": finalGoroutines,
		"diagnosticDropped": dropped, "peakDiagnosticQueueBytes": peakQueue, "samples": samples,
	}
	if len(samples) >= 20 {
		// Heap after a collection: the second half of the run against the second quarter. A bounded workload stays flat.
		quarter, half := len(samples)/4, len(samples)/2
		var early, late []float64
		for _, sample := range samples[quarter:half] {
			early = append(early, sample.HeapAllocMB)
		}
		for _, sample := range samples[half:] {
			late = append(late, sample.HeapAllocMB)
		}
		result["heapMedianSecondQuarterMB"], result["heapMedianSecondHalfMB"] = median(early), median(late)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if finalGoroutines > baselineGoroutines {
		t.Fatalf("goroutines leaked: baseline %d, final %d", baselineGoroutines, finalGoroutines)
	}
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// rewriteKCPConversations makes every KCP segment of a replayed datagram belong to a new conversation, so repeated
// replays look like reconnects instead of duplicates of the same stream.
func rewriteKCPConversations(payload []byte, mask uint32) {
	for len(payload) >= 24 {
		length := int(binary.LittleEndian.Uint32(payload[20:24]))
		if 24+length > len(payload) || payload[4] < 81 || payload[4] > 84 {
			return
		}
		binary.LittleEndian.PutUint32(payload[:4], binary.LittleEndian.Uint32(payload[:4])^mask)
		payload = payload[24+length:]
	}
}
