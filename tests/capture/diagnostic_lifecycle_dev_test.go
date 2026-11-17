//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
)

// Writer stop and restart lifecycle, and the development PCAP integrity record. Blocking writers are controllable so
// no test leaves a goroutine stuck when it ends.

// gateWriter blocks every Write until released, then forwards to an inner writer.
type gateWriter struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
	mu      sync.Mutex
	inner   io.Writer
	writes  int
}

func newGateWriter(inner io.Writer) *gateWriter {
	return &gateWriter{release: make(chan struct{}), entered: make(chan struct{}), inner: inner}
}

func (g *gateWriter) Write(p []byte) (int, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writes++
	return g.inner.Write(p)
}

func (g *gateWriter) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.writes
}

func waitGoroutines(t *testing.T, limit int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > limit && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > limit {
		t.Fatalf("goroutines = %d, want at most %d", got, limit)
	}
}

func TestPipelineCloseGivesEverySinkTheSharedDeadline(t *testing.T) {
	before := runtime.NumGoroutine()
	blocked := newGateWriter(io.Discard)
	var fast bytes.Buffer
	pipeline := newDiagnosticPipeline(true)
	stuck := pipeline.wrap("stuck", blocked, nil, 1<<20)
	healthy := pipeline.wrap("healthy", &fast, nil, 1<<20)
	stuck.Write([]byte("a\n"))
	<-blocked.entered
	for i := 0; i < 100; i++ {
		healthy.Write([]byte("record\n"))
	}
	started := time.Now()
	stats := pipeline.close(200 * time.Millisecond)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("close took %s", elapsed)
	}
	byName := map[string]diagnosticSinkStats{}
	for _, s := range stats {
		byName[s.Name] = s
	}
	// Before the fix the second sink was closed only after the first had used the whole deadline, and reported a timeout.
	if byName["healthy"].FlushTimedOut || byName["healthy"].Records != 100 || byName["healthy"].shutdown() != diagnosticShutdownClean {
		t.Fatalf("a healthy sink was starved by a blocked one: %+v", byName["healthy"])
	}
	if !byName["stuck"].FlushTimedOut || byName["stuck"].shutdown() != diagnosticShutdownTimedOut || pipelineShutdown(stats) != diagnosticShutdownTimedOut {
		t.Fatalf("the blocked sink must be reported timed out: %+v", byName["stuck"])
	}
	if pipeline.released(blocked) || !pipeline.released(&fast) {
		t.Fatal("only the drained sink's target may be released")
	}
	close(blocked.release)
	waitGoroutines(t, before)
	if !pipeline.released(blocked) {
		t.Fatal("the abandoned sink's target is released once its goroutine exits")
	}
}

func TestAbandonedWriterWritesNothingAfterItsCurrentCall(t *testing.T) {
	before := runtime.NumGoroutine()
	var file bytes.Buffer
	gate := newGateWriter(&file)
	sink := newAsyncDiagnosticWriter("stuck", gate, nil, 1<<20, false)
	sink.Write([]byte("first\n"))
	<-gate.entered
	sink.Write([]byte("second\n"))
	sink.Write([]byte("third\n"))
	if err := sink.close(50 * time.Millisecond); !errors.Is(err, errDiagnosticFlushTimeout) {
		t.Fatalf("close = %v", err)
	}
	close(gate.release)
	waitGoroutines(t, before)
	if gate.count() != 1 || file.String() != "first\n" {
		t.Fatalf("an abandoned writer kept writing: %d writes, %q", gate.count(), file.String())
	}
	stats := sink.snapshot()
	if stats.Unwritten != 2 || stats.Records != 0 || !stats.Exited {
		t.Fatalf("abandoned accounting = %+v", stats)
	}
}

