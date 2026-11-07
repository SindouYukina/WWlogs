package desktop

import (
	"context"
	"sync"

	"wwlogs/internal/capture"
)

// startupOutcome is what the launch-time capture start did. It exists for tests and the host's own logging; the UI
// reads the capture state and the environment check, which already describe every case.
type startupOutcome string

const (
	startupDisabled     startupOutcome = "disabled"
	startupEnvironment  startupOutcome = "environment_not_ready"
	startupNoAdapter    startupOutcome = "no_adapter"
	startupMissing      startupOutcome = "adapter_unavailable"
	startupUserTookOver startupOutcome = "user_took_over"
	startupStarted      startupOutcome = "started"
	startupStartFailed  startupOutcome = "start_failed"
)

// startupCapture lets a launch start at most one capture by itself. The automatic start is a new trigger for the same
// Bridge start path, not a second capture path, and it gives way for the rest of the run as soon as the user starts or
// stops a capture: a manual stop is never undone by it.
type startupCapture struct {
	mu      sync.Mutex
	settled bool
}

// claim settles the launch-time start and reports whether this call was the first to do so.
func (s *startupCapture) claim() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return false
	}
	s.settled = true
	return true
}

// startAtLaunch runs once when the application has started. It waits for the capture environment check, then starts
// only on the saved interface and only when that interface is present: it never picks another interface. The saved
// preferences are read after the check, so a change made while the check ran is respected.
func (b *Bridge) startAtLaunch(ctx context.Context) (startupOutcome, error) {
	if !b.settings.current().Settings.AutoStartCapture {
		b.startup.claim()
		return startupDisabled, nil
	}
	environment := b.environment.check(ctx)
	settings := b.settings.current().Settings
	switch {
	case !settings.AutoStartCapture:
		b.startup.claim()
		return startupDisabled, nil
	case environment.State != capture.EnvironmentReady:
		b.startup.claim()
		return startupEnvironment, nil
	case settings.DefaultAdapterIPv4 == "":
		b.startup.claim()
		return startupNoAdapter, nil
	case !environmentHasAdapter(environment, settings.DefaultAdapterIPv4):
		b.startup.claim()
		return startupMissing, nil
	}
	if !b.startup.claim() {
		return startupUserTookOver, nil
	}
	if err := b.startCapture(settings.DefaultAdapterIPv4); err != nil {
		return startupStartFailed, err
	}
	return startupStarted, nil
}

func environmentHasAdapter(environment capture.CaptureEnvironment, ipv4 string) bool {
	for _, adapter := range environment.Adapters {
		if adapter.IPv4 == ipv4 {
			return true
		}
	}
	return false
}
