//go:build wwdev

package capture

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
)

// Stress tests for the development diagnostic pipeline: slow, failing, and blocked writers must never hold up the
// producer in live (drop) mode, every record is whole, and stopping is bounded.

type slowWriter struct {
	mu    sync.Mutex
	delay time.Duration
	buf   bytes.Buffer
}

func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *slowWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

type failingDiagnosticTarget struct{ after int }

func (w *failingDiagnosticTarget) Write(p []byte) (int, error) {
	if w.after <= 0 {
		return 0, errors.New("disk full")
	}
	w.after--
	return len(p), nil
}

type blockedWriter struct{ release chan struct{} }

func (w *blockedWriter) Write(p []byte) (int, error) {
	<-w.release
	return len(p), nil
}

// diagnosticRecord is one 999-byte line ending in its index.
func diagnosticRecord(i int) []byte {
	return []byte(fmt.Sprintf("%s|%08d\n", strings.Repeat("x", 990), i))
}

func TestDiagnosticSinkNeverBlocksTheProducerInLiveMode(t *testing.T) {
	target := &slowWriter{delay: 5 * time.Millisecond}
	sink := newAsyncDiagnosticWriter("test", target, nil, 64<<10, false)
	started := time.Now()
	const records = 2000
	for i := 0; i < records; i++ {
		if _, err := sink.Write(diagnosticRecord(i)); err != nil {
			t.Fatalf("a full queue must not surface an error: %v", err)
		}
	}
	elapsed := time.Since(started)
	// Writing 2000 records synchronously would take 10 s at 5 ms each.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("producer waited %s for a slow writer", elapsed)
	}
	if err := sink.close(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	stats := sink.snapshot()
	if stats.Dropped == 0 || stats.Records+stats.Dropped != records || stats.PeakQueue > 64<<10 {
		t.Fatalf("drop accounting = %+v", stats)
	}
	for _, line := range strings.Split(strings.TrimSuffix(target.String(), "\n"), "\n") {
		if len(line) != 999 {
			t.Fatalf("a record was split or merged: %d bytes", len(line))
		}
	}
}

