package desktop

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// shutdownWaitLimit includes Npcap read cancellation and the diagnostic writer's own 10s drain deadline.
const shutdownWaitLimit = 15 * time.Second

// shutdownGate defers native teardown until cleanup ends, without blocking the Windows message loop.
type shutdownGate struct {
	once  sync.Once
	ready atomic.Bool
	stop  func(context.Context) error
	quit  func()
	err   error
}

func newShutdownGate(stop func(context.Context) error, quit func()) *shutdownGate {
	return &shutdownGate{stop: stop, quit: quit}
}

func (g *shutdownGate) request() {
	g.once.Do(func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), shutdownWaitLimit)
			defer cancel()
			g.err = g.stop(ctx)
			if g.err != nil {
				fmt.Fprintln(os.Stderr, "WWlogs shutdown: capture cleanup deadline exceeded; unfinished diagnostics are incomplete")
			}
			g.ready.Store(true)
			g.quit()
		}()
	})
}

func (g *shutdownGate) shouldQuit() bool {
	if g.ready.Load() {
		return true
	}
	g.request()
	return false
}
