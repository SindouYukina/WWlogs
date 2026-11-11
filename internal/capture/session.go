package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// A Session is one capture session for an application host: one Npcap handle (or one offline PCAP), one processor,
// and one statistics owner, so a new session never shares statistics, encounters, or effects with an earlier one.
// It reuses the decoder, transport recovery, and statistics engine unchanged; it adds only lifecycle and snapshot
// publishing. A Session is used from one goroutine: OpenSession, then Run once (or Close without running).

// SessionConfig selects the packet source and the session options.
type SessionConfig struct {
	// LocalIPv4 selects the Npcap adapter for live capture and decides packet direction in both modes.
	LocalIPv4 string
	// ReplayPath replays an offline PCAP instead of capturing live.
	ReplayPath string
	// ReplayPacing replays at capture speed times this factor; 0 replays as fast as possible.
	ReplayPacing float64
	// MinPort and MaxPort bound the game server UDP ports; zero selects the defaults.
	MinPort, MaxPort int
	// PublishInterval is the initial minimum wall time between published snapshots; zero selects
	// DefaultPublishInterval. SetPublishInterval changes it while the session runs. It never affects packet handling.
	PublishInterval time.Duration
	// Diagnostics writes the Development diagnostic PCAP for a live session. Public builds reject it.
	Diagnostics bool
	// History receives the session's Combat History journal (history_journal.go); nil keeps no history. It never affects
	// packet handling, statistics or snapshots.
	History HistorySink
}

// Session defaults and bounds.
const (
	DefaultMinPort         = 13100
	DefaultMaxPort         = 13120
	DefaultPublishInterval = 250 * time.Millisecond
	// MinPublishInterval caps the snapshot rate at 10 per second.
	MinPublishInterval = 100 * time.Millisecond
	// sessionReadFailureLimit ends a live session whose adapter keeps failing instead of retrying forever.
	sessionReadFailureLimit = 5 * time.Second
)

// Session stop reasons.
const (
	StopReasonRequested = "requested"
	StopReasonReplayEnd = "replay_end"
	StopReasonError     = "error"
)

// SessionInfo describes an opened session.
type SessionInfo struct {
	CaptureSessionID uint64 `json:"captureSessionId"`
	Source           string `json:"source"`
	LocalIPv4        string `json:"localIPv4"`
	// Handle is the applied Npcap configuration for live capture.
	Handle string `json:"handle,omitempty"`
	// Diagnostics reports whether Development diagnostics are recorded for this session.
	Diagnostics bool `json:"diagnostics"`
	// DiagnosticsWarning reports a failed diagnostic setup without stopping combat analysis.
	DiagnosticsWarning string `json:"diagnosticsWarning,omitempty"`
}

// SessionResult is how a session ended.
type SessionResult struct {
	StopReason string `json:"stopReason"`
	UDPPackets int    `json:"udpPackets"`
	// DiagnosticsSummary is the Development diagnostic outcome (PCAP integrity state); empty otherwise.
	DiagnosticsSummary string `json:"diagnosticsSummary,omitempty"`
	// HistoryIncomplete reports that the session's Combat History could not be stored completely before the capture ended.
	HistoryIncomplete bool `json:"historyIncomplete,omitempty"`
}

// MasterData is a loaded, read-only Master database shared by sessions.
type MasterData struct {
	masters *gameMaster
	display *DisplayResolver
}

