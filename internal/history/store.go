// Package history keeps the player's Combat History: a local SQLite file, separate from the read-only Master, that stores
// capture sessions and the encounters the engine segmented in them. It receives the engine's journal through a Recorder
// (one per capture session) on its own goroutine, so storage never slows, stalls or changes a live capture and analysis, and
// it answers the History view's queries. The file is local and encrypted at rest (encryption.go, keys.go).
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"wwlogs/internal/buildinfo"
)

// State says what a Store can do.
type State string

const (
	// StateReady records new sessions and answers queries.
	StateReady State = "ready"
	// StateReadOnly answers queries but records nothing: another process records into the file.
	StateReadOnly State = "read_only"
	// StateUnavailable neither records nor reads.
	StateUnavailable State = "unavailable"
)

// Reasons a Store is not ready. They are stable codes for the UI; none of them carries a path.
const (
	ReasonNewerSchema    = "newer_schema"
	ReasonLocked         = "locked_by_other_process"
	ReasonNotHistory     = "not_a_history_file"
	ReasonCorrupt        = "integrity_check_failed"
	ReasonOpenFailed     = "open_failed"
	ReasonNoHistoryFile  = "no_history_file"
	ReasonStorageStopped = "storage_stopped"
	// ReasonKeyMissing: an encrypted history file exists but its key file does not; no new key is ever made for it.
	ReasonKeyMissing = "key_missing"
	// ReasonKeyUnavailable: the key file cannot be read or unsealed (for example DPAPI refused it for this user).
	ReasonKeyUnavailable = "key_unavailable"
	// ReasonKeyMismatch: the unsealed key does not open the history file, or the file is not an encrypted history.
	ReasonKeyMismatch = "key_mismatch"
)

