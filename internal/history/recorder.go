package history

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wwlogs/internal/capture"
)

// Capture session sources.
const (
	SourceLive   = "live"
	SourceReplay = "replay"
)

var (
	// ErrNotRecording reports a Store that cannot record (see Status).
	ErrNotRecording = errors.New("the combat history is not recording")
	// ErrDuplicateSource reports a replayed file whose capture is already in the history.
	ErrDuplicateSource = errors.New("this capture file is already in the combat history")
	// ErrFinishTimeout reports a session whose queued history was still being stored when the deadline passed. Nothing is
	// marked complete: the session stays open and the next open of the file marks it abandoned if it never finishes.
	ErrFinishTimeout = errors.New("the combat history was still being stored at the deadline")
)

// SessionStart describes a capture session about to run.
type SessionStart struct {
	Source string
	// SourceSHA256 identifies a replayed file; empty for live capture.
	SourceSHA256 string
	Level        capture.HistoryLevel
	// Paused is a capture that starts with recording switched off: no session row exists until the engine starts a span.
	Paused bool
}

// Recorder bounds. The capture goroutine never waits: what does not fit is counted and its encounters marked incomplete.
const (
	maxQueuedEntries            = 1 << 17
	maxQueuedBatches            = 1 << 12
	maxTransactionEntries       = 1 << 13
	writeLinger                 = 200 * time.Millisecond
	maxTaintedEncounters        = 1024
	maxConsecutiveWriteFailures = 3
	writeTimeout                = 30 * time.Second
)

// Reasons an encounter is incomplete or was abandoned (encounter.incomplete_reason).
const (
	incompleteEntriesLost       = "entries_lost"
	incompleteApplicationExit   = "application_exit"
	incompleteRecordingDisabled = "recording_disabled"
	incompleteNotFinished       = "not_finished"
)

// Recorder stores one capture session's journal. It is the session's capture.HistorySink: Submit only queues, and one
// goroutine of its own writes the queue in transactions.
//
// One capture can hold several recorded spans (capture.HistoryRecordingStarted ... Stopped); each span is one capture_session
// row. A Recorder that begins unpaused creates the row of its first span at once, as before spans existed; every later span
// gets its row when its start entry is stored.
type Recorder struct {
	store  *Store
	start  SessionStart
	level  capture.HistoryLevel
	queue  chan []capture.HistoryEntry
	queued atomic.Int64
	done   chan struct{}
	// firstSpan is the row id of the first span, once it exists.
	firstSpan atomic.Int64

	mu             sync.Mutex
	closed         bool
	journalDropped uint64
	queueDropped   uint64
	// tainted holds encounter keys whose entries never reached storage; taintedAll is set when there were too many to keep.
	tainted    map[string]struct{}
	taintedAll bool

	// The fields below belong to the writer goroutine.
	// span is the row of the span being recorded; 0 between spans. sourceUsed is set once a span carried the replay digest.
	span        int64
	sourceUsed  bool
	writeFailed uint64
	failures    int
	stopped     bool
	// encounters maps the engine's encounter keys to their rows across every span of the capture.
	encounters map[string]int64
	finishOnce sync.Once
	finishErr  error
}

