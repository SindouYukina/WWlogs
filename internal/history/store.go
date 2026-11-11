// Package history keeps the player's Combat History: a local SQLite file, separate from the read-only Master, that stores
// capture sessions and the encounters the engine segmented in them. It receives the engine's journal through a Recorder
// (one per capture session) on its own goroutine, so storage never waits on, slows or changes capture and analysis, and
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
	"wwlogs/internal/versioninfo"
)

// State says what a Store can do.
type State string

const (
	// StateReady records new sessions and answers queries.
	StateReady State = "ready"
	// StateReadOnly answers queries but records nothing: a newer schema wrote the file or another process records into it.
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
	ReasonMigrateFailed  = "migration_failed"
	ReasonNoHistoryFile  = "no_history_file"
	ReasonStorageStopped = "storage_stopped"
	// ReasonKeyMissing: an encrypted history file exists but its key file does not; no new key is ever made for it.
	ReasonKeyMissing = "key_missing"
	// ReasonKeyUnavailable: the key file cannot be read or unsealed (for example DPAPI refused it for this user).
	ReasonKeyUnavailable = "key_unavailable"
	// ReasonKeyMismatch: the unsealed key does not open the history file, or the file is not an encrypted history.
	ReasonKeyMismatch = "key_mismatch"
	// ReasonEncryptionMigration: a plaintext file written before encryption could not be converted; it is unchanged and
	// open read-only (Status.MigrationFailure says why).
	ReasonEncryptionMigration = "encryption_migration_failed"
)

// PlaintextMigration values.
const (
	PlaintextConverted = "converted"
	PlaintextFailed    = "failed"
	PlaintextPending   = "pending"
)