// Status is the Store's state for the UI.
type Status struct {
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`
	// SchemaVersion is the file's schema; LatestSchemaVersion is the one this build reads and writes.
	SchemaVersion       int `json:"schemaVersion"`
	LatestSchemaVersion int `json:"latestSchemaVersion"`
	// RecoveredSessions and RecoveredEncounters count what an earlier run left open and this open marked abandoned;
	// RemovedReplays counts replays an earlier run left unfinished, which this open removed.
	RecoveredSessions   int `json:"recoveredSessions"`
	RecoveredEncounters int `json:"recoveredEncounters"`
	RemovedReplays      int `json:"removedReplays,omitempty"`
	// Encrypted is true when the open file is encrypted at rest with Encryption (aes-256-xts) and a key KeyProtection seals
	// (dpapi-user).
	Encrypted     bool   `json:"encrypted"`
	Encryption    string `json:"encryption,omitempty"`
	KeyProtection string `json:"keyProtection,omitempty"`
}

// Options configure a Store. Zero values use the running build and the host clock and time zone.
type Options struct {
	Path string
	// Location is the zone an encounter's recorded date and offset are taken in; nil uses the host's. Views choose their
	// own zone per query.
	Location *time.Location
	Now      func() time.Time
	// Build and MasterSHA256 identify the analyser that recorded a session; they are stored with it.
	Build        *buildinfo.Identity
	MasterSHA256 string
	// KeyProvider seals the history key; nil uses the platform's (DPAPI for the current user on Windows).
	KeyProvider KeyProvider
}

// Store is one Combat History file. All methods are safe for concurrent use.
type Store struct {
	path     string
	db       *sql.DB
	lock     *writerLock
	location *time.Location
	now      func() time.Time
	build    buildinfo.Identity
	master   string
	provider KeyProvider
	// key is the unsealed database key, kept for the connections the pool opens; Close clears it.
	key []byte

	// models caches the analysis models of recently analysed encounters (analysis.go).
	models modelCache
	// library caches the encounter index of the library and the career (library.go).
	library indexCache

	mu       sync.Mutex
	status   Status
	closed   bool
	sessions sync.WaitGroup
}

// DefaultPath is the history file under %LOCALAPPDATA%\WWlogs, beside settings.json and outside the executable.
func DefaultPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return "", errors.New("no local application data directory")
		}
	}
	return filepath.Join(base, "WWlogs", "history", "combat-history.db"), nil
}

// Open opens or creates the history file at options.Path. It always returns a Store: when the file cannot be recorded into,
// the Store says why in Status and, where it can, still answers queries. The error describes the same problem.
func Open(options Options) (*Store, error) {
	store := &Store{path: options.Path, location: options.Location, now: options.Now, master: options.MasterSHA256, provider: options.KeyProvider}
	if store.provider == nil {
		store.provider = defaultKeyProvider()
	}
	store.status.LatestSchemaVersion = schemaVersion
	if store.location == nil {
		store.location = time.Local
	}
	if store.now == nil {
		store.now = time.Now
	}
	if options.Build != nil {
		store.build = *options.Build
	} else {
		store.build = buildinfo.Current()
	}
	err := store.open()
	if err != nil && store.status.State == StateUnavailable {
		// A read-only Store keeps its handle for queries; an unavailable one keeps nothing open.
		store.closeHandles()
	}
	return store, err
}

func (s *Store) setStatus(state State, reason string) {
	s.status.State, s.status.Reason = state, reason
}

// open opens the file for recording, or creates it. A file this build cannot use as its history is never modified: a
// plaintext SQLite file (the history is never written unencrypted), a file of another application or of another layout, a
// file whose key is missing or does not open it, and a file another WWlogs records into, which is opened read-only.
func (s *Store) open() error {
	if s.path == "" {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return errors.New("no history path")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return fmt.Errorf("create history directory: %w", err)
	}
	lock, err := acquireWriterLock(s.path + ".lock")
	if errors.Is(err, errLocked) {
		// Another WWlogs records here; this one may read what is already stored, but never writes or recovers.
		if _, statErr := os.Stat(s.path); statErr != nil {
			s.setStatus(StateUnavailable, ReasonLocked)
			return err
		}
		return s.openReadOnly()
	}
	if err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return err
	}
	s.lock = lock
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	kind, err := inspectFile(s.path)
	if err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return fmt.Errorf("inspect history: %w", err)
	}
	if kind == filePlain {
		s.setStatus(StateUnavailable, ReasonNotHistory)
		return errors.New("the file is an unencrypted SQLite file, not a WWlogs history")
	}
	if err := s.unlockKey(kind != fileOpaque); err != nil {
		return err
	}
	db, err := openEncrypted(s.path, s.key, false)
	if err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return fmt.Errorf("open history: %w", err)
	}
	s.db = db
	identity, err := readIdentity(ctx, db)
	if err != nil {
		if isNotADatabase(err) {
			s.setStatus(StateUnavailable, ReasonKeyMismatch)
			return errors.New("the history key does not open the history file")
		}
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return err
	}
	s.status.Encrypted, s.status.Encryption, s.status.KeyProtection = true, encryptionName, s.provider.ID()
	if identity.empty() {
		if err := s.create(ctx); err != nil {
			s.setStatus(StateUnavailable, ReasonOpenFailed)
			return err
		}
	} else if err := checkIdentity(identity); err != nil {
		s.status.SchemaVersion = identity.version
		s.setStatus(StateUnavailable, identityReason(identity))
		return err
	} else if err := quickCheck(ctx, db); err != nil {
		s.setStatus(StateUnavailable, ReasonCorrupt)
		return err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return fmt.Errorf("enable history write-ahead log: %w", err)
	}
	s.status.SchemaVersion = schemaVersion
	if err := s.recover(ctx); err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return err
	}
	s.setStatus(StateReady, "")
	return nil
}

// create lays the schema out in a new, empty file: the whole layout and its identity in one transaction, so a file is either
// empty or a complete history.
func (s *Store) create(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create history: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{"PRAGMA application_id=" + strconv.Itoa(applicationID), schemaDDL, "PRAGMA user_version=" + strconv.Itoa(schemaVersion)} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create history: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create history: %w", err)
	}
	return nil
}

// unlockKey unseals the history key, creating one only when allowed: never for an encrypted file that lost its key file.
func (s *Store) unlockKey(create bool) error {
	key, err := loadKey(s.path, s.provider, create)
	switch {
	case errors.Is(err, errKeyMissing):
		s.setStatus(StateUnavailable, ReasonKeyMissing)
		return err
	case err != nil:
		s.setStatus(StateUnavailable, ReasonKeyUnavailable)
		return err
	}
	s.key = key
	return nil
}

// openReadOnly opens the history another process records into, for queries only.
func (s *Store) openReadOnly() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kind, err := inspectFile(s.path)
	if err != nil {
		s.setStatus(StateUnavailable, ReasonLocked)
		return err
	}
	if kind == filePlain {
		s.setStatus(StateUnavailable, ReasonNotHistory)
		return errors.New("the file is an unencrypted SQLite file, not a WWlogs history")
	}
	key, err := loadKey(s.path, s.provider, false)
	if err != nil {
		s.setStatus(StateUnavailable, ReasonLocked)
		return errors.Join(err, errors.New("the history file cannot be read"))
	}
	s.key = key
	db, err := openEncrypted(s.path, key, true)
	if err != nil {
		s.setStatus(StateUnavailable, ReasonLocked)
		return fmt.Errorf("open history read-only: %w", err)
	}
	s.db = db
	identity, err := readIdentity(ctx, db)
	if err == nil {
		err = checkIdentity(identity)
	}
	if err != nil {
		s.status.SchemaVersion = identity.version
		s.setStatus(StateUnavailable, ReasonLocked)
		return errors.Join(err, errors.New("the history file cannot be read"))
	}
	s.status.SchemaVersion = identity.version
	s.status.Encrypted, s.status.Encryption, s.status.KeyProtection = true, encryptionName, s.provider.ID()
	s.setStatus(StateReadOnly, ReasonLocked)
	return errLocked
}

type fileIdentity struct {
	applicationID int64
	version       int
	tables        int
}

func (i fileIdentity) empty() bool { return i.applicationID == 0 && i.version == 0 && i.tables == 0 }

func readIdentity(ctx context.Context, db *sql.DB) (fileIdentity, error) {
	var identity fileIdentity
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&identity.applicationID); err != nil {
		return identity, fmt.Errorf("read history file identity: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&identity.version); err != nil {
		return identity, fmt.Errorf("read history schema version: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&identity.tables); err != nil {
		return identity, fmt.Errorf("read history schema: %w", err)
	}
	return identity, nil
}

// checkIdentity accepts a WWlogs history of the layout this build reads and writes, and nothing else.
func checkIdentity(identity fileIdentity) error {
	switch {
	case identity.applicationID != applicationID:
		return errors.New("the file is not a WWlogs history file")
	case identity.version != schemaVersion:
		return fmt.Errorf("history schema %d is not the schema %d this build reads", identity.version, schemaVersion)
	}
	return nil
}

// identityReason is the status reason for a file checkIdentity refused: a later WWlogs wrote a newer layout, or it is no
// WWlogs history this build knows.
func identityReason(identity fileIdentity) string {
	if identity.applicationID == applicationID && identity.version > schemaVersion {
		return ReasonNewerSchema
	}
	return ReasonNotHistory
}

func quickCheck(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("check history file: %w", err)
	}
	if result != "ok" {
		return errors.New("the history file failed its integrity check")
	}
	return nil
}

// recover closes what an earlier run of this file left open. A replay that never finished storing was never stored: it is
// removed, so the same file can be replayed again (Recorder.Finish). A live session never wrote its end, so the session and
// its unfinished encounters are abandoned: they stay listed, never as finished fights. An encounter that never had confirmed
// activity, with nothing of it lost, would have been discarded and is removed.
func (s *Store) recover(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	replays, err := tx.ExecContext(ctx, "DELETE FROM capture_session WHERE status = 'open' AND source = ?", SourceReplay)
	if err != nil {
		return fmt.Errorf("recover history: %w", err)
	}
	removed, _ := replays.RowsAffected()
	if _, err := tx.ExecContext(ctx, "DELETE FROM encounter WHERE state = 'pending' AND complete = 1 AND capture_session_id IN (SELECT id FROM capture_session WHERE status = 'open')"); err != nil {
		return fmt.Errorf("recover history: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE encounter SET state = 'abandoned', complete = 0, incomplete_reason = COALESCE(incomplete_reason, ?) WHERE state IN ('open', 'pending') AND capture_session_id IN (SELECT id FROM capture_session WHERE status = 'open')",
		incompleteApplicationExit)
	if err != nil {
		return fmt.Errorf("recover history: %w", err)
	}
	encounters, _ := result.RowsAffected()
	result, err = tx.ExecContext(ctx, "UPDATE capture_session SET status = 'abandoned' WHERE status = 'open'")
	if err != nil {
		return fmt.Errorf("recover history: %w", err)
	}
	sessions, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("recover history: %w", err)
	}
	s.status.RecoveredSessions, s.status.RecoveredEncounters, s.status.RemovedReplays = int(sessions), int(encounters), int(removed)
	return nil
}

// Status reports what the Store can do.
func (s *Store) Status() Status {
	if s == nil {
		return Status{State: StateUnavailable, Reason: ReasonNoHistoryFile, LatestSchemaVersion: schemaVersion}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Store) readable() bool {
	status := s.Status()
	return s.db != nil && (status.State == StateReady || status.State == StateReadOnly || status.Reason == ReasonStorageStopped)
}

// closeWaitLimit bounds how long Close waits for sessions still storing their history.
const closeWaitLimit = 5 * time.Second

// Close waits, at most closeWaitLimit, for recording sessions to finish, then releases the file and its lock. A session
// still storing after that loses the rest and stays open in the file, so the next open marks a live one abandoned and
// removes a replay.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		s.sessions.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(closeWaitLimit):
	}
	return s.closeHandles()
}

func (s *Store) closeHandles() error {
	var err error
	if s.db != nil {
		// A clean close checkpoints the write-ahead log into the file.
		err = s.db.Close()
		s.db = nil
	}
	s.lock.release()
	s.lock = nil
	clear(s.key)
	s.key = nil
	return err
}

// Path is the file the Store uses.
func (s *Store) Path() string { return s.path }

// unixNanos stores a time as UTC nanoseconds; the zero time is NULL.
func unixNanos(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixNano()
}

// timeFromNanos restores a stored time in the host's local representation, the one the engine's capture times use, so a
// stored encounter projects to exactly the view the live engine produced.
func timeFromNanos(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(0, value.Int64)
}

// localDate is the calendar date of at in the store's recording zone, with that zone's offset at that instant: the
// recorded provenance of an encounter (local_date, utc_offset_s), never the date a view shows it on.
func (s *Store) localDate(at time.Time) (string, int) {
	local := at.In(s.location)
	_, offset := local.Zone()
	return local.Format("2006-01-02"), offset
}
