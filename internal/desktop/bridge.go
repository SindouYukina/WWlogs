package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

// Bridge is the only Go object the frontend can call. It forwards to the CaptureService and the Publisher and
// returns snapshots as JSON copies, so no window ever holds a reference to Go state. Every window calls it on its
// own schedule; a slow window only receives fewer snapshots.
type Bridge struct {
	capture    *service.CaptureService
	defaults   capture.SessionConfig
	overlay    func() *application.WebviewWindow
	shutdown   *shutdownGate
	settings   *settingsStore
	catalog    localeCatalog
	projection *displayProjection
	listening  service.ListeningMonitor
	process    *service.GameProcessWatcher
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

// SaveSettings persists supported preferences in the current Windows user's data directory.
func (b *Bridge) SaveSettings(value Settings) (SettingsState, error) { return b.settings.save(value) }

// SetLocale persists the single global display locale without touching capture or statistics.
func (b *Bridge) SetLocale(locale string) (SettingsState, error) { return b.settings.setLocale(locale) }

// SetAppearance persists the theme and motion preference; every window applies them from its next snapshot.
func (b *Bridge) SetAppearance(theme string, reduceMotion bool) (SettingsState, error) {
	return b.settings.setAppearance(theme, reduceMotion)
}

// ResetOverlayPosition recovers an overlay after a display configuration change.
func (b *Bridge) ResetOverlayPosition() {
	if w := b.overlay(); w != nil {
		w.SetSize(440, 640)
		w.Center()
	}
}

// NextSnapshot returns the newest snapshot once its sequence is greater than after, or the current one after about
// a second. The returned JSON is a copy.
func (b *Bridge) NextSnapshot(ctx context.Context, after uint64) (json.RawMessage, error) {
	waitCtx, cancel := context.WithTimeout(ctx, snapshotWaitLimit)
	defer cancel()
	snapshot, err := b.capture.Publisher().Next(waitCtx, after)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return b.encode(snapshot)
}

// CurrentSnapshot returns the newest snapshot without waiting.
func (b *Bridge) CurrentSnapshot() (json.RawMessage, error) {
	return b.encode(b.capture.Publisher().Latest())
}

func (b *Bridge) encode(snapshot *service.Snapshot) (json.RawMessage, error) {
	return b.projection.encode(snapshot, b.settings.current(), b.catalog, b.listeningStatus(snapshot))
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

// ListAdapters lists the Npcap IPv4 interfaces.
func (b *Bridge) ListAdapters() ([]capture.Adapter, error) {
	return capture.ListIPv4Adapters()
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

// SetOverlayVisible shows or hides the overlay window.
func (b *Bridge) SetOverlayVisible(visible bool) bool {
	window := b.overlay()
	if window == nil {
		return false
	}
	if visible {
		window.Show()
	} else {
		window.Hide()
	}
	return window.IsVisible()
}

// OverlayVisible reports whether the overlay window is shown.
func (b *Bridge) OverlayVisible() bool {
	window := b.overlay()
	return window != nil && window.IsVisible()
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
