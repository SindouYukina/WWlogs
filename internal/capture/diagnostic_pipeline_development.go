//go:build wwdev

package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/gopacket"
)

// Development diagnostics are written off the capture goroutine. Each sink owns a bounded FIFO of whole records
// (one Write call is one record) and one writer goroutine, so a slow disk, a slow console, or a failed file never makes
// the capture loop wait.
//
// Policy: during live capture a full queue drops the new record and counts it; the packet path never blocks. During
// offline replay there is no packet to lose, so a full queue makes the producer wait instead, and replay output stays
// complete and deterministic. After the capture loop ends the pipeline switches to waiting as well, so final records
// are kept.
//
// Capacities come from the archived LIVE bundles: the busiest sink, the one-second live diagnostic snapshot, averaged
// at most 137 KB/s; combat events at most 20 KB/s; the PCAP at most 12 KB/s. 8 MiB therefore holds about a minute of
// the busiest sink, and the 32 MiB PCAP queue tens of minutes of a stalled disk.
const (
	diagnosticSinkCapacityBytes = 8 << 20
	diagnosticPCAPCapacityBytes = 32 << 20
	diagnosticConsoleCapacity   = 4 << 20
	// diagnosticFlushTimeout bounds how long stopping waits for queued records.
	diagnosticFlushTimeout = 10 * time.Second
)

// errDiagnosticFlushTimeout reports that a sink still held records when the stop timeout expired.
var errDiagnosticFlushTimeout = errors.New("diagnostic sink did not drain before the timeout")

type diagnosticSinkStats struct {
	Name          string
	Records       uint64
	Bytes         uint64
	Dropped       uint64
	DroppedBytes  uint64
	WriteErrors   uint64
	FirstError    string
	PeakQueue     int
	FlushTimedOut bool
}

// asyncDiagnosticWriter is one bounded diagnostic sink. Write never returns an error to the capture path: drops and
// writer failures are counted and reported through stats.
type asyncDiagnosticWriter struct {
	name     string
	target   io.Writer
	flush    func() error
	capacity int

	mu        sync.Mutex
	available *sync.Cond // signalled when the writer goroutine frees queue space
	pending   *sync.Cond // signalled when a record is queued or the sink closes
	queue     [][]byte
	queued    int
	block     bool
	closed    bool
	failed    error
	stats     diagnosticSinkStats
	done      chan struct{}
}

func newAsyncDiagnosticWriter(name string, target io.Writer, flush func() error, capacity int, block bool) *asyncDiagnosticWriter {
	w := &asyncDiagnosticWriter{name: name, target: target, flush: flush, capacity: capacity, block: block, done: make(chan struct{})}
	w.available = sync.NewCond(&w.mu)
	w.pending = sync.NewCond(&w.mu)
	w.stats.Name = name
	go w.run()
	return w
}

// Write queues one record. In drop mode a record that does not fit is dropped; in wait mode the caller waits for
// space. A record larger than the whole capacity is always dropped.
func (w *asyncDiagnosticWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.failed != nil || len(p) > w.capacity {
		w.drop(len(p))
		return len(p), nil
	}
	for w.queued+len(p) > w.capacity {
		if !w.block {
			w.drop(len(p))
			return len(p), nil
		}
		w.available.Wait()
		if w.closed || w.failed != nil {
			w.drop(len(p))
			return len(p), nil
		}
	}
	record := append([]byte(nil), p...)
	w.queue = append(w.queue, record)
	w.queued += len(record)
	if w.queued > w.stats.PeakQueue {
		w.stats.PeakQueue = w.queued
	}
	w.pending.Signal()
	return len(p), nil
}

func (w *asyncDiagnosticWriter) drop(size int) {
	w.stats.Dropped++
	w.stats.DroppedBytes += uint64(size)
}

func (w *asyncDiagnosticWriter) setBlocking(block bool) {
	w.mu.Lock()
	w.block = block
	w.mu.Unlock()
}

