package desktop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func validOverlayBounds(b OverlayBounds) bool {
	return b.Width >= 320 && b.Width <= 1200 && b.Height >= 360 && b.Height <= 1600 && b.X >= -100000 && b.X <= 100000 && b.Y >= -100000 && b.Y <= 100000
}

// fitOverlay uses Wails DIP work areas and keeps the entire window reachable after a display change.
func fitOverlay(bounds OverlayBounds, areas []application.Rect) OverlayBounds {
	if !validOverlayBounds(bounds) {
		bounds = OverlayBounds{Width: 440, Height: 640}
	}
	if len(areas) == 0 {
		return bounds
	}
	chosen := areas[0]
	for _, area := range areas {
		if bounds.X >= area.X && bounds.X < area.X+area.Width && bounds.Y >= area.Y && bounds.Y < area.Y+area.Height {
			chosen = area
			break
		}
	}
	bounds.Width = min(bounds.Width, chosen.Width)
	bounds.Height = min(bounds.Height, chosen.Height)
	bounds.X = max(chosen.X, min(bounds.X, chosen.X+chosen.Width-bounds.Width))
	bounds.Y = max(chosen.Y, min(bounds.Y, chosen.Y+chosen.Height-bounds.Height))
	return bounds
}

// trackOverlay debounces disk writes; shutdown flushes the final bounds and cancels its timer.
func trackOverlay(app *application.App, window *application.WebviewWindow, store *settingsStore) func() {
	var mu sync.Mutex
	var timer *time.Timer
	closed := false
	areas := func() []application.Rect {
		result := []application.Rect{}
		for _, screen := range app.Screen.GetAll() {
			result = append(result, screen.WorkArea)
		}
		return result
	}
	read := func() OverlayBounds {
		x, y := window.Position()
		w, h := window.Size()
		return OverlayBounds{x, y, w, h}
	}
	save := func() {
		if err := store.setOverlay(read()); err != nil {
			store.mu.Lock()
			store.state.Warning = "overlay_save_failed"
			store.mu.Unlock()
		}
	}
	constrain := func() {
		current := read()
		fitted := fitOverlay(current, areas())
		if fitted != current {
			window.SetSize(fitted.Width, fitted.Height)
			window.SetPosition(fitted.X, fitted.Y)
		}
	}
	changed := func(*application.WindowEvent) {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(400*time.Millisecond, func() {
			mu.Lock()
			active := !closed
			mu.Unlock()
			if active {
				save()
			}
		})
	}
	window.OnWindowEvent(events.Common.WindowDidMove, changed)
	window.OnWindowEvent(events.Common.WindowDidResize, changed)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		bounds := OverlayBounds{Width: 440, Height: 640}
		if store.current().Settings.Overlay != nil {
			bounds = *store.current().Settings.Overlay
		}
		bounds = fitOverlay(bounds, areas())
		window.SetSize(bounds.Width, bounds.Height)
		window.SetPosition(bounds.X, bounds.Y)
	})
	// Wails refreshes its inventory on display changes; compare the small DIP list outside the capture path.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		previous := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := fmt.Sprint(areas())
				if current != previous {
					previous = current
					constrain()
				}
			}
		}
	}()
	return func() {
		cancel()
		mu.Lock()
		closed = true
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
		save()
	}
}
