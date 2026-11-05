package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Quotas preserve existing evidence. Reaching a limit disables only recording, never analysis.
// A session reserves its full allowance before writing, including across concurrent processes.
const (
	diagnosticSessionBytes  = int64(2 << 30)
	diagnosticSpoolBytes    = int64(10 << 30)
	diagnosticMetadataBytes = int64(1 << 20)
	diagnosticSpoolSessions = 200
)

type diagnosticSetupError struct{ err error }

func (e *diagnosticSetupError) Error() string { return e.err.Error() }
func (e *diagnosticSetupError) Unwrap() error { return e.err }

// diagnosticByteBudget is shared by the asynchronous sinks of one session.
type diagnosticByteBudget struct {
	mu        sync.Mutex
	remaining int64
}
type budgetedDiagnosticWriter struct {
	target io.Writer
	budget *diagnosticByteBudget
}

func (w *budgetedDiagnosticWriter) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	if int64(len(p)) > w.budget.remaining {
		w.budget.mu.Unlock()
		return 0, errors.New("diagnostic session capacity reached")
	}
	w.budget.remaining -= int64(len(p))
	w.budget.mu.Unlock()
	return w.target.Write(p)
}

type diagnosticReservation struct {
	ReservedBytes int64 `json:"reservedBytes"`
}

// reserveDiagnosticStorage holds a short exclusive filesystem lock while accounting for other
// reservations. No old session, active recording, or incomplete evidence is deleted or overwritten.
func reserveDiagnosticStorage(directory string) (func(), error) {
	root := filepath.Dir(directory)
	lockPath := filepath.Join(root, ".capacity.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock diagnostic capacity: %w", err)
	}
	defer func() { lock.Close(); os.Remove(lockPath) }()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var used int64
	sessions, files := 0, 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessions++
		path := filepath.Join(root, entry.Name())
		if path == directory {
			continue
		}
		var size int64
		err = filepath.WalkDir(path, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			files++
			if files > 100000 || d.Type()&os.ModeSymlink != 0 {
				return errors.New("diagnostic spool requires manual review")
			}
			if !d.IsDir() {
				info, e := d.Info()
				if e != nil {
					return e
				}
				size += info.Size()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		var reservation diagnosticReservation
		data, readErr := os.ReadFile(filepath.Join(path, "storage.json"))
		if readErr == nil {
			if len(data) > 4096 || json.Unmarshal(data, &reservation) != nil || reservation.ReservedBytes < 0 || reservation.ReservedBytes > diagnosticSessionBytes+diagnosticMetadataBytes {
				return nil, errors.New("invalid diagnostic reservation")
			}
		} else if !os.IsNotExist(readErr) {
			return nil, readErr
		}
		used += max(size, reservation.ReservedBytes)
		if used > diagnosticSpoolBytes {
			return nil, errors.New("diagnostic spool capacity reached; archive sessions before recording again")
		}
	}
	if sessions > diagnosticSpoolSessions || used+diagnosticSessionBytes+diagnosticMetadataBytes > diagnosticSpoolBytes {
		return nil, errors.New("diagnostic spool capacity reached; archive sessions before recording again")
	}
	path := filepath.Join(directory, "storage.json")
	write := func(reserved int64) error {
		data, _ := json.Marshal(diagnosticReservation{ReservedBytes: reserved})
		return os.WriteFile(path, data, 0o600)
	}
	if err := write(diagnosticSessionBytes + diagnosticMetadataBytes); err != nil {
		return nil, err
	}
	return func() { _ = write(0) }, nil
}
