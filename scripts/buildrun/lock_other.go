//go:build !windows

package buildrun

import (
	"errors"
	"time"
)

// fileLock is unavailable outside Windows; official WWlogs builds are Windows-only.
type fileLock struct{}

func acquireLock(string, time.Duration, func()) (*fileLock, error) {
	return nil, errors.New("official build numbering requires Windows")
}

func (l *fileLock) release() {}
