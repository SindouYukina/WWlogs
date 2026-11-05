package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// fakeSession publishes a view per tick until its context ends.
type fakeSession struct {
	id        uint64
	damage    int64
	runs      atomic.Int32
	closes    atomic.Int32
	ignoreCtx bool
	release   chan struct{}
	runErr    error
	burst     int
	closeWait <-chan struct{}
}

func (f *fakeSession) Info() capture.SessionInfo {
	return capture.SessionInfo{CaptureSessionID: f.id, Source: "replay"}
}

func (f *fakeSession) Close() {
	f.closes.Add(1)
	if f.closeWait != nil {
		<-f.closeWait
	}
}

func (f *fakeSession) Run(ctx context.Context, publish func(capture.ViewSnapshot)) (capture.SessionResult, error) {
	f.runs.Add(1)
	view := func() capture.ViewSnapshot {
		return capture.ViewSnapshot{CaptureSessionID: f.id, StatisticsIntervalID: 1, Statistics: capture.ViewStatistics{DamageDealt: f.damage}}
	}
	publish(view())
	for i := 0; i < f.burst; i++ {
		publish(view())
	}
	if f.runErr != nil {
		return capture.SessionResult{StopReason: capture.StopReasonError}, f.runErr
	}
	if f.ignoreCtx {
		<-f.release
	} else {
		<-ctx.Done()
	}
	final := view()
	final.Final = true
	publish(final)
	return capture.SessionResult{StopReason: capture.StopReasonRequested}, nil
}

func newTestService(sessions ...*fakeSession) (*CaptureService, *atomic.Int32) {
	var opened atomic.Int32
	service := NewCaptureService(func(capture.SessionConfig) (Session, error) {
		index := int(opened.Add(1)) - 1
		if index >= len(sessions) {
			return nil, errors.New("no more sessions")
		}
		return sessions[index], nil
	})
	return service, &opened
}

func stop(t *testing.T, s *CaptureService) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStartStopRestartKeepsSessionsApart(t *testing.T) {
	first, second := &fakeSession{id: 1, damage: 100}, &fakeSession{id: 2, damage: 7}
	s, _ := newTestService(first, second)
	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(capture.SessionConfig{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start = %v, want ErrBusy", err)
	}
	stop(t, s)
	status := s.Status()
	if status.State != StateStopped || status.LastResult == nil || status.LastResult.StopReason != capture.StopReasonRequested {
		t.Fatalf("status after stop = %+v", status)
	}
	if latest := s.Publisher().Latest(); latest.View == nil || !latest.View.Final || latest.View.Statistics.DamageDealt != 100 {
		t.Fatalf("the stopped session's final view was not kept: %+v", latest.View)
	}

	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	// The first snapshot of a new session carries no view of the previous one.
	afterStart := s.Publisher().Latest()
	if afterStart.View != nil && afterStart.View.CaptureSessionID != 2 {
		t.Fatalf("restart published the previous session's view: %+v", afterStart.View)
	}
	waitFor(t, func() bool { v := s.Publisher().Latest().View; return v != nil && v.CaptureSessionID == 2 })
	if v := s.Publisher().Latest().View; v.Statistics.DamageDealt != 7 {
		t.Fatalf("restart mixed sessions: %+v", v)
	}
	stop(t, s)
	if first.runs.Load() != 1 || second.runs.Load() != 1 {
		t.Fatalf("runs = %d, %d", first.runs.Load(), second.runs.Load())
	}
}

func TestConcurrentStopsWaitForTheSameSession(t *testing.T) {
	session := &fakeSession{id: 1}
	s, _ := newTestService(session)
	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			errs <- s.Stop(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Stop: %v", err)
		}
	}
	if s.Status().State != StateStopped || session.runs.Load() != 1 {
		t.Fatalf("state=%s runs=%d", s.Status().State, session.runs.Load())
	}
	// Stop with no session is a no-op.
	stop(t, s)
}

func TestFailedStartReportsErrorAndAllowsRetry(t *testing.T) {
	s, opened := newTestService()
	if err := s.Start(capture.SessionConfig{}); err == nil {
		t.Fatal("Start with a failing opener succeeded")
	}
	status := s.Status()
	if status.State != StateError || status.LastError == "" || s.Publisher().Latest().Service.State != StateError {
		t.Fatalf("status = %+v", status)
	}
	if err := s.Start(capture.SessionConfig{}); err == nil || opened.Load() != 2 {
		t.Fatalf("a retry after an error must open again: err=%v opened=%d", err, opened.Load())
	}
}

