package desktop

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// desktopWindowShape is a Desktop window's size and minimum size in DIPs.
type desktopWindowShape struct {
	width, height, minWidth, minHeight int
}

// desktopShape is the Desktop window's default, the size the Showcase review window has always used and a little larger
// than the 1280 x 820 of earlier versions; the minimum is unchanged. A size the user sets is remembered and replaces the
// default (desktopWindowKeeper).
var desktopShape = desktopWindowShape{width: 1360, height: 880, minWidth: 980, minHeight: 620}

// maxDesktopWindow bounds a remembered size in DIPs, as placements are validated.
const maxDesktopWindow = 20000

// fit returns the shape for a work area in DIPs: never larger than the work area, so the window, title bar included,
// opens fully on screen, and with a minimum the work area can hold. A missing work area keeps the shape.
func (shape desktopWindowShape) fit(workArea application.Rect) desktopWindowShape {
	if workArea.Width <= 0 || workArea.Height <= 0 {
		return shape
	}
	shape.width, shape.height = min(shape.width, workArea.Width), min(shape.height, workArea.Height)
	shape.minWidth, shape.minHeight = min(shape.minWidth, shape.width), min(shape.minHeight, shape.height)
	return shape
}

// fitDesktopWindow shrinks the Desktop window to the primary work area when its default shape does not fit, and centers
// it there; on a large enough display it changes nothing. The minimum is lowered before the size, because Windows would
// otherwise hold the window at the old minimum.
func fitDesktopWindow(window *application.WebviewWindow, screen *application.Screen, shape desktopWindowShape) {
	if window == nil || screen == nil {
		return
	}
	fitted := shape.fit(screen.WorkArea)
	if fitted == shape {
		return
	}
	window.SetMinSize(fitted.minWidth, fitted.minHeight)
	window.SetSize(fitted.width, fitted.height)
	window.Center()
}

// placeDesktop returns where a remembered Desktop window opens: on the display it was left on, at its position, with its
// size in DIPs kept across a display scale change, the whole window inside that display's work area, and a minimum that
// work area can hold. It uses the same placement rules as the Overlays. It reports false when nothing is remembered or
// the display is gone; the window then opens centered on the primary display at its creation size (desktopWindowKeeper).
func placeDesktop(saved *DesktopPlacement, screens []overlayScreen, shape desktopWindowShape) (OverlayPlacement, desktopWindowShape, bool) {
	if !validDesktopPlacement(saved) {
		return OverlayPlacement{}, shape, false
	}
	rect := application.Rect{X: saved.X, Y: saved.Y, Width: saved.Width, Height: saved.Height}
	onScreen := false
	for _, screen := range screens {
		if overlap(rect, screen.Bounds) > 0 {
			onScreen = true
			break
		}
	}
	if !onScreen {
		return OverlayPlacement{}, shape, false
	}
	spec := overlaySpec{width: shape.width, height: shape.height, minWidth: shape.minWidth, minHeight: shape.minHeight, maxWidth: maxDesktopWindow, maxHeight: maxDesktopWindow}
	placed := placeOverlay(spec, &OverlayPlacement{X: saved.X, Y: saved.Y, Width: saved.Width, Height: saved.Height, Scale: saved.Scale}, screens, defaultOverlayScale)
	limits := shape
	best := 0
	for _, screen := range screens {
		if area := overlap(toRect(placed), screen.WorkArea); area > best && screen.Scale > 0 {
			best = area
			work := screen.WorkArea
			limits = shape.fit(application.Rect{Width: scaled(work.Width, 1/screen.Scale), Height: scaled(work.Height, 1/screen.Scale)})
		}
	}
	return placed, limits, true
}

const (
	// desktopSaveDelay debounces writes while the window is dragged or resized.
	desktopSaveDelay = 400 * time.Millisecond
	// desktopShowFallback shows the window even if its runtime never reports ready.
	desktopShowFallback = 5 * time.Second
)

