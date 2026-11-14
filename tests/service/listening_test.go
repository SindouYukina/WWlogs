package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func seconds(value float64) *float64 { return &value }

type listeningView struct {
	capture, protocol uint64
	keyReady          bool
	handshakeFailed   bool
	frames            uint64
	firstAge, idle    *float64
	decryptFailures   int
	transport         capture.TransportState
	protocolStatus    string
}

func (v listeningView) build() *capture.ViewSnapshot {
	view := &capture.ViewSnapshot{CaptureSessionID: v.capture, ProtocolSession: int(v.protocol)}
	view.Health.Session = capture.ViewSessionHealth{KeyReady: v.keyReady, LastHandshakeFailed: v.handshakeFailed, FramesObserved: v.frames, FirstFrameAgeSeconds: v.firstAge, FrameIdleSeconds: v.idle, ConsecutiveDecryptFailures: v.decryptFailures}
	view.Health.Session.Protocol.Status = v.protocolStatus
	view.Health.Transport.State = v.transport
	if view.Health.Transport.State == "" {
		view.Health.Transport.State = capture.TransportHealthy
	}
	return view
}

var (
	running  = Status{State: StateRunning}
	noOSInfo = GameProcess{}
	gameUp   = GameProcess{Supported: true, Running: true, PID: 100}
	gameDown = GameProcess{Supported: true}
)

func TestListeningClassification(t *testing.T) {
	active := listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1)}
	for _, test := range []struct {
		name    string
		status  Status
		view    listeningView
		process GameProcess
		state   ListeningState
		reason  ListeningReason
	}{
		{"capture stopped", Status{State: StateStopped}, listeningView{}, noOSInfo, ListeningOff, ReasonCaptureOff},
		{"capture failed", Status{State: StateError}, listeningView{}, noOSInfo, ListeningError, ReasonCaptureFailed},
		{"capture running, game closed", running, listeningView{capture: 1}, gameDown, ListeningWaiting, ReasonGameNotRunning},
		{"capture running, no traffic", running, listeningView{capture: 1}, gameUp, ListeningWaiting, ReasonNoGameTraffic},
		{"process unknown, no traffic", running, listeningView{capture: 1}, noOSInfo, ListeningWaiting, ReasonNoGameTraffic},
		{"first frames just arrived", running, listeningView{capture: 1, frames: 1, firstAge: seconds(0.01), idle: seconds(0)}, gameUp, ListeningEstablishing, ReasonHandshakeInProgress},
		{"capture began after login", running, listeningView{capture: 1, frames: 80, firstAge: seconds(30), idle: seconds(1)}, gameUp, ListeningWaiting, ReasonTrafficWithoutKey},
		{"missed connection still busy just under the limit", running, listeningView{capture: 1, frames: 80, firstAge: seconds(30), idle: seconds(capture.LiveFrameSilenceLimit.Seconds() - 0.01)}, gameUp, ListeningWaiting, ReasonTrafficWithoutKey},
		{"missed connection went quiet: back at the login screen", running, listeningView{capture: 1, frames: 80, firstAge: seconds(73), idle: seconds(capture.LiveFrameSilenceLimit.Seconds())}, gameUp, ListeningWaiting, ReasonAwaitingRelogin},
		{"game closed after a missed login", running, listeningView{capture: 1, frames: 80, firstAge: seconds(73), idle: seconds(30)}, gameDown, ListeningWaiting, ReasonGameNotRunning},
		{"handshake failed", running, listeningView{capture: 1, frames: 1, handshakeFailed: true, firstAge: seconds(1), idle: seconds(0)}, gameUp, ListeningError, ReasonHandshakeFailed},
		{"normal parsing", running, active, gameUp, ListeningActive, ReasonReceiving},
		{"process list unavailable while parsing", running, active, noOSInfo, ListeningActive, ReasonReceiving},
		{"short idle is still active", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(6)}, gameUp, ListeningActive, ReasonReceiving},
		{"silence with no process info is stale, never closed", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(600), idle: seconds(300)}, noOSInfo, ListeningStale, ReasonNoRecentFrames},
		{"silence while the game runs is stale", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(600), idle: seconds(30)}, gameUp, ListeningStale, ReasonNoRecentFrames},
		{"silence just under the limit is still active", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(600), idle: seconds(capture.LiveFrameSilenceLimit.Seconds() - 0.01)}, gameUp, ListeningActive, ReasonReceiving},
		{"silence at the limit is stale, the same moment the Effects clock stalls", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(600), idle: seconds(capture.LiveFrameSilenceLimit.Seconds())}, gameUp, ListeningStale, ReasonNoRecentFrames},
		{"gap while frames flow stays active", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1), transport: capture.TransportRecovering}, gameUp, ListeningActive, ReasonTransportRecovering},
		{"gap with silence is not a closed game", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(20), transport: capture.TransportGapDetected}, gameUp, ListeningStale, ReasonTransportRecovering},
		{"recovery exhausted", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1), transport: capture.TransportUnrecoverable}, gameUp, ListeningError, ReasonTransportUnrecovered},
		{"a run of decrypt failures", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1), decryptFailures: 20}, gameUp, ListeningError, ReasonDecryptFailing},
		{"isolated decrypt failures do not alarm", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1), decryptFailures: 3}, gameUp, ListeningActive, ReasonReceiving},
		{"a supported game version parses", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1), protocolStatus: capture.ProtocolStatusSupported}, gameUp, ListeningActive, ReasonReceiving},
		{"an unsupported game version is an error, not listening", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1), protocolStatus: capture.ProtocolStatusUnsupported}, gameUp, ListeningError, ReasonUnsupportedGameVersion},
		{"the version report after the key is still establishing", running, listeningView{capture: 1, protocol: 1, keyReady: true, frames: 2, firstAge: seconds(60), idle: seconds(0), protocolStatus: capture.ProtocolStatusPending}, gameUp, ListeningEstablishing, ReasonHandshakeInProgress},
	} {
		t.Run(test.name, func(t *testing.T) {
			var monitor ListeningMonitor
			got := monitor.Evaluate(test.status, test.view.build(), test.process, time.Now())
			if got.State != test.state || got.Reason != test.reason {
				t.Fatalf("got %s/%s, want %s/%s", got.State, got.Reason, test.state, test.reason)
			}
		})
	}
}

