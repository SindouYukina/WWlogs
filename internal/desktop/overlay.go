package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Overlay kinds are stable IDs shared with the frontend's ?surface= parameter and settings.json.
const (
	overlayCombat  = "combat"
	overlayEffects = "effects"
)

// overlaySpec holds the fixed window shape of one Overlay. Sizes are DIPs.
type overlaySpec struct {
	windowName          string
	title               string
	width, height       int
	minWidth, minHeight int
	maxWidth, maxHeight int
	offsetX, offsetY    int
	defaultOpacity      int
}

var overlaySpecs = map[string]overlaySpec{
	overlayCombat:  {windowName: "overlay-combat", title: "WWlogs Combat Overlay", width: 486, height: 268, minWidth: 340, minHeight: 150, maxWidth: 1200, maxHeight: 900, offsetX: 48, offsetY: 48, defaultOpacity: 74},
	overlayEffects: {windowName: "overlay-effects", title: "WWlogs Effects Overlay", width: 320, height: 420, minWidth: 220, minHeight: 120, maxWidth: 900, maxHeight: 1200, offsetX: 48, offsetY: 336, defaultOpacity: 62},
}

// overlayWindowOptions is the single definition of an Overlay's native window, for production and Showcase surfaces.
func overlayWindowOptions(spec overlaySpec, url string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:           spec.windowName,
		Title:          spec.title,
		Width:          spec.width,
		Height:         spec.height,
		MinWidth:       spec.minWidth,
		MinHeight:      spec.minHeight,
		MaxWidth:       spec.maxWidth,
		MaxHeight:      spec.maxHeight,
		URL:            url,
		Frameless:      true,
		AlwaysOnTop:    true,
		BackgroundType: application.BackgroundTypeTransparent,
		Hidden:         true,
		// Without the frameless decorations Windows keeps no caption, frame, shadow, or rounded corners around the
		// transparent area.
		Windows: application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true},
	}
}

// OverlayState is what the frontend sees of one Overlay. Editing is transient and never persisted, so a restart always
// returns to Normal Mode.
type OverlayState struct {
	Visible      bool `json:"visible"`
	Opacity      int  `json:"opacity"`
	AlwaysOnTop  bool `json:"alwaysOnTop"`
	ClickThrough bool `json:"clickThrough"`
	Editing      bool `json:"editing"`
}

// OverlayStates is comparable, so a change is detected by the projection without encoding it.
type OverlayStates struct {
	Combat  OverlayState `json:"combat"`
	Effects OverlayState `json:"effects"`
}

// OverlayChange carries only the fields a request wants to change.
type OverlayChange struct {
	Visible      *bool `json:"visible,omitempty"`
	Opacity      *int  `json:"opacity,omitempty"`
	AlwaysOnTop  *bool `json:"alwaysOnTop,omitempty"`
	ClickThrough *bool `json:"clickThrough,omitempty"`
	Editing      *bool `json:"editing,omitempty"`
}

// overlayScreen is the part of a Wails screen placement needs. Physical pixels are used throughout: Wails DIP spaces are
// per screen, so a DIP rectangle cannot be compared across monitors with different scaling.
type overlayScreen struct {
	Bounds, WorkArea application.Rect
	Scale            float64
	Primary          bool
}

func overlap(a, b application.Rect) int {
	width := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
	height := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
	if width <= 0 || height <= 0 {
		return 0
	}
	return width * height
}

func scaled(value int, factor float64) int {
	return int(float64(value)*factor + 0.5)
}

