package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// fakeRecorder stands in for a history recorder and remembers how the service ended it.
type fakeRecorder struct {
	mu       sync.Mutex
	finished []string
	aborted  int
	finish   error
	// deadline and cancelled describe the context Finish received.
	deadline  bool
	cancelled bool
}

func (r *fakeRecorder) Level() capture.HistoryLevel           { return capture.HistoryEvents }
func (r *fakeRecorder) Submit([]capture.HistoryEntry, uint64) {}
func (r *fakeRecorder) SubmitWait(context.Context, []capture.HistoryEntry, uint64) error {
	return nil
}
func (r *fakeRecorder) Abort() { r.mu.Lock(); r.aborted++; r.mu.Unlock() }
func (r *fakeRecorder) Finish(ctx context.Context, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, reason)
	_, r.deadline = ctx.Deadline()
	r.cancelled = ctx.Err() != nil
	return r.finish
}

func TestHistoryIsFinishedAfterTheSessionStops(t *testing.T) {
	recorder := &fakeRecorder{}
	var given capture.HistorySink
	service := NewCaptureService(func(cfg capture.SessionConfig) (Session, error) {
		given = cfg.History
		return &fakeSession{}, nil
	})
	service.SetHistory(func(capture.SessionConfig) (HistoryRecorder, string, error) { return recorder, "", nil })
	if err := service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	if given != HistoryRecorder(recorder) {
		t.Fatal("the session did not receive the history recorder")
	}
	if got := service.Status().History; got != HistoryRecording {
		t.Fatalf("history state = %q", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.finished) != 1 || recorder.aborted != 0 {
		t.Fatalf("finished %v, aborted %d", recorder.finished, recorder.aborted)
	}
}

// A live session's last history is stored within historyFinishLimit, so a stalled disk never holds up the capture lifecycle.
// A replay's is complete only once all of it was committed, so it waits without a deadline; Stop cancels that wait, and the
// recorder then removes the replay.
func TestHistoryFinishWaitsByTheSessionSource(t *testing.T) {
	for _, source := range []string{capture.SourceLive, capture.SourceReplay} {
		t.Run(source, func(t *testing.T) {
			recorder := &fakeRecorder{}
			service := NewCaptureService(func(capture.SessionConfig) (Session, error) { return &fakeSession{source: source}, nil })
			service.SetHistory(func(capture.SessionConfig) (HistoryRecorder, string, error) { return recorder, "", nil })
			if err := service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := service.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if len(recorder.finished) != 1 {
				t.Fatalf("finished %v", recorder.finished)
			}
			if live := source == capture.SourceLive; recorder.deadline != live || recorder.cancelled == live {
				t.Fatalf("%s finished with deadline %t, cancelled %t", source, recorder.deadline, recorder.cancelled)
			}
		})
	}
}

func TestHistoryOfASessionThatCannotOpenIsAborted(t *testing.T) {
	recorder := &fakeRecorder{}
	service := NewCaptureService(func(capture.SessionConfig) (Session, error) { return nil, errors.New("no adapter") })
	service.SetHistory(func(capture.SessionConfig) (HistoryRecorder, string, error) { return recorder, "", nil })
	if err := service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}); err == nil {
		t.Fatal("a failed open started")
	}
	if recorder.aborted != 1 || len(recorder.finished) != 0 {
		t.Fatalf("aborted %d, finished %v", recorder.aborted, recorder.finished)
	}
}

// slowAbortRecorder is a history recorder whose Abort takes as long as the test wants, like a writer draining its queue and
// a transaction removing the session's rows (history.Recorder.Abort waits for both).
type slowAbortRecorder struct {
	fakeRecorder
	aborting chan struct{} // closed when Abort begins
	release  chan struct{} // Abort returns once this is closed
}

func (r *slowAbortRecorder) Abort() {
	close(r.aborting)
	<-r.release
	r.fakeRecorder.Abort()
}

// Removing the history of a capture that cannot open never holds the lifecycle lock: a Stop meanwhile keeps its own
// deadline and Status answers at once, while a new Start stays busy until the removal ended.
func TestFailedOpenRemovesItsHistoryOutsideTheLifecycleLock(t *testing.T) {
	recorder := &slowAbortRecorder{aborting: make(chan struct{}), release: make(chan struct{})}
	service := NewCaptureService(func(capture.SessionConfig) (Session, error) { return nil, errors.New("no adapter") })
	service.SetHistory(func(capture.SessionConfig) (HistoryRecorder, string, error) { return recorder, "", nil })
	started := make(chan error, 1)
	go func() { started <- service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}) }()
	select {
	case <-recorder.aborting:
	case <-time.After(3 * time.Second):
		t.Fatal("the history of the failed open was not removed")
	}
	released := false
	release := func() {
		if !released {
			released = true
			close(recorder.release)
		}
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- service.Stop(ctx) }()
	select {
	case err := <-stopped:
		if !errors.Is(err, ErrStopTimeout) {
			t.Fatalf("Stop during the history removal = %v, want ErrStopTimeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop waited for the history removal instead of its own deadline")
	}
	answered := make(chan Status, 1)
	go func() { answered <- service.Status() }()
	select {
	case status := <-answered:
		if status.State != StateStopping {
			t.Fatalf("state during the history removal = %s, want stopping", status.State)
		}
	case <-time.After(time.Second):
		t.Fatal("Status waited for the history removal")
	}
	if err := service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start during the history removal = %v, want ErrBusy", err)
	}

	release()
	if err := <-started; err == nil {
		t.Fatal("a failed open started")
	}
	if status := service.Status(); status.State != StateError || status.LastError == "" {
		t.Fatalf("status after the failed open = %+v", status)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if err := service.Stop(ctx2); err != nil {
		t.Fatalf("Stop after the failed open = %v", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.aborted != 1 || len(recorder.finished) != 0 {
		t.Fatalf("aborted %d, finished %v", recorder.aborted, recorder.finished)
	}
}

func TestHistoryThatCannotBeginNeverStopsTheCapture(t *testing.T) {
	var given capture.HistorySink
	service := NewCaptureService(func(cfg capture.SessionConfig) (Session, error) {
		given = cfg.History
		return &fakeSession{}, nil
	})
	service.SetHistory(func(capture.SessionConfig) (HistoryRecorder, string, error) {
		return nil, HistoryUnavailable, errors.New("disk full")
	})
	if err := service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}); err != nil {
		t.Fatalf("a history failure stopped the capture: %v", err)
	}
	if given != nil || service.Status().History != HistoryUnavailable || service.Status().State != StateRunning {
		t.Fatalf("status = %+v", service.Status())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = service.Stop(ctx)
}