func TestGameExitClosesOnlyTheSessionItBelongsTo(t *testing.T) {
	var monitor ListeningMonitor
	now := time.Now()
	live := listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1)}
	if got := monitor.Evaluate(running, live.build(), gameUp, now); got.State != ListeningActive {
		t.Fatalf("live: %+v", got)
	}
	if got := monitor.Evaluate(running, live.build(), gameDown, now); got.State != ListeningClosed || got.Reason != ReasonGameProcessEnded {
		t.Fatalf("exit: %+v", got)
	}
	// The same key must not read as success when a different process appears without its own handshake.
	relaunched := GameProcess{Supported: true, Running: true, PID: 200}
	if got := monitor.Evaluate(running, live.build(), relaunched, now); got.State != ListeningWaiting || got.Reason != ReasonAwaitingLogin {
		t.Fatalf("relaunch: %+v", got)
	}
	// A new handshake re-establishes the status, without any carried-over closure.
	next := live
	next.protocol = 2
	if got := monitor.Evaluate(running, next.build(), relaunched, now); got.State != ListeningActive {
		t.Fatalf("new session: %+v", got)
	}
}

func TestProcessThatWasNeverSeenCannotClose(t *testing.T) {
	var monitor ListeningMonitor
	live := listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1)}
	if got := monitor.Evaluate(running, live.build(), gameDown, time.Now()); got.State == ListeningClosed {
		t.Fatalf("closure without an observed process: %+v", got)
	}
}

func TestRecoveryReturnsToActiveAndNewCaptureForgetsTheOldSession(t *testing.T) {
	var monitor ListeningMonitor
	now := time.Now()
	view := listeningView{capture: 1, protocol: 1, keyReady: true, frames: 500, firstAge: seconds(60), idle: seconds(1)}
	monitor.Evaluate(running, view.build(), gameUp, now)
	stalled := view
	stalled.idle = seconds(60)
	first := monitor.Evaluate(running, stalled.build(), gameUp, now.Add(time.Minute))
	if first.State != ListeningStale || !first.Since.Equal(now.Add(time.Minute)) {
		t.Fatalf("stale: %+v", first)
	}
	if again := monitor.Evaluate(running, stalled.build(), gameUp, now.Add(2*time.Minute)); !again.Since.Equal(first.Since) {
		t.Fatalf("since moved without a state change: %+v", again)
	}
	if got := monitor.Evaluate(running, view.build(), gameUp, now.Add(3*time.Minute)); got.State != ListeningActive {
		t.Fatalf("recovered: %+v", got)
	}
	fresh := listeningView{capture: 2}
	if got := monitor.Evaluate(running, fresh.build(), GameProcess{Supported: true, Running: true, PID: 100}, now.Add(4*time.Minute)); got.State != ListeningWaiting {
		t.Fatalf("new capture inherited success: %+v", got)
	}
}

// A harmless system program renamed to the game's process name exercises the real process list query.
func TestFindGameProcessSeesAStartAndAnExit(t *testing.T) {
	if findGameProcess().Running {
		t.Skip("the game is running on this machine")
	}
	target := filepath.Join(t.TempDir(), gameProcessName)
	source, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Skipf("no system program to copy: %v", err)
	}
	if err := os.WriteFile(target, source, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(target, "-n", "30", "127.0.0.1")
	if err := command.Start(); err != nil {
		t.Skipf("cannot start a copied system program: %v", err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	seen := findGameProcess()
	if !seen.Supported || !seen.Running || seen.PID != uint32(command.Process.Pid) {
		t.Fatalf("running: %+v, pid %d", seen, command.Process.Pid)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	if gone := findGameProcess(); !gone.Supported || gone.Running {
		t.Fatalf("exited: %+v", gone)
	}
}

func TestGameProcessNameIsTheClientNotTheLauncher(t *testing.T) {
	if !isGameProcessName("client-win64-shipping.EXE") {
		t.Fatal("process names are case-insensitive on Windows")
	}
	if isGameProcessName("Wuthering Waves.exe") {
		t.Fatal("the launcher is not the game")
	}
}

func TestListeningStatusCarriesNoSensitiveFields(t *testing.T) {
	var monitor ListeningMonitor
	status := monitor.Evaluate(Status{State: StateStopped}, nil, GameProcess{}, time.Now())
	if status.State != ListeningOff {
		t.Fatalf("off: %+v", status)
	}
	// ListeningStatus is State, Reason and Since by construction; a process ID or key field would fail this literal.
	_ = ListeningStatus{State: status.State, Reason: status.Reason, Since: status.Since}
}