// placeOverlay returns where an Overlay belongs on the current displays. A placement on a display that still exists keeps
// its position and its logical size under that display's scale. A placement on a removed display, or none at all, gets
// the kind's default corner on the primary display. The whole window always stays inside one work area.
func placeOverlay(spec overlaySpec, saved *OverlayPlacement, screens []overlayScreen) OverlayPlacement {
	if len(screens) == 0 {
		if saved != nil {
			return *saved
		}
		return OverlayPlacement{Width: spec.width, Height: spec.height, Scale: 1}
	}
	chosen := screens[0]
	for _, screen := range screens {
		if screen.Primary {
			chosen = screen
			break
		}
	}
	anchored := false
	logicalWidth, logicalHeight := spec.width, spec.height
	var x, y int
	if saved != nil && saved.Scale > 0 {
		logicalWidth = scaled(saved.Width, 1/saved.Scale)
		logicalHeight = scaled(saved.Height, 1/saved.Scale)
		rect := application.Rect{X: saved.X, Y: saved.Y, Width: saved.Width, Height: saved.Height}
		best := 0
		for _, screen := range screens {
			if area := overlap(rect, screen.Bounds); area > best {
				best, chosen, anchored = area, screen, true
			}
		}
		x, y = saved.X, saved.Y
	}
	scale := chosen.Scale
	if scale <= 0 {
		scale = 1
	}
	area := chosen.WorkArea
	if !anchored {
		x, y = area.X+scaled(spec.offsetX, scale), area.Y+scaled(spec.offsetY, scale)
	}
	logicalWidth = min(max(logicalWidth, spec.minWidth), spec.maxWidth)
	logicalHeight = min(max(logicalHeight, spec.minHeight), spec.maxHeight)
	width, height := scaled(logicalWidth, scale), scaled(logicalHeight, scale)
	// On an unchanged scale the saved pixels are exact; converting through DIPs would drift by a pixel on every restart.
	if anchored && saved.Scale == scale && saved.Width >= width-1 && saved.Width <= width+1 && saved.Height >= height-1 && saved.Height <= height+1 {
		width, height = saved.Width, saved.Height
	}
	width, height = min(width, area.Width), min(height, area.Height)
	return OverlayPlacement{
		X:      max(area.X, min(x, area.X+area.Width-width)),
		Y:      max(area.Y, min(y, area.Y+area.Height-height)),
		Width:  width,
		Height: height,
		Scale:  scale,
	}
}

// applyOverlayChange updates persisted preferences for a request. Editing and visibility are linked so that Edit Mode
// is never left on a hidden window. It returns the resulting editing state.
func applyOverlayChange(prefs *OverlayPreferences, editing bool, change OverlayChange) bool {
	if change.Opacity != nil {
		prefs.Opacity = clampOpacity(*change.Opacity)
	}
	if change.AlwaysOnTop != nil {
		prefs.AlwaysOnTop = *change.AlwaysOnTop
	}
	if change.ClickThrough != nil {
		prefs.ClickThrough = *change.ClickThrough
	}
	if change.Visible != nil {
		prefs.Visible = *change.Visible
		if !prefs.Visible {
			editing = false
		}
	}
	if change.Editing != nil {
		editing = *change.Editing
		if editing {
			prefs.Visible = true
		}
	}
	return editing
}

// overlayEntry is one managed Overlay. window stays nil until the Overlay is first shown: an Overlay nobody uses costs
// no WebView2 renderer. Once created it is hidden rather than destroyed, so there is never a second window of a kind.
type overlayEntry struct {
	kind    string
	spec    overlaySpec
	window  *application.WebviewWindow
	ready   bool
	editing bool
	timer   *time.Timer
	// hitWidth and hitHeight are the size the layered hit-test region was last refreshed for.
	hitWidth, hitHeight int
}

// overlayManager owns both Overlay windows. operation serializes requests, which call into the UI thread, and must not be
// taken by a Wails hook; mu guards entry fields and, apart from creating a window, is not held across a window call.
type overlayManager struct {
	app       *application.App
	store     *settingsStore
	notify    func()
	shutting  func() bool
	operation sync.Mutex
	mu        sync.Mutex
	entries   map[string]*overlayEntry
	closed    bool
	cancel    context.CancelFunc
}

func newOverlayManager(app *application.App, store *settingsStore, notify func(), shutting func() bool) *overlayManager {
	entries := map[string]*overlayEntry{}
	for kind, spec := range overlaySpecs {
		entries[kind] = &overlayEntry{kind: kind, spec: spec}
	}
	return &overlayManager{app: app, store: store, notify: notify, shutting: shutting, entries: entries}
}

// start opens the Overlays that were visible when the application last closed and watches for display changes.
func (m *overlayManager) start(showAll bool) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.watchDisplays(ctx)
	for _, entry := range m.entries {
		if showAll {
			// A Development flag for one launch: held in memory, not a user request to rewrite settings now.
			_ = m.store.changeOverlay(entry.kind, false, func(prefs *OverlayPreferences) { prefs.Visible = true })
		}
		m.operation.Lock()
		m.applyLocked(entry)
		m.operation.Unlock()
	}
	m.notify()
}

// states describes both Overlays for the snapshot shell.
func (m *overlayManager) states() OverlayStates {
	if m == nil {
		return OverlayStates{}
	}
	m.mu.Lock()
	combat, effects := m.entries[overlayCombat].editing, m.entries[overlayEffects].editing
	m.mu.Unlock()
	return OverlayStates{Combat: m.store.overlay(overlayCombat).state(combat), Effects: m.store.overlay(overlayEffects).state(effects)}
}

