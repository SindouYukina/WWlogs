package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"wwlogs/internal/buildinfo"
	"wwlogs/internal/capture"
)

// State is the CaptureService lifecycle state.
type State string

// CaptureService states. Starting and Stopping are transitions; only one session exists at a time.
const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateError    State = "error"
)

// Status is the service state published with every snapshot.
type Status struct {
	State State `json:"state"`
	// Session describes the current session, or the last one after it ended.
	Session   *capture.SessionInfo `json:"session,omitempty"`
	StartedAt time.Time            `json:"startedAt,omitempty"`
	// LastError is the reason for StateError: a failed start or a session that ended with an error.
	LastError string `json:"lastError,omitempty"`
	// LastResult is how the last session ended.
	LastResult *capture.SessionResult `json:"lastResult,omitempty"`
	App        AppIdentity            `json:"app"`
}

// AppIdentity is the build identity shown in the UI, taken from buildinfo. It carries no paths or hashes.
type AppIdentity struct {
	Version string `json:"version"`
	// BuildNumber is the official Build Number; zero with Official false marks an unofficial build.
	BuildNumber uint64 `json:"buildNumber"`
	Official    bool   `json:"official"`
	Commit      string `json:"commit"`
	// Dirty is "true" when tracked sources were modified at build time, "false", or "unknown".
	Dirty   string `json:"dirty"`
	Profile string `json:"profile"`
}

// Session is one runnable capture session; *capture.Session implements it.
type Session interface {
	Info() capture.SessionInfo
	Run(ctx context.Context, publish func(capture.ViewSnapshot)) (capture.SessionResult, error)
	Close()
	// SetPublishInterval changes the snapshot cadence while the session runs; it must be safe for concurrent use.
	SetPublishInterval(interval time.Duration)
}

// Opener opens a session for cfg. It must release everything it opened when it fails.
type Opener func(cfg capture.SessionConfig) (Session, error)

// Errors returned by CaptureService.
var (
	ErrBusy        = errors.New("a capture session is already starting, running, or stopping")
	ErrStopTimeout = errors.New("the capture session did not stop before the deadline")
	ErrClosed      = errors.New("the capture service is shutting down")
)

// CaptureService runs at most one capture session at a time and publishes its snapshots. All methods are safe for
// concurrent use. The session goroutine never waits for readers: it hands each snapshot to the Publisher, which only
// replaces a pointer.
type CaptureService struct {
	open      Opener
	publisher *Publisher
	identity  AppIdentity

	mu         sync.Mutex
	status     Status
	generation uint64 // increases with every Start; snapshots of any other generation are ignored
	view       *capture.ViewSnapshot
	// publishInterval is the user's snapshot cadence; zero leaves each session's configured value. session is the
	// running session of the current generation, so a cadence change reaches it without a restart.
	publishInterval time.Duration
	session         Session
	cancel          context.CancelFunc
	done            chan struct{} // closed when the current session goroutine has released everything
	stopAsked       bool          // Stop arrived while the session was still opening
	closed          bool
}

// NewCaptureService creates a stopped service using open to create sessions.
func NewCaptureService(open Opener) *CaptureService {
	record := buildinfo.Current()
	identity := AppIdentity{Version: record.Version, BuildNumber: record.BuildNumber, Official: record.Official(),
		Commit: record.Commit, Dirty: record.Dirty, Profile: string(record.Profile)}
	status := Status{State: StateStopped, App: identity}
	return &CaptureService{open: open, publisher: NewPublisher(status), identity: identity, status: status}
}

// SessionOpener adapts capture.OpenSession with preloaded Master data.
func SessionOpener(masters *capture.MasterData) Opener {
	return func(cfg capture.SessionConfig) (Session, error) {
		session, err := capture.OpenSession(cfg, masters)
		if err != nil {
			return nil, err
		}
		return session, nil
	}
}

// Publisher returns the snapshot publisher.
func (s *CaptureService) Publisher() *Publisher {
	return s.publisher
}

