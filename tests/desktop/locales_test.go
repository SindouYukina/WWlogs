package desktop

import (
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
	state, err := store.setLocale("ja")
	if err != nil || state.LocaleRevision != 2 {
		t.Fatalf("locale revision: %+v %v", state, err)
	}
	if _, err := store.setDefaultAdapter("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog()).current()
	if reloaded.Settings.Locale != "ja" {
		t.Fatal("another preference update overwrote the locale")
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
