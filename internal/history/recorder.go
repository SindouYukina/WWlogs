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

// Capture session sources, as the capture names them.
const (
	SourceLive   = capture.SourceLive
	SourceReplay = capture.SourceReplay
)

var (
	// ErrNotRecording reports a Store that cannot record (see Status).
	ErrNotRecording = errors.New("the combat history is not recording")
	// ErrDuplicateSource reports a replayed file whose capture is already in the history.
	ErrDuplicateSource = errors.New("this capture file is already in the combat history")
	// ErrFinishTimeout reports a live session whose queued history was still being stored when the deadline passed. Nothing
	// is marked complete: the session stays open and the next open of the file marks it abandoned if it never finishes.
	ErrFinishTimeout = errors.New("the combat history was still being stored at the deadline")
	// ErrReplayNotStored reports a replay whose history was removed instead of being kept: the replay was stopped before its
	// end, or part of its journal could not be stored. A replay is kept only when all of it was stored.
	ErrReplayNotStored = errors.New("the replay was not stored in the combat history")
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

// Recorder bounds. A live capture never waits: what does not fit is counted and its encounters marked incomplete. A replay
// waits while maxPacedEntries are queued, which keeps a full transaction gathering while the previous one is written and
// bounds what remains to store when the file ends.
const (
	maxQueuedEntries            = 1 << 17
	maxQueuedBatches            = 1 << 12
	maxPacedEntries             = 4 * maxTransactionEntries
	maxTransactionEntries       = 1 << 13
	writeLinger                 = 200 * time.Millisecond
	maxTaintedEncounters        = 1024
	maxRetainedEntries          = 1024
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

// Recorder stores one capture session's journal. It is the session's capture.HistorySink: Submit (live) and SubmitWait
// (replay) only queue, and one goroutine of its own writes the queue in transactions.
//
// One capture can hold several recorded spans (capture.HistoryRecordingStarted ... Stopped); each span is one capture_session
// row. A Recorder that begins unpaused creates the row of its first span at once, as before spans existed; every later span
// gets its row when its start entry is stored.
//
// Losses are never silent and always land in the span they belong to (losses.go): what could not be stored is counted there
// and marks the encounters it belonged to incomplete.
//
// A replay is kept only when all of it was stored: a replay stopped before its end, or one that lost any entry, is removed
// when it finishes (Finish), and one an earlier run left unfinished is removed when the file opens (Store.recover). A live
// capture keeps what it stored, its losses marked.
type Recorder struct {
	store  *Store
	start  SessionStart
	level  capture.HistoryLevel
	queue  chan submission
	queued atomic.Int64
	done   chan struct{}
	// room wakes a replay waiting in SubmitWait: the writer signals it whenever it takes entries off the queue.
	room chan struct{}
	// firstSpan is the row id of the first span, once it exists.
	firstSpan atomic.Int64
	// lost and halted are Health's view of the losses and of a writer that stopped trying.
	lost   atomic.Uint64
	halted atomic.Bool
	// discard tells the writer to drop what is still queued without storing it: the replay is being removed.
	discard atomic.Bool

	mu     sync.Mutex
	closed bool
	// waiting holds, in journal order, the span markers and loss notes of drains the queue could not take; they go first
	// into the next submission it takes.
	waiting held

	// The fields below belong to the writer goroutine; Finish reads them after it has ended.
	// span is the row of the span being recorded; 0 between spans. lastSpan is the span that closed last, which losses
	// between spans are counted on. sourceUsed is set once a span carried the replay digest.
	span       int64
	lastSpan   int64
	sourceUsed bool
	// spanLost is set once the span lost an entry that belonged to no single encounter: every encounter of the span is
	// then incomplete. lostKeys are encounters that lost an entry before their row existed.
	spanLost bool
	lostKeys map[string]struct{}
	failures int
	stopped  bool
	// carry holds, in journal order, the span markers and loss notes of transactions that failed, for the next one.
	carry held
	// spans lists the rows of every span this recorder created.
	spans []int64
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
		store: s, start: start, level: start.Level, queue: make(chan submission, maxQueuedBatches), done: make(chan struct{}),
		room: make(chan struct{}, 1), lostKeys: map[string]struct{}{}, encounters: map[string]int64{},
	}
	if !start.Paused {
		id, used, err := recorder.insertSpan(ctx, s.db, capture.HistoryStartCapture, false)
		if err != nil {
			return nil, err
		}
		recorder.span, recorder.sourceUsed = id, used
		recorder.spans = append(recorder.spans, id)
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

// Submit queues one drain of a live capture's journal. It never waits: when the queue has no room, or after Finish, the
// drain's entries become a loss note in their place (losses.go) and its span markers wait in order for the next submission.
func (r *Recorder) Submit(entries []capture.HistoryEntry, dropped uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.journalLossLocked(r.waiting, dropped)
	if len(entries) == 0 && len(pending) == len(r.waiting) {
		return
	}
	if r.offerLocked(submission{held: pending, entries: entries}, maxQueuedEntries) {
		r.waiting = nil
		return
	}
	waiting, lost := pending.appendLost(lostInQueue, entries)
	r.lost.Add(lost)
	if waiting.markers() > maxRetainedEntries {
		// Far more span changes than a capture makes while its storage is stalled: the spans cannot be told apart any more,
		// so nothing more of this capture is stored.
		r.halted.Store(true)
	}
	r.waiting = waiting
}

// SubmitWait queues one drain of a replay's journal, waiting while the queue holds maxPacedEntries (backpressure), so a
// replay never loses an entry for lack of room: the replay reads its file only as fast as the writer stores it. It returns
// ctx's error when ctx ends before the entries were accepted; they then become a loss note. A writer that stopped after
// repeated failures still takes entries at once and counts them as not stored.
func (r *Recorder) SubmitWait(ctx context.Context, entries []capture.HistoryEntry, dropped uint64) error {
	r.mu.Lock()
	r.waiting = r.journalLossLocked(r.waiting, dropped)
	r.mu.Unlock()
	if len(entries) == 0 {
		return nil
	}
	for {
		r.mu.Lock()
		if r.closed {
			var lost uint64
			r.waiting, lost = r.waiting.appendLost(lostInQueue, entries)
			r.lost.Add(lost)
			r.mu.Unlock()
			return nil
		}
		sub := submission{held: r.waiting, entries: entries}
		// A drain larger than the bound fits an empty queue, so a replay never waits for room that cannot come.
		if r.offerLocked(sub, max(maxPacedEntries, int64(sub.size()))) {
			r.waiting = nil
			r.mu.Unlock()
			return nil
		}
		r.mu.Unlock()
		select {
		case <-r.room:
		case <-ctx.Done():
			r.mu.Lock()
			var lost uint64
			r.waiting, lost = r.waiting.appendLost(lostInQueue, entries)
			r.lost.Add(lost)
			r.mu.Unlock()
			return ctx.Err()
		}
	}
}

// offerLocked queues sub when the queue then holds at most bound entries and has a free place; it never waits.
func (r *Recorder) offerLocked(sub submission, bound int64) bool {
	if r.closed {
		return false
	}
	size := int64(sub.size())
	if r.queued.Load()+size > bound {
		return false
	}
	select {
	case r.queue <- sub:
		r.queued.Add(size)
		return true
	default:
		return false
	}
}

// journalLossLocked adds a note for entries the engine journal refused at its own bound. The journal cannot say which
// encounters lost an entry, so none of the span's encounters can be complete.
func (r *Recorder) journalLossLocked(h held, dropped uint64) held {
	if dropped == 0 {
		return h
	}
	r.lost.Add(dropped)
	note := &lossNote{unattributed: true}
	note.count(lostInJournal, dropped)
	return h.appendNote(note)
}

// signalRoom wakes a replay waiting in SubmitWait. One pending signal is enough: the single producer checks again.
func (r *Recorder) signalRoom() {
	select {
	case r.room <- struct{}{}:
	default:
	}
}

// RecorderHealth is how a recorder's storage is doing. Stopped is set once repeated transaction failures stopped the writer;
// Lost counts the journal entries of this capture that never reached storage.
type RecorderHealth struct {
	Stopped bool   `json:"stopped"`
	Lost    uint64 `json:"lost"`
}

// Health reports the recorder's storage state; it is safe to call from any goroutine.
func (r *Recorder) Health() RecorderHealth {
	if r == nil {
		return RecorderHealth{}
	}
	return RecorderHealth{Stopped: r.halted.Load(), Lost: r.lost.Load()}
}

// run is the writer goroutine. It gathers what arrives within writeLinger into one transaction, and signals room to a
// waiting replay whenever it takes entries off the queue.
func (r *Recorder) run() {
	defer close(r.done)
	for {
		first, ok := <-r.queue
		if !ok {
			return
		}
		r.signalRoom()
		subs, size := []submission{first}, first.size()
		linger := time.NewTimer(writeLinger)
	gather:
		for size < maxTransactionEntries {
			select {
			case next, open := <-r.queue:
				if !open {
					break gather
				}
				r.signalRoom()
				subs, size = append(subs, next), size+next.size()
			case <-linger.C:
				break gather
			}
		}
		linger.Stop()
		if !r.discard.Load() {
			r.write(subs)
		}
		r.queued.Add(-int64(size))
		r.signalRoom()
	}
}

// write stores subs in one transaction, after what earlier failed transactions carried. A failed transaction stores
// nothing: its entries become loss notes held in journal order with its span markers for the next transaction, and after
// maxConsecutiveWriteFailures the writer stops trying and only counts.
func (r *Recorder) write(subs []submission) {
	if len(r.carry) > 0 {
		subs = append([]submission{{held: r.carry}}, subs...)
		r.carry = nil
	}
	if r.stopped || r.halted.Load() {
		r.stopped = true
		r.loseAll(subs)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	w := r.writer()
	err := r.inTransaction(ctx, func(tx *sql.Tx) error {
		w.tx = tx
		for _, sub := range subs {
			if err := w.applySubmission(ctx, sub); err != nil {
				return err
			}
		}
		return w.settleLosses(ctx)
	})
	if err != nil {
		r.failures++
		if r.failures >= maxConsecutiveWriteFailures {
			r.stopped = true
			r.halted.Store(true)
		}
		r.loseAll(subs)
		return
	}
	r.failures = 0
	r.adopt(w)
}

// loseAll turns everything of a transaction that did not store into held items for the next one.
func (r *Recorder) loseAll(subs []submission) {
	carry, lost := lostSubmissions(lostInTransaction, subs)
	r.lost.Add(lost)
	r.carry = carry
}

// writer starts a transaction's view of the writer state; adopt keeps it once the transaction committed.
func (r *Recorder) writer() *sessionWriter {
	lostKeys := make(map[string]struct{}, len(r.lostKeys))
	for key := range r.lostKeys {
		lostKeys[key] = struct{}{}
	}
	return &sessionWriter{recorder: r, staged: map[string]int64{}, span: r.span, lastSpan: r.lastSpan, sourceUsed: r.sourceUsed,
		spanLost: r.spanLost, lostKeys: lostKeys}
}

func (r *Recorder) adopt(w *sessionWriter) {
	r.span, r.lastSpan, r.sourceUsed, r.spanLost, r.lostKeys = w.span, w.lastSpan, w.sourceUsed, w.spanLost, w.lostKeys
	r.spans = append(r.spans, w.newSpans...)
	for _, id := range w.newSpans {
		r.firstSpan.CompareAndSwap(0, id)
	}
	for key, id := range w.staged {
		if id == 0 {
			delete(r.encounters, key)
			continue
		}
		r.encounters[key] = id
	}
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

// Finish ends the session once it stopped submitting; later calls return the first result.
//
// A live session stores everything still queued, waiting at most until ctx ends, then records how its open span ended:
// encounters that never finished are abandoned, never shown as finished fights. A replay is kept only when it reached its
// end and every entry of it was stored: Finish waits until the writer committed all of it (ctx can end the wait), and
// otherwise removes every span of the replay and returns ErrReplayNotStored. A nil result for a replay therefore means the
// whole replay is in the file.
func (r *Recorder) Finish(ctx context.Context, stopReason string) error {
	r.finishOnce.Do(func() {
		r.closeQueue()
		if r.start.Source == SourceReplay {
			r.finishErr = r.finishReplay(ctx, stopReason)
			r.store.sessions.Done()
			return
		}
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

// close stores what failed transactions still carry, then writes the end of the span still open, if any.
func (r *Recorder) close(stopReason string) error {
	if r.span == 0 && len(r.carry) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	w := r.writer()
	err := r.inTransaction(ctx, func(tx *sql.Tx) error {
		w.tx = tx
		if err := w.applySubmission(ctx, submission{held: r.carry}); err != nil {
			return err
		}
		if w.span == 0 {
			return w.settleLosses(ctx)
		}
		return w.closeSpan(ctx, stopReason, nil)
	})
	if err != nil {
		return err
	}
	r.carry = nil
	r.adopt(w)
	return nil
}

// closeQueue stops taking entries. What waited for room (span markers and loss notes) goes to the writer last; the writer
// keeps draining, so the send waits for one place at most.
func (r *Recorder) closeQueue() {
	r.mu.Lock()
	r.closed = true
	waiting := r.waiting
	r.waiting = nil
	r.mu.Unlock()
	if len(waiting) > 0 {
		sub := submission{held: waiting}
		r.queued.Add(int64(sub.size()))
		r.queue <- sub
	}
	close(r.queue)
}

// finishReplay keeps a replay only when it reached its end and every entry of it was stored; otherwise it drops what is still
// queued and removes the replay.
func (r *Recorder) finishReplay(ctx context.Context, stopReason string) error {
	var reason error
	if stopReason != capture.StopReasonReplayEnd {
		reason = fmt.Errorf("the replay stopped before its end (%s)", stopReason)
		r.discard.Store(true)
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		r.discard.Store(true)
		<-r.done
		if reason == nil {
			reason = fmt.Errorf("storing it did not finish: %w", ctx.Err())
		}
	}
	if reason == nil {
		if lost := r.lost.Load(); lost > 0 {
			reason = fmt.Errorf("%d journal entries of it could not be stored", lost)
		} else if err := r.close(stopReason); err != nil {
			reason = err
		} else {
			return nil
		}
	}
	return errors.Join(fmt.Errorf("%w: %w", ErrReplayNotStored, reason), r.removeSpans())
}

// removeSpans deletes every span this recorder created, with everything stored in them. A span that cannot be removed now
// stays open, so the next open of the file removes it (Store.recover).
func (r *Recorder) removeSpans() error {
	if len(r.spans) == 0 {
		return nil
	}
	defer r.store.models.clear()
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	err := r.inTransaction(ctx, func(tx *sql.Tx) error {
		for _, id := range r.spans {
			if _, err := tx.ExecContext(ctx, "DELETE FROM capture_session WHERE id = ?", id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remove the unfinished replay: %w", err)
	}
	r.spans, r.span = nil, 0
	return nil
}

// Abort removes the history of a session that never ran, for a capture that failed to open after BeginSession.
func (r *Recorder) Abort() {
	r.finishOnce.Do(func() {
		r.discard.Store(true)
		r.closeQueue()
		<-r.done
		r.finishErr = r.removeSpans()
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
