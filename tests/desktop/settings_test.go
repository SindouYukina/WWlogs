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

// Every official theme, Spectro included, survives a restart; a settings file written before Spectro existed loads as it
// is and switches to it without being reset.
func TestEveryThemePersistsAndEarlierSettingsSwitchToSpectro(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	for _, theme := range []string{"electro", "aero", "havoc", "glacio", "fusion", "spectro"} {
		if _, err := loadSettings(path, testLocaleCatalog()).setAppearance(theme, false); err != nil {
			t.Fatalf("%s: %v", theme, err)
		}
		reloaded := loadSettings(path, testLocaleCatalog()).current()
		if reloaded.Warning != "" || reloaded.Settings.Theme != theme || effectiveTheme(reloaded.Settings) != theme {
			t.Fatalf("%s not restored: %+v", theme, reloaded)
		}
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"locale":"ja","theme":"fusion","reduceMotion":true,"updateIntervalMs":500}`), 0o600); err != nil {
		t.Fatal(err)
	}
	earlier := loadSettings(path, testLocaleCatalog())
	if state := earlier.current(); state.Warning != "" || effectiveTheme(state.Settings) != "fusion" || !state.Settings.ReduceMotion || state.Settings.UpdateIntervalMs != 500 {
		t.Fatalf("earlier settings: %+v", state)
	}
	if _, err := earlier.setAppearance("spectro", true); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog()).current().Settings
	if reloaded.Theme != "spectro" || !reloaded.ReduceMotion || reloaded.Locale != "ja" || reloaded.UpdateIntervalMs != 500 {
		t.Fatalf("switch to spectro kept other preferences: %+v", reloaded)
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

func TestUpdateIntervalDefaultsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if got := effectiveUpdateInterval(store.current().Settings); got != capture.DefaultPublishInterval {
		t.Fatalf("first run: %v", got)
	}
	for _, ms := range []int{0, 100, 249, 300, 1001, -250} {
		if _, err := store.setUpdateInterval(ms); err == nil {
			t.Fatalf("update interval %d accepted", ms)
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
	reloaded := loadSettings(path, testLocaleCatalog()).current().Settings
	if effectiveUpdateInterval(reloaded) != time.Second || reloaded.Theme != "glacio" || !reloaded.ReduceMotion {
		t.Fatalf("not restored or another preference was overwritten: %+v", reloaded)
	}
	// Other preferences keep the interval.
	if state, err := store.setLocale("en"); err != nil || state.Settings.UpdateIntervalMs != 1000 {
		t.Fatalf("locale update: %+v %v", state, err)
	}
	// A file written before the setting existed, and a value from a newer version, load as the default without a warning.
	for _, content := range []string{`{"version":1,"locale":"en","theme":"aero"}`, `{"version":1,"locale":"en","theme":"aero","updateIntervalMs":125}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		state := loadSettings(path, testLocaleCatalog()).current()
		if state.Warning != "" || state.Settings.Theme != "aero" || effectiveUpdateInterval(state.Settings) != 250*time.Millisecond {
			t.Fatalf("%s: %+v", content, state)
		}
	}
	// The field is optional, so default preferences are written exactly as before and older versions can read them.
	data, err := json.Marshal(Settings{Version: 1, Locale: "en"})
	if err != nil || strings.Contains(string(data), "updateIntervalMs") {
		t.Fatalf("default settings encode the interval: %s %v", data, err)
	}
}

// Builds before the frame-rate setting was retired saved animationFps. Such a file loads without a warning, keeps
// every other preference, and the next save drops the retired field instead of failing or keeping a dead value.
func TestRetiredAnimationFPSFieldLoadsAndIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"version":1,"defaultAdapterIPv4":"127.0.0.1","locale":"ja","theme":"havoc","reduceMotion":true,"updateIntervalMs":750,"animationFps":30,` +
		`"overlays":{"combat":{"visible":true,"opacity":55,"alwaysOnTop":true,"clickThrough":false},"effects":{"visible":false,"opacity":40,"alwaysOnTop":false,"clickThrough":true}}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(path, testLocaleCatalog())
	state := store.current()
	want := func(s Settings) bool {
		return s.DefaultAdapterIPv4 == "127.0.0.1" && s.Locale == "ja" && s.Theme == "havoc" && s.ReduceMotion && s.UpdateIntervalMs == 750 &&
			s.Overlays.Combat.Visible && s.Overlays.Combat.Opacity == 55 && !s.Overlays.Combat.ClickThrough && s.Overlays.Effects.Opacity == 40 && !s.Overlays.Effects.AlwaysOnTop
	}
	if state.Warning != "" || !want(state.Settings) {
		t.Fatalf("old file: %+v", state)
	}
	if _, err := store.setUpdateInterval(500); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "animationFps") {
		t.Fatalf("the retired field was written again: %s %v", data, err)
	}
	reloaded := loadSettings(path, testLocaleCatalog()).current().Settings
	reloaded.UpdateIntervalMs = 750
	if !want(reloaded) {
		t.Fatalf("a save lost another preference: %+v", reloaded)
	}
}
