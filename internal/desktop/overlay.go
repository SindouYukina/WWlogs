package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
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
	overlayEffects: {windowName: "overlay-effects", title: "WWlogs Effects Overlay", width: 640, height: 120, minWidth: 220, minHeight: 120, maxWidth: 1600, maxHeight: 1200, offsetX: 48, offsetY: 336, defaultOpacity: 62},
}

// ReviewOverlayWindows returns the native window of every Overlay kind, shown at its default size and offset and loading
// route(kind): the one Overlay window definition, for a review surface that shows Overlays without the Overlay manager.
func ReviewOverlayWindows(route func(kind string) string) map[string]application.WebviewWindowOptions {
	windows := make(map[string]application.WebviewWindowOptions, len(overlaySpecs))
	for kind, spec := range overlaySpecs {
		options := overlayWindowOptions(spec, route(kind))
		options.Hidden = false
		options.InitialPosition = application.WindowXY
		options.X, options.Y = spec.offsetX, spec.offsetY
		windows[kind] = options
	}
	return windows
}

// overlayWindowOptions is the single definition of an Overlay's native window, for production and review surfaces.
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
	// Scale is the display scale in percent; the window draws its content with CSS zoom Scale/100.
	Scale   int  `json:"scale"`
	Editing bool `json:"editing"`
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
	Scale        *int  `json:"scale,omitempty"`
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

// at returns the window shape at an Overlay display scale (percent): the default size and the limits grow and shrink
// with the content, so a user's size at 100% means the same at any scale. The offsets stay in DIPs.
func (spec overlaySpec) at(scale int) overlaySpec {
	factor := float64(effectiveOverlayScale(scale)) / 100
	spec.width, spec.height = scaled(spec.width, factor), scaled(spec.height, factor)
	spec.minWidth, spec.minHeight = scaled(spec.minWidth, factor), scaled(spec.minHeight, factor)
	spec.maxWidth, spec.maxHeight = scaled(spec.maxWidth, factor), scaled(spec.maxHeight, factor)
	return spec
}

