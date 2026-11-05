package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// PublishInterval is the minimum wall time between published snapshots; zero selects DefaultPublishInterval.
	PublishInterval time.Duration
	// Diagnostics writes the Development diagnostic PCAP for a live session. Public builds reject it.
	Diagnostics bool
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
}

// MasterData is a loaded, read-only Master database shared by sessions.
type MasterData struct {
	masters *gameMaster
}

// LoadMasterData loads the combat Master database for locale.
func LoadMasterData(path, locale string) (*MasterData, error) {
	masters, err := loadGameMasterDatabase(path, locale)
	if err != nil {
		return nil, err
	}
	return &MasterData{masters: masters}, nil
}

// DefaultMasterDatabasePath resolves data/master_data.db next to the executable or the working directory.
func DefaultMasterDatabasePath() string {
	return defaultMasterDatabasePath()
}

// Session is an opened capture session.
type Session struct {
	cfg         SessionConfig
	handle      *pcap.Handle
	processor   *processor
	diagnostics *sessionDiagnostics
	info        SessionInfo
	used        bool
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
	if cfg.PublishInterval == 0 {
		cfg.PublishInterval = DefaultPublishInterval
	}
	if cfg.PublishInterval < MinPublishInterval {
		cfg.PublishInterval = MinPublishInterval
	}
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
	session.processor = p
	session.info.CaptureSessionID = p.damageStatistics.Snapshot().CaptureSessionID
	return session, nil
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

	lastPublish := time.Time{}
	maybePublish := func(now time.Time) {
		if publish != nil && (lastPublish.IsZero() || now.Sub(lastPublish) >= s.cfg.PublishInterval) {
			lastPublish = now
			publish(p.viewSnapshot())
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
		if live {
			p.observePacketTiming(capturedAt, receivedAt, time.Now())
		}
		maybePublish(time.Now())
	}
	finishTransport(streams, p, &stats)
	p.sampleCaptureStats(handle, time.Now(), true)
	p.finishCapture()
	if publish != nil {
		final := p.viewSnapshot()
		final.Final = true
		publish(final)
	}
	return result, err
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

// Adapter is an Npcap interface address that can carry game traffic.
type Adapter struct {
	IPv4        string `json:"ipv4"`
	Description string `json:"description"`
}

// ListIPv4Adapters lists the IPv4 addresses of the Npcap interfaces.
func ListIPv4Adapters() ([]Adapter, error) {
	devices, err := pcap.FindAllDevs()
	if err != nil {
		return nil, fmt.Errorf("list Npcap network interfaces: %w", err)
	}
	adapters := []Adapter{}
	for _, device := range devices {
		for _, address := range device.Addresses {
			if address.IP == nil || address.IP.To4() == nil {
				continue
			}
			adapters = append(adapters, Adapter{IPv4: address.IP.String(), Description: device.Description})
		}
	}
	return adapters, nil
}
