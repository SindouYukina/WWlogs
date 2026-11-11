package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

// launchSession stands in for a live session: it runs until stopped.
type launchSession struct{ closes int }

func (s *launchSession) Info() capture.SessionInfo {
	return capture.SessionInfo{CaptureSessionID: 7, Source: "live"}
}
func (s *launchSession) Close()                           { s.closes++ }
func (s *launchSession) SetPublishInterval(time.Duration) {}
func (s *launchSession) SetHistoryRecording(bool)         {}
func (s *launchSession) HistoryRecording() capture.HistoryRecordingState {
	return capture.HistoryRecordingState{}
}
func (s *launchSession) Run(ctx context.Context, publish func(capture.ViewSnapshot)) (capture.SessionResult, error) {
	publish(capture.ViewSnapshot{CaptureSessionID: 7})
	<-ctx.Done()
	return capture.SessionResult{StopReason: capture.StopReasonRequested}, nil
}

// launchFixture is a Bridge with a real CaptureService and settings store, and a scripted environment check.
type launchFixture struct {
	bridge *Bridge
	path   string
	mu     sync.Mutex
	opened []capture.SessionConfig
	fail   error
}

func newLaunchFixture(t *testing.T, settings string, probe func() capture.CaptureEnvironment) *launchFixture {
	t.Helper()
	fixture := &launchFixture{path: filepath.Join(t.TempDir(), "WWlogs", "settings.json")}
	if settings != "" {
		if err := os.MkdirAll(filepath.Dir(fixture.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.path, []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opener := func(cfg capture.SessionConfig) (service.Session, error) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.opened = append(fixture.opened, cfg)
		if fixture.fail != nil {
			return nil, fixture.fail
		}
		return &launchSession{}, nil
	}
	fixture.bridge = &Bridge{
		capture:     service.NewCaptureService(opener),
		defaults:    capture.SessionConfig{Diagnostics: true},
		settings:    loadSettings(fixture.path, testLocaleCatalog()),
		environment: newEnvironmentChecker(probe),
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = fixture.bridge.capture.Shutdown(ctx)
	})
	return fixture
}

func (f *launchFixture) starts() []capture.SessionConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capture.SessionConfig(nil), f.opened...)
}

func readyWith(addresses ...string) func() capture.CaptureEnvironment {
	return func() capture.CaptureEnvironment {
		environment := capture.CaptureEnvironment{State: capture.EnvironmentReady, Adapters: []capture.Adapter{}, InterfaceCount: len(addresses)}
		for _, address := range addresses {
			environment.Adapters = append(environment.Adapters, capture.Adapter{IPv4: address, Description: "test"})
		}
		return environment
	}
}

const autoStartOn = `{"version":1,"locale":"en","defaultAdapterIPv4":"192.0.2.10","autoStartCapture":true}`

func TestAutoStartCaptureDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if store.current().Settings.AutoStartCapture {
		t.Fatal("first run starts capture by itself")
	}
	// A file from before this preference existed has no value and stays off.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"locale":"ja","defaultAdapterIPv4":"192.0.2.10","theme":"havoc","updateIntervalMs":500}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store = loadSettings(path, testLocaleCatalog())
	if state := store.current(); state.Warning != "" || state.Settings.AutoStartCapture {
		t.Fatalf("older file: %+v", state)
	}
	if _, err := store.setAutoStartCapture(true); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog()).current().Settings
	if !reloaded.AutoStartCapture || reloaded.DefaultAdapterIPv4 != "192.0.2.10" || reloaded.Locale != "ja" || reloaded.Theme != "havoc" || reloaded.UpdateIntervalMs != 500 {
		t.Fatalf("auto start not persisted beside the other preferences: %+v", reloaded)
	}
	// Other preferences have their own updates and must not undo it.
	if state, err := store.setDefaultAdapter("192.0.2.11"); err != nil || !state.Settings.AutoStartCapture {
		t.Fatalf("adapter update: %+v %v", state, err)
	}
	if _, err := store.setAutoStartCapture(false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "autoStartCapture") {
		t.Fatalf("off is the default and is not written: %s", body)
	}
}