// update applies a request. An opacity change comes from a slider, so it reaches the windows at once while the disk write
// is debounced; every other change is an explicit command and is written immediately.
func (m *overlayManager) update(kind string, change OverlayChange) (OverlayStates, error) {
	if _, known := overlaySpecs[kind]; !known {
		return OverlayStates{}, errors.New("unknown overlay")
	}
	m.operation.Lock()
	defer m.operation.Unlock()
	m.mu.Lock()
	entry := m.entries[kind]
	editing := entry.editing
	m.mu.Unlock()
	onlyOpacity := change.Opacity != nil && change.Visible == nil && change.AlwaysOnTop == nil && change.ClickThrough == nil && change.Editing == nil
	var nextEditing bool
	err := m.store.changeOverlay(kind, !onlyOpacity, func(prefs *OverlayPreferences) { nextEditing = applyOverlayChange(prefs, editing, change) })
	if err != nil {
		return m.states(), err
	}
	if onlyOpacity {
		m.persistSoon(entry)
	}
	m.mu.Lock()
	entry.editing = nextEditing
	m.mu.Unlock()
	m.applyLocked(entry)
	m.notify()
	return m.states(), nil
}

// reset forgets the saved placement, which also recovers an Overlay that a display change left somewhere unexpected.
func (m *overlayManager) reset(kind string) error {
	if _, known := overlaySpecs[kind]; !known {
		return errors.New("unknown overlay")
	}
	m.operation.Lock()
	defer m.operation.Unlock()
	if err := m.store.changeOverlay(kind, true, func(prefs *OverlayPreferences) { prefs.Placement = nil }); err != nil {
		return err
	}
	m.mu.Lock()
	entry := m.entries[kind]
	ready := entry.ready
	m.mu.Unlock()
	if ready {
		entry.window.SetPhysicalBounds(toRect(placeOverlay(entry.spec, nil, m.screens())))
	}
	m.notify()
	return nil
}

// applyLocked brings the native window in line with the preferences. The caller holds operation.
func (m *overlayManager) applyLocked(entry *overlayEntry) {
	prefs := m.store.overlay(entry.kind)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if entry.window == nil && prefs.Visible {
		m.createLocked(entry)
	}
	window, ready, editing := entry.window, entry.ready, entry.editing
	m.mu.Unlock()
	// Before the runtime is ready the request stays in the preferences; onReady applies them.
	if window == nil || !ready {
		return
	}
	window.SetAlwaysOnTop(prefs.AlwaysOnTop)
	window.SetIgnoreMouseEvents(prefs.ClickThrough && !editing)
	// Edge resizing is gated in the window runtime, not with SetResizable. Dropping the native resizable style more than
	// doubled the GPU process's CPU for two live Overlays in repeated measurements, and a frameless window has no native
	// edge to grab either way.
	window.ExecJS(fmt.Sprintf("window._wails.setResizable(%v);", editing))
	if prefs.Visible {
		window.Show()
	} else {
		window.Hide()
	}
}

// createLocked creates the native window. The caller holds mu.
func (m *overlayManager) createLocked(entry *overlayEntry) {
	window := m.app.Window.NewWithOptions(overlayWindowOptions(entry.spec, "/?view=overlay&surface="+entry.kind))
	entry.window = window
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { m.onReady(entry) })
	changed := func(*application.WindowEvent) { m.persistSoon(entry) }
	window.OnWindowEvent(events.Common.WindowDidMove, changed)
	window.OnWindowEvent(events.Common.WindowDidResize, changed)
	// A close request (for example Alt+F4) only hides the window, so the Desktop window can show it again. The hook runs
	// on the UI thread, where operation must not be taken. Quit destroys windows directly and is not affected.
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if m.shutting() {
			return
		}
		event.Cancel()
		hidden := false
		go func() { _, _ = m.update(entry.kind, OverlayChange{Visible: &hidden}) }()
	})
}

// onReady places the window once its runtime exists. Native construction emits temporary minimum sizes before this, so
// bounds are not tracked until now.
func (m *overlayManager) onReady(entry *overlayEntry) {
	m.operation.Lock()
	defer m.operation.Unlock()
	m.mu.Lock()
	if m.closed || entry.ready {
		m.mu.Unlock()
		return
	}
	window := entry.window
	m.mu.Unlock()
	window.SetPhysicalBounds(toRect(placeOverlay(entry.spec, m.store.overlay(entry.kind).Placement, m.screens())))
	m.mu.Lock()
	entry.ready = true
	m.mu.Unlock()
	m.applyLocked(entry)
	m.persistSoon(entry)
}