func (w *asyncDiagnosticWriter) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.closed {
			w.pending.Wait()
		}
		if len(w.queue) == 0 && w.closed {
			w.mu.Unlock()
			w.flushTarget()
			return
		}
		batch := w.queue
		w.queue = nil
		w.mu.Unlock()

		for i, record := range batch {
			_, err := w.target.Write(record)
			w.mu.Lock()
			w.queued -= len(record)
			if err != nil {
				// fail drops what is still queued; the rest of this batch can no longer be written either.
				w.fail(err)
				for _, rest := range batch[i+1:] {
					w.drop(len(rest))
				}
				w.drop(len(record))
				w.available.Broadcast()
				w.mu.Unlock()
				break
			}
			w.stats.Records++
			w.stats.Bytes += uint64(len(record))
			w.available.Broadcast()
			w.mu.Unlock()
		}
		w.mu.Lock()
		idle := len(w.queue) == 0
		w.mu.Unlock()
		if idle {
			// Push buffered bytes out whenever the queue runs dry, so a crash loses little.
			w.flushTarget()
		}
	}
}

func (w *asyncDiagnosticWriter) flushTarget() {
	if w.flush == nil {
		return
	}
	if err := w.flush(); err != nil {
		w.mu.Lock()
		w.fail(err)
		w.mu.Unlock()
	}
}

// fail records the first writer error. Later records are dropped; the capture path is never told.
func (w *asyncDiagnosticWriter) fail(err error) {
	w.stats.WriteErrors++
	if w.failed == nil {
		w.failed = err
		w.stats.FirstError = err.Error()
		for _, record := range w.queue {
			w.drop(len(record))
		}
		w.queue, w.queued = nil, 0
	}
}

// close stops accepting records and waits up to timeout for the queue to drain. A writer blocked in the operating
// system cannot be interrupted; the timeout keeps stopping bounded and the sink reports the records it still held.
func (w *asyncDiagnosticWriter) close(timeout time.Duration) error {
	w.mu.Lock()
	w.closed = true
	w.pending.Broadcast()
	w.available.Broadcast()
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-time.After(timeout):
		w.mu.Lock()
		w.stats.FlushTimedOut = true
		w.mu.Unlock()
		return errDiagnosticFlushTimeout
	}
}

func (w *asyncDiagnosticWriter) snapshot() diagnosticSinkStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// diagnosticPipeline owns every asynchronous sink of one capture run.
type diagnosticPipeline struct {
	block bool
	sinks []*asyncDiagnosticWriter
}

func newDiagnosticPipeline(live bool) *diagnosticPipeline {
	return &diagnosticPipeline{block: !live}
}

// wrap returns an asynchronous sink for target, or nil when target is nil.
func (d *diagnosticPipeline) wrap(name string, target io.Writer, flush func() error, capacity int) io.Writer {
	if target == nil {
		return nil
	}
	sink := newAsyncDiagnosticWriter(name, target, flush, capacity, d.block)
	d.sinks = append(d.sinks, sink)
	return sink
}

// finishCapture keeps every remaining record: nothing is lost to waiting once packets no longer arrive.
func (d *diagnosticPipeline) finishCapture() {
	for _, sink := range d.sinks {
		sink.setBlocking(true)
	}
}

// close drains and stops every sink, sharing one deadline.
func (d *diagnosticPipeline) close(timeout time.Duration) []diagnosticSinkStats {
	deadline := time.Now().Add(timeout)
	for _, sink := range d.sinks {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		_ = sink.close(remaining)
	}
	return d.stats()
}

func (d *diagnosticPipeline) stats() []diagnosticSinkStats {
	stats := make([]diagnosticSinkStats, 0, len(d.sinks))
	for _, sink := range d.sinks {
		stats = append(stats, sink.snapshot())
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Name < stats[j].Name })
	return stats
}

// droppedRecords sums dropped records across sinks: one diagnostic-layer measure, unrelated to packet loss.
func (d *diagnosticPipeline) droppedRecords() uint64 {
	var total uint64
	for _, sink := range d.sinks {
		total += sink.snapshot().Dropped
	}
	return total
}

// diagnosticPipelineLine reports the pipeline at the end of a run: counts and error text only, never record content.
func diagnosticPipelineLine(stats []diagnosticSinkStats) string {
	var b strings.Builder
	var records, dropped, errorsTotal uint64
	timeouts := 0
	for _, sink := range stats {
		records += sink.Records
		dropped += sink.Dropped
		errorsTotal += sink.WriteErrors
		if sink.FlushTimedOut {
			timeouts++
		}
	}
	fmt.Fprintf(&b, "diagnostic_pipeline sinks=%d records=%d dropped_records=%d write_errors=%d flush_timeouts=%d", len(stats), records, dropped, errorsTotal, timeouts)
	for _, sink := range stats {
		fmt.Fprintf(&b, " %s=%d/%d/%d/peak:%d", sink.Name, sink.Records, sink.Dropped, sink.WriteErrors, sink.PeakQueue)
	}
	return b.String()
}

