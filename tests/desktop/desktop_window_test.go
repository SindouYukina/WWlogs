package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestDesktopWindowFitsEveryCommonWorkArea(t *testing.T) {
	// 1080p, 1440p and 4K at 100, 125, 150 and 200% Windows scaling, each with a 48 DIP taskbar.
	for _, size := range [][2]int{{1920, 1080}, {2560, 1440}, {3840, 2160}} {
		for _, dpi := range []float64{1, 1.25, 1.5, 2} {
			area := application.Rect{Width: int(float64(size[0]) / dpi), Height: int(float64(size[1])/dpi) - 48}
			got := desktopShape.fit(area)
			if got.width > area.Width || got.height > area.Height || got.minWidth > got.width || got.minHeight > got.height {
				t.Fatalf("%dx%d at %.0f%%: %+v does not fit %+v", size[0], size[1], dpi*100, got, area)
			}
			// A work area that holds the default window keeps it unchanged.
			if area.Width >= desktopShape.width && area.Height >= desktopShape.height && got != desktopShape {
				t.Fatalf("%dx%d at %.0f%%: %+v changed although the default fits", size[0], size[1], dpi*100, got)
			}
		}
	}
}

func TestDesktopWindowDefaultIsTheShowcaseSize(t *testing.T) {
	// The owner chose the Showcase review size: a little larger than the 1280 x 820 of earlier versions.
	if want := (desktopWindowShape{width: 1360, height: 880, minWidth: 980, minHeight: 620}); desktopShape != want {
		t.Fatalf("default shape = %+v, want %+v", desktopShape, want)
	}
	// It fits a 1080p display at 100% with a 48 DIP taskbar unchanged.
	if got := desktopShape.fit(application.Rect{Width: 1920, Height: 1080 - 48}); got != desktopShape {
		t.Fatalf("1080p at 100%%: %+v", got)
	}
}

func TestDesktopWindowShrinksOnlyWhereItMust(t *testing.T) {
	// 1080p at 150%: 1280 x 672 DIPs. The window fills the work area instead of hanging off the screen.
	got := desktopShape.fit(application.Rect{Width: 1280, Height: 672})
	if got.width != 1280 || got.height != 672 || got.minWidth != 980 || got.minHeight != 620 {
		t.Fatalf("1080p at 150%%: %+v", got)
	}
	// 1080p at 200%: 960 x 492 DIPs, smaller than the minimum. The minimum follows the work area so the window can fit.
	got = desktopShape.fit(application.Rect{Width: 960, Height: 492})
	if got.width != 960 || got.height != 492 || got.minWidth != 960 || got.minHeight != 492 {
		t.Fatalf("1080p at 200%%: %+v", got)
	}
	if got := desktopShape.fit(application.Rect{}); got != desktopShape {
		t.Fatalf("no work area: %+v", got)
	}
}

var screen4K150 = overlayScreen{Bounds: application.Rect{Width: 3840, Height: 2160}, WorkArea: application.Rect{Width: 3840, Height: 2088}, Scale: 1.5, Primary: true}

func TestRememberedDesktopWindowOpensWhereItWasLeft(t *testing.T) {
	saved := &DesktopPlacement{X: 300, Y: 200, Width: 2100, Height: 1350, Scale: 1.5}
	placed, limits, ok := placeDesktop(saved, []overlayScreen{screen4K150}, desktopShape)
	if !ok || toRect(placed) != (application.Rect{X: 300, Y: 200, Width: 2100, Height: 1350}) || placed.Scale != 1.5 {
		t.Fatalf("same display: ok=%v %+v", ok, placed)
	}
	if limits.minWidth != 980 || limits.minHeight != 620 {
		t.Fatalf("limits = %+v", limits)
	}
}

func TestRememberedDesktopWindowKeepsItsSizeAcrossDisplayScaling(t *testing.T) {
	// Left at 1500 x 900 DIPs on a 100% display; the same display now runs at 150%.
	saved := &DesktopPlacement{X: 100, Y: 100, Width: 1500, Height: 900, Scale: 1}
	placed, _, ok := placeDesktop(saved, []overlayScreen{screen4K150}, desktopShape)
	if !ok || placed.Width != 2250 || placed.Height != 1350 || placed.X != 100 || placed.Y != 100 {
		t.Fatalf("scale change: ok=%v %+v", ok, placed)
	}
}

func TestRememberedDesktopWindowStaysInsideTheWorkArea(t *testing.T) {
	// Left partly below the taskbar: it moves up into the work area and keeps its size.
	saved := &DesktopPlacement{X: 1000, Y: 1500, Width: 2040, Height: 1320, Scale: 1.5}
	placed, _, ok := placeDesktop(saved, []overlayScreen{screen4K150}, desktopShape)
	if !ok || placed.Width != 2040 || placed.Height != 1320 || placed.Y+placed.Height > 2088 || placed.X != 1000 {
		t.Fatalf("below the taskbar: ok=%v %+v", ok, placed)
	}
	// A display too small for the minimum lowers the minimum to what it can hold.
	small := overlayScreen{Bounds: application.Rect{Width: 1920, Height: 1080}, WorkArea: application.Rect{Width: 1920, Height: 984}, Scale: 2, Primary: true}
	placed, limits, ok := placeDesktop(&DesktopPlacement{X: 0, Y: 0, Width: 1960, Height: 1240, Scale: 2}, []overlayScreen{small}, desktopShape)
	if !ok || placed.Width > 1920 || placed.Height > 984 || limits.minWidth != 960 || limits.minHeight != 492 {
		t.Fatalf("small display: ok=%v %+v %+v", ok, placed, limits)
	}
}