func toRect(placement OverlayPlacement) application.Rect {
	return application.Rect{X: placement.X, Y: placement.Y, Width: placement.Width, Height: placement.Height}
}

func (m *overlayManager) screens() []overlayScreen {
	var result []overlayScreen
	for _, screen := range m.app.Screen.GetAll() {
		result = append(result, overlayScreen{Bounds: screen.PhysicalBounds, WorkArea: screen.PhysicalWorkArea, Scale: float64(screen.ScaleFactor), Primary: screen.IsPrimary})
	}
	return result
}

// persistSoon debounces disk writes while a window moves or a slider drags.
func (m *overlayManager) persistSoon(entry *overlayEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !entry.ready {
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.timer = time.AfterFunc(400*time.Millisecond, func() { m.persist(entry) })
}

func (m *overlayManager) persist(entry *overlayEntry) {
	m.mu.Lock()
	window, ready := entry.window, entry.ready
	closed := m.closed
	m.mu.Unlock()
	if closed || !ready {
		return
	}
	m.refreshHitRegion(entry, window)
	m.savePlacement(entry.kind, window)
}

// refreshHitRegion makes the whole window answer the mouse again after a resize. Windows keeps the old hit-test region of
// a layered window through a user resize, so without this a click on the newly added area lands on the window behind it,
// including Edit Mode's Done button. Wails refreshes the region in SetPhysicalBounds. A same-size call sends no size
// event, so this cannot re-arm itself.
func (m *overlayManager) refreshHitRegion(entry *overlayEntry, window *application.WebviewWindow) {
	bounds := window.PhysicalBounds()
	m.mu.Lock()
	changed := entry.hitWidth != bounds.Width || entry.hitHeight != bounds.Height
	entry.hitWidth, entry.hitHeight = bounds.Width, bounds.Height
	m.mu.Unlock()
	if changed {
		window.SetPhysicalBounds(bounds)
	}
}

func (m *overlayManager) savePlacement(kind string, window *application.WebviewWindow) {
	bounds := window.PhysicalBounds()
	scale := 1.0
	if screen, err := window.GetScreen(); err == nil && screen != nil && screen.ScaleFactor > 0 {
		scale = float64(screen.ScaleFactor)
	}
	placement := OverlayPlacement{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height, Scale: scale}
	if err := m.store.saveOverlayPlacement(kind, placement); err != nil {
		m.store.setWarning("overlay_save_failed")
		m.notify()
	}
}

// watchDisplays reacts to monitors being removed or rearranged. Wails refreshes its inventory on display changes, so
// comparing that small list is enough, and it runs outside the capture path.
func (m *overlayManager) watchDisplays(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	previous := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			screens := m.screens()
			current := fmt.Sprint(screens)
			if previous != "" && current != previous {
				m.refit(screens)
			}
			previous = current
		}
	}
}

func (m *overlayManager) refit(screens []overlayScreen) {
	m.operation.Lock()
	defer m.operation.Unlock()
	for _, entry := range m.entries {
		m.mu.Lock()
		window, ready := entry.window, entry.ready
		m.mu.Unlock()
		if !ready {
			continue
		}
		scale := 1.0
		if screen, err := window.GetScreen(); err == nil && screen != nil && screen.ScaleFactor > 0 {
			scale = float64(screen.ScaleFactor)
		}
		bounds := window.PhysicalBounds()
		current := OverlayPlacement{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height, Scale: scale}
		if fitted := placeOverlay(entry.spec, &current, screens); toRect(fitted) != toRect(current) {
			window.SetPhysicalBounds(toRect(fitted))
		}
	}
}

// close stops timers and watchers; with persist it also saves the final placements. It is safe to call twice.
func (m *overlayManager) close(persist bool) {
	if m == nil {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	type saved struct {
		kind   string
		window *application.WebviewWindow
	}
	var windows []saved
	for _, entry := range m.entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		if entry.ready {
			windows = append(windows, saved{entry.kind, entry.window})
		}
	}
	m.mu.Unlock()
	if persist {
		for _, item := range windows {
			m.savePlacement(item.kind, item.window)
		}
	}
	if err := m.store.flush(); err != nil {
		m.store.setWarning("overlay_save_failed")
	}
}
