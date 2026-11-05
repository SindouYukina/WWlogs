package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user", "settings.json")
	store := loadSettings(path, testLocaleCatalog())
	if state := store.current(); state.Warning != "" || state.Settings.Version != 1 {
		t.Fatalf("defaults: %+v", state)
	}
	wanted := Settings{Version: 1, Locale: "en", DefaultAdapterIPv4: "127.0.0.1", ShowOverlayOnStartup: true}
	if _, err := store.save(wanted); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(path, testLocaleCatalog()).current(); got.Warning != "" || got.Settings != wanted {
		t.Fatalf("reload: %+v", got)
	}
	invalid := wanted
	invalid.DefaultAdapterIPv4 = "not-an-ip"
	if _, err := store.save(invalid); err == nil {
		t.Fatal("invalid IPv4 accepted")
	}
	if got := loadSettings(path, testLocaleCatalog()).current().Settings; got != wanted {
		t.Fatal("failed save replaced preferences")
	}
	wanted.ShowOverlayOnStartup = false
	if _, err := store.save(wanted); err != nil {
		t.Fatal(err)
	}
	if loadSettings(path, testLocaleCatalog()).current().Settings != wanted {
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
		if state.Warning == "" || state.Settings != (Settings{Version: 1, Locale: "en"}) {
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
	before := store.current()
	if _, err := store.save(Settings{Version: 1, ShowOverlayOnStartup: true}); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if store.current() != before {
		t.Fatal("failed write changed preferences")
	}
}

func testLocaleCatalog() localeCatalog {
	var catalog localeCatalog
	_ = json.Unmarshal([]byte(`{"fallbackLocale":"en","supportedLocales":["en","ja"],"languages":{"en":{"label":"English","masterLocale":"en-US"},"ja":{"label":"Japanese","masterLocale":"ja-JP"}}}`), &catalog)
	return catalog
}
