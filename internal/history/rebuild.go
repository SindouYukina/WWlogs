package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Opening again, and rebuilding a history the user gave up on.
//
// A history that cannot be opened is never deleted, overwritten or replaced on its own (store.go). The UI then offers to try
// again (Retry), and, only when a new history would solve it (Status.Rebuildable), to delete the history and create a new,
// empty one in its place (Rebuild), which the user confirms twice. A rebuild touches only the files of that history, the
// database, its logs, its key file, its key record and what a migration left, never another history, the settings or the
// capture spool:
//
//  1. combat-history.rebuild records the user's decision and the stage the rebuild reached.
//  2. Every file of the old history is renamed to <name>.discarded: nothing is deleted yet.
//  3. A new history is created with a derived key, then opened once and checked.
//  4. Only then are the .discarded files deleted, and the rebuild record with them.
//
// When the new history cannot be created, the discarded files are renamed back and the old history is as it was. A rebuild
// that was interrupted (power loss, a killed process) is continued by the next open (resumeRebuild), because the user decided
// it; the old files are deleted only once a new history has opened. A damaged rebuild record says no decision for sure, so
// the old files are put back instead.

const (
	rebuildSuffix   = ".rebuild"
	discardedSuffix = ".discarded"
)

// Rebuild stages in combat-history.rebuild.
const (
	rebuildDiscard = "discard"
	rebuildCreate  = "create"
	rebuildDone    = "done"
)

var (
	// ErrNotRebuildable refuses a rebuild of a history that opens, that another WWlogs uses, or that a new history would not
	// replace (Status.Rebuildable).
	ErrNotRebuildable = errors.New("the combat history is not one to rebuild")
	// errRebuildRecord reports a rebuild record that cannot be read.
	errRebuildRecord = errors.New("the rebuild record is damaged")
)

// rebuildStep runs after each named step of a rebuild; tests make it fail or end the process there.
var rebuildStep = func(step string) error { return nil }

func (s *Store) rebuildPath() string { return historyFile(s.path, rebuildSuffix) }

// historyFiles are every file of the history at path a rebuild deletes: the database and its logs, the key file and the key
// record with their temporary files, and what a migration left. Neither the writer lock nor the rebuild record is one.
func historyFiles(path string) []string {
	files := append([]string{}, databaseFiles(path)...)
	files = append(files, keyPath(path), keyPath(path)+".tmp", keyRecordPath(path), keyRecordPath(path)+".tmp")
	files = append(files, databaseFiles(historyFile(path, migratingSuffix))...)
	files = append(files, databaseFiles(historyFile(path, legacySuffix))...)
	return append(files, historyFile(path, migrationSuffix), historyFile(path, migrationSuffix)+".tmp")
}

// rebuildableReason says whether a store unavailable for reason holds a history only a new one would replace: a key that does
// not open it, a damaged file or log, or a format this build cannot read. A history another WWlogs uses, one that cannot be
// read for a while (storage, the motherboard identity, the protocol key) or one left between two files by a migration is not.
func rebuildableReason(reason string) bool {
	switch reason {
	case ReasonKeyMismatch, ReasonKeyMissing, ReasonKeyUnavailable, ReasonKeyVerification, ReasonFormatUnsupported, ReasonNewerSchema,
		ReasonNotHistory, ReasonCorrupt, ReasonWALIntegrity:
		return true
	}
	return false
}

// canCreate reports whether a new history could be created on this computer with this build.
func (s *Store) canCreate() bool {
	key, _, err := newHistoryKey(s.inputs)
	clear(key.key)
	return err == nil
}

func (s *Store) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Retry opens the history again when it could not be opened: what kept it closed may have passed, such as a file another
// program held or a firmware table that could not be read. It changes nothing for a store that is open or read-only.
func (s *Store) Retry() (Status, error) {
	if s == nil {
		return s.Status(), ErrUnavailable
	}
	s.reopening.Lock()
	defer s.reopening.Unlock()
	if s.isClosed() || s.Status().State != StateUnavailable {
		return s.Status(), nil
	}
	next, err := Open(s.options)
	s.adopt(next)
	return s.Status(), err
}

// Rebuild deletes the history the store cannot open and creates a new, empty one in its place (see above). Only a store whose
// status is Rebuildable is rebuilt; the UI calls it after the user confirmed twice. The store is then open on the new history,
// or, when the rebuild could not be done, unavailable on the old one as before.
func (s *Store) Rebuild() (Status, error) {
	if s == nil {
		return s.Status(), ErrNotRebuildable
	}
	s.reopening.Lock()
	defer s.reopening.Unlock()
	if status := s.Status(); s.isClosed() || status.State != StateUnavailable || !status.Rebuildable {
		return status, ErrNotRebuildable
	}
	lock, err := acquireWriterLock(s.path + ".lock")
	if err != nil {
		return s.Status(), fmt.Errorf("rebuild history: %w", err)
	}
	// The decision is recorded under the lock; the open after it carries the rebuild out (resumeRebuild).
	if !s.canCreate() {
		lock.release()
		return s.Status(), errors.New("rebuild history: no new history can be created on this computer")
	}
	err = writeRebuildRecord(s.rebuildPath(), rebuildDiscard)
	lock.release()
	if err != nil {
		return s.Status(), fmt.Errorf("rebuild history: %w", err)
	}
	next, openErr := Open(s.options)
	rebuildErr := next.rebuildErr
	s.adopt(next)
	if rebuildErr != nil {
		return s.Status(), fmt.Errorf("rebuild history: %w", rebuildErr)
	}
	return s.Status(), openErr
}

