package service

import (
	"strings"
	"sync"
	"time"

	"wwlogs/internal/capture"
)

// ListeningState answers "is WWlogs currently hearing the game?". WWlogs is a passive observer, so these states describe
// evidence seen on the wire and in the process list, never a connection to the game.
type ListeningState string

const (
	ListeningOff          ListeningState = "off"          // No capture is running.
	ListeningWaiting      ListeningState = "waiting"      // Capture works; no usable game session is confirmed.
	ListeningEstablishing ListeningState = "establishing" // Game frames just appeared and a key is expected.
	ListeningActive       ListeningState = "active"       // A key is usable and frames arrived recently.
	ListeningStale        ListeningState = "stale"        // A session was usable; recent evidence is missing.
	ListeningError        ListeningState = "error"        // Explicit capture, handshake, decrypt, or transport failure.
	ListeningClosed       ListeningState = "closed"       // The game process of this session has exited.
)

// ListeningReason is a stable, non-sensitive classification for the UI to translate.
type ListeningReason string

const (
	ReasonCaptureOff          ListeningReason = "capture_off"
	ReasonCaptureStarting     ListeningReason = "capture_starting"
	ReasonCaptureFailed       ListeningReason = "capture_failed"
	ReasonNoGameTraffic       ListeningReason = "no_game_traffic"
	ReasonGameNotRunning      ListeningReason = "game_not_running"
	ReasonHandshakeInProgress ListeningReason = "handshake_in_progress"
	ReasonTrafficWithoutKey   ListeningReason = "traffic_without_key"
	// ReasonAwaitingRelogin: the connection whose login the capture missed has gone quiet, so the game is back at its
	// login screen; the running capture takes the next login's handshake on its own.
	ReasonAwaitingRelogin ListeningReason = "awaiting_relogin"
	ReasonAwaitingLogin   ListeningReason = "awaiting_login"
	ReasonHandshakeFailed ListeningReason = "handshake_failed"
	// ReasonUnsupportedGameVersion: the game reported a version other than the current one this WWlogs build decodes, or
	// none it could read, so its combat frames are not decoded (capture.ProtocolStatusUnsupported).
	ReasonUnsupportedGameVersion ListeningReason = "unsupported_game_version"
	ReasonDecryptFailing         ListeningReason = "decrypt_failing"
	ReasonTransportUnrecovered   ListeningReason = "transport_unrecoverable"
	ReasonReceiving              ListeningReason = "receiving"
	ReasonTransportRecovering    ListeningReason = "transport_recovering"
	ReasonNoRecentFrames         ListeningReason = "no_recent_frames"
	ReasonGameProcessEnded       ListeningReason = "game_process_ended"
)

// Thresholds come from research/topics/output/listening-status.md, measured over 41 captures (2.36 hours).
const (
	// The longest pause between frames in any capture was 6.0 s and the highest per-capture p99 was 2.7 s.
	listeningStaleAfter = capture.LiveFrameSilenceLimit
	// A key response arrived within 0.02 s of the first frame in captures that began before login.
	listeningEstablishWindowSeconds = 5.0
	// No capture has ever shown a decrypt failure, so a run of them is explicit evidence of a wrong or lost key.
	listeningDecryptFailureLimit = 20
)

// ListeningStatus is what every window shows. It contains no key material, addresses, or process identifiers.
type ListeningStatus struct {
	State  ListeningState  `json:"state"`
	Reason ListeningReason `json:"reason"`
	// Since is when State last changed, for "for 12 s" style display.
	Since time.Time `json:"since"`
}

// GameProcess is one observation of the game's process list entry.
type GameProcess struct {
	// Supported is false when the observation cannot apply, such as a replay. Absence then proves nothing.
	Supported bool
	Running   bool
	PID       uint32
}

// ListeningMonitor derives ListeningStatus. Its only memory is which capture and protocol session the evidence belongs
// to, so an old session's success can never leak into a new one.
type ListeningMonitor struct {
	mu              sync.Mutex
	captureSession  uint64
	protocolSession int
	sessionPID      uint32
	state           ListeningState
	since           time.Time
}