// desktopWindowKeeper remembers where the user leaves the Desktop window and opens it there again: its position, its
// size and whether it was maximized. The window is created hidden and shown once its runtime is ready and it has been
// placed, so it never appears at one size and then jumps to another.
type desktopWindowKeeper struct {
	app    *application.App
	store  *settingsStore
	shape  desktopWindowShape
	window *application.WebviewWindow
	// created is the shape the window is created with: the remembered size, or the default.
	created desktopWindowShape

	mu                   sync.Mutex
	ready, shown, closed bool
	timer                *time.Timer
	// normal is the last normal bounds, kept while the window is maximized so the size to restore to is not lost.
	normal *DesktopPlacement
	// defaultBounds are the bounds a reset gave the window. While it keeps them nothing is remembered, so the next launch
	// opens at the default as well, wherever the default then lies.
	defaultBounds *application.Rect
}

func newDesktopWindowKeeper(app *application.App, store *settingsStore, shape desktopWindowShape) *desktopWindowKeeper {
	created := shape
	if saved := store.desktopPlacement(); saved != nil {
		created.width = min(max(scaled(saved.Width, 1/saved.Scale), shape.minWidth), maxDesktopWindow)
		created.height = min(max(scaled(saved.Height, 1/saved.Scale), shape.minHeight), maxDesktopWindow)
	}
	return &desktopWindowKeeper{app: app, store: store, shape: shape, created: created}
}

// size is the creation size in DIPs: the remembered one when there is one, otherwise the default. Creating the window at
// that size means placing it never changes its size on the same display.
func (k *desktopWindowKeeper) size() (int, int) {
	return k.created.width, k.created.height
}

// attach starts tracking the window. The application loop must run for the window to be placed and shown.
func (k *desktopWindowKeeper) attach(window *application.WebviewWindow) {
	k.window = window
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { k.place() })
	changed := func(*application.WindowEvent) { k.saveSoon() }
	window.OnWindowEvent(events.Common.WindowDidMove, changed)
	window.OnWindowEvent(events.Common.WindowDidResize, changed)
	k.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		time.AfterFunc(desktopShowFallback, k.showOnce)
	})
}

// place opens the window where it was left, or at its default when nothing usable is remembered, then shows it.
func (k *desktopWindowKeeper) place() {
	k.mu.Lock()
	if k.ready || k.closed {
		k.mu.Unlock()
		return
	}
	k.mu.Unlock()
	saved := k.store.desktopPlacement()
	if placed, limits, ok := placeDesktop(saved, displayScreens(k.app), k.shape); ok {
		k.window.SetMinSize(limits.minWidth, limits.minHeight)
		k.window.SetPhysicalBounds(toRect(placed))
		normal := DesktopPlacement{X: placed.X, Y: placed.Y, Width: placed.Width, Height: placed.Height, Scale: placed.Scale}
		k.mu.Lock()
		k.normal = &normal
		k.mu.Unlock()
		if saved.Maximized {
			k.window.Maximise()
		}
	} else {
		// Nothing usable is remembered, or its display is gone: the window stays centered on the primary display at its
		// creation size, which keeps a remembered size and shrinks only when the work area cannot hold it.
		fitDesktopWindow(k.window, k.app.Screen.GetPrimary(), k.created)
	}
	k.mu.Lock()
	k.ready = true
	k.mu.Unlock()
	k.showOnce()
}

func (k *desktopWindowKeeper) showOnce() {
	k.mu.Lock()
	if k.shown || k.closed {
		k.mu.Unlock()
		return
	}
	k.shown = true
	k.mu.Unlock()
	k.window.Show()
}

// saveSoon records the window after the user stops moving or resizing it.
func (k *desktopWindowKeeper) saveSoon() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.ready || k.closed {
		return
	}
	if k.timer != nil {
		k.timer.Stop()
	}
	k.timer = time.AfterFunc(desktopSaveDelay, k.save)
}

