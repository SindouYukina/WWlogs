package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Deleting history.
//
// The user decides how long history is kept: nothing is ever deleted automatically. A deletion runs in one transaction, so
// the history is either as before or without what was deleted; foreign keys remove an encounter's results with it. Deleted
// rows leave free pages that SQLite reuses; a deletion that frees much of the file, and every clear, compacts it with VACUUM
// while nothing records. Compaction rewrites the encrypted file but is not a secure erase: the file system may keep earlier
// sectors (themselves encrypted) until it reuses them.

var (
	// ErrEncounterRecording refuses deleting an encounter that is still being recorded.
	ErrEncounterRecording = errors.New("the encounter is still being recorded")
	// ErrRecordingActive refuses clearing the history while a capture records into it.
	ErrRecordingActive = errors.New("a capture is recording into the combat history")
	// ErrNotWritable refuses a deletion in a history this store cannot write.
	ErrNotWritable = errors.New("the combat history cannot be changed")
)

// DeleteResult says what a deletion removed.
type DeleteResult struct {
	Encounters int `json:"encounters"`
	// Spans counts recorded spans removed because no encounter of theirs remained.
	Spans int `json:"spans"`
	// Compacted is true when the file was compacted afterwards.
	Compacted bool `json:"compacted"`
}

// endingKinds lists endingChanges for SQL.
const endingKinds = "'remove', 'holder_removed', 'superseded', 'untracked', 'instant'"

// compactionShare is the share of free pages from which a deletion compacts the file.
const (
	compactionShare    = 4
	compactionMinPages = 256
)

func (s *Store) writable() error {
	status := s.Status()
	if s.db == nil || status.State != StateReady {
		return ErrNotWritable
	}
	return nil
}