// Evaluate classifies the current evidence. It is safe for concurrent use, and it is idempotent for the same input.
func (m *ListeningMonitor) Evaluate(status Status, view *capture.ViewSnapshot, process GameProcess, now time.Time) ListeningStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, reason := m.classify(status, view, process)
	if state != m.state || m.since.IsZero() {
		m.state, m.since = state, now
	}
	return ListeningStatus{State: state, Reason: reason, Since: m.since}
}

func (m *ListeningMonitor) classify(status Status, view *capture.ViewSnapshot, process GameProcess) (ListeningState, ListeningReason) {
	switch status.State {
	case StateError:
		return ListeningError, ReasonCaptureFailed
	case StateStarting:
		return ListeningWaiting, ReasonCaptureStarting
	case StateStopped, StateStopping:
		return ListeningOff, ReasonCaptureOff
	}
	if view == nil {
		return ListeningWaiting, ReasonCaptureStarting
	}
	if view.CaptureSessionID != m.captureSession {
		m.captureSession, m.protocolSession, m.sessionPID = view.CaptureSessionID, -1, 0
	}
	if view.ProtocolSession != m.protocolSession {
		// A new handshake belongs to whichever game process is running now.
		m.protocolSession, m.sessionPID = view.ProtocolSession, 0
	}
	if process.Supported && process.Running && m.sessionPID == 0 {
		m.sessionPID = process.PID
	}
	health := view.Health.Session
	if !health.KeyReady {
		switch {
		case health.LastHandshakeFailed:
			return ListeningError, ReasonHandshakeFailed
		case process.Supported && !process.Running:
			return ListeningWaiting, ReasonGameNotRunning
		case health.FramesObserved == 0 || health.FirstFrameAgeSeconds == nil:
			return ListeningWaiting, ReasonNoGameTraffic
		case *health.FirstFrameAgeSeconds < listeningEstablishWindowSeconds:
			return ListeningEstablishing, ReasonHandshakeInProgress
		case health.FrameIdleSeconds != nil && *health.FrameIdleSeconds >= listeningStaleAfter.Seconds():
			// The connection without a key went quiet: a logged-in game never pauses this long, so the player has left
			// it for the login screen. Nothing needs restarting; the next login brings a new handshake.
			return ListeningWaiting, ReasonAwaitingRelogin
		default:
			// Frames are visible but the login handshake was missed: capture began after this session logged in.
			return ListeningWaiting, ReasonTrafficWithoutKey
		}
	}
	if process.Supported && m.sessionPID != 0 {
		if !process.Running {
			// Normal exit and a crash look identical from here, so the status only says the game has closed.
			return ListeningClosed, ReasonGameProcessEnded
		}
		if process.PID != m.sessionPID {
			// A different process needs its own handshake; the previous key must not read as success.
			return ListeningWaiting, ReasonAwaitingLogin
		}
	}
	if health.ConsecutiveDecryptFailures >= listeningDecryptFailureLimit {
		return ListeningError, ReasonDecryptFailing
	}
	switch health.Protocol.Status {
	case capture.ProtocolStatusUnsupported:
		// Frames arrive and decrypt, but no decoder can read this game version: that must not look like listening.
		return ListeningError, ReasonUnsupportedGameVersion
	case capture.ProtocolStatusPending:
		// The login exchange is still in progress: the version report follows the key immediately.
		return ListeningEstablishing, ReasonHandshakeInProgress
	}
	transport := view.Health.Transport.State
	if transport == capture.TransportUnrecoverable {
		return ListeningError, ReasonTransportUnrecovered
	}
	recovering := transport == capture.TransportGapDetected || transport == capture.TransportRecovering
	if health.FrameIdleSeconds == nil || *health.FrameIdleSeconds >= listeningStaleAfter.Seconds() {
		if recovering {
			return ListeningStale, ReasonTransportRecovering
		}
		return ListeningStale, ReasonNoRecentFrames
	}
	if recovering {
		return ListeningActive, ReasonTransportRecovering
	}
	return ListeningActive, ReasonReceiving
}

// gameProcessName is the shipping client under Client/Binaries/Win64. The launcher (Wuthering Waves.exe) stays open or
// closes independently of a play session, so it is deliberately not considered.
const gameProcessName = "Client-Win64-Shipping.exe"

func isGameProcessName(name string) bool {
	return strings.EqualFold(name, gameProcessName)
}
