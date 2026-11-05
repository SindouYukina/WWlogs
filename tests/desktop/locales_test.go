package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestLocaleSettingsMigrationRevisionAndGeometry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"version":1,"locale":"unsupported","defaultAdapterIPv4":""}`)
	os.WriteFile(path, original, 0600)
	store := loadSettings(path, testLocaleCatalog())
	if store.current().Settings.Locale != "en" {
		t.Fatal("invalid locale did not fall back")
	}
	if _, err := store.setLocale("external-pack"); err == nil {
		t.Fatal("external locale accepted")
	}
	stale := store.current().Settings
	state, err := store.setLocale("ja")
	if err != nil || state.LocaleRevision != 2 {
		t.Fatalf("locale revision: %+v %v", state, err)
	}
	bounds := OverlayBounds{X: -100, Y: 30, Width: 440, Height: 640}
	if err := store.setOverlay(bounds); err != nil {
		t.Fatal(err)
	}
	if _, err := store.save(stale); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog()).current()
	if reloaded.Settings.Locale != "ja" || reloaded.Settings.Overlay == nil || *reloaded.Settings.Overlay != bounds {
		t.Fatal("stale form overwrote locale or geometry")
	}
	copy := store.current()
	copy.Settings.Overlay.X = 1000
	if store.current().Settings.Overlay.X != bounds.X {
		t.Fatal("settings copy mutated store")
	}
	os.WriteFile(path, []byte("{"), 0600)
	broken := loadSettings(path, testLocaleCatalog())
	if err := broken.setOverlay(bounds); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{" {
		t.Fatal("automatic geometry persistence overwrote corrupt settings")
	}
}

func TestOverlayBoundsStayReachable(t *testing.T) {
	areas := []application.Rect{{X: 0, Y: 0, Width: 1280, Height: 720}, {X: -1920, Y: -100, Width: 1920, Height: 1080}}
	for _, input := range []OverlayBounds{{X: 9000, Y: 9000, Width: 800, Height: 1000}, {X: -1800, Y: 10, Width: 440, Height: 640}} {
		result := fitOverlay(input, areas)
		reachable := false
		for _, area := range areas {
			if result.X >= area.X && result.Y >= area.Y && result.X+result.Width <= area.X+area.Width && result.Y+result.Height <= area.Y+area.Height {
				reachable = true
			}
		}
		if !reachable {
			t.Fatalf("unreachable: %+v", result)
		}
	}
}

func TestLocaleManifestComesFromBundledAssets(t *testing.T) {
	if _, err := readLocaleCatalog(fstest.MapFS{}); err == nil {
		t.Fatal("missing bundled manifest accepted")
	}
	assets := fstest.MapFS{"locales/manifest.json": &fstest.MapFile{Data: []byte(`{"fallbackLocale":"en","supportedLocales":["en"],"languages":{"en":{"masterLocale":"en-US","label":"English"}}}`)}}
	catalog, err := readLocaleCatalog(assets)
	if err != nil || !catalog.valid("en") || catalog.valid("ja") {
		t.Fatal("manifest is not the language authority")
	}
}
