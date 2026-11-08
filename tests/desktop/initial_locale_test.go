package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fullLocaleCatalog() localeCatalog {
	language := func(master string) struct {
		Label        string `json:"label"`
		MasterLocale string `json:"masterLocale"`
	} {
		return struct {
			Label        string `json:"label"`
			MasterLocale string `json:"masterLocale"`
		}{Label: master, MasterLocale: master}
	}
	catalog := localeCatalog{FallbackLocale: "en", SupportedLocales: []string{"zh-TW", "zh-CN", "en", "ja", "ko"}}
	catalog.Languages = map[string]struct {
		Label        string `json:"label"`
		MasterLocale string `json:"masterLocale"`
	}{"zh-TW": language("zh-TW"), "zh-CN": language("zh-CN"), "en": language("en-US"), "ja": language("ja-JP"), "ko": language("ko-KR")}
	return catalog
}

func TestInitialLocaleMapsWindowsLanguages(t *testing.T) {
	cases := []struct {
		name      string
		preferred []string
		want      string
	}{
		{"zh-TW", []string{"zh-TW"}, "zh-TW"},
		{"zh-HK", []string{"zh-HK"}, "zh-TW"},
		{"zh-MO", []string{"zh-MO"}, "zh-TW"},
		{"zh-Hant", []string{"zh-Hant"}, "zh-TW"},
		{"zh-CN", []string{"zh-CN"}, "zh-CN"},
		{"zh-SG", []string{"zh-SG"}, "zh-CN"},
		{"zh-Hans", []string{"zh-Hans"}, "zh-CN"},
		{"ja", []string{"ja"}, "ja"},
		{"ja-JP", []string{"ja-JP"}, "ja"},
		{"ko", []string{"ko"}, "ko"},
		{"ko-KR", []string{"ko-KR"}, "ko"},
		{"en-US", []string{"en-US"}, "en"},
		{"en-GB", []string{"en-GB"}, "en"},
		{"en", []string{"en"}, "en"},
		{"de-DE", []string{"de-DE"}, "en"},
		{"fr-FR", []string{"fr-FR"}, "en"},
		{"script wins over region", []string{"zh-Hant-SG"}, "zh-TW"},
		{"script wins over region, simplified", []string{"zh-Hans-HK"}, "zh-CN"},
		{"script with region", []string{"zh-Hant-TW"}, "zh-TW"},
		{"legacy traditional", []string{"zh-CHT"}, "zh-TW"},
		{"legacy simplified", []string{"zh-CHS"}, "zh-CN"},
		{"case", []string{"ZH-hk"}, "zh-TW"},
		{"lower script", []string{"zh-hant"}, "zh-TW"},
		{"underscore", []string{"zh_TW"}, "zh-TW"},
		{"windows sort suffix", []string{"zh-TW_radstr"}, "zh-TW"},
		{"japanese sort suffix", []string{"ja-JP_radstr"}, "ja"},
		{"unicode extension", []string{"ja-JP-u-ca-japanese"}, "ja"},
		{"spaces", []string{"  ko-KR  "}, "ko"},
		{"bare zh alone is undecided", []string{"zh"}, "en"},
		{"bare zh consults the next preference", []string{"zh", "ja-JP"}, "ja"},
		{"unknown region of zh is undecided", []string{"zh-US", "ko-KR"}, "ko"},
		{"order is the user's", []string{"ja-JP", "en-US"}, "ja"},
		{"an unsupported first preference is skipped", []string{"de-DE", "zh-HK", "en-US"}, "zh-TW"},
		{"english before a later match", []string{"en-GB", "ja-JP"}, "en"},
		{"all unsupported", []string{"de-DE", "fr-FR", "es-ES"}, "en"},
		{"no languages", nil, "en"},
		{"empty and pseudo", []string{"", "qps-ploc"}, "en"},
	}
	catalog := fullLocaleCatalog()
	for _, tc := range cases {
		if got := initialLocaleFor(tc.preferred, catalog); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWindowsReportsTheDisplayLanguages(t *testing.T) {
	languages := userPreferredUILanguages()
	if len(languages) == 0 || strings.TrimSpace(languages[0]) == "" {
		t.Fatalf("no preferred UI language: %q", languages)
	}
}

func seededStore(t *testing.T, body *string) (*settingsStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if body != nil {
		if err := os.WriteFile(path, []byte(*body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return loadSettings(path, fullLocaleCatalog()), path
}

func TestFirstLaunchStartsOnAutomaticAndFollowsWindows(t *testing.T) {
	store, path := seededStore(t, nil)
	store.seedInitialLocale([]string{"ja-JP"})
	if got := store.current().Settings; got.Locale != "ja" || got.LanguagePreference != languagePreferenceAuto {
		t.Fatalf("first launch = %+v", got)
	}
	// Automatic is what was saved, so a later change of the Windows language applies at the next launch.
	reloaded := loadSettings(path, fullLocaleCatalog())
	reloaded.seedInitialLocale([]string{"ko-KR"})
	if got := reloaded.current().Settings; got.Locale != "ko" || got.LanguagePreference != languagePreferenceAuto {
		t.Fatalf("a later Windows language was not followed: %+v", got)
	}
	again := loadSettings(path, fullLocaleCatalog())
	if got := again.current().Settings; got.Locale != "ko" || got.LanguagePreference != languagePreferenceAuto {
		t.Fatalf("the resolved language was not saved: %+v", got)
	}
}

func TestSavedLocaleIsNeverReplaced(t *testing.T) {
	for _, locale := range []string{"en", "ja", "zh-TW"} {
		body := `{"version":1,"locale":"` + locale + `","defaultAdapterIPv4":"","theme":"aero"}`
		store, path := seededStore(t, &body)
		store.seedInitialLocale([]string{"ko-KR"})
		if got := store.current().Settings.Locale; got != locale {
			t.Fatalf("saved %s became %s", locale, got)
		}
		content, err := os.ReadFile(path)
		if err != nil || string(content) != body {
			t.Fatalf("a settled preference rewrote the file: %s %v", content, err)
		}
	}
}

func TestManualChoiceSurvivesAWindowsLanguageChange(t *testing.T) {
	store, path := seededStore(t, nil)
	store.seedInitialLocale([]string{"ja-JP"})
	if _, err := store.setLocale("ko"); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, fullLocaleCatalog())
	reloaded.seedInitialLocale([]string{"zh-TW"})
	if got := reloaded.current().Settings.Locale; got != "ko" {
		t.Fatalf("manual choice lost: %q", got)
	}
}

func TestMissingOrInvalidLocaleFieldIsSeededAndOtherPreferencesKept(t *testing.T) {
	for _, body := range []string{
		`{"version":1,"defaultAdapterIPv4":"192.168.1.2","theme":"aero"}`,
		`{"version":1,"locale":"","defaultAdapterIPv4":"192.168.1.2","theme":"aero"}`,
		`{"version":1,"locale":"external-pack","defaultAdapterIPv4":"192.168.1.2","theme":"aero"}`,
	} {
		body := body
		store, path := seededStore(t, &body)
		store.seedInitialLocale([]string{"zh-HK"})
		state := store.current().Settings
		if state.Locale != "zh-TW" || state.DefaultAdapterIPv4 != "192.168.1.2" || state.Theme != "aero" {
			t.Fatalf("%s: %+v", body, state)
		}
		reloaded := loadSettings(path, fullLocaleCatalog()).current().Settings
		if reloaded.Locale != "zh-TW" || reloaded.DefaultAdapterIPv4 != "192.168.1.2" || reloaded.Theme != "aero" {
			t.Fatalf("%s reloaded: %+v", body, reloaded)
		}
	}
}

func TestDamagedSettingsAreSeededInMemoryAndNotOverwritten(t *testing.T) {
	for _, body := range []string{`{`, `{"version":99}`} {
		body := body
		store, path := seededStore(t, &body)
		store.seedInitialLocale([]string{"ko-KR"})
		state := store.current()
		if state.Settings.Locale != "ko" || state.Warning == "" {
			t.Fatalf("%s: %+v", body, state)
		}
		content, _ := os.ReadFile(path)
		if string(content) != body {
			t.Fatal("damaged settings were overwritten")
		}
	}
}

func TestSeedingIsOneShotAndWriteFailureKeepsMemory(t *testing.T) {
	store, _ := seededStore(t, nil)
	store.seedInitialLocale([]string{"ja"})
	store.seedInitialLocale([]string{"ko"})
	if got := store.current().Settings.Locale; got != "ja" {
		t.Fatalf("second seed applied: %q", got)
	}
	root := t.TempDir()
	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	unwritable := loadSettings(filepath.Join(blocker, "settings.json"), fullLocaleCatalog())
	unwritable.seedInitialLocale([]string{"ja"})
	if got := unwritable.current().Settings.Locale; got != "ja" {
		t.Fatalf("a failed write lost the seed: %q", got)
	}
}

func TestSeedDoesNotChangeTheFallback(t *testing.T) {
	store, _ := seededStore(t, nil)
	if got := store.current().Settings.Locale; got != "en" {
		t.Fatalf("loading alone must stay on the catalog fallback: %q", got)
	}
	catalog := fullLocaleCatalog()
	if catalog.FallbackLocale != "en" || initialLocaleFor([]string{"de-DE"}, catalog) != catalog.FallbackLocale {
		t.Fatal("the unsupported case must end on the catalog fallback")
	}
}
