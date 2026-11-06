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
	got := placeOverlay(spec, nil, []overlayScreen{left150, primary100})
	if got.X != spec.offsetX || got.Y != spec.offsetY || got.Width != spec.width || got.Height != spec.height || got.Scale != 1 {
		t.Fatalf("default placement: %+v", got)
	}
}

func TestPlacementKeepsPositionAndLogicalSizeOnSameDisplay(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: -2000, Y: 100, Width: 900, Height: 600, Scale: 1.5}
	got := placeOverlay(spec, &saved, []overlayScreen{primary100, left150})
	if got != saved {
		t.Fatalf("placement moved: %+v", got)
	}
}

func TestPlacementRescalesWhenDisplayScaleChanged(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: 100, Y: 100, Width: 800, Height: 400, Scale: 2}
	got := placeOverlay(spec, &saved, []overlayScreen{primary100})
	if got.Width != 400 || got.Height != 200 || got.Scale != 1 {
		t.Fatalf("logical size not preserved: %+v", got)
	}
}

func TestPlacementRecoversFromRemovedDisplay(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: -2000, Y: 100, Width: 900, Height: 600, Scale: 1.5}
	got := placeOverlay(spec, &saved, []overlayScreen{primary100})
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
		if got := placeOverlay(spec, &saved, []overlayScreen{small}); !inside(got, small.WorkArea) {
			t.Fatalf("%+v -> %+v", saved, got)
		}
	}
}

func TestPlacementWithInvalidScaleUsesDefault(t *testing.T) {
	spec := overlaySpecs[overlayCombat]
	saved := OverlayPlacement{X: 500, Y: 500, Width: 500, Height: 300}
	if got := placeOverlay(spec, &saved, []overlayScreen{primary100}); got.X != spec.offsetX || got.Y != spec.offsetY {
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