// placeOverlay returns where an Overlay belongs on the current displays at an Overlay display scale (percent). A
// placement on a display that still exists keeps its position, and its size at 100% under that display's scale times the
// Overlay scale; the 100% size is recomputed from the saved one every time, so 100% -> 150% -> 100% returns the same
// size. A placement on a removed display, or none at all, gets the kind's default corner on the primary display. The
// whole window always stays inside one work area; a scale too large for it is clamped there, never changed.
func placeOverlay(spec overlaySpec, saved *OverlayPlacement, screens []overlayScreen, overlayScale int) OverlayPlacement {
	overlayScale = effectiveOverlayScale(overlayScale)
	shape := spec.at(overlayScale)
	if len(screens) == 0 {
		if saved != nil && effectiveOverlayScale(saved.OverlayScale) == overlayScale {
			return *saved
		}
		return OverlayPlacement{Width: shape.width, Height: shape.height, Scale: 1, OverlayScale: overlayScale}
	}
	chosen := screens[0]
	for _, screen := range screens {
		if screen.Primary {
			chosen = screen
			break
		}
	}
	anchored := false
	logicalWidth, logicalHeight := shape.width, shape.height
	var x, y int
	if saved != nil && saved.Scale > 0 {
		// The DIP size at the saved Overlay scale, converted to this scale.
		ratio := float64(overlayScale) / float64(effectiveOverlayScale(saved.OverlayScale))
		logicalWidth = scaled(saved.Width, ratio/saved.Scale)
		logicalHeight = scaled(saved.Height, ratio/saved.Scale)
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
	logicalWidth = min(max(logicalWidth, shape.minWidth), shape.maxWidth)
	logicalHeight = min(max(logicalHeight, shape.minHeight), shape.maxHeight)
	width, height := scaled(logicalWidth, scale), scaled(logicalHeight, scale)
	// On unchanged scales the saved pixels are exact; converting through DIPs would drift by a pixel on every restart.
	if anchored && saved.Scale == scale && effectiveOverlayScale(saved.OverlayScale) == overlayScale && saved.Width >= width-1 && saved.Width <= width+1 && saved.Height >= height-1 && saved.Height <= height+1 {
		width, height = saved.Width, saved.Height
	}
	width, height = min(width, area.Width), min(height, area.Height)
	return OverlayPlacement{
		X:            max(area.X, min(x, area.X+area.Width-width)),
		Y:            max(area.Y, min(y, area.Y+area.Height-height)),
		Width:        width,
		Height:       height,
		Scale:        scale,
		OverlayScale: overlayScale,
	}
}

// overlayIgnoresMouse reports whether an Overlay window lets mouse input through. Edit Mode always takes the mouse, so the
// window can be moved and resized. Otherwise the click-through preference decides, except that a blank window, one that
// draws nothing, never takes a click: the user cannot see it.
func overlayIgnoresMouse(clickThrough, editing, blank bool) bool {
	return !editing && (clickThrough || blank)
}

// effectsBlank reports whether the Effects Overlay has nothing to draw for a view: both of the engine's rows are empty,
// counting the cells beyond its bound. The frontend draws these same rows, so the window and its content agree.
func effectsBlank(view *capture.ViewSnapshot) bool {
	if view == nil {
		return true
	}
	rows := view.Effects
	return len(rows.Buffs.Effects)+rows.Buffs.Truncated+len(rows.Debuffs.Effects)+rows.Debuffs.Truncated == 0
}

// snapshotSource waits for the first published snapshot after a sequence, as service.Publisher.Next does.
type snapshotSource func(ctx context.Context, after uint64) (*service.Snapshot, error)

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
	if change.Scale != nil {
		prefs.Scale = clampOverlayScale(*change.Scale)
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
	// blank is true while the Overlay has nothing to draw by the published snapshots; only the Effects Overlay follows
	// them. A blank window outside Edit Mode lets the mouse through (overlayIgnoresMouse).
	blank bool
	timer *time.Timer
	// hitWidth and hitHeight are the size the layered hit-test region was last refreshed for.
	hitWidth, hitHeight int
	// scale is the Overlay display scale (percent) the window's size was last laid out for; zero before the first layout.
	scale int
	// base is the placement the user last chose (or the restored one), and rescaled the bounds the last scale change
	// set. While the window still has those bounds, a scale change starts from base again, so rounding never compounds.
	base     *OverlayPlacement
	rescaled application.Rect
	// topmost is the always-on-top state last set on the window, and topmostSet says whether one was set yet.
	topmost, topmostSet bool
	// foregroundAtCreation is the window that held the foreground just before this window was created.
	foregroundAtCreation uintptr
}

// overlayManager owns both Overlay windows. operation serializes requests, which call into the UI thread, and must not be
// taken by a Wails hook; mu guards entry fields and, apart from creating a window, is not held across a window call.
type overlayManager struct {
	app      *application.App
	store    *settingsStore
	notify   func()
	shutting func() bool
	// snapshots are the published snapshots the Overlay windows draw.
	snapshots snapshotSource
	operation sync.Mutex
	mu        sync.Mutex
	entries   map[string]*overlayEntry
	closed    bool
	cancel    context.CancelFunc
}

func newOverlayManager(app *application.App, store *settingsStore, notify func(), shutting func() bool, snapshots snapshotSource) *overlayManager {
	entries := map[string]*overlayEntry{}
	for kind, spec := range overlaySpecs {
		entries[kind] = &overlayEntry{kind: kind, spec: spec}
	}
	return &overlayManager{app: app, store: store, notify: notify, shutting: shutting, snapshots: snapshots, entries: entries}
}

// start opens the Overlays that were visible when the application last closed, watches for display changes and follows
// what the Effects Overlay has to draw.
func (m *overlayManager) start(showAll bool) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.watchDisplays(ctx)
	if m.snapshots != nil {
		go m.followEffects(ctx, m.snapshots)
	}
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

// update applies a request. An opacity or scale change comes from a slider, so it reaches the windows at once while the
// disk write is debounced; every other change is an explicit command and is written immediately.
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
	onlyLook := (change.Opacity != nil || change.Scale != nil) && change.Visible == nil && change.AlwaysOnTop == nil && change.ClickThrough == nil && change.Editing == nil
	var nextEditing bool
	err := m.store.changeOverlay(kind, !onlyLook, func(prefs *OverlayPreferences) { nextEditing = applyOverlayChange(prefs, editing, change) })
	if err != nil {
		return m.states(), err
	}
	if onlyLook {
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
	ready, scale := entry.ready, entry.scale
	m.mu.Unlock()
	if ready {
		entry.window.SetPhysicalBounds(toRect(placeOverlay(entry.spec, nil, m.screens(), scale)))
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
	window, ready, editing, blank := entry.window, entry.ready, entry.editing, entry.blank
	m.mu.Unlock()
	// Before the runtime is ready the request stays in the preferences; onReady applies them.
	if window == nil || !ready {
		return
	}
	// Outside Edit Mode nothing here may activate the window: the foreground window is normally the game. The always-on-top
	// and first-show calls below avoid the Wails methods' activating forms for that reason.
	setOverlayActivation(window, editing)
	m.mu.Lock()
	changeTopmost := !entry.topmostSet || entry.topmost != prefs.AlwaysOnTop
	entry.topmost, entry.topmostSet = prefs.AlwaysOnTop, true
	m.mu.Unlock()
	if changeTopmost {
		setOverlayTopmost(window, prefs.AlwaysOnTop)
	}
	m.rescaleLocked(entry, window, effectiveOverlayScale(prefs.Scale))
	window.SetIgnoreMouseEvents(overlayIgnoresMouse(prefs.ClickThrough, editing, blank))
	// Edge resizing is gated in the window runtime, not with SetResizable. Dropping the native resizable style more than
	// doubled the GPU process's CPU for two live Overlays in repeated measurements, and a frameless window has no native
	// edge to grab either way.
	window.ExecJS(fmt.Sprintf("window._wails.setResizable(%v);", editing))
	switch {
	case !prefs.Visible:
		window.Hide()
	case !window.IsVisible():
		// Settings such as opacity also pass through here. Only a hidden window needs showing.
		if !editing {
			showOverlayInactive(window)
		}
		window.Show()
	}
}

// rescaleLocked resizes the window when its Overlay display scale changed: the size at 100% is kept, the position stays
// anchored, the size limits follow the scale, and the result stays inside one work area. The content follows the same
// scale through CSS zoom, so window bounds, drawn content and hit testing always agree. The caller holds operation.
func (m *overlayManager) rescaleLocked(entry *overlayEntry, window *application.WebviewWindow, scale int) {
	m.mu.Lock()
	previous := entry.scale
	entry.scale = scale
	m.mu.Unlock()
	if previous == scale {
		return
	}
	shape := entry.spec.at(scale)
	if previous == 0 {
		window.SetMinSize(shape.minWidth, shape.minHeight)
		window.SetMaxSize(shape.maxWidth, shape.maxHeight)
		return
	}
	// The bounds are read before any limit changes. Wails resizes a window that a new limit excludes at once, which
	// would look like a user resize here and break the exact return to the remembered size.
	current := m.currentPlacement(window, previous)
	m.mu.Lock()
	source := rescaleSource(entry.base, entry.rescaled, current)
	entry.base = &source
	m.mu.Unlock()
	next := placeOverlay(entry.spec, &source, m.screens(), scale)
	// Both the current and the new bounds lie within the union of the two scales' limits, so neither limit change
	// below resizes the window on its own.
	during := relaxedLimits(entry.spec.at(previous), shape)
	window.SetMinSize(during.minWidth, during.minHeight)
	window.SetMaxSize(during.maxWidth, during.maxHeight)
	if toRect(next) != toRect(current) {
		window.SetPhysicalBounds(toRect(next))
	}
	window.SetMinSize(shape.minWidth, shape.minHeight)
	window.SetMaxSize(shape.maxWidth, shape.maxHeight)
	// The bounds the window really has, so a later change can tell this resize from one by the user.
	actual := window.PhysicalBounds()
	m.mu.Lock()
	entry.rescaled = actual
	m.mu.Unlock()
}

// relaxedLimits admits every size that either shape admits.
func relaxedLimits(a, b overlaySpec) overlaySpec {
	a.minWidth, a.minHeight = min(a.minWidth, b.minWidth), min(a.minHeight, b.minHeight)
	a.maxWidth, a.maxHeight = max(a.maxWidth, b.maxWidth), max(a.maxHeight, b.maxHeight)
	return a
}

// rescaleSource is the placement a scale change starts from: the remembered base while the window still has the bounds
// the previous scale change gave it, otherwise the current bounds, which the user has moved or resized since.
func rescaleSource(base *OverlayPlacement, rescaled application.Rect, current OverlayPlacement) OverlayPlacement {
	if base != nil && toRect(current) == rescaled {
		return *base
	}
	return current
}

// currentPlacement is the window's bounds with the scales they were laid out for.
func (m *overlayManager) currentPlacement(window *application.WebviewWindow, overlayScale int) OverlayPlacement {
	placement := physicalPlacement(window)
	placement.OverlayScale = overlayScale
	return placement
}

// physicalPlacement is a window's bounds in physical pixels with the scale of the display it is on.
func physicalPlacement(window *application.WebviewWindow) OverlayPlacement {
	scale := 1.0
	if screen, err := window.GetScreen(); err == nil && screen != nil && screen.ScaleFactor > 0 {
		scale = float64(screen.ScaleFactor)
	}
	bounds := window.PhysicalBounds()
	return OverlayPlacement{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height, Scale: scale}
}

// createLocked creates the native window. The caller holds mu.
func (m *overlayManager) createLocked(entry *overlayEntry) {
	entry.foregroundAtCreation = foregroundWindow()
	window := m.app.Window.NewWithOptions(overlayWindowOptions(entry.spec.at(m.store.overlay(entry.kind).Scale), "/?view=overlay&surface="+entry.kind+"&"+bootLocaleQuery(m.store)))
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
	previous := entry.foregroundAtCreation
	m.mu.Unlock()
	returnForeground(window, previous)
	prefs := m.store.overlay(entry.kind)
	scale := effectiveOverlayScale(prefs.Scale)
	placed := placeOverlay(entry.spec, prefs.Placement, m.screens(), scale)
	window.SetPhysicalBounds(toRect(placed))
	m.mu.Lock()
	entry.ready = true
	entry.scale = scale
	entry.base, entry.rescaled = &placed, toRect(placed)
	m.mu.Unlock()
	m.applyLocked(entry)
	m.persistSoon(entry)
}

func toRect(placement OverlayPlacement) application.Rect {
	return application.Rect{X: placement.X, Y: placement.Y, Width: placement.Width, Height: placement.Height}
}

func (m *overlayManager) screens() []overlayScreen {
	return displayScreens(m.app)
}

// displayScreens lists the displays in physical pixels, as window placements are kept.
func displayScreens(app *application.App) []overlayScreen {
	var result []overlayScreen
	for _, screen := range app.Screen.GetAll() {
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
	m.mu.Lock()
	overlayScale := m.entries[kind].scale
	m.mu.Unlock()
	placement := m.currentPlacement(window, effectiveOverlayScale(overlayScale))
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
		window, ready, overlayScale := entry.window, entry.ready, entry.scale
		m.mu.Unlock()
		if !ready {
			continue
		}
		current := m.currentPlacement(window, effectiveOverlayScale(overlayScale))
		if fitted := placeOverlay(entry.spec, &current, screens, overlayScale); toRect(fitted) != toRect(current) {
			window.SetPhysicalBounds(toRect(fitted))
		}
	}
}

// followEffects keeps the Effects Overlay's blank state in step with the published snapshots, the same ones its window
// draws, so an Overlay that shows nothing outside Edit Mode never takes a click either. It ends with the manager's context
// or the publisher. A publish only compares one count here; the window is touched when the mouse rule changes.
func (m *overlayManager) followEffects(ctx context.Context, next snapshotSource) {
	var after uint64
	for {
		snapshot, err := next(ctx, after)
		if err != nil || snapshot == nil {
			return
		}
		after = snapshot.Sequence
		m.setBlank(overlayEffects, effectsBlank(snapshot.View))
	}
}

// setBlank records whether an Overlay has anything to draw and updates its window's mouse rule when that changes it.
// followEffects is the only writer, so an unchanged state returns without waiting for a window request in progress.
func (m *overlayManager) setBlank(kind string, blank bool) {
	entry := m.entries[kind]
	m.mu.Lock()
	unchanged := m.closed || entry.blank == blank
	m.mu.Unlock()
	if unchanged {
		return
	}
	m.operation.Lock()
	defer m.operation.Unlock()
	m.mu.Lock()
	if m.closed || entry.blank == blank {
		m.mu.Unlock()
		return
	}
	previous := entry.blank
	entry.blank = blank
	window, ready, editing := entry.window, entry.ready, entry.editing
	m.mu.Unlock()
	if window == nil || !ready {
		return
	}
	clickThrough := m.store.overlay(kind).ClickThrough
	if overlayIgnoresMouse(clickThrough, editing, blank) != overlayIgnoresMouse(clickThrough, editing, previous) {
		window.SetIgnoreMouseEvents(overlayIgnoresMouse(clickThrough, editing, blank))
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
