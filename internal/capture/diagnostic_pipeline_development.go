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
// are kept. Every wait is bounded: a writer that makes no progress for diagnosticStallTimeout is declared stalled and
// its records are dropped, so a stuck disk can never hold up the capture goroutine indefinitely.
//
// Capacities come from the archived LIVE bundles: the busiest sink, the one-second live diagnostic snapshot, averaged
// at most 137 KB/s; combat events at most 20 KB/s; the PCAP at most 12 KB/s. 8 MiB therefore holds about a minute of
// the busiest sink, and the 32 MiB PCAP queue tens of minutes of a stalled disk.
//
// Stopping (close) gives every sink one shared deadline. A sink that drained exits and releases its target. A sink that
// did not is abandoned: its goroutine may still be inside one operating-system write that cannot be cancelled, but it
// performs no further write or flush after that call returns, and the caller must not touch its target (see released).
// Abandoned records are counted as unwritten. The operating-system handle is released when that call returns or when
// the process exits.
const (
	diagnosticSinkCapacityBytes = 8 << 20
	diagnosticPCAPCapacityBytes = 32 << 20
	diagnosticConsoleCapacity   = 4 << 20
	// diagnosticFlushTimeout bounds how long stopping waits for queued records.
	diagnosticFlushTimeout = 10 * time.Second
	// diagnosticStallTimeout bounds one wait for queue space in waiting mode.
	diagnosticStallTimeout = 10 * time.Second
)

var (
	// errDiagnosticFlushTimeout reports that a sink still held records when the stop deadline expired.
	errDiagnosticFlushTimeout = errors.New("diagnostic sink did not drain before the deadline")
	// errDiagnosticWriterStalled reports a writer that made no progress while a producer waited for space.
	errDiagnosticWriterStalled = errors.New("diagnostic writer made no progress")
)

// Shutdown outcomes, from best to worst. They are kept apart because each tells a different story about the files.
const (
	diagnosticShutdownClean    = "clean"
	diagnosticShutdownFailure  = "writer_failure"
	diagnosticShutdownTimedOut = "timed_out"
)

type diagnosticSinkStats struct {
	Name          string
	Records       uint64
	Bytes         uint64
	Dropped       uint64
	DroppedBytes  uint64
	Unwritten     uint64 // records still queued when the sink was abandoned
	WriteErrors   uint64
	FirstError    string
	PeakQueue     int
	FlushTimedOut bool
	Exited        bool
}

// shutdown classifies a stopped sink.
func (s diagnosticSinkStats) shutdown() string {
	switch {
	case s.FlushTimedOut:
		return diagnosticShutdownTimedOut
	case s.WriteErrors > 0:
		return diagnosticShutdownFailure
	default:
		return diagnosticShutdownClean
	}
}

// asyncDiagnosticWriter is one bounded diagnostic sink. Write never returns an error to the capture path: drops and
// writer failures are counted and reported through stats.
type asyncDiagnosticWriter struct {
	name     string
	target   io.Writer
	flush    func() error
	capacity int
	stall    time.Duration

	mu        sync.Mutex
	available *sync.Cond // signalled when the writer goroutine frees queue space or gives up
	pending   *sync.Cond // signalled when a record is queued or the sink closes
	queue     [][]byte
	queued    int
	block     bool
	closed    bool
	abandoned bool
	failed    error
	progress  uint64 // completed target writes, for stall detection
	stats     diagnosticSinkStats
	done      chan struct{}
	// onRelease runs once the goroutine no longer uses the target, so the target's owner can close it.
	onRelease func()
}

func newAsyncDiagnosticWriter(name string, target io.Writer, flush func() error, capacity int, block bool) *asyncDiagnosticWriter {
	w := &asyncDiagnosticWriter{name: name, target: target, flush: flush, capacity: capacity, block: block,
		stall: diagnosticStallTimeout, done: make(chan struct{})}
	w.available = sync.NewCond(&w.mu)
	w.pending = sync.NewCond(&w.mu)
	w.stats.Name = name
	go w.run()
	return w
}

