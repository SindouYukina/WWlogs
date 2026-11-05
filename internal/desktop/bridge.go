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
	capture  *service.CaptureService
	defaults capture.SessionConfig
	overlay  func() *application.WebviewWindow
}

// snapshotWaitLimit bounds one NextSnapshot call; an unchanged snapshot returned at the limit is a heartbeat.
const snapshotWaitLimit = time.Second

// stopWaitLimit bounds StopCapture and the shutdown stop.
const stopWaitLimit = 10 * time.Second

// StartRequest is a capture start from the UI.
type StartRequest struct {
	LocalIPv4 string `json:"localIPv4"`
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
	return snapshot.JSON()
}

// CurrentSnapshot returns the newest snapshot without waiting.
func (b *Bridge) CurrentSnapshot() (json.RawMessage, error) {
	return b.capture.Publisher().Latest().JSON()
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

// ServiceShutdown stops the capture when the application quits, so the handle and writers are released first.
func (b *Bridge) ServiceShutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), stopWaitLimit)
	defer cancel()
	return b.capture.Stop(ctx)
}