func TestRestoreKeepsAbandonedTargetsBehindTheClosedSink(t *testing.T) {
	gate := newGateWriter(io.Discard)
	var events bytes.Buffer
	p := NewProcessor(nil, gate, &bytes.Buffer{}, nil)
	p.Dev.combatEventsJSONL = &events
	pipeline := newDiagnosticPipeline(true)
	restore := wrapProcessorDiagnostics(p, pipeline, nil)
	p.Dev.stdout.Write([]byte("console\n"))
	<-gate.entered
	p.Dev.combatEventsJSONL.Write([]byte("{}\n"))
	pipeline.close(100 * time.Millisecond)
	restore()
	if p.Dev.combatEventsJSONL != &events {
		t.Fatal("a drained sink's writer must be restored")
	}
	if p.Dev.stdout == io.Writer(gate) {
		t.Fatal("a writer still owned by an abandoned goroutine must not be handed back")
	}
	p.Dev.stdout.Write([]byte("late\n")) // dropped by the closed sink, never written concurrently
	close(gate.release)
}

func TestWaitingModeIsBoundedWhenTheWriterStalls(t *testing.T) {
	before := runtime.NumGoroutine()
	gate := newGateWriter(io.Discard)
	sink := newAsyncDiagnosticWriter("stalled", gate, nil, 64, true)
	sink.stall = 100 * time.Millisecond
	sink.Write(bytes.Repeat([]byte("x"), 40))
	<-gate.entered
	sink.Write(bytes.Repeat([]byte("y"), 40)) // fills the queue behind the stuck record
	started := time.Now()
	sink.Write(bytes.Repeat([]byte("z"), 40)) // must not wait forever
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("a waiting producer was held %s by a stalled writer", elapsed)
	}
	stats := sink.snapshot()
	if stats.FirstError != errDiagnosticWriterStalled.Error() || stats.Dropped == 0 {
		t.Fatalf("stall accounting = %+v", stats)
	}
	close(gate.release)
	sink.close(time.Second)
	waitGoroutines(t, before)
}

func TestRepeatedStartStopWithTimeoutsReleasesEverything(t *testing.T) {
	before := runtime.NumGoroutine()
	var gates []*gateWriter
	for round := 0; round < 25; round++ {
		pipeline := newDiagnosticPipeline(true)
		gate := newGateWriter(io.Discard)
		gates = append(gates, gate)
		stuck := pipeline.wrap("stuck", gate, nil, 1<<16)
		var buffer bytes.Buffer
		ok := pipeline.wrap("ok", &buffer, nil, 1<<16)
		stuck.Write([]byte("r\n"))
		for i := 0; i < 20; i++ {
			ok.Write([]byte("r\n"))
		}
		stats := pipeline.close(20 * time.Millisecond)
		if pipelineShutdown(stats) != diagnosticShutdownTimedOut {
			t.Fatalf("round %d shutdown = %s", round, pipelineShutdown(stats))
		}
	}
	for _, gate := range gates {
		close(gate.release)
	}
	waitGoroutines(t, before)
}

func TestLatestFileWriterAbandonsOnTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	writer := newLatestFileWriter(path)
	if err := writer.close([]byte("final"), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "final" {
		t.Fatalf("status = %q", data)
	}
}

// pcapTestRun writes packets through a development PCAP and returns its integrity record and file path.
func pcapTestRun(t *testing.T, dir, runID string, packets int, target func(file io.Writer) io.Writer, capacity int, timeout time.Duration) (diagnosticPCAPIntegrity, string, *diagnosticPipeline) {
	t.Helper()
	path := filepath.Join(dir, "capture.pcap")
	pipeline := newDiagnosticPipeline(true)
	recorder, err := openDiagnosticPCAP(path, layers.LinkTypeEthernet, runID, pipeline, time.Now(), target)
	if err != nil {
		t.Fatal(err)
	}
	if capacity > 0 {
		recorder.sink.mu.Lock()
		recorder.sink.capacity = capacity
		recorder.sink.mu.Unlock()
	}
	for i := 0; i < packets; i++ {
		data := bytes.Repeat([]byte{byte(i)}, 60)
		recorder.writePacket(gopacket.CaptureInfo{Timestamp: time.Unix(1_800_000_000, int64(i)*1000), CaptureLength: 60, Length: 60}, data)
	}
	pipeline.finishCapture()
	pipeline.close(timeout)
	record := recorder.finish(time.Now(), "requested", CaptureCounter{Status: CaptureCounterAvailable, Value: 3})
	return record, path, pipeline
}

