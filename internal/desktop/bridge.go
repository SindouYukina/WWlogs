package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

// Bridge is the only Go object the frontend can call. It forwards to the CaptureService and the Publisher and
// returns snapshots as JSON copies, so no window ever holds a reference to Go state. Every window calls it on its
// own schedule; a slow window only receives fewer snapshots.
type Bridge struct {
	capture    *service.CaptureService
	defaults   capture.SessionConfig
	overlays   *overlayManager
	shell      shellSignal
	shutdown   *shutdownGate
	settings   *settingsStore
	catalog    localeCatalog
	projection *displayProjection
	listening  service.ListeningMonitor
	process    *service.GameProcessWatcher
	// environment and npcap answer the first-use questions; openBrowser hands a checked URL to the system browser.
	environment *environmentChecker
	npcap       *npcapDownloadResolver
	openBrowser func(string) error
}

// snapshotWaitLimit bounds one NextSnapshot call; an unchanged snapshot returned at the limit is a heartbeat.
const snapshotWaitLimit = time.Second

// stopWaitLimit bounds an interactive StopCapture request; native shutdown allows a separate drain budget.
const stopWaitLimit = 10 * time.Second

// StartRequest is a capture start from the UI.
type StartRequest struct {
	LocalIPv4 string `json:"localIPv4"`
}

// GetSettings returns validated preferences and a recoverable startup warning, if any.
func (b *Bridge) GetSettings() SettingsState { return b.settings.current() }

// SetDefaultAdapter persists the capture interface preselected at launch.
func (b *Bridge) SetDefaultAdapter(ipv4 string) (SettingsState, error) {
	return b.settings.setDefaultAdapter(ipv4)
}

// SetLocale persists the single global display locale without touching capture or statistics.
func (b *Bridge) SetLocale(locale string) (SettingsState, error) {
	state, err := b.settings.setLocale(locale)
	b.shell.fire()
	return state, err
}

// SetAppearance persists the theme and motion preference; every window applies them from its next snapshot.
func (b *Bridge) SetAppearance(theme string, reduceMotion bool) (SettingsState, error) {
	state, err := b.settings.setAppearance(theme, reduceMotion)
	b.shell.fire()
	return state, err
}

// UpdateOverlay changes one Overlay's window properties. The other Overlay is not touched.
func (b *Bridge) UpdateOverlay(kind string, change OverlayChange) (OverlayStates, error) {
	if b.overlays == nil {
		return OverlayStates{}, errors.New("overlays are unavailable")
	}
	return b.overlays.update(kind, change)
}

// ResetOverlay returns one Overlay to its default position and size.
func (b *Bridge) ResetOverlay(kind string) error {
	if b.overlays == nil {
		return errors.New("overlays are unavailable")
	}
	return b.overlays.reset(kind)
}

// NextSnapshot returns the newest snapshot once its sequence is greater than after, when host state changes, or the
// current one after about a second. The returned JSON is a copy.
func (b *Bridge) NextSnapshot(ctx context.Context, after uint64) (json.RawMessage, error) {
	waitCtx, cancel := context.WithTimeout(ctx, snapshotWaitLimit)
	defer cancel()
	changed := b.shell.changed()
	go func() {
		select {
		case <-changed:
			cancel()
		case <-waitCtx.Done():
		}
	}()
	snapshot, err := b.capture.Publisher().Next(waitCtx, after)
	if err != nil && ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return b.encode(snapshot)
}

// CurrentSnapshot returns the newest snapshot without waiting.
func (b *Bridge) CurrentSnapshot() (json.RawMessage, error) {
	return b.encode(b.capture.Publisher().Latest())
}

func (b *Bridge) encode(snapshot *service.Snapshot) (json.RawMessage, error) {
	return b.projection.encode(snapshot, b.settings.current(), b.catalog, b.listeningStatus(snapshot), b.overlays.states())
}

// shellSignal wakes long-polling windows when host state changes, so a setting applies at once instead of at the next
// heartbeat. Its zero value is ready to use.
type shellSignal struct {
	mu     sync.Mutex
	signal chan struct{}
}

func (s *shellSignal) changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signal == nil {
		s.signal = make(chan struct{})
	}
	return s.signal
}

func (s *shellSignal) fire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signal != nil {
		close(s.signal)
		s.signal = nil
	}
}

// listeningStatus is evaluated on every call, because silence changes the status while the snapshot stays the same.
func (b *Bridge) listeningStatus(snapshot *service.Snapshot) service.ListeningStatus {
	var process service.GameProcess
	// A replay has no game process, so absence must not count as evidence.
	if b.process != nil && (snapshot.Service.Session == nil || snapshot.Service.Session.Source != "replay") {
		process = b.process.Latest()
	}
	return b.listening.Evaluate(snapshot.Service, snapshot.View, process, time.Now())
}

// CheckCaptureEnvironment detects Npcap, enumerates its interfaces and classifies the result. Repeated calls during a
// check share it, and no call touches a running capture.
func (b *Bridge) CheckCaptureEnvironment(ctx context.Context) capture.CaptureEnvironment {
	return b.environment.check(ctx)
}

// NpcapDownloadInfo returns the latest official installer, or the official download page when it cannot be confirmed.
func (b *Bridge) NpcapDownloadInfo(ctx context.Context) NpcapDownload {
	return b.npcap.resolve(ctx)
}

// OpenNpcapDownload hands the official installer URL to the user's browser. The frontend cannot choose the URL, and
// WWlogs neither runs the installer nor keeps a copy of it.
func (b *Bridge) OpenNpcapDownload(ctx context.Context) (NpcapDownload, error) {
	download := b.npcap.resolve(ctx)
	if !validNpcapURL(download.URL) {
		return download, errors.New("the download address is not an official Npcap address")
	}
	return download, b.openBrowser(download.URL)
}

// StartCapture starts a live capture on the interface with request.LocalIPv4, using the host's other options.
func (b *Bridge) StartCapture(request StartRequest) error {
	cfg := b.defaults
	if request.LocalIPv4 != "" {
		cfg.LocalIPv4 = request.LocalIPv4
	}
	return b.capture.Start(cfg)
}

// StopCapture stops the current capture and waits until its resources are released.
func (b *Bridge) StopCapture(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(ctx, stopWaitLimit)
	defer cancel()
	return b.capture.Stop(stopCtx)
}

// ServiceShutdown is a fallback for startup failure and non-window exits. Normal native exits use shutdownGate first.
func (b *Bridge) ServiceShutdown() error {
	if b.shutdown != nil && b.shutdown.ready.Load() {
		return b.shutdown.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownWaitLimit)
	defer cancel()
	return b.capture.Shutdown(ctx)
}