// LoadMasterData loads the combat Master database for locale and retains its one read-only connection for display lookups.
func LoadMasterData(path, locale string) (*MasterData, error) {
	if _, ok := supportedMasterLocales[locale]; !ok {
		return nil, fmt.Errorf("unsupported master locale %q", locale)
	}
	db, databasePath, hash, err := openMasterDatabase(path)
	if err != nil {
		return nil, err
	}
	masters, err := loadMasterDataFromDatabase(db, databasePath, hash, locale)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	display, err := newDisplayResolver(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &MasterData{masters: masters, display: display}, nil
}

// DisplayResolver returns the locale-aware query resolver sharing this MasterData's SQLite connection.
func (m *MasterData) DisplayResolver() *DisplayResolver {
	if m == nil {
		return nil
	}
	return m.display
}

// SHA256 is the SHA-256 of the loaded Master database bytes.
func (m *MasterData) SHA256() string {
	if m == nil || m.masters == nil {
		return ""
	}
	return m.masters.manifest.SourceHash
}

// Close releases the read-only Master database shared by this data set and its display resolver.
func (m *MasterData) Close() error {
	if m == nil || m.display == nil {
		return nil
	}
	return m.display.Close()
}

// Session is an opened capture session.
type Session struct {
	cfg         SessionConfig
	handle      *pcap.Handle
	processor   *processor
	diagnostics *sessionDiagnostics
	info        SessionInfo
	used        bool
	// publishInterval is read by the Run goroutine before every publish decision and written by SetPublishInterval.
	publishInterval atomic.Int64
}

// normalizePublishInterval maps zero to the default and caps the snapshot rate at MinPublishInterval.
func normalizePublishInterval(interval time.Duration) time.Duration {
	if interval == 0 {
		return DefaultPublishInterval
	}
	return max(interval, MinPublishInterval)
}

// OpenSession validates cfg, opens the packet source, and prepares a fresh processor. All resources it opened are
// released when it fails.
func OpenSession(cfg SessionConfig, masters *MasterData) (*Session, error) {
	if masters == nil || masters.masters == nil {
		return nil, errors.New("master data is not loaded")
	}
	if cfg.MinPort == 0 && cfg.MaxPort == 0 {
		cfg.MinPort, cfg.MaxPort = DefaultMinPort, DefaultMaxPort
	}
	if cfg.MinPort < 1 || cfg.MaxPort > 65535 || cfg.MinPort > cfg.MaxPort {
		return nil, fmt.Errorf("invalid port range %d-%d", cfg.MinPort, cfg.MaxPort)
	}
	cfg.PublishInterval = normalizePublishInterval(cfg.PublishInterval)
	if cfg.ReplayPacing < 0 {
		return nil, fmt.Errorf("invalid replay pacing %g", cfg.ReplayPacing)
	}
	live := cfg.ReplayPath == ""
	if live && cfg.LocalIPv4 == "" {
		return nil, errors.New("a local IPv4 address is required for live capture")
	}
	if cfg.Diagnostics && !live {
		return nil, errors.New("development diagnostics apply to live capture only")
	}
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("load embedded key: %w", err)
	}
	session := &Session{cfg: cfg, info: SessionInfo{LocalIPv4: cfg.LocalIPv4, Diagnostics: cfg.Diagnostics}}
	session.publishInterval.Store(int64(cfg.PublishInterval))
	if live {
		device, err := findDeviceByIP(cfg.LocalIPv4)
		if err != nil {
			return nil, fmt.Errorf("find Npcap adapter: %w", err)
		}
		handle, applied, err := openLiveCapture(device, defaultLiveCaptureConfig())
		if err != nil {
			return nil, fmt.Errorf("open Npcap adapter: %w", err)
		}
		session.handle, session.info.Source, session.info.Handle = handle, "live", applied.String()
	} else {
		handle, err := pcap.OpenOffline(cfg.ReplayPath)
		if err != nil {
			return nil, fmt.Errorf("open replay PCAP: %w", err)
		}
		session.handle, session.info.Source = handle, "replay"
	}
	if err := session.handle.SetBPFFilter(fmt.Sprintf("udp and portrange %d-%d", cfg.MinPort, cfg.MaxPort)); err != nil {
		session.handle.Close()
		return nil, fmt.Errorf("set capture filter: %w", err)
	}
	session.diagnostics, err = openSessionDiagnostics(cfg.Diagnostics, session.handle.LinkType())
	if err != nil {
		var setupError *diagnosticSetupError
		if !errors.As(err, &setupError) {
			session.handle.Close()
			return nil, err
		}
		session.info.Diagnostics = false
		session.info.DiagnosticsWarning = "Development diagnostics are unavailable; this session cannot be replayed."
		session.diagnostics, _ = openSessionDiagnostics(false, session.handle.LinkType())
	}
	p := newProcessor(key, io.Discard, io.Discard, nil)
	p.masters = masters.masters
	// A host shows one protocol session at a time: a new game handshake starts a new statistics interval.
	p.intervalPerProtocolSession = true
	p.observeCaptureSource(live)
	session.diagnostics.attach(p)
	if cfg.History != nil {
		p.damageStatistics.EnableHistory(cfg.History.Level())
	}
	session.processor = p
	session.info.CaptureSessionID = p.damageStatistics.Snapshot().CaptureSessionID
	return session, nil
}