// Status is the Store's state for the UI.
type Status struct {
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`
	// SchemaVersion is the file's schema; LatestSchemaVersion is the one this build writes.
	SchemaVersion       int `json:"schemaVersion"`
	LatestSchemaVersion int `json:"latestSchemaVersion"`
	// RecoveredSessions and RecoveredEncounters count what an earlier run left open and this open marked abandoned.
	RecoveredSessions   int `json:"recoveredSessions"`
	RecoveredEncounters int `json:"recoveredEncounters"`
	// Encrypted is true when the open file is encrypted at rest with Encryption (aes-256-xts) and a key KeyProtection seals
	// (dpapi-user). It is false for a plaintext file this build could only read.
	Encrypted     bool   `json:"encrypted"`
	Encryption    string `json:"encryption,omitempty"`
	KeyProtection string `json:"keyProtection,omitempty"`
	// PlaintextMigration is converted when this open encrypted a plaintext file of an earlier build, failed when that
	// conversion failed (MigrationFailure gives the reason; the plaintext file is unchanged), and pending when a plaintext
	// file is open read-only because another process records into it. PlaintextBackups counts plaintext pre-migration copies
	// that could not be converted and remain on disk.
	PlaintextMigration string `json:"plaintextMigration,omitempty"`
	MigrationFailure   string `json:"migrationFailure,omitempty"`
	PlaintextBackups   int    `json:"plaintextBackups,omitempty"`
}

// Options configure a Store. Zero values use the running build and the host clock and time zone.
type Options struct {
	Path string
	// Location is the time zone local dates are taken in; nil uses the host's.
	Location *time.Location
	Now      func() time.Time
	// Build, ProtocolSchema and MasterSHA256 identify what decoded a session; they are stored with it.
	Build          *buildinfo.Identity
	ProtocolSchema string
	MasterSHA256   string
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
	protocol string
	master   string
	provider KeyProvider
	// key is the unsealed database key, kept for the connections the pool opens; Close clears it.
	key []byte

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
	store := &Store{path: options.Path, location: options.Location, now: options.Now, protocol: options.ProtocolSchema, master: options.MasterSHA256, provider: options.KeyProvider}
	if store.provider == nil {
		store.provider = defaultKeyProvider()
	}
	store.status.LatestSchemaVersion = latestVersion()
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
	if store.protocol == "" {
		store.protocol = versioninfo.WindowsSchemaLabel()
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
		return s.openReadOnly(ReasonLocked)
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
		// A file of a build before encryption: only a WWlogs history is converted; any other SQLite file stays untouched.
		if foreign, err := plaintextIsForeign(ctx, s.path); err != nil || foreign {
			s.setStatus(StateUnavailable, ReasonNotHistory)
			return errors.Join(errors.New("the file is not a WWlogs history file"), err)
		}
	}
	if err := s.unlockKey(kind != fileOpaque); err != nil {
		return err
	}
	if kind == filePlain {
		if err := encryptPlaintextFile(ctx, s.path, s.key); err != nil {
			var failed *migrationError
			if errors.As(err, &failed) {
				s.status.MigrationFailure = failed.reason
			}
			// The plaintext file is unchanged and nothing is recorded into it; the next open tries again. Its rows cannot be
			// read either: they are at an older schema that only the migration after the conversion brings up to date.
			s.status.PlaintextMigration = PlaintextFailed
			s.setStatus(StateUnavailable, ReasonEncryptionMigration)
			return err
		}
		s.status.PlaintextMigration = PlaintextConverted
	}
	for _, backup := range plaintextBackups(s.path) {
		if encryptPlaintextFile(ctx, backup, s.key) != nil {
			s.status.PlaintextBackups++
		}
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
	switch {
	case identity.empty():
		// A new file: nothing to keep, so it is created at the latest schema directly.
	case identity.applicationID != applicationID:
		s.setStatus(StateUnavailable, ReasonNotHistory)
		return errors.New("the file is not a WWlogs history file")
	case identity.version > latestVersion():
		// A newer WWlogs wrote it: read what this build understands, never write.
		s.closeHandles()
		return s.openReadOnly(ReasonNewerSchema)
	}
	if !identity.empty() {
		if err := quickCheck(ctx, db); err != nil {
			s.setStatus(StateUnavailable, ReasonCorrupt)
			return err
		}
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return fmt.Errorf("enable history write-ahead log: %w", err)
	}
	if identity.version < latestVersion() {
		if err := s.migrate(ctx, identity); err != nil {
			s.setStatus(StateUnavailable, ReasonMigrateFailed)
			return err
		}
	}
	s.status.SchemaVersion = latestVersion()
	if err := s.recover(ctx); err != nil {
		s.setStatus(StateUnavailable, ReasonOpenFailed)
		return err
	}
	s.setStatus(StateReady, "")
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

// plaintextIsForeign reports a plaintext SQLite file that is neither empty nor a WWlogs history.
func plaintextIsForeign(ctx context.Context, path string) (bool, error) {
	db, err := openPlain(path, true)
	if err != nil {
		return false, err
	}
	defer db.Close()
	identity, err := readIdentity(ctx, db)
	if err != nil {
		return false, err
	}
	return !identity.empty() && identity.applicationID != applicationID, nil
}

// openPlaintextReadOnly reads a plaintext file of an earlier build without changing it.
func (s *Store) openPlaintextReadOnly(ctx context.Context, reason string) error {
	db, err := openPlain(s.path, true)
	if err != nil {
		s.setStatus(StateUnavailable, reason)
		return fmt.Errorf("open plaintext history read-only: %w", err)
	}
	return s.adoptReadOnly(ctx, db, reason)
}

// openReadOnly opens the file for queries only: encrypted with its existing key, or a plaintext file of an earlier build.
func (s *Store) openReadOnly(reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kind, err := inspectFile(s.path)
	if err != nil {
		s.setStatus(StateUnavailable, reason)
		return err
	}
	if kind == filePlain {
		s.status.PlaintextMigration = PlaintextPending
		if err := s.openPlaintextReadOnly(ctx, reason); err != nil {
			return err
		}
		return s.readOnlyResult(reason)
	}
	key, err := loadKey(s.path, s.provider, false)
	if err != nil {
		s.setStatus(StateUnavailable, reason)
		return errors.Join(err, errors.New("the history file cannot be read"))
	}
	s.key = key
	db, err := openEncrypted(s.path, key, true)
	if err != nil {
		s.setStatus(StateUnavailable, reason)
		return fmt.Errorf("open history read-only: %w", err)
	}
	if err := s.adoptReadOnly(ctx, db, reason); err != nil {
		return err
	}
	s.status.Encrypted, s.status.Encryption, s.status.KeyProtection = true, encryptionName, s.provider.ID()
	return s.readOnlyResult(reason)
}

// adoptReadOnly keeps db for queries when it is a history this build reads.
func (s *Store) adoptReadOnly(ctx context.Context, db *sql.DB, reason string) error {
	identity, err := readIdentity(ctx, db)
	if err != nil || identity.applicationID != applicationID {
		_ = db.Close()
		s.setStatus(StateUnavailable, reason)
		return errors.Join(err, errors.New("the history file cannot be read"))
	}
	if identity.version != latestVersion() {
		// A newer schema may have changed what this build reads, and an older one lacks what its queries read until it is
		// migrated, which a read-only open never does: the store only lists that the file exists.
		s.db = db
		s.status.SchemaVersion = identity.version
		s.setStatus(StateUnavailable, reason)
		return fmt.Errorf("history schema %d is not readable by this build without migrating it (latest %d)", identity.version, latestVersion())
	}
	s.db = db
	s.status.SchemaVersion = identity.version
	s.setStatus(StateReadOnly, reason)
	return nil
}

func (s *Store) readOnlyResult(reason string) error {
	if reason == ReasonLocked {
		return errLocked
	}
	return fmt.Errorf("history schema %d is read-only for this build", s.status.SchemaVersion)
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

// migrate brings the file to the latest schema. An existing file is copied first, so a failed or wrong migration can never
// lose what was stored; each step then runs in its own transaction and records itself.
func (s *Store) migrate(ctx context.Context, identity fileIdentity) error {
	if !identity.empty() {
		// The copy is encrypted with the same key: a pre-migration copy never exists in plaintext.
		backup := s.path + ".schema-" + strconv.Itoa(identity.version) + "-" + s.now().UTC().Format("20060102T150405Z") + ".bak"
		if err := backupEncrypted(ctx, s.db, backup, s.key); err != nil {
			return fmt.Errorf("back up history before migration: %w", err)
		}
	}
	for _, step := range migrations {
		if step.version <= identity.version {
			continue
		}
		if err := s.applyMigration(ctx, step); err != nil {
			return fmt.Errorf("migrate history to schema %d (%s): %w", step.version, step.name, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, step migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if step.version == 1 {
		if _, err := tx.ExecContext(ctx, "PRAGMA application_id="+strconv.Itoa(applicationID)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, step.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migration (version, name, applied_at_ns, app_version, app_commit) VALUES (?, ?, ?, ?, ?)",
		step.version, step.name, s.now().UnixNano(), s.build.Version, s.build.Commit); err != nil {
		return err
	}
	// The foreign keys of every stored row must still hold after the step.
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	violation := rows.Next()
	rows.Close()
	if violation {
		return errors.New("the migrated history has foreign key violations")
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version="+strconv.Itoa(step.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// recover closes what an earlier run of this file left open. An application that ended without finishing a session never
// wrote its end, so the session and its unfinished encounters are abandoned: they stay listed, never as finished fights. An
// encounter that never had confirmed activity, with nothing of it lost, would have been discarded and is removed.
func (s *Store) recover(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	s.status.RecoveredSessions, s.status.RecoveredEncounters = int(sessions), int(encounters)
	return nil
}

// Status reports what the Store can do.
func (s *Store) Status() Status {
	if s == nil {
		return Status{State: StateUnavailable, Reason: ReasonNoHistoryFile, LatestSchemaVersion: latestVersion()}
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
// still storing after that loses the rest and stays open in the file, so the next open marks it abandoned.
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

// localDate is the calendar date of at in the store's time zone, with that zone's offset at that instant.
func (s *Store) localDate(at time.Time) (string, int) {
	local := at.In(s.location)
	_, offset := local.Zone()
	return local.Format("2006-01-02"), offset
}
