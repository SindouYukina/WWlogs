// Package buildrun allocates official WWlogs Build Numbers and installs one build run's executables as a unit.
//
// It is build tooling used by scripts/embed-key.go; no product package imports it. The owner's counter lives in the
// Git-ignored .local/build-state.json. A run holds an exclusive operating-system lock on .local/build-state.lock for
// its whole duration, so concurrent official runs are serialized and the operating system releases the lock when a
// run is killed. The counter only moves when every requested executable of the run was built, verified and installed.
package buildrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	stateFileName  = "build-state.json"
	backupFileName = "build-state.backup.json"
	lockFileName   = "build-state.lock"
	schemaVersion  = 1
)

// ErrNotInitialized means no official counter exists. The caller builds an unofficial executable instead; nothing
// is created on disk.
var ErrNotInitialized = errors.New("official build counter is not initialized")

// ErrBlocked means the counter exists but cannot be trusted or could have existed before. Nothing is reset.
var ErrBlocked = errors.New("official build counter is blocked")

// SuccessRecord describes the last complete run. It stays in the private state file and never enters an artifact.
type SuccessRecord struct {
	BuildNumber uint64    `json:"buildNumber"`
	CompletedAt time.Time `json:"completedAt"`
	Commit      string    `json:"commit"`
	Dirty       string    `json:"dirty"`
	Target      string    `json:"target"`
	Profiles    []string  `json:"profiles"`
}

// State is the persisted counter.
type State struct {
	SchemaVersion int `json:"schemaVersion"`
	// LastSuccessfulBuildNumber is the number of the last run whose whole batch was installed. Zero means the
	// counter was initialized and no run has completed yet.
	LastSuccessfulBuildNumber uint64 `json:"lastSuccessfulBuildNumber"`
	// HighestIssuedBuildNumber is the highest number that reached an installed location. It runs ahead of
	// LastSuccessfulBuildNumber only when a run was interrupted while installing; that number is never issued again.
	HighestIssuedBuildNumber uint64         `json:"highestIssuedBuildNumber"`
	LastSuccessful           *SuccessRecord `json:"lastSuccessful,omitempty"`
}

func (s State) validate() error {
	if s.SchemaVersion != schemaVersion {
		return fmt.Errorf("unsupported schemaVersion %d", s.SchemaVersion)
	}
	if s.HighestIssuedBuildNumber < s.LastSuccessfulBuildNumber {
		return errors.New("highestIssuedBuildNumber is below lastSuccessfulBuildNumber")
	}
	if s.LastSuccessfulBuildNumber > 0 && (s.LastSuccessful == nil || s.LastSuccessful.BuildNumber != s.LastSuccessfulBuildNumber) {
		return errors.New("lastSuccessful does not match lastSuccessfulBuildNumber")
	}
	return nil
}

// next is the candidate for the following run.
func (s State) next() uint64 {
	return max(s.LastSuccessfulBuildNumber, s.HighestIssuedBuildNumber) + 1
}

// Store is the counter directory, normally <repository>/.local.
type Store struct {
	Dir string
	// LockTimeout bounds the wait for another official run; zero waits up to 30 minutes.
	LockTimeout time.Duration
	// Waiting is called once when the lock is held by another run.
	Waiting func()
}

// Allocation is an exclusive hold on the counter and the candidate number of one run.
type Allocation struct {
	Number      uint64
	Initialized bool // true when this run creates the counter

	store        *Store
	lock         *fileLock
	state        State
	issued       bool
	issuedBefore uint64
	finished     bool
}