// wrapProcessorDiagnostics moves the processor's diagnostic writers behind the pipeline and returns a function that
// restores the direct writers once the pipeline is closed, for the synchronous end-of-run summaries.
func wrapProcessorDiagnostics(p *processor, d *diagnosticPipeline, flush map[io.Writer]func() error) func() {
	type field struct {
		name string
		ptr  *io.Writer
	}
	fields := []field{
		{"console", &p.stdout}, {"diagnostic", &p.diagnostic}, {"audit", &p.audit},
		{"combat-events", &p.combatEventsJSONL}, {"buff-snapshots", &p.buffSnapshots}, {"buff-events", &p.buffLifecycle},
		{"combat-notify-audit", &p.combatNotifyAudit}, {"frame-archive", &p.frameArchive}, {"schema-decode", &p.schemaDecodeOut},
		{"part-refs", &p.partRefOut}, {"role-config", &p.roleConfigOut}, {"hp-state", &p.hpStateOut},
		{"damage-push", &p.damagePushOut}, {"client-push", &p.clientPushOut}, {"bullet-refs", &p.bulletRefOut},
		{"shield-refs", &p.shieldRefOut}, {"skill-refs", &p.skillRefOut}, {"summon-owner", &p.summonOwnerOut},
		{"combat-state", &p.combatStateOut}, {"encounter-events", &p.encounterOut},
	}
	originals := make([]io.Writer, len(fields))
	for i, f := range fields {
		originals[i] = *f.ptr
		if *f.ptr == nil {
			continue
		}
		capacity := diagnosticSinkCapacityBytes
		if f.name == "console" || f.name == "diagnostic" {
			capacity = diagnosticConsoleCapacity
		}
		*f.ptr = d.wrap(f.name, *f.ptr, flush[*f.ptr], capacity)
	}
	return func() {
		for i, f := range fields {
			*f.ptr = originals[i]
		}
	}
}

// latestFileWriter rewrites one small file (the run status) from its own goroutine. Only the newest content is kept:
// a write that is still in progress makes the next submission replace the pending one instead of waiting.
type latestFileWriter struct {
	path    string
	mu      sync.Mutex
	next    []byte
	closed  bool
	wake    chan struct{}
	done    chan struct{}
	errs    uint64
	lastErr string
}

func newLatestFileWriter(path string) *latestFileWriter {
	w := &latestFileWriter{path: path, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go w.run()
	return w
}

func (w *latestFileWriter) submit(data []byte) {
	w.mu.Lock()
	if !w.closed {
		w.next = data
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *latestFileWriter) run() {
	defer close(w.done)
	for range w.wake {
		w.mu.Lock()
		data, closed := w.next, w.closed
		w.next = nil
		w.mu.Unlock()
		if data != nil {
			if err := os.WriteFile(w.path, data, 0o600); err != nil {
				w.mu.Lock()
				w.errs++
				w.lastErr = err.Error()
				w.mu.Unlock()
			}
		}
		if closed {
			w.mu.Lock()
			pending := w.next
			w.mu.Unlock()
			if pending == nil {
				return
			}
		}
	}
}

// close writes the final content synchronously after the goroutine stops, within timeout.
func (w *latestFileWriter) close(final []byte, timeout time.Duration) error {
	w.mu.Lock()
	w.closed = true
	w.next = final
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	select {
	case <-w.done:
	case <-time.After(timeout):
		return errDiagnosticFlushTimeout
	}
	return nil
}

func (w *latestFileWriter) errors() (uint64, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.errs, w.lastErr
}

// pcapPacketRecord encodes one classic PCAP record (microsecond timestamps, little endian, as pcapgo writes it) so the
// header and data are queued as a single record and can never be split by a drop.
func pcapPacketRecord(info gopacket.CaptureInfo, data []byte) []byte {
	record := make([]byte, 16+len(data))
	binary.LittleEndian.PutUint32(record[0:4], uint32(info.Timestamp.Unix()))
	binary.LittleEndian.PutUint32(record[4:8], uint32(info.Timestamp.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(record[8:12], uint32(len(data)))
	length := info.Length
	if length < len(data) {
		length = len(data)
	}
	binary.LittleEndian.PutUint32(record[12:16], uint32(length))
	copy(record[16:], data)
	return record
}