// SetPublishInterval changes the minimum wall time between published snapshots of a running or unopened session. It is
// safe to call from any goroutine. The next publish decision uses it, so a shorter interval applies at the next packet
// or read timeout. Only the UI snapshot cadence changes: every packet is still processed, and the statistics, the
// timeline bins and effect lifecycles do not depend on when snapshots are taken.
func (s *Session) SetPublishInterval(interval time.Duration) {
	s.publishInterval.Store(int64(normalizePublishInterval(interval)))
}

// Info describes the session.
func (s *Session) Info() SessionInfo {
	return s.info
}

// Close releases a session that will not be run. Run releases its own resources.
func (s *Session) Close() {
	if s == nil || s.used {
		return
	}
	s.used = true
	s.diagnostics.finish(StopReasonRequested, CaptureCounter{})
	s.handle.Close()
}

// Run captures until ctx is cancelled, the replay ends, or the source fails. publish receives immutable snapshots on
// this goroutine at most once per publish interval, plus one final snapshot after the capture finished; it must
// return promptly. Run always releases the handle and the diagnostic writers before it returns.
func (s *Session) Run(ctx context.Context, publish func(ViewSnapshot)) (result SessionResult, err error) {
	if s.used {
		return SessionResult{}, errors.New("session already used")
	}
	s.used = true
	p, handle := s.processor, s.handle
	live := s.info.Source == "live"
	streams := make(map[string]*stream)
	stats := captureStats{}
	result.StopReason = StopReasonReplayEnd
	defer func() {
		// A decoder panic must not take down the host: report it and still release every resource.
		if recovered := recover(); recovered != nil {
			result.StopReason, err = StopReasonError, fmt.Errorf("capture session failed: %v", recovered)
		}
		result.UDPPackets = stats.udpPackets
		result.DiagnosticsSummary = s.diagnostics.finish(result.StopReason, p.captureLayerHealth().KernelDropped)
		handle.Close()
	}()

	// handOffHistory passes what the engine journaled to the history sink. The sink only queues, so this never waits.
	handOffHistory := func() {
		if s.cfg.History == nil {
			return
		}
		if entries, dropped := p.damageStatistics.TakeHistory(); len(entries) > 0 || dropped > 0 {
			s.cfg.History.Submit(entries, dropped)
		}
	}
	lastPublish := time.Time{}
	var viewClock liveViewClock
	maybePublish := func(now time.Time) {
		if publish != nil && (lastPublish.IsZero() || now.Sub(lastPublish) >= time.Duration(s.publishInterval.Load())) {
			lastPublish = now
			if live {
				p.viewClockLead, p.viewClockStalled = viewClock.read(now, p.currentFrameTime)
			}
			publish(p.viewSnapshot())
			s.diagnostics.observeEffectDisplay(p, now)
		}
	}
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	var replayStart time.Time
	var firstCaptured time.Time
	var failingSince time.Time
	maybePublish(time.Now())
loop:
	for {
		select {
		case <-ctx.Done():
			result.StopReason = StopReasonRequested
			break loop
		default:
		}
		packet, readErr := packetSource.NextPacket()
		if readErr != nil {
			switch {
			case readErr == pcap.NextErrorTimeoutExpired:
				failingSince = time.Time{}
				p.sampleCaptureStats(handle, time.Now(), false)
				if live {
					p.sessionHealth.observeClock(time.Now())
				}
				maybePublish(time.Now())
				continue
			case !live && readErr == io.EOF:
				break loop
			case live:
				if failingSince.IsZero() {
					failingSince = time.Now()
				} else if time.Since(failingSince) > sessionReadFailureLimit {
					result.StopReason, err = StopReasonError, fmt.Errorf("read from Npcap adapter: %w", readErr)
					break loop
				}
				time.Sleep(10 * time.Millisecond)
				continue
			default:
				// An unreadable offline record is skipped, as the CLI replay does.
				continue
			}
		}
		failingSince = time.Time{}
		capturedAt := packet.Metadata().CaptureInfo.Timestamp
		if !live && s.cfg.ReplayPacing > 0 {
			if replayStart.IsZero() {
				replayStart, firstCaptured = time.Now(), capturedAt
			} else if !waitUntil(ctx, replayStart.Add(time.Duration(float64(capturedAt.Sub(firstCaptured))/s.cfg.ReplayPacing))) {
				result.StopReason = StopReasonRequested
				break loop
			}
		}
		s.diagnostics.packet(packet.Metadata().CaptureInfo, packet.Data())
		receivedAt := time.Now()
		p.observePacketRead()
		p.sampleCaptureStats(handle, receivedAt, false)
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		stats.udpPackets++
		direction := packetDirection(packet, s.cfg.LocalIPv4, udp, s.cfg.MinPort, s.cfg.MaxPort)
		consumeDatagram(udp.Payload, direction, capturedAt, streams, p, &stats)
		handOffHistory()
		if live {
			viewClock.observeFrame(p.currentFrameTime, receivedAt)
			p.observePacketTiming(capturedAt, receivedAt, time.Now())
			p.sessionHealth.observeClock(time.Now())
		} else {
			p.sessionHealth.observeClock(capturedAt)
		}
		maybePublish(time.Now())
	}
	finishTransport(streams, p, &stats)
	p.sampleCaptureStats(handle, time.Now(), true)
	p.finishCapture()
	handOffHistory()
	if publish != nil {
		p.viewClockLead, p.viewClockStalled = 0, false
		final := p.viewSnapshot()
		final.Final = true
		publish(final)
	}
	return result, err
}