// Status returns the current status.
func (s *CaptureService) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Start opens a session and runs it in the background. Opening happens before Start returns, so a missing adapter or
// a bad configuration is reported to the caller. A Start while another session is starting, running, or stopping
// returns ErrBusy and changes nothing.
func (s *CaptureService) Start(cfg capture.SessionConfig) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	switch s.status.State {
	case StateStarting, StateRunning, StateStopping:
		s.mu.Unlock()
		return ErrBusy
	}
	s.generation++
	generation := s.generation
	if s.publishInterval != 0 {
		cfg.PublishInterval = s.publishInterval
	}
	s.stopAsked = false
	s.view = nil // the new session's first snapshot replaces the old view; nothing is carried over
	s.status = Status{State: StateStarting, App: s.identity}
	done := make(chan struct{})
	s.done = done
	s.publishLocked()
	s.mu.Unlock()

	session, err := s.open(cfg)

	s.mu.Lock()
	if err != nil {
		s.status = Status{State: StateError, LastError: err.Error(), App: s.identity}
		s.publishLocked()
		close(done)
		s.mu.Unlock()
		return err
	}
	info := session.Info()
	if s.stopAsked {
		// Close may drain diagnostic writers; never hold the lifecycle lock while waiting.
		s.mu.Unlock()
		session.Close()
		s.mu.Lock()
		s.status = Status{State: StateStopped, Session: &info, App: s.identity,
			LastResult: &capture.SessionResult{StopReason: capture.StopReasonRequested}}
		s.publishLocked()
		close(done)
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	// A change that arrived while the session was opening still applies.
	if s.publishInterval != 0 {
		session.SetPublishInterval(s.publishInterval)
	}
	s.session = session
	s.status = Status{State: StateRunning, Session: &info, StartedAt: time.Now(), App: s.identity}
	s.publishLocked()
	s.mu.Unlock()
	go s.run(ctx, generation, session, done)
	return nil
}

func (s *CaptureService) run(ctx context.Context, generation uint64, session Session, done chan struct{}) {
	defer close(done)
	result, err := session.Run(ctx, func(view capture.ViewSnapshot) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if generation != s.generation {
			return
		}
		s.view = &view
		s.publishLocked()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return
	}
	s.cancel = nil
	s.session = nil
	status := s.status
	status.LastResult = &result
	if err != nil {
		status.State, status.LastError = StateError, err.Error()
	} else {
		status.State, status.LastError = StateStopped, ""
	}
	s.status = status
	s.publishLocked()
}

// SetPublishInterval sets the minimum wall time between published snapshots for the running session and every later
// one. It is the single control point for the UI data cadence: the capture goroutine still processes every packet, so
// statistics, encounters and effects are identical at any interval; only how often a view snapshot is built and
// published changes. Zero restores each session's configured value for later sessions.
func (s *CaptureService) SetPublishInterval(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishInterval = interval
	if s.session != nil && interval != 0 {
		s.session.SetPublishInterval(interval)
	}
}

// Stop asks the current session to stop and waits until it has released the capture handle, the diagnostic writers,
// and its goroutine, or until ctx ends (ErrStopTimeout; the session still finishes in the background). Stop with no
// session is a no-op, and concurrent Stops all wait for the same session.
func (s *CaptureService) Stop(ctx context.Context) error {
	s.mu.Lock()
	done := s.done
	switch s.status.State {
	case StateStarting:
		s.stopAsked = true
		s.status.State = StateStopping
		s.publishLocked()
	case StateRunning:
		s.status.State = StateStopping
		s.cancel()
		s.publishLocked()
	case StateStopping:
	default:
		if done == nil {
			s.mu.Unlock()
			return nil
		}
	}
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ErrStopTimeout
	}
}

// Shutdown rejects new starts, stops the session within ctx, and wakes all snapshot readers.
// On timeout diagnostic sessions remain unfinished, never falsely marked complete.
func (s *CaptureService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	err := s.Stop(ctx)
	s.publisher.Close()
	return err
}

// publishLocked publishes the current status with the current view. The caller holds s.mu.
func (s *CaptureService) publishLocked() {
	status := s.status
	if status.Session != nil {
		info := *status.Session
		status.Session = &info
	}
	if status.LastResult != nil {
		result := *status.LastResult
		status.LastResult = &result
	}
	s.publisher.publish(status, s.view)
}