// BeginSession prepares the history of a capture session and starts its writer. Unless the capture starts paused, it also
// records the session row of the first span.
func (s *Store) BeginSession(start SessionStart) (*Recorder, error) {
	if s == nil {
		return nil, ErrNotRecording
	}
	if start.Source != SourceLive && start.Source != SourceReplay {
		return nil, fmt.Errorf("unknown capture source %q", start.Source)
	}
	if start.Level == capture.HistoryOff {
		return nil, errors.New("a history session needs a history level")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.status.State != StateReady || s.db == nil {
		return nil, ErrNotRecording
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if start.Source == SourceReplay && start.SourceSHA256 != "" {
		var existing int64
		err := s.db.QueryRowContext(ctx, "SELECT id FROM capture_session WHERE source = ? AND source_sha256 = ?", start.Source, start.SourceSHA256).Scan(&existing)
		if err == nil {
			return nil, ErrDuplicateSource
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("check history source: %w", err)
		}
	}
	recorder := &Recorder{
		store: s, start: start, level: start.Level, queue: make(chan []capture.HistoryEntry, maxQueuedBatches), done: make(chan struct{}),
		tainted: map[string]struct{}{}, encounters: map[string]int64{},
	}
	if !start.Paused {
		id, used, err := recorder.insertSpan(ctx, s.db, capture.HistoryStartCapture, false)
		if err != nil {
			return nil, err
		}
		recorder.span, recorder.sourceUsed = id, used
		recorder.firstSpan.Store(id)
	}
	s.sessions.Add(1)
	go recorder.run()
	return recorder, nil
}

// execer is what insertSpan needs from a database handle or a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertSpan records the session row of a new span and reports whether it carries the replay digest. Only the first span of
// a replay carries the file's digest, so the same capture is never stored twice and a later span of it does not collide.
func (r *Recorder) insertSpan(ctx context.Context, db execer, reason string, sourceUsed bool) (int64, bool, error) {
	s := r.store
	uid, err := newSessionUID()
	if err != nil {
		return 0, false, err
	}
	var digest any
	if r.start.Source == SourceReplay && r.start.SourceSHA256 != "" && !sourceUsed {
		digest = r.start.SourceSHA256
	}
	events := boolInt(r.level >= capture.HistoryEvents)
	opened := s.now()
	zone, offset := opened.In(s.location).Zone()
	result, err := db.ExecContext(ctx, `INSERT INTO capture_session (session_uid, source, source_sha256, status, opened_at_ns, utc_offset_s, zone_name,
		history_level, app_version, app_build, app_commit, app_dirty, app_profile, protocol_schema, master_sha256,
		combat_events_recorded, effect_events_recorded, skill_casts_recorded, start_reason)
		VALUES (?, ?, ?, 'open', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		uid, r.start.Source, digest, opened.UnixNano(), offset, zone, levelName(r.level),
		s.build.Version, int64(s.build.BuildNumber), s.build.Commit, s.build.Dirty, string(s.build.Profile), s.protocol, s.master,
		events, events, reason)
	if err != nil {
		return 0, false, fmt.Errorf("record capture session: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("record capture session: %w", err)
	}
	return id, digest != nil, nil
}

func newSessionUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create history session identity: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// SessionID is the row id of the first recorded span, or 0 before any span exists.
func (r *Recorder) SessionID() int64 { return r.firstSpan.Load() }

// Level is the history level this session records at.
func (r *Recorder) Level() capture.HistoryLevel { return r.level }

// Submit queues one drain of the journal. It never waits: a full queue, or a queue after Finish, drops the batch, counts
// it and marks the encounters it named incomplete.
func (r *Recorder) Submit(entries []capture.HistoryEntry, dropped uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if dropped > 0 {
		r.journalDropped += dropped
		// The journal cannot say which encounters lost an entry, so none of this span's encounters can be complete.
		r.taintedAll = true
	}
	if len(entries) == 0 {
		return
	}
	if !r.closed && r.queued.Load()+int64(len(entries)) <= maxQueuedEntries {
		select {
		case r.queue <- entries:
			r.queued.Add(int64(len(entries)))
			return
		default:
		}
	}
	r.queueDropped += uint64(len(entries))
	r.taintLocked(entries)
}

func (r *Recorder) taintLocked(entries []capture.HistoryEntry) {
	for _, entry := range entries {
		if entry.Encounter == "" {
			continue
		}
		if len(r.tainted) >= maxTaintedEncounters {
			r.taintedAll = true
			return
		}
		r.tainted[entry.Encounter] = struct{}{}
	}
}

// run is the writer goroutine. It gathers what arrives within writeLinger into one transaction.
func (r *Recorder) run() {
	defer close(r.done)
	for {
		batch, ok := <-r.queue
		if !ok {
			return
		}
		// Capacity is capped so gathering never appends into the backing array of a submitted batch.
		entries := batch[:len(batch):len(batch)]
		linger := time.NewTimer(writeLinger)
	gather:
		for len(entries) < maxTransactionEntries {
			select {
			case next, open := <-r.queue:
				if !open {
					break gather
				}
				entries = append(entries, next...)
			case <-linger.C:
				break gather
			}
		}
		linger.Stop()
		r.write(entries)
		r.queued.Add(-int64(len(entries)))
	}
}

// write stores entries in one transaction. A failed transaction loses all of them: they are counted and their encounters
// marked incomplete, and after maxConsecutiveWriteFailures the session stops trying.
func (r *Recorder) write(entries []capture.HistoryEntry) {
	if r.stopped {
		r.lose(entries)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	w := sessionWriter{recorder: r, staged: map[string]int64{}, span: r.span, sourceUsed: r.sourceUsed}
	pending := r.takeTaint()
	var unresolved []string
	err := r.inTransaction(ctx, func(tx *sql.Tx) error {
		w.tx = tx
		for _, entry := range entries {
			if err := w.apply(ctx, entry); err != nil {
				return fmt.Errorf("%s: %w", entry.Kind, err)
			}
		}
		var err error
		unresolved, err = w.applyTaint(ctx, pending)
		return err
	})
	if err != nil {
		r.retaint(pending.keys)
		r.lose(entries)
		r.failures++
		if r.failures >= maxConsecutiveWriteFailures {
			r.stopped = true
		}
		return
	}
	r.failures = 0
	r.span, r.sourceUsed = w.span, w.sourceUsed
	for _, id := range w.newSpans {
		r.firstSpan.CompareAndSwap(0, id)
	}
	r.retaint(unresolved)
	for key, id := range w.staged {
		if id == 0 {
			delete(r.encounters, key)
			continue
		}
		r.encounters[key] = id
	}
}

// lose accounts for entries that could not be stored.
func (r *Recorder) lose(entries []capture.HistoryEntry) {
	r.writeFailed += uint64(len(entries))
	r.mu.Lock()
	r.taintLocked(entries)
	r.mu.Unlock()
}

// retaint puts back keys a transaction could not mark yet.
func (r *Recorder) retaint(keys []string) {
	if len(keys) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range keys {
		if len(r.tainted) >= maxTaintedEncounters {
			r.taintedAll = true
			return
		}
		r.tainted[key] = struct{}{}
	}
}

// takeTaint returns the tainted keys collected so far and whether every encounter is tainted. A caller that cannot mark a key
// yet puts it back with retaint.
func (r *Recorder) takeTaint() taint {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := taint{all: r.taintedAll}
	for key := range r.tainted {
		t.keys = append(t.keys, key)
	}
	r.tainted = map[string]struct{}{}
	return t
}

// endSpanTaint forgets the taint of a span that closed: its encounters were marked, and the next span starts clean.
func (r *Recorder) endSpanTaint() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tainted, r.taintedAll = map[string]struct{}{}, false
}

type taint struct {
	keys []string
	all  bool
}

// takeDropCounts returns the journal and queue losses counted since the last span closed.
func (r *Recorder) takeDropCounts() (journalDropped, queueDropped uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	journalDropped, queueDropped = r.journalDropped, r.queueDropped
	r.journalDropped, r.queueDropped = 0, 0
	return journalDropped, queueDropped
}

func (r *Recorder) inTransaction(ctx context.Context, apply func(*sql.Tx) error) error {
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := apply(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Finish ends the session: it stores everything still queued, waiting at most until ctx ends, then records how the open
// span ended. Encounters that never finished are abandoned, never shown as finished fights. Finish must be called once,
// after the session stopped submitting; later calls return the first result.
func (r *Recorder) Finish(ctx context.Context, stopReason string) error {
	r.finishOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.queue)
		r.mu.Unlock()
		select {
		case <-r.done:
			r.finishErr = r.close(stopReason)
			r.store.sessions.Done()
		case <-ctx.Done():
			r.finishErr = ErrFinishTimeout
			// Storing continues in the background; the session is closed once the queue is written.
			go func() {
				<-r.done
				_ = r.close(stopReason)
				r.store.sessions.Done()
			}()
		}
	})
	return r.finishErr
}

// close writes the end of the span still open, if any.
func (r *Recorder) close(stopReason string) error {
	if r.span == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	w := sessionWriter{recorder: r, staged: map[string]int64{}, span: r.span, sourceUsed: r.sourceUsed}
	err := r.inTransaction(ctx, func(tx *sql.Tx) error {
		w.tx = tx
		return w.closeSpan(ctx, stopReason, nil)
	})
	if err == nil {
		r.span = 0
	}
	return err
}

// Abort removes the history of a session that never ran, for a capture that failed to open after BeginSession.
func (r *Recorder) Abort() {
	r.finishOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.queue)
		r.mu.Unlock()
		<-r.done
		if id := r.firstSpan.Load(); id != 0 {
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			defer cancel()
			_, r.finishErr = r.store.db.ExecContext(ctx, "DELETE FROM capture_session WHERE id = ?", id)
		}
		r.store.sessions.Done()
	})
}

// parseEncounterKey splits the engine's "<statistics interval>-<sequence>" key.
func parseEncounterKey(key string) (interval, sequence uint64, ok bool) {
	left, right, found := strings.Cut(key, "-")
	if !found {
		return 0, 0, false
	}
	interval, errInterval := strconv.ParseUint(left, 10, 64)
	sequence, errSequence := strconv.ParseUint(right, 10, 64)
	return interval, sequence, errInterval == nil && errSequence == nil
}
