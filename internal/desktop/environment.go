package desktop

import (
	"context"
	"sync"
	"time"

	"wwlogs/internal/capture"
)

// environmentCheckLimit bounds how long the UI waits. Enumeration runs inside the Npcap library and cannot be cancelled,
// so a stuck driver must not leave the UI checking forever.
const environmentCheckLimit = 15 * time.Second

// environmentChecker runs at most one interface enumeration at a time. Requests that arrive during a check share its
// result, so repeated presses cannot stack up calls into the library.
type environmentChecker struct {
	probe func() capture.CaptureEnvironment
	limit time.Duration

	mu      sync.Mutex
	current *environmentCall
}

type environmentCall struct {
	done   chan struct{}
	result capture.CaptureEnvironment
}

func newEnvironmentChecker(probe func() capture.CaptureEnvironment) *environmentChecker {
	return &environmentChecker{probe: probe, limit: environmentCheckLimit}
}

// check returns the result of the check in progress, or of a new one. A check that outlives the limit keeps running and
// later requests join it, so at most one goroutine can ever be stuck in the library.
func (c *environmentChecker) check(ctx context.Context) capture.CaptureEnvironment {
	c.mu.Lock()
	call := c.current
	if call == nil {
		call = &environmentCall{done: make(chan struct{})}
		c.current = call
		go func() {
			call.result = c.probe()
			c.mu.Lock()
			c.current = nil
			c.mu.Unlock()
			close(call.done)
		}()
	}
	c.mu.Unlock()
	timer := time.NewTimer(c.limit)
	defer timer.Stop()
	select {
	case <-call.done:
		return call.result
	case <-timer.C:
	case <-ctx.Done():
	}
	return capture.CaptureEnvironment{State: capture.EnvironmentCheckFailed, Adapters: []capture.Adapter{}}
}
