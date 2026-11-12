package capture

import "time"

// ViewSessionHealth is observable evidence about the game protocol session. It never carries key material, frame
// contents, or identifiers; it only says whether a key is usable and how recently frames arrived. It does not affect
// decoding, statistics, or attribution.
type ViewSessionHealth struct {
	// KeyReady means the latest handshake produced a session key. It says nothing about whether the session is still
	// alive: use FrameIdleSeconds for that.
	KeyReady            bool `json:"keyReady"`
	Handshakes          int  `json:"handshakes"`
	HandshakeErrors     int  `json:"handshakeErrors"`
	LastHandshakeFailed bool `json:"lastHandshakeFailed"`
	// FramesObserved counts CRC-valid game frames in this capture session, decryptable or not.
	FramesObserved uint64 `json:"framesObserved"`
	// ConsecutiveDecryptFailures resets on any successful decrypt or new handshake.
	ConsecutiveDecryptFailures int `json:"consecutiveDecryptFailures"`
	// FirstFrameAgeSeconds and FrameIdleSeconds are measured on the capture clock; nil before the first frame.
	FirstFrameAgeSeconds *float64 `json:"firstFrameAgeSeconds,omitempty"`
	FrameIdleSeconds     *float64 `json:"frameIdleSeconds,omitempty"`
	// Protocol says whether the protocol session reported the current game version and is decoded (protocol_session.go).
	Protocol ViewProtocolHealth `json:"protocol"`
}

// ViewProtocolHealth is the protocol of the current protocol session: ProtocolStatusNone before any handshake,
// ProtocolStatusPending until the session reports its client version, then supported or unsupported. GameVersion is the
// major.minor the session reported; nothing else about the protocol is exposed.
type ViewProtocolHealth struct {
	Status      string `json:"status"`
	GameVersion string `json:"gameVersion,omitempty"`
}

type sessionHealthState struct {
	framesObserved             uint64
	firstFrameAt               time.Time
	lastFrameAt                time.Time
	lastHandshakeFailed        bool
	consecutiveDecryptFailures int
	clock                      time.Time
}

func (s *sessionHealthState) observeFrame(at time.Time) {
	s.framesObserved++
	if s.firstFrameAt.IsZero() {
		s.firstFrameAt = at
	}
	if at.After(s.lastFrameAt) {
		s.lastFrameAt = at
	}
	if at.After(s.clock) {
		s.clock = at
	}
}

// observeClock advances the clock that ages frames. Live capture passes wall time so silence is visible; a replay passes
// capture time so a recorded pause reads the same at any pacing.
func (s *sessionHealthState) observeClock(at time.Time) {
	if at.After(s.clock) {
		s.clock = at
	}
}

func (p *processor) sessionHealthSnapshot() ViewSessionHealth {
	s := &p.sessionHealth
	view := ViewSessionHealth{
		KeyReady:                   len(p.session) == 32 && !s.lastHandshakeFailed,
		Handshakes:                 p.stats.handshakeOK,
		HandshakeErrors:            p.stats.handshakeErrors,
		LastHandshakeFailed:        s.lastHandshakeFailed,
		FramesObserved:             s.framesObserved,
		ConsecutiveDecryptFailures: s.consecutiveDecryptFailures,
	}
	view.Protocol.Status, view.Protocol.GameVersion = p.protocol.status()
	if !s.firstFrameAt.IsZero() {
		first := max(s.clock.Sub(s.firstFrameAt).Seconds(), 0)
		idle := max(s.clock.Sub(s.lastFrameAt).Seconds(), 0)
		view.FirstFrameAgeSeconds, view.FrameIdleSeconds = &first, &idle
	}
	return view
}
