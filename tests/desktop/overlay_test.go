package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func inside(p OverlayPlacement, area application.Rect) bool {
	return p.X >= area.X && p.Y >= area.Y && p.X+p.Width <= area.X+area.Width && p.Y+p.Height <= area.Y+area.Height
}

var (
	primary100 = overlayScreen{Bounds: application.Rect{Width: 1920, Height: 1080}, WorkArea: application.Rect{Width: 1920, Height: 1040}, Scale: 1, Primary: true}
	left150    = overlayScreen{Bounds: application.Rect{X: -2560, Y: -200, Width: 2560, Height: 1440}, WorkArea: application.Rect{X: -2560, Y: -200, Width: 2560, Height: 1400}, Scale: 1.5}
)

func TestPlacementWithoutSavedStateUsesPrimaryDisplayCorner(t *testing.T) {
	spec := overlaySpecs[overlayEffects]
	got := placeOverlay(spec, nil, []overlayScreen{left150, primary100}, 100)
	if got.X != spec.offsetX || got.Y != spec.offsetY || got.Width != spec.width || got.Height != spec.height || got.Scale != 1 {
		t.Fatalf("default placement: %+v", got)
	}
}

func TestPlacementKeepsPositionAndLogicalSizeOnSameDisplay(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: -2000, Y: 100, Width: 900, Height: 600, Scale: 1.5}
	got := placeOverlay(spec, &saved, []overlayScreen{primary100, left150}, 100)
	// A placement from before the Overlay scale existed reads as 100%.
	want := saved
	want.OverlayScale = 100
	if got != want {
		t.Fatalf("placement moved: %+v", got)
	}
}

func TestPlacementRescalesWhenDisplayScaleChanged(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: 100, Y: 100, Width: 800, Height: 400, Scale: 2}
	got := placeOverlay(spec, &saved, []overlayScreen{primary100}, 100)
	if got.Width != 400 || got.Height != 200 || got.Scale != 1 {
		t.Fatalf("logical size not preserved: %+v", got)
	}
}

func TestPlacementRecoversFromRemovedDisplay(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: -2000, Y: 100, Width: 900, Height: 600, Scale: 1.5}
	got := placeOverlay(spec, &saved, []overlayScreen{primary100}, 100)
	if !inside(got, primary100.WorkArea) || got.X != spec.offsetX || got.Y != spec.offsetY || got.Width != 600 || got.Height != 400 {
		t.Fatalf("removed display: %+v", got)
	}
}

func TestPlacementStaysInsideWorkArea(t *testing.T) {
	small := overlayScreen{Bounds: application.Rect{Width: 800, Height: 300}, WorkArea: application.Rect{Y: 40, Width: 800, Height: 260}, Scale: 1, Primary: true}
	spec := overlaySpecs[overlayEffects]
	for _, saved := range []OverlayPlacement{
		{X: 790, Y: 290, Width: 320, Height: 420, Scale: 1},
		{X: 0, Y: 0, Width: 5000, Height: 5000, Scale: 1},
		{X: -40, Y: 10, Width: 320, Height: 200, Scale: 1},
	} {
		if got := placeOverlay(spec, &saved, []overlayScreen{small}, 100); !inside(got, small.WorkArea) {
			t.Fatalf("%+v -> %+v", saved, got)
		}
	}
}

func TestPlacementWithInvalidScaleUsesDefault(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: 500, Y: 500, Width: 500, Height: 300}
	if got := placeOverlay(spec, &saved, []overlayScreen{primary100}, 100); got.X != spec.offsetX || got.Y != spec.offsetY {
		t.Fatalf("invalid scale: %+v", got)
	}
}

