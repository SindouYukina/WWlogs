package desktop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestShutdownGateDefersQuitAndRunsOnce(t *testing.T) {
	entered, release, quit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	gate := newShutdownGate(func(ctx context.Context) error {
		calls.Add(1)
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > shutdownWaitLimit {
			t.Error("cleanup has no bounded deadline")
		}
		close(entered)
		<-release
		return errors.New("incomplete")
	}, func() { close(quit) })
	if gate.shouldQuit() {
		t.Fatal("quit allowed before cleanup")
	}
	<-entered
	gate.request()
	if gate.shouldQuit() {
		t.Fatal("quit allowed during cleanup")
	}
	close(release)
	select {
	case <-quit:
	case <-time.After(time.Second):
		t.Fatal("quit not resumed")
	}
	if !gate.shouldQuit() || calls.Load() != 1 || gate.err == nil {
		t.Fatal("cleanup result lost or cleanup repeated")
	}
}
