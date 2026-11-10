//go:build windows

package buildrun

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// fileLock is an exclusive LockFileEx lock. Windows releases it when the handle closes, including when the process
// is killed, so an interrupted run never leaves the counter locked.
type fileLock struct {
	file *os.File
}

func acquireLock(path string, timeout time.Duration, waiting func()) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open build state lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	announced := false
	for {
		overlapped := new(windows.Overlapped)
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
		if err == nil {
			return &fileLock{file: file}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING) {
			file.Close()
			return nil, fmt.Errorf("lock build state: %w", err)
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("another official build run still holds the build state lock after %s", timeout)
		}
		if !announced && waiting != nil {
			waiting()
			announced = true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (l *fileLock) release() {
	if l == nil || l.file == nil {
		return
	}
	windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, new(windows.Overlapped))
	l.file.Close()
	l.file = nil
}
