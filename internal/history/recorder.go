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

// Recorder stores one capture session's journal. It is the session's capture.HistorySink: Submit only queues, and one
// goroutine of its own writes the queue in transactions.
type Recorder struct {
	store     *Store
	sessionID int64
	level     capture.HistoryLevel
	queue     chan []capture.HistoryEntry
	queued    atomic.Int64
	done      chan struct{}

	mu             sync.Mutex
	closed         bool
	journalDropped uint64
	queueDropped   uint64
	// tainted holds encounter keys whose entries never reached storage; taintedAll is set when there were too many to keep.
	tainted    map[string]struct{}
	taintedAll bool

	// The fields below belong to the writer goroutine.
	writeFailed uint64
	failures    int
	stopped     bool
	encounters  map[string]int64
	finishOnce  sync.Once
	finishErr   error
}

// BeginSession records a new capture session and starts its writer.
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
	var digest any
	if start.Source == SourceReplay && start.SourceSHA256 != "" {
		digest = start.SourceSHA256
		var existing int64
		err := s.db.QueryRowContext(ctx, "SELECT id FROM capture_session WHERE source = ? AND source_sha256 = ?", start.Source, start.SourceSHA256).Scan(&existing)
		if err == nil {
			return nil, ErrDuplicateSource
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("check history source: %w", err)
		}
	}
	uid, err := newSessionUID()
	if err != nil {
		return nil, err
	}
	opened := s.now()
	zone, offset := opened.In(s.location).Zone()
	result, err := s.db.ExecContext(ctx, `INSERT INTO capture_session (session_uid, source, source_sha256, status, opened_at_ns, utc_offset_s, zone_name,
		history_level, app_version, app_build, app_commit, app_dirty, app_profile, protocol_schema, master_sha256)
		VALUES (?, ?, ?, 'open', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, start.Source, digest, opened.UnixNano(), offset, zone, levelName(start.Level),
		s.build.Version, int64(s.build.BuildNumber), s.build.Commit, s.build.Dirty, string(s.build.Profile), s.protocol, s.master)
	if err != nil {
		return nil, fmt.Errorf("record capture session: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("record capture session: %w", err)
	}
	recorder := &Recorder{
		store: s, sessionID: id, level: start.Level, queue: make(chan []capture.HistoryEntry, maxQueuedBatches), done: make(chan struct{}),
		tainted: map[string]struct{}{}, encounters: map[string]int64{},
	}
	s.sessions.Add(1)
	go recorder.run()
	return recorder, nil
}

func newSessionUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create history session identity: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// SessionID is the stored session's row id.
func (r *Recorder) SessionID() int64 { return r.sessionID }

// Level is the history level this session records at.
func (r *Recorder) Level() capture.HistoryLevel { return r.level }

// Submit queues one drain of the journal. It never waits: a full queue, or a queue after Finish, drops the batch, counts
// it and marks the encounters it named incomplete.
func (r *Recorder) Submit(entries []capture.HistoryEntry, dropped uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if dropped > 0 {
		r.journalDropped += dropped
		// The journal cannot say which encounters lost an entry, so none of this session's encounters can be complete.
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
		entries := batch
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
	staged := map[string]int64{}
	pending := r.takeTaint()
	var unresolved []string
	err := r.inTransaction(ctx, func(tx *sql.Tx) error {
		w := sessionWriter{tx: tx, recorder: r, staged: staged}
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
	r.retaint(unresolved)
	for key, id := range staged {
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

type taint struct {
	keys []string
	all  bool
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

// Finish ends the session: it stores everything still queued, waiting at most until ctx ends, then records how the session
// ended. Encounters that never finished are abandoned, never shown as finished fights. Finish must be called once, after
// the session stopped submitting; later calls return the first result.
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

// close writes the session's end.
func (r *Recorder) close(stopReason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	r.mu.Lock()
	journalDropped, queueDropped := r.journalDropped, r.queueDropped
	r.mu.Unlock()
	return r.inTransaction(ctx, func(tx *sql.Tx) error {
		w := sessionWriter{tx: tx, recorder: r, staged: map[string]int64{}}
		// Keys that still have no row named encounters that never reached storage at all; nothing is left to mark.
		if _, err := w.applyTaint(ctx, r.takeTaint()); err != nil {
			return err
		}
		// A pending encounter with all of its entries stored never had confirmed activity and would have been discarded. One that
		// lost entries may have been a real fight whose activity was lost, so it stays, as unfinished, never silently removed.
		if _, err := tx.ExecContext(ctx, "DELETE FROM encounter WHERE capture_session_id = ? AND state = 'pending' AND complete = 1", r.sessionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE encounter SET state = 'abandoned', complete = 0 WHERE capture_session_id = ? AND state IN ('open', 'pending')", r.sessionID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE capture_session SET status = 'closed', closed_at_ns = ?, stop_reason = ?, journal_dropped = ?, queue_dropped = ?, write_failed = ?
			WHERE id = ?`, r.store.now().UnixNano(), stopReason, int64(journalDropped), int64(queueDropped), int64(r.writeFailed), r.sessionID)
		return err
	})
}

// Abort removes a session that never ran, for a capture that failed to open after BeginSession.
func (r *Recorder) Abort() {
	r.finishOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.queue)
		r.mu.Unlock()
		<-r.done
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		_, r.finishErr = r.store.db.ExecContext(ctx, "DELETE FROM capture_session WHERE id = ?", r.sessionID)
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