func TestLaunchStartRunsOnceOnTheSavedInterface(t *testing.T) {
	fixture := newLaunchFixture(t, autoStartOn, readyWith("192.0.2.9", "192.0.2.10"))
	outcome, err := fixture.bridge.startAtLaunch(context.Background())
	if err != nil || outcome != startupStarted {
		t.Fatalf("launch start: %s %v", outcome, err)
	}
	starts := fixture.starts()
	if len(starts) != 1 || starts[0].LocalIPv4 != "192.0.2.10" || !starts[0].Diagnostics {
		t.Fatalf("the launch start must use the shared start path on the saved interface: %+v", starts)
	}
	status := fixture.bridge.capture.Status()
	if status.State != service.StateRunning || status.Session == nil || status.Session.CaptureSessionID != 7 {
		t.Fatalf("status after the launch start: %+v", status)
	}
	// It belongs to the launch only: running it again starts nothing.
	if outcome, _ := fixture.bridge.startAtLaunch(context.Background()); outcome != startupUserTookOver || len(fixture.starts()) != 1 {
		t.Fatalf("second launch start: %s, %d starts", outcome, len(fixture.starts()))
	}
	// A manual stop stays stopped.
	if err := fixture.bridge.StopCapture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := fixture.bridge.startAtLaunch(context.Background()); outcome != startupUserTookOver {
		t.Fatalf("launch start after a manual stop: %s", outcome)
	}
	time.Sleep(50 * time.Millisecond)
	if state := fixture.bridge.capture.Status().State; state != service.StateStopped || len(fixture.starts()) != 1 {
		t.Fatalf("capture restarted after a manual stop: %s, %d starts", state, len(fixture.starts()))
	}
	// A manual start afterwards opens a new session as usual.
	if err := fixture.bridge.StartCapture(StartRequest{LocalIPv4: "192.0.2.10"}); err != nil {
		t.Fatal(err)
	}
	if len(fixture.starts()) != 2 || fixture.bridge.capture.Status().State != service.StateRunning {
		t.Fatalf("manual start after the launch start: %d starts", len(fixture.starts()))
	}
}

func TestLaunchStartNeverGuessesAndNeverStartsWhenNotReady(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		probe    func() capture.CaptureEnvironment
		want     startupOutcome
	}{
		{"off by default", "", readyWith("192.0.2.10"), startupDisabled},
		{"off in the file", `{"version":1,"locale":"en","defaultAdapterIPv4":"192.0.2.10"}`, readyWith("192.0.2.10"), startupDisabled},
		{"no interface chosen", `{"version":1,"locale":"en","autoStartCapture":true}`, readyWith("192.0.2.10"), startupNoAdapter},
		{"saved interface gone", autoStartOn, readyWith("192.0.2.9", "192.0.2.11"), startupMissing},
		{"Npcap missing", autoStartOn, func() capture.CaptureEnvironment {
			return capture.CaptureEnvironment{State: capture.EnvironmentNpcapMissing, Adapters: []capture.Adapter{}}
		}, startupEnvironment},
		{"Npcap unavailable", autoStartOn, func() capture.CaptureEnvironment {
			return capture.CaptureEnvironment{State: capture.EnvironmentNpcapUnavailable, Adapters: []capture.Adapter{}}
		}, startupEnvironment},
		{"no IPv4", autoStartOn, func() capture.CaptureEnvironment {
			return capture.CaptureEnvironment{State: capture.EnvironmentNoIPv4, Adapters: []capture.Adapter{}}
		}, startupEnvironment},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLaunchFixture(t, test.settings, test.probe)
			outcome, err := fixture.bridge.startAtLaunch(context.Background())
			if err != nil || outcome != test.want {
				t.Fatalf("outcome = %s %v, want %s", outcome, err, test.want)
			}
			if starts := fixture.starts(); len(starts) != 0 {
				t.Fatalf("started a capture: %+v", starts)
			}
			if state := fixture.bridge.capture.Status().State; state != service.StateStopped {
				t.Fatalf("state = %s", state)
			}
			// The saved interface is never replaced by another one.
			if saved := fixture.bridge.settings.current().Settings.DefaultAdapterIPv4; test.settings == autoStartOn && saved != "192.0.2.10" {
				t.Fatalf("saved interface changed to %q", saved)
			}
			// The user can still start by hand.
			if test.want == startupMissing {
				if err := fixture.bridge.StartCapture(StartRequest{LocalIPv4: "192.0.2.9"}); err != nil || len(fixture.starts()) != 1 {
					t.Fatalf("manual start: %v", err)
				}
			}
		})
	}
}