// LiveFrameSilenceLimit is how long a live capture may see no game frame at all before it no longer knows what the game is
// doing. A quiet fight is not silence: the game keeps sending frames while nothing happens, and in the 24 valid archived
// captures 96 stretches of 10 s to over 60 s without a combat event never lasted longer than this between frames, except one
// 39.6 s gap before a new handshake. Across those captures the longest gap of an established session is 5.98 s. The listening
// status uses the same limit for its stale state.
const LiveFrameSilenceLimit = 8 * time.Second

// liveClockStepLimit separates a frame reassembled late from a capture clock that was set back. The largest backward step
// between frames in the archived captures is 15.6 ms.
const liveClockStepLimit = 5 * time.Second

// liveViewClock estimates the game clock of a live session between frames, so a quiet connection does not freeze every
// countdown until its next frame. It runs only on a live session: a replay reads exactly at capture time, so the same
// capture gives the same view at any speed, and a recorded silence reads the same however long it took to replay.
type liveViewClock struct {
	newestFrame time.Time // the newest frame's capture time
	newestWall  time.Time // when that frame was received
	highWater   time.Time // the latest query time handed out; time read from this clock never runs backwards
}

// observeFrame records a decoded frame. A frame with an older capture time, such as one reassembled late, does not move
// the clock. A large backward step means the capture clock itself was set back, so the clock starts over from it.
func (c *liveViewClock) observeFrame(frame, wall time.Time) {
	if frame.IsZero() {
		return
	}
	switch {
	case frame.After(c.newestFrame):
		c.newestFrame, c.newestWall = frame, wall
	case c.newestFrame.Sub(frame) > liveClockStepLimit:
		c.newestFrame, c.newestWall, c.highWater = frame, wall, time.Time{}
	}
}

// read returns how far a publish at now reads the Effects view past frame, the processor's current frame time, and whether
// the clock has stalled. The reading never passes LiveFrameSilenceLimit beyond the newest frame.
func (c *liveViewClock) read(now, frame time.Time) (lead time.Duration, stalled bool) {
	if c.newestWall.IsZero() || frame.IsZero() {
		return 0, false
	}
	silence := max(now.Sub(c.newestWall), 0)
	query := c.newestFrame.Add(min(silence, LiveFrameSilenceLimit))
	if query.Before(c.highWater) {
		query = c.highWater
	}
	c.highWater = query
	return query.Sub(frame), silence >= LiveFrameSilenceLimit
}

// waitUntil sleeps until deadline; it returns false when ctx ends first.
func waitUntil(ctx context.Context, deadline time.Time) bool {
	wait := time.Until(deadline)
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