// Begin locks the counter and returns the next number. With initialize it creates a counter that never existed;
// it refuses when a backup or state file shows that one did.
func (s *Store) Begin(initialize bool) (*Allocation, error) {
	statePath := filepath.Join(s.Dir, stateFileName)
	stateExists, err := exists(statePath)
	if err != nil {
		return nil, err
	}
	if !stateExists && !initialize {
		return nil, ErrNotInitialized
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create build state directory: %w", err)
	}
	timeout := s.LockTimeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	lock, err := acquireLock(filepath.Join(s.Dir, lockFileName), timeout, s.Waiting)
	if err != nil {
		return nil, err
	}
	allocation := &Allocation{store: s, lock: lock}
	state, err := s.load(initialize)
	if err != nil {
		lock.release()
		return nil, err
	}
	allocation.state = state
	allocation.Initialized = state.SchemaVersion == 0
	if allocation.Initialized {
		allocation.state = State{SchemaVersion: schemaVersion}
	}
	allocation.Number = allocation.state.next()
	return allocation, nil
}

// load reads the state under the lock. A missing state is only accepted for a first initialization.
func (s *Store) load(initialize bool) (State, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, stateFileName))
	if errors.Is(err, fs.ErrNotExist) {
		if !initialize {
			return State{}, ErrNotInitialized
		}
		if backup, backupErr := exists(filepath.Join(s.Dir, backupFileName)); backupErr != nil {
			return State{}, backupErr
		} else if backup {
			return State{}, fmt.Errorf("%w: %s is missing but %s exists; restore the state instead of initializing", ErrBlocked, stateFileName, backupFileName)
		}
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("%w: read %s: %v", ErrBlocked, stateFileName, err)
	}
	if initialize {
		return State{}, fmt.Errorf("%w: %s already exists; initialization is only for a counter that never existed", ErrBlocked, stateFileName)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("%w: %s is not valid JSON: %v", ErrBlocked, stateFileName, err)
	}
	if err := state.validate(); err != nil {
		return State{}, fmt.Errorf("%w: %s: %v", ErrBlocked, stateFileName, err)
	}
	return state, nil
}

// MarkIssued records, before anything is installed, that the number may reach an installed location. If the run is
// killed while installing, the next run skips this number instead of issuing it twice.
func (a *Allocation) MarkIssued() error {
	if a.finished {
		return errors.New("allocation already finished")
	}
	next := a.state
	next.HighestIssuedBuildNumber = a.Number
	if err := a.store.write(next, false); err != nil {
		return err
	}
	a.issuedBefore = a.state.HighestIssuedBuildNumber
	a.state, a.issued = next, true
	return nil
}

// Commit records the run as the last successful one and releases the lock.
func (a *Allocation) Commit(record SuccessRecord) error {
	if a.finished {
		return errors.New("allocation already finished")
	}
	if !a.issued {
		return errors.New("commit before MarkIssued")
	}
	record.BuildNumber = a.Number
	next := a.state
	next.LastSuccessfulBuildNumber, next.HighestIssuedBuildNumber, next.LastSuccessful = a.Number, a.Number, &record
	if err := a.store.write(next, true); err != nil {
		return err
	}
	a.state = next
	a.finish()
	return nil
}

// Abort ends a failed run. When the run's installation was fully rolled back, the issued mark is withdrawn so the
// number can be used by the next run; otherwise the mark stays and the number is skipped.
func (a *Allocation) Abort(rolledBack bool) error {
	if a.finished {
		return nil
	}
	defer a.finish()
	if !a.issued || !rolledBack {
		return nil
	}
	previous := a.state
	previous.HighestIssuedBuildNumber = a.issuedBefore
	return a.store.write(previous, false)
}

func (a *Allocation) finish() {
	a.finished = true
	a.lock.release()
}

// write replaces the state file atomically. A successful run also refreshes the backup, whose presence later stops a
// lost state file from being silently re-initialized.
func (s *Store) write(state State, refreshBackup bool) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := replaceFile(filepath.Join(s.Dir, stateFileName), data); err != nil {
		return fmt.Errorf("write %s: %w", stateFileName, err)
	}
	if refreshBackup {
		if err := replaceFile(filepath.Join(s.Dir, backupFileName), data); err != nil {
			return fmt.Errorf("write %s: %w", backupFileName, err)
		}
	}
	return nil
}

func replaceFile(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_, writeErr := temporary.Write(data)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("%w: inspect %s: %v", ErrBlocked, filepath.Base(path), err)
}
