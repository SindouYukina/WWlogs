package service

import (
	"context"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const gameProcessPollInterval = 2 * time.Second

// findGameProcess reads the system process list by name. It opens no handle to any process and never touches the game.
func findGameProcess() GameProcess {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return GameProcess{}
	}
	defer syscall.CloseHandle(snapshot)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := syscall.Process32First(snapshot, &entry); err != nil {
		return GameProcess{}
	}
	for {
		if isGameProcessName(syscall.UTF16ToString(entry.ExeFile[:])) {
			return GameProcess{Supported: true, Running: true, PID: entry.ProcessID}
		}
		if err := syscall.Process32Next(snapshot, &entry); err != nil {
			return GameProcess{Supported: true}
		}
	}
}

// GameProcessWatcher polls only while enabled reports true, so an idle application does no process enumeration.
type GameProcessWatcher struct {
	latest atomic.Pointer[GameProcess]
}

// Latest returns the most recent observation. Before the first poll Supported is false, which proves nothing.
func (w *GameProcessWatcher) Latest() GameProcess {
	if value := w.latest.Load(); value != nil {
		return *value
	}
	return GameProcess{}
}

// Run polls until ctx ends.
func (w *GameProcessWatcher) Run(ctx context.Context, enabled func() bool) {
	ticker := time.NewTicker(gameProcessPollInterval)
	defer ticker.Stop()
	for {
		if enabled() {
			value := findGameProcess()
			w.latest.Store(&value)
		} else {
			w.latest.Store(nil)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