// DeleteEncounter removes one listed encounter that no capture is recording any more: its results, its combat events, and
// the effect changes stored during it in its protocol session. An effect instance that also has changes outside the
// encounter keeps them and no longer counts its start as recorded. A span left without any encounter is removed with
// everything it still holds, unless it is still recording.
func (s *Store) DeleteEncounter(ctx context.Context, id int64) (DeleteResult, error) {
	var result DeleteResult
	// A deletion changes what an analysis, the library and the career read; their caches are dropped whatever the outcome.
	defer s.models.clear()
	defer s.library.clear()
	if err := s.writable(); err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("delete history encounter: %w", err)
	}
	defer tx.Rollback()
	var span, protocolSession int64
	var state string
	var started int64
	var end sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT capture_session_id, protocol_session, state, started_at_ns, coalesce(ended_at_ns, checkpoint_at_ns, last_activity_at_ns)
		FROM encounter WHERE id = ? AND state IN `+listedStates, id).Scan(&span, &protocolSession, &state, &started, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, fmt.Errorf("delete history encounter: %w", err)
	}
	if state == "open" {
		return result, ErrEncounterRecording
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM combat_event WHERE encounter_id = ?", id); err != nil {
		return result, fmt.Errorf("delete history encounter: %w", err)
	}
	if end.Valid {
		window := `capture_session_id = ? AND observed_at_ns BETWEEN ? AND ? AND instance IN (SELECT instance FROM effect_instance WHERE capture_session_id = ? AND protocol_session = ?)`
		if _, err := tx.ExecContext(ctx, `UPDATE effect_instance SET start_recorded = 0 WHERE capture_session_id = ? AND instance IN (SELECT instance FROM effect_event WHERE `+window+`)`,
			span, span, started, end.Int64, span, protocolSession); err != nil {
			return result, fmt.Errorf("delete history effects: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS deleted_effect_instance (instance INTEGER PRIMARY KEY); DELETE FROM deleted_effect_instance`); err != nil {
			return result, fmt.Errorf("delete history effects: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO deleted_effect_instance (instance) SELECT instance FROM effect_event WHERE `+window,
			span, started, end.Int64, span, protocolSession); err != nil {
			return result, fmt.Errorf("delete history effects: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM effect_event WHERE "+window, span, started, end.Int64, span, protocolSession); err != nil {
			return result, fmt.Errorf("delete history effects: %w", err)
		}
		// The life of an instance that lost changes follows the changes it keeps.
		if _, err := tx.ExecContext(ctx, `UPDATE effect_instance SET
			first_change_ns = (SELECT min(e.observed_at_ns) FROM effect_event e WHERE e.capture_session_id = effect_instance.capture_session_id AND e.instance = effect_instance.instance),
			ended_at_ns = (SELECT CASE WHEN e.change_kind IN (`+endingKinds+`) THEN e.observed_at_ns END FROM effect_event e
				WHERE e.capture_session_id = effect_instance.capture_session_id AND e.instance = effect_instance.instance ORDER BY e.sequence DESC LIMIT 1)
			WHERE capture_session_id = ? AND instance IN (SELECT instance FROM deleted_effect_instance)`, span); err != nil {
			return result, fmt.Errorf("delete history effects: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM deleted_effect_instance"); err != nil {
			return result, fmt.Errorf("delete history effects: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM effect_instance WHERE capture_session_id = ? AND NOT EXISTS
			(SELECT 1 FROM effect_event e WHERE e.capture_session_id = effect_instance.capture_session_id AND e.instance = effect_instance.instance)`, span); err != nil {
			return result, fmt.Errorf("delete history effects: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM encounter WHERE id = ?", id); err != nil {
		return result, fmt.Errorf("delete history encounter: %w", err)
	}
	result.Encounters = 1
	removed, err := tx.ExecContext(ctx, "DELETE FROM capture_session WHERE id = ? AND status != 'open' AND NOT EXISTS (SELECT 1 FROM encounter WHERE capture_session_id = ?)", span, span)
	if err != nil {
		return result, fmt.Errorf("delete history span: %w", err)
	}
	if count, err := removed.RowsAffected(); err == nil {
		result.Spans = int(count)
	}
	if err := foreignKeysHold(ctx, tx); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("delete history encounter: %w", err)
	}
	result.Compacted = s.compactIfWorthIt(ctx, false)
	return result, nil
}

// ClearHistory removes every recorded span and encounter, then compacts the file. It refuses while a capture records into the
// history, so a recording is never cut into pieces. The key stays: the emptied file is still encrypted with it.
func (s *Store) ClearHistory(ctx context.Context) (DeleteResult, error) {
	var result DeleteResult
	defer s.models.clear()
	defer s.library.clear()
	if err := s.writable(); err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("clear history: %w", err)
	}
	defer tx.Rollback()
	var open int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM capture_session WHERE status = 'open'").Scan(&open); err != nil {
		return result, fmt.Errorf("clear history: %w", err)
	}
	if open > 0 {
		return result, ErrRecordingActive
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM encounter WHERE state IN "+listedStates).Scan(&result.Encounters); err != nil {
		return result, fmt.Errorf("clear history: %w", err)
	}
	removed, err := tx.ExecContext(ctx, "DELETE FROM capture_session")
	if err != nil {
		return result, fmt.Errorf("clear history: %w", err)
	}
	if count, err := removed.RowsAffected(); err == nil {
		result.Spans = int(count)
	}
	if err := foreignKeysHold(ctx, tx); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("clear history: %w", err)
	}
	result.Compacted = s.compactIfWorthIt(ctx, true)
	return result, nil
}

func foreignKeysHold(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	violation := rows.Next()
	rows.Close()
	if violation {
		return errors.New("the deletion would leave foreign key violations")
	}
	return nil
}

// compactIfWorthIt rebuilds the file when always is set or free pages make up a large share of it, and only while no span
// records, then truncates the write-ahead log. A failure only leaves the free pages for SQLite to reuse.
func (s *Store) compactIfWorthIt(ctx context.Context, always bool) bool {
	var open, pages, free int64
	if s.db.QueryRowContext(ctx, "SELECT count(*) FROM capture_session WHERE status = 'open'").Scan(&open) != nil || open > 0 {
		return false
	}
	if s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages) != nil || s.db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free) != nil {
		return false
	}
	if !always && (free < compactionMinPages || free*compactionShare < pages) {
		return false
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		return false
	}
	_, _ = s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return true
}
