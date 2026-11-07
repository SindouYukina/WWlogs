package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestAppearanceDefaultsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if theme := effectiveTheme(store.current().Settings); theme != "electro" {
		t.Fatalf("first run theme = %q", theme)
	}
	if _, err := store.setAppearance("not-a-theme", false); err == nil {
		t.Fatal("unknown theme accepted")
	}
	if _, err := store.setAppearance("havoc", true); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog()).current().Settings
	if reloaded.Theme != "havoc" || !reloaded.ReduceMotion {
		t.Fatalf("appearance not persisted: %+v", reloaded)
	}
	// Other preferences have their own updates and must not undo the appearance.
	state, err := store.setDefaultAdapter("127.0.0.1")
	if err != nil || state.Settings.Theme != "havoc" || !state.Settings.ReduceMotion || state.Settings.DefaultAdapterIPv4 != "127.0.0.1" {
		t.Fatalf("adapter update: %+v %v", state, err)
	}
	// A theme written by a newer version loads as the default instead of invalidating every preference.
	if err := os.WriteFile(path, []byte(`{"version":1,"locale":"en","theme":"from-the-future"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	future := loadSettings(path, testLocaleCatalog()).current()
	if future.Warning != "" || effectiveTheme(future.Settings) != "electro" {
		t.Fatalf("future theme: %+v", future)
	}
}

func TestSettingsRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if state := store.current(); state.Warning != "" || state.Settings.Version != 1 {
		t.Fatalf("defaults: %+v", state)
	}
	if _, err := store.setDefaultAdapter("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(path, testLocaleCatalog()).current(); got.Warning != "" || got.Settings.DefaultAdapterIPv4 != "127.0.0.1" {
		t.Fatalf("reload: %+v", got)
	}
	if _, err := store.setDefaultAdapter("not-an-ip"); err == nil {
		t.Fatal("invalid IPv4 accepted")
	}
	if got := loadSettings(path, testLocaleCatalog()).current().Settings; got.DefaultAdapterIPv4 != "127.0.0.1" {
		t.Fatal("failed save replaced preferences")
	}
	if _, err := store.setDefaultAdapter(""); err != nil {
		t.Fatal(err)
	}
	if loadSettings(path, testLocaleCatalog()).current().Settings.DefaultAdapterIPv4 != "" {
		t.Fatal("existing file not replaced")
	}
}

func TestInvalidSettingsRecoverWithoutOverwritingEvidence(t *testing.T) {
	for _, body := range []string{`{`, `{"version":99}`, `{"version":1,"defaultAdapterIPv4":"::1"}`, `{"version":1} {}`} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		state := loadSettings(path, testLocaleCatalog()).current()
		if state.Warning == "" || state.Settings.Locale != "en" || state.Settings.DefaultAdapterIPv4 != "" || state.Settings.Overlays == nil || state.Settings.Overlays.Combat.Visible {
			t.Fatalf("recovery: %+v", state)
		}
		content, _ := os.ReadFile(path)
		if string(content) != body {
			t.Fatal("invalid settings were silently overwritten")
		}
	}
}

func TestSettingsWriteFailureKeepsMemory(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(filepath.Join(blocker, "settings.json"), testLocaleCatalog())
	if _, err := store.setDefaultAdapter("127.0.0.1"); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if store.current().Settings.DefaultAdapterIPv4 != "" {
		t.Fatal("failed write changed preferences")
	}
}

func testLocaleCatalog() localeCatalog {
	var catalog localeCatalog
	_ = json.Unmarshal([]byte(`{"fallbackLocale":"en","supportedLocales":["en","ja"],"languages":{"en":{"label":"English","masterLocale":"en-US"},"ja":{"label":"Japanese","masterLocale":"ja-JP"}}}`), &catalog)
	return catalog
}

func TestDisplayPerformanceDefaultsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	first := store.current().Settings
	if effectiveUpdateInterval(first) != capture.DefaultPublishInterval || effectiveAnimationFPS(first) != 60 {
		t.Fatalf("first run: %v, %d FPS", effectiveUpdateInterval(first), effectiveAnimationFPS(first))
	}
	for _, ms := range []int{0, 100, 249, 300, 1001, -250} {
		if _, err := store.setUpdateInterval(ms); err == nil {
			t.Fatalf("update interval %d accepted", ms)
		}
	}
	for _, fps := range []int{0, 1, 59, 144, 240} {
		if _, err := store.setAnimationFPS(fps); err == nil {
			t.Fatalf("frame rate %d accepted", fps)
		}
	}
	if _, err := store.setAppearance("glacio", true); err != nil {
		t.Fatal(err)
	}
	for _, ms := range []int{250, 500, 750, 1000} {
		state, err := store.setUpdateInterval(ms)
		if err != nil || effectiveUpdateInterval(state.Settings) != time.Duration(ms)*time.Millisecond {
			t.Fatalf("set %d ms: %+v %v", ms, state, err)
		}
	}
	for _, fps := range []int{30, 120, 60, 30} {
		if _, err := store.setAnimationFPS(fps); err != nil {
			t.Fatal(err)
		}
	}
	reloaded := loadSettings(path, testLocaleCatalog()).current().Settings
	if effectiveUpdateInterval(reloaded) != time.Second || effectiveAnimationFPS(reloaded) != 30 || reloaded.Theme != "glacio" || !reloaded.ReduceMotion {
		t.Fatalf("not restored or another preference was overwritten: %+v", reloaded)
	}
	// Other preferences keep the display choices.
	if state, err := store.setLocale("en"); err != nil || state.Settings.UpdateIntervalMs != 1000 || state.Settings.AnimationFPS != 30 {
		t.Fatalf("locale update: %+v %v", state, err)
	}
	// A file written before these settings existed, and values from a newer version, load as defaults without a warning.
	for _, content := range []string{`{"version":1,"locale":"en","theme":"aero"}`, `{"version":1,"locale":"en","theme":"aero","updateIntervalMs":125,"animationFps":240}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		state := loadSettings(path, testLocaleCatalog()).current()
		if state.Warning != "" || state.Settings.Theme != "aero" || effectiveUpdateInterval(state.Settings) != 250*time.Millisecond || effectiveAnimationFPS(state.Settings) != 60 {
			t.Fatalf("%s: %+v", content, state)
		}
	}
	// The fields are optional, so default preferences are written exactly as before and older versions can read them.
	data, err := json.Marshal(Settings{Version: 1, Locale: "en"})
	if err != nil || strings.Contains(string(data), "updateIntervalMs") || strings.Contains(string(data), "animationFps") {
		t.Fatalf("default settings encode the new fields: %s %v", data, err)
	}
}