func TestSessionErrorEndsInErrorState(t *testing.T) {
	session := &fakeSession{id: 1, runErr: errors.New("adapter removed")}
	s, _ := newTestService(session)
	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Status().State == StateError })
	if status := s.Status(); status.LastError != "adapter removed" || status.LastResult.StopReason != capture.StopReasonError {
		t.Fatalf("status = %+v", status)
	}
}

func TestStopWhileOpeningClosesTheSession(t *testing.T) {
	session := &fakeSession{id: 1}
	opening := make(chan struct{})
	proceed := make(chan struct{})
	s := NewCaptureService(func(capture.SessionConfig) (Session, error) {
		close(opening)
		<-proceed
		return session, nil
	})
	started := make(chan error, 1)
	go func() { started <- s.Start(capture.SessionConfig{}) }()
	<-opening
	if err := s.Start(capture.SessionConfig{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start while opening = %v, want ErrBusy", err)
	}
	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stopped <- s.Stop(ctx)
	}()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.stopAsked })
	close(proceed)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if session.runs.Load() != 0 || session.closes.Load() != 1 || s.Status().State != StateStopped {
		t.Fatalf("runs=%d closes=%d state=%s", session.runs.Load(), session.closes.Load(), s.Status().State)
	}
}

func TestStopTimeoutKeepsTheServiceBusy(t *testing.T) {
	session := &fakeSession{id: 1, ignoreCtx: true, release: make(chan struct{})}
	s, _ := newTestService(session)
	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Stop(ctx); !errors.Is(err, ErrStopTimeout) {
		t.Fatalf("Stop = %v, want ErrStopTimeout", err)
	}
	if s.Status().State != StateStopping {
		t.Fatalf("state = %s, want stopping until the session releases its resources", s.Status().State)
	}
	if err := s.Start(capture.SessionConfig{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start during a pending stop = %v", err)
	}
	close(session.release)
	stop(t, s)
	if s.Status().State != StateStopped {
		t.Fatalf("state = %s", s.Status().State)
	}
}

func TestSlowReaderDoesNotSlowTheSession(t *testing.T) {
	session := &fakeSession{id: 1, burst: 50000}
	s, _ := newTestService(session)
	_ = s.Publisher().Latest() // a reader that never asks again
	started := time.Now()
	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Publisher().Latest().Sequence > 50000 })
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("50000 session publishes took %s", elapsed)
	}
	stop(t, s)
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestShutdownRejectsRestartAndClosesReaders(t *testing.T) {
	session := &fakeSession{id: 1}
	s, opened := newTestService(session)
	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(capture.SessionConfig{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("restart: %v", err)
	}
	if opened.Load() != 1 {
		t.Fatal("shutdown reopened a source")
	}
	if _, err := s.Publisher().Next(ctx, s.Publisher().Latest().Sequence); !errors.Is(err, ErrPublisherClosed) {
		t.Fatalf("reader: %v", err)
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownTimeoutNeverClaimsStopped(t *testing.T) {
	session := &fakeSession{id: 1, ignoreCtx: true, release: make(chan struct{})}
	s, _ := newTestService(session)
	if err := s.Start(capture.SessionConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, ErrStopTimeout) {
		t.Fatalf("shutdown: %v", err)
	}
	if s.Status().State != StateStopping || s.Status().LastResult != nil {
		t.Fatal("unfinished session claimed complete")
	}
	close(session.release)
	stop(t, s)
}

func TestShutdownDuringOpenDoesNotWaitOnLifecycleLock(t *testing.T) {
	opening, finishOpen, finishClose := make(chan struct{}), make(chan struct{}), make(chan struct{})
	session := &fakeSession{id: 1, closeWait: finishClose}
	s := NewCaptureService(func(capture.SessionConfig) (Session, error) {
		close(opening)
		<-finishOpen
		return session, nil
	})
	started := make(chan error, 1)
	go func() { started <- s.Start(capture.SessionConfig{}) }()
	<-opening
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- s.Shutdown(ctx) }()
	deadline := time.After(time.Second)
	for s.Status().State != StateStopping {
		select {
		case <-deadline:
			t.Fatal("shutdown did not enter stopping")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(finishOpen)
	select {
	case err := <-stopped:
		if !errors.Is(err, ErrStopTimeout) {
			t.Errorf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		close(finishClose)
		t.Fatal("session.Close blocked the shutdown deadline")
	}
	if err := s.Start(capture.SessionConfig{}); !errors.Is(err, ErrClosed) {
		t.Errorf("restart: %v", err)
	}
	close(finishClose)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	stop(t, s)
	if session.runs.Load() != 0 || session.closes.Load() != 1 {
		t.Fatal("opening session ran or closed more than once")
	}
}
