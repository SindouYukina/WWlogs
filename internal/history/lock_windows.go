//go:build windows

package history

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// writerLock is an exclusive LockFileEx lock beside the history file. Windows releases it when the handle closes, also when
// the process is killed, so a crashed WWlogs never leaves the history locked.
type writerLock struct {
	file *os.File
}

// errLocked reports that another WWlogs process records into the same history file.
var errLocked = errors.New("another WWlogs process is recording into this history")

func acquireWriterLock(path string) (*writerLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open history lock: %w", err)
	}
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if err != nil {
		file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, errLocked
		}
		return nil, fmt.Errorf("lock history: %w", err)
	}
	return &writerLock{file: file}, nil
}

func (l *writerLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, new(windows.Overlapped))
	_ = l.file.Close()
	l.file = nil
}