func TestEditModeCannotBeLeftOnHiddenWindow(t *testing.T) {
	yes, no := true, false
	prefs := defaultOverlayPreferences(overlayCombat)
	editing := applyOverlayChange(&prefs, false, OverlayChange{Editing: &yes})
	if !editing || !prefs.Visible {
		t.Fatalf("entering Edit Mode must show the window: %+v %v", prefs, editing)
	}
	editing = applyOverlayChange(&prefs, editing, OverlayChange{Visible: &no})
	if editing || prefs.Visible {
		t.Fatalf("hiding must leave Edit Mode: %+v %v", prefs, editing)
	}
	// Click-through stays a preference through Edit Mode, so leaving it restores Normal Mode as it was.
	prefs.ClickThrough = true
	editing = applyOverlayChange(&prefs, false, OverlayChange{Editing: &yes})
	if !prefs.ClickThrough || prefs.state(editing).ClickThrough != true || !prefs.state(editing).Editing {
		t.Fatalf("Edit Mode changed the click-through preference: %+v", prefs)
	}
	opacity := 500
	applyOverlayChange(&prefs, editing, OverlayChange{Opacity: &opacity})
	if prefs.Opacity != maxOverlayOpacity {
		t.Fatalf("opacity not clamped: %d", prefs.Opacity)
	}
}

func TestOverlayPreferencesAreIndependentAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	first := store.overlay(overlayCombat)
	if !first.AlwaysOnTop || !first.ClickThrough || first.Visible || first.Opacity == 0 || first.Placement != nil {
		t.Fatalf("defaults: %+v", first)
	}
	placement := OverlayPlacement{X: 10, Y: 20, Width: 300, Height: 200, Scale: 1.5}
	if err := store.saveOverlayPlacement(overlayEffects, placement); err != nil {
		t.Fatal(err)
	}
	if err := store.changeOverlay(overlayCombat, true, func(prefs *OverlayPreferences) { prefs.Opacity, prefs.Visible, prefs.ClickThrough = 40, true, false }); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog())
	combat, effects := reloaded.overlay(overlayCombat), reloaded.overlay(overlayEffects)
	if combat.Opacity != 40 || !combat.Visible || combat.ClickThrough || combat.Placement != nil {
		t.Fatalf("combat: %+v", combat)
	}
	if effects.Visible || !effects.ClickThrough || effects.Opacity != overlaySpecs[overlayEffects].defaultOpacity || effects.Placement == nil || *effects.Placement != placement {
		t.Fatalf("effects changed by the combat update: %+v", effects)
	}
	copied := reloaded.overlay(overlayEffects)
	copied.Placement.X = 999
	if reloaded.overlay(overlayEffects).Placement.X != placement.X {
		t.Fatal("overlay copy mutated the store")
	}
}

func TestSliderOpacityIsHeldUntilFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if err := store.changeOverlay(overlayCombat, false, func(prefs *OverlayPreferences) { prefs.Opacity = 33 }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a slider change wrote to disk")
	}
	if store.overlay(overlayCombat).Opacity != 33 {
		t.Fatal("slider value not visible to windows")
	}
	if err := store.flush(); err != nil {
		t.Fatal(err)
	}
	if loadSettings(path, testLocaleCatalog()).overlay(overlayCombat).Opacity != 33 {
		t.Fatal("flush lost the slider value")
	}
}

func TestV05SettingsMigrateVisibilityButNotGeometry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"version":1,"defaultAdapterIPv4":"127.0.0.1","showOverlayOnStartup":true,"locale":"ja","overlay":{"x":5,"y":6,"width":440,"height":640},"theme":"aero"}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(path, testLocaleCatalog())
	state := store.current()
	if state.Warning != "" || state.Settings.Locale != "ja" || state.Settings.Theme != "aero" || state.Settings.DefaultAdapterIPv4 != "127.0.0.1" {
		t.Fatalf("legacy preferences lost: %+v", state)
	}
	combat, effects := store.overlay(overlayCombat), store.overlay(overlayEffects)
	if !combat.Visible || combat.Placement != nil || effects.Visible {
		t.Fatalf("migration: %+v %+v", combat, effects)
	}
	if _, err := store.setDefaultAdapter("127.0.0.2"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var written map[string]any
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if _, stale := written["showOverlayOnStartup"]; stale {
		t.Fatal("the legacy field was written back")
	}
	if _, stale := written["overlay"]; stale {
		t.Fatal("the legacy geometry was written back")
	}
}