func TestDesktopWindowFallsBackToTheDefault(t *testing.T) {
	for name, saved := range map[string]*DesktopPlacement{
		"nothing remembered": nil,
		"display removed":    {X: -3000, Y: 100, Width: 2040, Height: 1320, Scale: 1.5},
		"invalid scale":      {X: 100, Y: 100, Width: 2040, Height: 1320},
		"invalid size":       {X: 100, Y: 100, Width: 0, Height: 1320, Scale: 1.5},
	} {
		if _, _, ok := placeDesktop(saved, []overlayScreen{screen4K150}, desktopShape); ok {
			t.Fatalf("%s: a remembered placement was used", name)
		}
	}
}

func TestDesktopPlacementIsSavedAndRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if store.desktopPlacement() != nil {
		t.Fatal("a new store remembers a Desktop window")
	}
	// The creation size is the default until the user leaves the window somewhere.
	keeper := newDesktopWindowKeeper(nil, store, desktopShape)
	if width, height := keeper.size(); width != 1360 || height != 880 {
		t.Fatalf("default creation size = %dx%d", width, height)
	}
	placement := DesktopPlacement{X: 120, Y: 80, Width: 2400, Height: 1500, Scale: 1.5, Maximized: true}
	if err := store.saveDesktopPlacement(placement); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog())
	if got := reloaded.desktopPlacement(); got == nil || *got != placement {
		t.Fatalf("reloaded placement = %+v", got)
	}
	// The window is created at the remembered size in DIPs, so it does not change size when it is placed.
	if width, height := newDesktopWindowKeeper(nil, reloaded, desktopShape).size(); width != 1600 || height != 1000 {
		t.Fatalf("remembered creation size = %dx%d", width, height)
	}
	// Other preferences are untouched, and a copy cannot change the store.
	if reloaded.overlay(overlayCombat).Opacity != defaultOverlayPreferences(overlayCombat).Opacity {
		t.Fatal("saving the Desktop window changed an Overlay preference")
	}
	reloaded.desktopPlacement().Width = 1
	if reloaded.desktopPlacement().Width != 2400 {
		t.Fatal("the returned placement aliases the store")
	}
	if err := store.saveDesktopPlacement(DesktopPlacement{Width: 100, Height: 100}); err == nil {
		t.Fatal("an invalid placement was saved")
	}
}

func TestInvalidDesktopPlacementInAFileIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	content := `{"version":1,"locale":"en","theme":"aero","desktopWindow":{"x":0,"y":0,"width":-5,"height":900,"scale":1.5}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(path, testLocaleCatalog())
	if store.desktopPlacement() != nil || store.current().Warning != "" || store.current().Settings.Theme != "aero" {
		t.Fatalf("invalid Desktop placement: %+v", store.current())
	}
	// A creation size below the minimum is raised to it.
	if err := store.saveDesktopPlacement(DesktopPlacement{X: 0, Y: 0, Width: 600, Height: 300, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if width, height := newDesktopWindowKeeper(nil, store, desktopShape).size(); width != 980 || height != 620 {
		t.Fatalf("creation size below the minimum = %dx%d", width, height)
	}
}

func TestDefaultDesktopBoundsAreCenteredInThePrimaryWorkArea(t *testing.T) {
	bounds, limits := defaultDesktopBounds(screen4K150, desktopShape)
	if bounds != (application.Rect{X: 900, Y: 384, Width: 2040, Height: 1320}) || limits != desktopShape {
		t.Fatalf("4K at 150%%: %+v %+v", bounds, limits)
	}
	// 1080p at 150%: the default does not fit, so it fills the work area and the minimum stays inside it.
	small := overlayScreen{Bounds: application.Rect{Width: 1920, Height: 1080}, WorkArea: application.Rect{Width: 1920, Height: 1008}, Scale: 1.5, Primary: true}
	bounds, limits = defaultDesktopBounds(small, desktopShape)
	if bounds.Width != 1920 || bounds.Height != 1008 || bounds.X != 0 || bounds.Y != 0 || limits.width != 1280 || limits.height != 672 {
		t.Fatalf("1080p at 150%%: %+v %+v", bounds, limits)
	}
	left := overlayScreen{Bounds: application.Rect{X: -3840, Width: 3840, Height: 2160}, WorkArea: application.Rect{X: -3840, Width: 3840, Height: 2088}, Scale: 1.5}
	if screen, ok := primaryDisplay([]overlayScreen{left, screen4K150}); !ok || !screen.Primary {
		t.Fatalf("primary display = %+v, %v", screen, ok)
	}
	if _, ok := primaryDisplay([]overlayScreen{left}); ok {
		t.Fatal("a display that is not primary was used as the primary one")
	}
}

func TestClearingTheDesktopPlacementRestoresTheDefaultAtNextLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if err := store.saveDesktopPlacement(DesktopPlacement{X: 10, Y: 20, Width: 2400, Height: 1500, Scale: 1.5, Maximized: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.clearDesktopPlacement(); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog())
	if reloaded.desktopPlacement() != nil {
		t.Fatalf("cleared placement came back: %+v", reloaded.desktopPlacement())
	}
	if width, height := newDesktopWindowKeeper(nil, reloaded, desktopShape).size(); width != 1360 || height != 880 {
		t.Fatalf("creation size after a reset = %dx%d", width, height)
	}
	if err := reloaded.clearDesktopPlacement(); err != nil {
		t.Fatalf("clearing twice: %v", err)
	}
}