// adopt takes the files, the key and the status of next, a store just opened on the same history, under the store's lock:
// queries read the handle and the state together (readable), so they see the store unavailable or open, never in between.
func (s *Store) adopt(next *Store) {
	s.mu.Lock()
	s.db, s.lock, s.key, s.protection, s.status = next.db, next.lock, next.key, next.protection, next.status
	next.db, next.lock, next.key = nil, nil, nil
	s.mu.Unlock()
	s.models.clear()
	s.library.clear()
}

type rebuildRecord struct {
	Stage string `json:"stage"`
}

func readRebuildRecord(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var record rebuildRecord
	if json.Unmarshal(data, &record) != nil || (record.Stage != rebuildDiscard && record.Stage != rebuildCreate && record.Stage != rebuildDone) {
		return "", true, errRebuildRecord
	}
	return record.Stage, true, nil
}

func writeRebuildRecord(path, stage string) error {
	data, err := json.Marshal(rebuildRecord{Stage: stage})
	if err != nil {
		return err
	}
	return writeFileDurably(path, append(data, '\n'), true)
}

// resumeRebuild carries out a rebuild the user decided, from the stage it reached, before anything else of the history is
// opened. It returns the failure of a rebuild that could not be done; the old files are then back in place.
func (s *Store) resumeRebuild(ctx context.Context) error {
	stage, present, err := readRebuildRecord(s.rebuildPath())
	switch {
	case !present && err == nil:
		return nil
	case errors.Is(err, errRebuildRecord):
		return errors.Join(err, s.restoreDiscarded())
	case err != nil:
		return err
	}
	switch stage {
	case rebuildDiscard:
		if err := s.discardHistory(); err != nil {
			return errors.Join(err, s.restoreDiscarded())
		}
		if err := writeRebuildRecord(s.rebuildPath(), rebuildCreate); err != nil {
			return errors.Join(err, s.restoreDiscarded())
		}
		fallthrough
	case rebuildCreate:
		// Files under the history's names now are the start of a new history an interrupted rebuild left.
		if err := removeEach(historyFiles(s.path)); err != nil {
			return errors.Join(err, s.restoreDiscarded())
		}
		if err := s.createEmptyHistory(ctx); err != nil {
			return errors.Join(err, removeEach(historyFiles(s.path)), s.restoreDiscarded())
		}
		if err := writeRebuildRecord(s.rebuildPath(), rebuildDone); err != nil {
			return err
		}
		fallthrough
	case rebuildDone:
		if err := rebuildStep("created"); err != nil {
			return err
		}
		var failed []error
		for _, file := range historyFiles(s.path) {
			failed = append(failed, removeIfPresent(file+discardedSuffix))
		}
		if err := errors.Join(failed...); err != nil {
			return err
		}
		return removeIfPresent(s.rebuildPath())
	}
	return nil
}

func removeEach(files []string) error {
	var failed []error
	for _, file := range files {
		failed = append(failed, removeIfPresent(file))
	}
	return errors.Join(failed...)
}

// discardHistory renames every file of the history to <name>.discarded. A .discarded file already there is what an earlier,
// interrupted rebuild discarded, deleted now as the user decided then.
func (s *Store) discardHistory() error {
	for _, file := range historyFiles(s.path) {
		present, err := fileExists(file)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if err := removeIfPresent(file + discardedSuffix); err != nil {
			return err
		}
		if err := moveDurably(file, file+discardedSuffix, false); err != nil {
			return err
		}
		if err := rebuildStep("discarded-file"); err != nil {
			return err
		}
	}
	return rebuildStep("discarded")
}

// restoreDiscarded puts every discarded file back under its name, after removing what a failed new history left there, and
// removes the rebuild record: the old history is as it was before the rebuild.
func (s *Store) restoreDiscarded() error {
	var failed []error
	for _, file := range historyFiles(s.path) {
		discarded, err := fileExists(file + discardedSuffix)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		if !discarded {
			continue
		}
		if err := removeIfPresent(file); err != nil {
			failed = append(failed, err)
			continue
		}
		failed = append(failed, moveDurably(file+discardedSuffix, file, false))
	}
	if err := errors.Join(failed...); err != nil {
		return err
	}
	return removeIfPresent(s.rebuildPath())
}

// createEmptyHistory creates a new, empty history with a derived key and its record, and proves it opens: the preflight with
// its key, then the file's identity.
func (s *Store) createEmptyHistory(ctx context.Context) error {
	key, record, err := newHistoryKey(s.inputs)
	if err != nil {
		return err
	}
	defer clear(key.key)
	if err := writeKeyRecord(s.path, record, false); err != nil {
		return err
	}
	db, err := openEncrypted(s.path, key.key, false)
	if err != nil {
		return err
	}
	err = createSchema(ctx, db)
	if err == nil {
		_, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("create history: %w", err)
	}
	if err := rebuildStep("created-file"); err != nil {
		return err
	}
	if outcome, err := preflight(s.path, key.key, true); err != nil || outcome != preflightReady {
		return errors.Join(err, fmt.Errorf("the new history does not decrypt with its key (%s)", outcome))
	}
	check, err := openEncrypted(s.path, key.key, false)
	if err != nil {
		return err
	}
	identity, err := readIdentity(ctx, check)
	if err == nil {
		err = checkIdentity(identity)
	}
	if closeErr := check.Close(); err == nil {
		err = closeErr
	}
	return err
}