// save records the normal bounds and whether the window is maximized. A minimized window keeps the previous record, and
// a maximized one keeps the normal bounds it will restore to.
func (k *desktopWindowKeeper) save() {
	if k.window.IsMinimised() {
		return
	}
	maximized := k.window.IsMaximised()
	var current OverlayPlacement
	if !maximized {
		current = physicalPlacement(k.window)
	}
	k.mu.Lock()
	if !maximized && k.defaultBounds != nil && toRect(current) == *k.defaultBounds {
		// Still where the reset put it.
		k.mu.Unlock()
		return
	}
	if !maximized && current.Width > 0 && current.Height > 0 {
		k.normal = &DesktopPlacement{X: current.X, Y: current.Y, Width: current.Width, Height: current.Height, Scale: current.Scale}
		k.defaultBounds = nil
	}
	normal := k.normal
	k.mu.Unlock()
	if normal == nil {
		// Maximized before any normal bounds were seen: there is no size to restore to yet.
		return
	}
	placement := *normal
	placement.Maximized = maximized
	if err := k.store.saveDesktopPlacement(placement); err != nil {
		k.store.setWarning("window_save_failed")
	}
}

// reset forgets the remembered window and returns it to its first-launch default: restored if it was maximized or
// minimized, at the default size fitted to the primary display's work area, centered there.
func (k *desktopWindowKeeper) reset() error {
	k.mu.Lock()
	if k.timer != nil {
		k.timer.Stop()
	}
	ready := k.ready && !k.closed
	k.mu.Unlock()
	if err := k.store.clearDesktopPlacement(); err != nil {
		k.store.setWarning("window_save_failed")
		return err
	}
	primary, ok := primaryDisplay(displayScreens(k.app))
	if !ready || !ok {
		return nil
	}
	if k.window.IsMaximised() || k.window.IsMinimised() {
		k.window.Restore()
	}
	bounds, limits := defaultDesktopBounds(primary, k.shape)
	k.window.SetMinSize(limits.minWidth, limits.minHeight)
	k.window.SetPhysicalBounds(bounds)
	actual := k.window.PhysicalBounds()
	k.mu.Lock()
	k.defaultBounds = &actual
	k.normal = &DesktopPlacement{X: actual.X, Y: actual.Y, Width: actual.Width, Height: actual.Height, Scale: primary.Scale}
	k.mu.Unlock()
	return nil
}

func primaryDisplay(screens []overlayScreen) (overlayScreen, bool) {
	for _, screen := range screens {
		if screen.Primary && screen.Scale > 0 {
			return screen, true
		}
	}
	return overlayScreen{}, false
}

// defaultDesktopBounds is the first-launch default in physical pixels on a display: the default size fitted to its work
// area, centered there, with a minimum the work area can hold.
func defaultDesktopBounds(screen overlayScreen, shape desktopWindowShape) (application.Rect, desktopWindowShape) {
	work := screen.WorkArea
	fitted := shape.fit(application.Rect{Width: scaled(work.Width, 1/screen.Scale), Height: scaled(work.Height, 1/screen.Scale)})
	width, height := min(scaled(fitted.width, screen.Scale), work.Width), min(scaled(fitted.height, screen.Scale), work.Height)
	return application.Rect{X: work.X + (work.Width-width)/2, Y: work.Y + (work.Height-height)/2, Width: width, Height: height}, fitted
}

// close stops tracking; with persist it records the window one last time, for a size changed just before quitting.
func (k *desktopWindowKeeper) close(persist bool) {
	if k == nil {
		return
	}
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return
	}
	if k.timer != nil {
		k.timer.Stop()
	}
	ready := k.ready
	k.mu.Unlock()
	if persist && ready {
		k.save()
	}
	k.mu.Lock()
	k.closed = true
	k.mu.Unlock()
}