func TestUserActionDuringTheCheckWins(t *testing.T) {
	for _, action := range []string{"stop", "start", "turn off"} {
		t.Run(action, func(t *testing.T) {
			release := make(chan struct{})
			entered := make(chan struct{})
			ready := readyWith("192.0.2.10")
			fixture := newLaunchFixture(t, autoStartOn, func() capture.CaptureEnvironment {
				close(entered)
				<-release
				return ready()
			})
			done := make(chan startupOutcome, 1)
			go func() {
				outcome, _ := fixture.bridge.startAtLaunch(context.Background())
				done <- outcome
			}()
			<-entered
			want := startupUserTookOver
			switch action {
			case "stop":
				if err := fixture.bridge.StopCapture(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "start":
				if err := fixture.bridge.StartCapture(StartRequest{LocalIPv4: "192.0.2.10"}); err != nil {
					t.Fatal(err)
				}
			case "turn off":
				if _, err := fixture.bridge.SetAutoStartCapture(false); err != nil {
					t.Fatal(err)
				}
				want = startupDisabled
			}
			close(release)
			if outcome := <-done; outcome != want {
				t.Fatalf("outcome = %s, want %s", outcome, want)
			}
			wantStarts := 0
			if action == "start" {
				wantStarts = 1
			}
			if starts := fixture.starts(); len(starts) != wantStarts {
				t.Fatalf("starts = %d, want %d (no duplicate session)", len(starts), wantStarts)
			}
		})
	}
}

func TestLaunchStartFailureIsReportedOnceWithoutRetry(t *testing.T) {
	fixture := newLaunchFixture(t, autoStartOn, readyWith("192.0.2.10"))
	fixture.fail = errors.New("open failed")
	outcome, err := fixture.bridge.startAtLaunch(context.Background())
	if outcome != startupStartFailed || err == nil {
		t.Fatalf("outcome = %s %v", outcome, err)
	}
	if status := fixture.bridge.capture.Status(); status.State != service.StateError || status.LastError == "" {
		t.Fatalf("a failed start must show as the existing capture error: %+v", status)
	}
	if outcome, _ := fixture.bridge.startAtLaunch(context.Background()); outcome != startupUserTookOver || len(fixture.starts()) != 1 {
		t.Fatalf("retried after a failure: %s, %d starts", outcome, len(fixture.starts()))
	}
}

func TestDevelopmentAutoStartFlagsOwnTheLaunch(t *testing.T) {
	// Run claims the launch start for -autostart and -replay before the application starts, so the saved preference
	// cannot add a second session to theirs.
	fixture := newLaunchFixture(t, autoStartOn, readyWith("192.0.2.10"))
	fixture.bridge.startup.claim()
	if outcome, _ := fixture.bridge.startAtLaunch(context.Background()); outcome != startupUserTookOver || len(fixture.starts()) != 0 {
		t.Fatalf("outcome = %s, %d starts", outcome, len(fixture.starts()))
	}
}