func TestDiagnosticSinkKeepsEveryRecordInReplayMode(t *testing.T) {
	target := &slowWriter{delay: 100 * time.Microsecond}
	sink := newAsyncDiagnosticWriter("test", target, nil, 8<<10, true)
	const records = 500
	for i := 0; i < records; i++ {
		sink.Write(diagnosticRecord(i))
	}
	if err := sink.close(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	stats := sink.snapshot()
	if stats.Dropped != 0 || stats.Records != records {
		t.Fatalf("replay mode lost records: %+v", stats)
	}
	lines := strings.Split(strings.TrimSuffix(target.String(), "\n"), "\n")
	for i, line := range lines {
		if !strings.HasSuffix(line, fmt.Sprintf("|%08d", i)) {
			t.Fatalf("record %d out of order: %q", i, line[len(line)-9:])
		}
	}
}

func TestDiagnosticSinkWriterFailureDoesNotDeadlock(t *testing.T) {
	for _, block := range []bool{false, true} {
		sink := newAsyncDiagnosticWriter("test", &failingDiagnosticTarget{after: 3}, nil, 4<<10, block)
		done := make(chan struct{})
		go func() {
			for i := 0; i < 1000; i++ {
				sink.Write(diagnosticRecord(i))
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("producer deadlocked on a failed writer (block=%v)", block)
		}
		if err := sink.close(5 * time.Second); err != nil {
			t.Fatal(err)
		}
		stats := sink.snapshot()
		if stats.FirstError != "disk full" || stats.WriteErrors == 0 || stats.Records != 3 || stats.Records+stats.Dropped != 1000 {
			t.Fatalf("failure accounting (block=%v) = %+v", block, stats)
		}
	}
}

func TestDiagnosticSinkCloseIsBoundedWhenTheWriterHangs(t *testing.T) {
	target := &blockedWriter{release: make(chan struct{})}
	sink := newAsyncDiagnosticWriter("test", target, nil, 64<<10, false)
	sink.Write(diagnosticRecord(1))
	sink.Write(diagnosticRecord(2))
	started := time.Now()
	err := sink.close(100 * time.Millisecond)
	if !errors.Is(err, errDiagnosticFlushTimeout) || time.Since(started) > time.Second {
		t.Fatalf("close = %v after %s", err, time.Since(started))
	}
	if !sink.snapshot().FlushTimedOut {
		t.Fatal("a timed-out flush must be reported")
	}
	close(target.release)
	<-sink.done
}

func TestDiagnosticSinkDropsOversizedRecordAndAfterClose(t *testing.T) {
	var target bytes.Buffer
	sink := newAsyncDiagnosticWriter("test", &target, nil, 1024, true)
	sink.Write(make([]byte, 2048))
	if err := sink.close(time.Second); err != nil {
		t.Fatal(err)
	}
	sink.Write([]byte("late\n"))
	if stats := sink.snapshot(); stats.Dropped != 2 || stats.Records != 0 || target.Len() != 0 {
		t.Fatalf("oversized and late records = %+v", stats)
	}
}

func TestDiagnosticPipelineStopsAllGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	for round := 0; round < 20; round++ {
		pipeline := newDiagnosticPipeline(true)
		var targets [4]bytes.Buffer
		writers := []interface{ Write([]byte) (int, error) }{}
		for i := range targets {
			writers = append(writers, pipeline.wrap("sink", &targets[i], nil, 1<<20))
		}
		for _, w := range writers {
			for i := 0; i < 50; i++ {
				w.Write(diagnosticRecord(i))
			}
		}
		pipeline.finishCapture()
		for _, stats := range pipeline.close(5 * time.Second) {
			if stats.Records != 50 || stats.Dropped != 0 {
				t.Fatalf("round %d stats = %+v", round, stats)
			}
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines before=%d after=%d", before, after)
	}
}

func TestWrapProcessorDiagnosticsRestoresDirectWriters(t *testing.T) {
	var console, events bytes.Buffer
	p := newProcessor(nil, &console, &bytes.Buffer{}, nil)
	p.combatEventsJSONL = &events
	pipeline := newDiagnosticPipeline(false)
	restore := wrapProcessorDiagnostics(p, pipeline, nil)
	if p.stdout == &console || p.combatEventsJSONL == &events || p.audit != nil || p.frameArchive != nil {
		t.Fatal("writers were not wrapped, or nil writers were wrapped")
	}
	p.stdout.Write([]byte("console line\n"))
	p.combatEventsJSONL.Write([]byte("{}\n"))
	pipeline.finishCapture()
	pipeline.close(5 * time.Second)
	restore()
	if p.stdout != &console || p.combatEventsJSONL != &events {
		t.Fatal("restore did not reinstate the direct writers")
	}
	if console.String() != "console line\n" || events.String() != "{}\n" {
		t.Fatalf("records were not delivered: %q %q", console.String(), events.String())
	}
}

func TestLatestFileWriterKeepsNewestStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	writer := newLatestFileWriter(path)
	for i := 0; i < 1000; i++ {
		writer.submit([]byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	if err := writer.close([]byte(`{"state":"STOPPED"}`), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"state":"STOPPED"}` {
		t.Fatalf("final status = %q %v", data, err)
	}
	if count, _ := writer.errors(); count != 0 {
		t.Fatalf("status write errors = %d", count)
	}
}

func TestPCAPPacketRecordMatchesPcapgo(t *testing.T) {
	info := gopacket.CaptureInfo{Timestamp: time.Date(2026, 11, 4, 1, 2, 3, 456789000, time.UTC), CaptureLength: 5, Length: 60}
	data := []byte{1, 2, 3, 4, 5}
	var reference bytes.Buffer
	writer := pcapgo.NewWriter(&reference)
	if err := writer.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		t.Fatal(err)
	}
	header := reference.Len()
	if err := writer.WritePacket(info, data); err != nil {
		t.Fatal(err)
	}
	if got := pcapPacketRecord(info, data); !bytes.Equal(got, reference.Bytes()[header:]) {
		t.Fatalf("record = %x, pcapgo = %x", got, reference.Bytes()[header:])
	}
}