func readIntegrity(t *testing.T, pcapPath string) diagnosticPCAPIntegrity {
	t.Helper()
	data, err := os.ReadFile(pcapIntegrityPath(pcapPath))
	if err != nil {
		t.Fatal(err)
	}
	var record diagnosticPCAPIntegrity
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestDiagnosticPCAPCompleteWhenEveryRecordIsWritten(t *testing.T) {
	record, path, _ := pcapTestRun(t, t.TempDir(), "run-1", 500, nil, 0, 5*time.Second)
	if record.State != pcapIntegrityComplete || len(record.IncompleteReasons) != 0 || record.PacketsOffered != 500 ||
		record.RecordsWritten != 500 || record.Shutdown != diagnosticShutdownClean || record.FileBytes != int64(record.ExpectedBytes) {
		t.Fatalf("complete record = %+v", record)
	}
	if record.KernelDropped.Value != 3 || record.Scope == "" {
		t.Fatalf("kernel drops are recorded beside, not inside, completeness: %+v", record)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := pcapgo.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		if _, _, err := reader.ReadPacketData(); err != nil {
			break
		}
		count++
	}
	if count != 500 {
		t.Fatalf("the PCAP holds %d packets, want 500", count)
	}
	if onDisk := readIntegrity(t, path); onDisk.State != pcapIntegrityComplete || onDisk.RunID != "run-1" {
		t.Fatalf("integrity file = %+v", onDisk)
	}
}

func TestDiagnosticPCAPIncompleteWhenTheQueueDrops(t *testing.T) {
	slow := func(file io.Writer) io.Writer { return &slowWriter{delay: 2 * time.Millisecond} }
	record, _, _ := pcapTestRun(t, t.TempDir(), "run-drop", 300, slow, 1024, 30*time.Second)
	if record.State != pcapIntegrityIncomplete || !containsRisk(record.IncompleteReasons, "records_dropped") || record.RecordsDropped == 0 ||
		record.RecordsWritten+record.RecordsDropped != record.PacketsOffered {
		t.Fatalf("dropped records must make the PCAP incomplete: %+v", record)
	}
}

func TestDiagnosticPCAPIncompleteOnWriterError(t *testing.T) {
	failing := func(file io.Writer) io.Writer { return &failingDiagnosticTarget{after: 10} }
	record, _, _ := pcapTestRun(t, t.TempDir(), "run-error", 50, failing, 1<<20, 5*time.Second)
	if record.State != pcapIntegrityIncomplete || !containsRisk(record.IncompleteReasons, "writer_error") || record.WriterError != "disk full" ||
		record.Shutdown != diagnosticShutdownFailure {
		t.Fatalf("a writer error must make the PCAP incomplete: %+v", record)
	}
}

func TestDiagnosticPCAPIncompleteOnStopTimeout(t *testing.T) {
	before := runtime.NumGoroutine()
	var gate *gateWriter
	blocking := func(file io.Writer) io.Writer { gate = newGateWriter(file); return gate }
	record, path, pipeline := pcapTestRun(t, t.TempDir(), "run-timeout", 20, blocking, 1<<20, 100*time.Millisecond)
	if record.State != pcapIntegrityIncomplete || !containsRisk(record.IncompleteReasons, "flush_timed_out") ||
		!containsRisk(record.IncompleteReasons, "writer_still_running") || record.Shutdown != diagnosticShutdownTimedOut || record.FileBytes != -1 {
		t.Fatalf("a stop timeout must make the PCAP incomplete and leave the file to its writer: %+v", record)
	}
	if pipeline.released(gate) {
		t.Fatal("the PCAP file is still owned by the abandoned writer")
	}
	close(gate.release)
	waitGoroutines(t, before)
	// Once the abandoned writer's last call returns it closes the file itself: the handle is not leaked.
	if err := os.Remove(path); err != nil {
		t.Fatalf("the PCAP handle is still held after the writer exited: %v", err)
	}
}

func TestDiagnosticPCAPRunsDoNotShareState(t *testing.T) {
	dir := t.TempDir()
	first, path, _ := pcapTestRun(t, dir, "run-a", 10, nil, 0, 5*time.Second)
	if first.State != pcapIntegrityComplete {
		t.Fatalf("first run = %+v", first)
	}
	// The second run reuses the path; while it runs, the file must not still claim the first run's completeness.
	pipeline := newDiagnosticPipeline(true)
	recorder, err := openDiagnosticPCAP(path, layers.LinkTypeEthernet, "run-b", pipeline, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if during := readIntegrity(t, path); during.State != pcapIntegrityInProgress || during.RunID != "run-b" || during.RecordsWritten != 0 {
		t.Fatalf("a new run must start from in_progress with its own ID: %+v", during)
	}
	recorder.writePacket(gopacket.CaptureInfo{Timestamp: time.Unix(1_800_000_000, 0), CaptureLength: 1, Length: 1}, []byte{1})
	pipeline.finishCapture()
	pipeline.close(5 * time.Second)
	second := recorder.finish(time.Now(), "requested", CaptureCounter{Status: CaptureCounterUnavailable})
	if second.RunID != "run-b" || second.PacketsOffered != 1 || second.State != pcapIntegrityComplete {
		t.Fatalf("second run = %+v", second)
	}
	if newDiagnosticRunID(time.Now()) == newDiagnosticRunID(time.Now()) {
		t.Fatal("run IDs must be unique")
	}
}

func TestDiagnosticDropsDoNotTouchStatisticsOrIntegrity(t *testing.T) {
	run := func(diagnostic io.Writer) (DamageStatisticsSnapshot, TransportHealthSnapshot) {
		p := NewProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
		addTestPlayerRoleGroupEntity(p, 1001, 77, 1102)
		addTestRuntimeEntity(p, 2002, func(e *RuntimeEntity) {
			e.HasEntityType, e.EntityTypeCode = true, EntityTypeMonster
			e.HasMonsterID, e.MonsterID = true, 9
		})
		p.PlayerRelevance.ObserveRoleEntity(1001)
		p.PlayerRelevance.ReconcileLocalPlayers(p.Entities)
		p.Dev.combatEventsJSONL = diagnostic
		p.DamageStatistics.ObserveBattleState(true, encounterAt(1), 0)
		for i := 0; i < 200; i++ {
			event := testDamageEvent(p, 1001, 2002)
			event.RPCID, event.Timestamp, event.EventTypeCandidate = uint64(i+1), encounterAt(2+float64(i)/100), "DAMAGE_CANDIDATE"
			p.WriteDamageEvent(event)
		}
		return p.DamageStatisticsSnapshot(), p.TransportHealthSnapshot()
	}
	reference, _ := run(nil)
	pipeline := newDiagnosticPipeline(true)
	sink := pipeline.wrap("combat-events", &slowWriter{delay: 5 * time.Millisecond}, nil, 512)
	withDrops, transport := run(sink)
	pipeline.close(30 * time.Second)
	if pipeline.droppedRecords() == 0 {
		t.Fatal("the fixture must drop diagnostic records")
	}
	if withDrops.DamageDealt != reference.DamageDealt || withDrops.OutgoingEvents != reference.OutgoingEvents || reference.OutgoingEvents == 0 {
		t.Fatalf("diagnostic drops changed statistics: %d/%d vs %d/%d", withDrops.DamageDealt, withDrops.OutgoingEvents, reference.DamageDealt, reference.OutgoingEvents)
	}
	if withDrops.Integrity.Status != IntegrityIntact || transport.State != TransportHealthy {
		t.Fatalf("diagnostic drops must not touch data integrity or transport health: %+v %s", withDrops.Integrity, transport.State)
	}
}
