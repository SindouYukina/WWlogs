//go:build !windows

package history

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// writerLock is an exclusive flock beside the history file; the kernel releases it with the process.
type writerLock struct {
	file *os.File
}

var errLocked = errors.New("another WWlogs process is recording into this history")

func acquireWriterLock(path string) (*writerLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open history lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
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
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