func TestInvalidOverlayPlacementResetsWithoutLosingOtherPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"version":1,"locale":"en","theme":"havoc","overlays":{"combat":{"visible":true,"opacity":55,"alwaysOnTop":false,"clickThrough":false,"placement":{"x":1,"y":2,"width":-5,"height":10,"scale":1}},"effects":{"visible":false,"opacity":500,"alwaysOnTop":true,"clickThrough":true,"placement":{"x":1,"y":2,"width":0,"height":10,"scale":1}}}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(path, testLocaleCatalog())
	combat, effects := store.overlay(overlayCombat), store.overlay(overlayEffects)
	if store.current().Warning != "overlay_geometry_reset" || combat.Placement != nil || effects.Placement != nil || combat.Opacity != 55 || combat.AlwaysOnTop || effects.Opacity != maxOverlayOpacity {
		t.Fatalf("recovery: %+v %+v %+v", store.current(), combat, effects)
	}
}

func TestAutomaticPlacementSaveKeepsCorruptSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(path, testLocaleCatalog())
	if err := store.saveOverlayPlacement(overlayCombat, OverlayPlacement{Width: 10, Height: 10, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.flush(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{" {
		t.Fatal("automatic persistence overwrote corrupt settings")
	}
	if store.overlay(overlayCombat).Placement == nil {
		t.Fatal("placement should last for this session")
	}
}

func TestShellSignalWakesWaitersOnce(t *testing.T) {
	var signal shellSignal
	waiter := signal.changed()
	select {
	case <-waiter:
		t.Fatal("signal fired early")
	default:
	}
	signal.fire()
	select {
	case <-waiter:
	default:
		t.Fatal("signal did not wake the waiter")
	}
	next := signal.changed()
	select {
	case <-next:
		t.Fatal("a new waiter must wait for the next change")
	default:
	}
	signal.fire()
	signal.fire()
}

func TestOverlayScaleMultipliesTheDefaultAndKeepsTheCorner(t *testing.T) {
	spec := overlaySpecs[overlayEffects]
	for _, scale := range []int{75, 100, 125, 150, 200} {
		got := placeOverlay(spec, nil, []overlayScreen{primary100}, scale)
		wantW, wantH := scaled(spec.width, float64(scale)/100), scaled(spec.height, float64(scale)/100)
		// 200% (840 px) no longer fits below the default corner of a 1040 px work area, so it moves up to stay inside.
		wantY := min(spec.offsetY, primary100.WorkArea.Height-wantH)
		if got.Width != wantW || got.Height != wantH || got.X != spec.offsetX || got.Y != wantY || got.OverlayScale != scale || !inside(got, primary100.WorkArea) {
			t.Fatalf("scale %d: %+v, want %dx%d at (%d, %d)", scale, got, wantW, wantH, spec.offsetX, wantY)
		}
	}
	// Windows display scaling multiplies on top; the two are never folded together.
	got := placeOverlay(overlaySpecs[overlayCombat], nil, []overlayScreen{left150}, 150)
	if got.Width != scaled(486, 1.5*1.5) || got.Height != scaled(268, 1.5*1.5) || got.Scale != 1.5 || got.OverlayScale != 150 {
		t.Fatalf("150%% Overlay on a 150%% display: %+v", got)
	}
}

func TestOverlayScaleChangesNeverCompound(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	// A user size of 520 x 300 DIP at 100% on a 125% display, then a round of scale changes.
	saved := OverlayPlacement{X: 200, Y: 120, Width: 650, Height: 375, Scale: 1.25, OverlayScale: 100}
	screen := overlayScreen{Bounds: application.Rect{Width: 2560, Height: 1440}, WorkArea: application.Rect{Width: 2560, Height: 1400}, Scale: 1.25, Primary: true}
	// The manager rescales from the remembered base while the window keeps the bounds the last change gave it.
	base, rescaled := saved, toRect(saved)
	current := saved
	for _, scale := range []int{150, 83, 137, 200, 75, 100} {
		source := rescaleSource(&base, rescaled, current)
		base = source
		current = placeOverlay(spec, &source, []overlayScreen{screen}, scale)
		rescaled = toRect(current)
		if current.X != saved.X || current.Y != saved.Y || current.OverlayScale != scale {
			t.Fatalf("scale %d moved the window: %+v", scale, current)
		}
		wantW := scaled(520, 1.25*float64(scale)/100)
		if current.Width < wantW-1 || current.Width > wantW+1 {
			t.Fatalf("scale %d: width %d, want %d +-1", scale, current.Width, wantW)
		}
	}
	if current.Width != saved.Width || current.Height != saved.Height {
		t.Fatalf("100%% -> ... -> 100%% did not return to the user's size: %+v", current)
	}
}

func TestOverlayScaleAcrossResolutionsAndDisplayScales(t *testing.T) {
	// 1080p, 1440p and 4K at 100, 125, 150 and 200% Windows scaling, each with a 48 DIP taskbar at the bottom.
	for _, size := range [][2]int{{1920, 1080}, {2560, 1440}, {3840, 2160}} {
		for _, dpi := range []float64{1, 1.25, 1.5, 2} {
			taskbar := scaled(48, dpi)
			screen := overlayScreen{Bounds: application.Rect{Width: size[0], Height: size[1]}, WorkArea: application.Rect{Width: size[0], Height: size[1] - taskbar}, Scale: dpi, Primary: true}
			for _, kind := range []string{overlayCombat, overlayEffects} {
				spec := overlaySpecs[kind]
				for _, scale := range []int{75, 100, 125, 150, 200} {
					got := placeOverlay(spec, nil, []overlayScreen{screen}, scale)
					// The physical size is the default times the Overlay scale times the display scale, once each, unless
					// the work area is smaller; the window then fills it and the chosen scale is kept.
					wantW := min(scaled(scaled(spec.width, float64(scale)/100), dpi), screen.WorkArea.Width)
					wantH := min(scaled(scaled(spec.height, float64(scale)/100), dpi), screen.WorkArea.Height)
					if got.Width != wantW || got.Height != wantH || got.OverlayScale != scale || got.Scale != dpi || !inside(got, screen.WorkArea) {
						t.Fatalf("%dx%d at %.0f%%, %s at %d%%: %+v, want %dx%d", size[0], size[1], dpi*100, kind, scale, got, wantW, wantH)
					}
				}
			}
		}
	}
}

func TestOverlayScaleLimitsFollowTheScaleAndTheWorkArea(t *testing.T) {
	spec := overlaySpecs[overlayEffects]
	// The 100% minimum 220 x 120 is 165 x 90 at 75%; a saved minimum-size window keeps it.
	tiny := OverlayPlacement{X: 10, Y: 10, Width: 220, Height: 120, Scale: 1, OverlayScale: 100}
	if got := placeOverlay(spec, &tiny, []overlayScreen{primary100}, 75); got.Width != 165 || got.Height != 90 {
		t.Fatalf("minimum at 75%%: %+v", got)
	}
	// 200% of a tall window does not fit a 1080p work area: it is clamped there and stays reachable; the scale stays.
	tall := OverlayPlacement{X: 1500, Y: 600, Width: 400, Height: 900, Scale: 1, OverlayScale: 100}
	got := placeOverlay(spec, &tall, []overlayScreen{primary100}, 200)
	if !inside(got, primary100.WorkArea) || got.Height != primary100.WorkArea.Height || got.Width != 800 || got.OverlayScale != 200 {
		t.Fatalf("200%% on 1080p: %+v", got)
	}
}

func TestOverlayScaleSettingIsClampedPersistedAndDefaulted(t *testing.T) {
	prefs := defaultOverlayPreferences(overlayEffects)
	if prefs.state(false).Scale != 100 {
		t.Fatalf("default scale = %d", prefs.state(false).Scale)
	}
	for value, want := range map[int]int{75: 75, 83: 83, 137: 137, 200: 200, 74: 75, 201: 200, -5: 75, 0: 75} {
		v := value
		applyOverlayChange(&prefs, false, OverlayChange{Scale: &v})
		if prefs.Scale != want {
			t.Fatalf("scale %d -> %d, want %d", value, prefs.Scale, want)
		}
	}
	// An older file has no scale and no placement scale: both read as 100%, and other preferences survive.
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"version":1,"locale":"en","overlays":{"combat":{"visible":true,"opacity":70,"alwaysOnTop":true,"clickThrough":false,"placement":{"x":10,"y":20,"width":486,"height":268,"scale":1}},"effects":{"visible":false,"opacity":55,"alwaysOnTop":true,"clickThrough":true}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(path, testLocaleCatalog())
	combat := store.overlay(overlayCombat)
	if combat.state(false).Scale != 100 || combat.Opacity != 70 || combat.Placement == nil || combat.Placement.Width != 486 {
		t.Fatalf("legacy file: %+v", combat)
	}
	scale := 137
	if err := store.changeOverlay(overlayEffects, true, func(p *OverlayPreferences) { applyOverlayChange(p, false, OverlayChange{Scale: &scale}) }); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog())
	if reloaded.overlay(overlayEffects).state(false).Scale != 137 || reloaded.overlay(overlayCombat).state(false).Scale != 100 {
		t.Fatalf("scales are independent and persisted: %+v / %+v", reloaded.overlay(overlayEffects), reloaded.overlay(overlayCombat))
	}
	var raw map[string]any
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &raw)
	if raw["overlays"].(map[string]any)["effects"].(map[string]any)["scale"] != float64(137) {
		t.Fatalf("scale not written: %s", data)
	}
	// A placement scale outside the supported range is not trusted.
	if validPlacement(&OverlayPlacement{Width: 10, Height: 10, Scale: 1, OverlayScale: 500}) {
		t.Fatal("out-of-range placement scale accepted")
	}
}

func TestRescaleLimitsAdmitBothScales(t *testing.T) {
	// Wails resizes a window that a new limit excludes, so a scale change first widens the limits to admit both sizes.
	spec := overlaySpecs[overlayCombat]
	for _, pair := range [][2]int{{200, 75}, {75, 137}, {100, 200}, {150, 100}} {
		from, to := spec.at(pair[0]), spec.at(pair[1])
		during := relaxedLimits(from, to)
		for _, shape := range []overlaySpec{from, to} {
			if shape.minWidth < during.minWidth || shape.minHeight < during.minHeight || shape.maxWidth > during.maxWidth || shape.maxHeight > during.maxHeight {
				t.Fatalf("%d%% -> %d%%: %+v does not admit %+v", pair[0], pair[1], during, shape)
			}
		}
	}
}

func TestRescaleStartsOverWhenTheUserMovedOrResized(t *testing.T) {
	base := OverlayPlacement{X: 100, Y: 100, Width: 486, Height: 268, Scale: 1, OverlayScale: 100}
	rescaled := application.Rect{X: 100, Y: 100, Width: 729, Height: 402}
	untouched := OverlayPlacement{X: 100, Y: 100, Width: 729, Height: 402, Scale: 1, OverlayScale: 150}
	if got := rescaleSource(&base, rescaled, untouched); got != base {
		t.Fatalf("an untouched window must rescale from its base: %+v", got)
	}
	resized := untouched
	resized.Width = 800
	if got := rescaleSource(&base, rescaled, resized); got != resized {
		t.Fatalf("a user resize becomes the new base: %+v", got)
	}
	moved := untouched
	moved.X = 300
	if got := rescaleSource(&base, rescaled, moved); got != moved {
		t.Fatalf("a user move becomes the new base: %+v", got)
	}
	if got := rescaleSource(nil, rescaled, untouched); got != untouched {
		t.Fatalf("without a base the current bounds are used: %+v", got)
	}
}