// Write queues one record. In drop mode a record that does not fit is dropped; in waiting mode the caller waits for
// space, at most until the writer has made no progress for the stall timeout. A record larger than the whole capacity
// is always dropped.
func (w *asyncDiagnosticWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.failed != nil || len(p) > w.capacity {
		w.drop(len(p))
		return len(p), nil
	}
	if w.queued+len(p) > w.capacity && w.block {
		w.waitForSpaceLocked(len(p))
	}
	if w.closed || w.failed != nil || w.queued+len(p) > w.capacity {
		w.drop(len(p))
		return len(p), nil
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

// waitForSpaceLocked waits until size fits, the sink closes or fails, or the writer stalls.
func (w *asyncDiagnosticWriter) waitForSpaceLocked(size int) {
	lastProgress, since := w.progress, time.Now()
	wake := time.AfterFunc(w.stall, func() {
		w.mu.Lock()
		w.available.Broadcast()
		w.mu.Unlock()
	})
	defer wake.Stop()
	for w.queued+size > w.capacity && !w.closed && w.failed == nil {
		w.available.Wait()
		if w.progress != lastProgress {
			lastProgress, since = w.progress, time.Now()
			wake.Reset(w.stall)
			continue
		}
		if time.Since(since) >= w.stall {
			w.fail(errDiagnosticWriterStalled)
			w.available.Broadcast()
			return
		}
	}
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
	defer func() {
		w.mu.Lock()
		w.stats.Exited = true
		w.queue, w.queued = nil, 0
		release := w.onRelease
		w.onRelease = nil
		w.mu.Unlock()
		close(w.done)
		if release != nil {
			release()
		}
	}()
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.closed && !w.abandoned {
			w.pending.Wait()
		}
		if w.abandoned || (len(w.queue) == 0 && w.closed) {
			// A failed or abandoned target is not flushed: it may be the one that is stuck.
			flush := !w.abandoned && w.failed == nil
			w.mu.Unlock()
			if flush {
				w.flushTarget()
			}
			return
		}
		batch := w.queue
		w.queue = nil
		w.mu.Unlock()

		for i, record := range batch {
			_, err := w.target.Write(record)
			w.mu.Lock()
			// fail and abandonment reset the queue while a record is in flight; never let the count go negative.
			if w.queued -= len(record); w.queued < 0 {
				w.queued = 0
			}
			w.progress++
			if w.abandoned {
				// The stop deadline passed while this write was in progress: write nothing more.
				w.stats.Unwritten += uint64(len(batch) - i - 1)
				w.available.Broadcast()
				w.mu.Unlock()
				return
			}
			if err == nil && w.failed != nil {
				// The writer was declared stalled while this call was in progress; the rest is not written.
				w.stats.Records++
				w.stats.Bytes += uint64(len(record))
				for _, rest := range batch[i+1:] {
					w.drop(len(rest))
				}
				w.available.Broadcast()
				w.mu.Unlock()
				break
			}
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
		idle := len(w.queue) == 0 && !w.abandoned && w.failed == nil
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

// beginClose stops accepting records and lets the goroutine drain.
func (w *asyncDiagnosticWriter) beginClose() {
	w.mu.Lock()
	w.closed = true
	w.pending.Broadcast()
	w.available.Broadcast()
	w.mu.Unlock()
}

// awaitClose waits until deadline for the goroutine to exit. On timeout the sink is abandoned (see the file comment).
func (w *asyncDiagnosticWriter) awaitClose(deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-w.done:
		return nil
	case <-timer.C:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	default:
	}
	w.abandoned = true
	w.stats.FlushTimedOut = true
	w.stats.Unwritten += uint64(len(w.queue))
	w.queue, w.queued = nil, 0
	w.pending.Broadcast()
	w.available.Broadcast()
	return errDiagnosticFlushTimeout
}

// close stops one sink within timeout.
func (w *asyncDiagnosticWriter) close(timeout time.Duration) error {
	w.beginClose()
	return w.awaitClose(time.Now().Add(timeout))
}

// whenReleased runs fn once the goroutine no longer uses the target: now if it has already exited, otherwise when it
// does. An abandoned writer therefore closes its own file after its last operating-system call returns.
func (w *asyncDiagnosticWriter) whenReleased(fn func()) {
	w.mu.Lock()
	if w.stats.Exited {
		w.mu.Unlock()
		fn()
		return
	}
	w.onRelease = fn
	w.mu.Unlock()
}

// exited reports whether the goroutine has returned and no longer uses the target.
func (w *asyncDiagnosticWriter) exited() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *asyncDiagnosticWriter) snapshot() diagnosticSinkStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// diagnosticPipeline owns every asynchronous sink of one capture run. A pipeline is never reused: a new run creates a
// new pipeline over new targets, so an abandoned writer of an earlier run cannot share them.
type diagnosticPipeline struct {
	block bool
	sinks []*asyncDiagnosticWriter
	// byTarget maps each wrapped target to its sink, for released.
	byTarget map[io.Writer]*asyncDiagnosticWriter
}

func newDiagnosticPipeline(live bool) *diagnosticPipeline {
	return &diagnosticPipeline{block: !live, byTarget: map[io.Writer]*asyncDiagnosticWriter{}}
}

// wrap returns an asynchronous sink for target, or nil when target is nil.
func (d *diagnosticPipeline) wrap(name string, target io.Writer, flush func() error, capacity int) io.Writer {
	if target == nil {
		return nil
	}
	sink := newAsyncDiagnosticWriter(name, target, flush, capacity, d.block)
	d.sinks = append(d.sinks, sink)
	d.byTarget[target] = sink
	return sink
}

// finishCapture keeps every remaining record: nothing is lost to waiting once packets no longer arrive.
func (d *diagnosticPipeline) finishCapture() {
	for _, sink := range d.sinks {
		sink.setBlocking(true)
	}
}

// close stops every sink: all of them stop accepting records at once, then each is awaited against one shared
// deadline, so a blocked sink cannot use up the time of the others.
func (d *diagnosticPipeline) close(timeout time.Duration) []diagnosticSinkStats {
	deadline := time.Now().Add(timeout)
	for _, sink := range d.sinks {
		sink.beginClose()
	}
	for _, sink := range d.sinks {
		_ = sink.awaitClose(deadline)
	}
	return d.stats()
}

// released reports whether target may be used directly by the caller: it was never wrapped, or its sink has exited.
// A target of an abandoned sink stays unreleased until that goroutine returns from its operating-system call.
func (d *diagnosticPipeline) released(target io.Writer) bool {
	if d == nil || target == nil {
		return true
	}
	sink, ok := d.byTarget[target]
	return !ok || sink.exited()
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

// pipelineShutdown is the worst sink outcome.
func pipelineShutdown(stats []diagnosticSinkStats) string {
	outcome := diagnosticShutdownClean
	for _, sink := range stats {
		switch sink.shutdown() {
		case diagnosticShutdownTimedOut:
			return diagnosticShutdownTimedOut
		case diagnosticShutdownFailure:
			outcome = diagnosticShutdownFailure
		}
	}
	return outcome
}

// diagnosticPipelineLine reports the pipeline at the end of a run: counts and error text only, never record content.
func diagnosticPipelineLine(runID string, stats []diagnosticSinkStats) string {
	var b strings.Builder
	var records, dropped, unwritten, errorsTotal uint64
	timeouts := 0
	for _, sink := range stats {
		records += sink.Records
		dropped += sink.Dropped
		unwritten += sink.Unwritten
		errorsTotal += sink.WriteErrors
		if sink.FlushTimedOut {
			timeouts++
		}
	}
	fmt.Fprintf(&b, "diagnostic_pipeline run_id=%s shutdown=%s sinks=%d records=%d dropped_records=%d unwritten_records=%d write_errors=%d flush_timeouts=%d",
		runID, pipelineShutdown(stats), len(stats), records, dropped, unwritten, errorsTotal, timeouts)
	for _, sink := range stats {
		fmt.Fprintf(&b, " %s=%d/%d/%d/peak:%d", sink.Name, sink.Records, sink.Dropped, sink.WriteErrors, sink.PeakQueue)
	}
	return b.String()
}

// wrapProcessorDiagnostics moves the processor's diagnostic writers behind the pipeline and returns a function that,
// once the pipeline is closed, restores the direct writers whose sinks exited. A writer whose sink was abandoned stays
// behind the closed sink, so later writes are dropped and counted instead of racing the abandoned goroutine.
func wrapProcessorDiagnostics(p *Processor, d *diagnosticPipeline, flush map[io.Writer]func() error) func() {
	type field struct {
		name string
		ptr  *io.Writer
	}
	fields := []field{
		{"console", &p.Dev.stdout}, {"diagnostic", &p.Diagnostic}, {"audit", &p.Dev.audit},
		{"combat-events", &p.Dev.combatEventsJSONL}, {"buff-snapshots", &p.Dev.buffSnapshots}, {"buff-events", &p.Dev.buffLifecycle},
		{"combat-notify-audit", &p.Dev.combatNotifyAudit}, {"frame-archive", &p.Dev.frameArchive}, {"schema-decode", &p.Dev.schemaDecodeOut},
		{"part-refs", &p.Dev.partRefOut}, {"role-config", &p.Dev.roleConfigOut}, {"hp-state", &p.Dev.hpStateOut},
		{"damage-push", &p.Dev.damagePushOut}, {"client-push", &p.Dev.clientPushOut}, {"bullet-refs", &p.Dev.bulletRefOut},
		{"shield-refs", &p.Dev.shieldRefOut}, {"skill-refs", &p.Dev.skillRefOut}, {"summon-owner", &p.Dev.summonOwnerOut},
		{"combat-state", &p.Dev.combatStateOut}, {"encounter-events", &p.Dev.encounterOut},
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
			if d.released(originals[i]) {
				*f.ptr = originals[i]
			}
		}
	}
}

// latestFileWriter rewrites one small file (the run status) from its own goroutine. Only the newest content is kept:
// a write that is still in progress makes the next submission replace the pending one instead of waiting.
type latestFileWriter struct {
	path      string
	mu        sync.Mutex
	next      []byte
	closed    bool
	abandoned bool
	wake      chan struct{}
	done      chan struct{}
	errs      uint64
	lastErr   string
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
		data, closed, abandoned := w.next, w.closed, w.abandoned
		w.next = nil
		w.mu.Unlock()
		if abandoned {
			return
		}
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

// close writes the final content from the goroutine within timeout. On timeout the writer is abandoned: it writes
// nothing after its current call returns.
func (w *latestFileWriter) close(final []byte, timeout time.Duration) error {
	w.mu.Lock()
	w.closed = true
	w.next = final
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
		return nil
	case <-timer.C:
		w.mu.Lock()
		w.abandoned = true
		w.mu.Unlock()
		return errDiagnosticFlushTimeout
	}
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
