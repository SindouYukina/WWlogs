package desktop

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func storeWithSystemLanguages(t *testing.T, body *string, languages *[]string) (*settingsStore, string) {
	t.Helper()
	store, path := seededStore(t, body)
	store.useSystemLanguages(func() []string { return *languages })
	store.seedInitialLocale(*languages)
	return store, path
}

func savedLanguageFields(t *testing.T, path string) (locale, preference string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Locale             string `json:"locale"`
		LanguagePreference string `json:"languagePreference"`
	}
	if err := json.Unmarshal(content, &saved); err != nil {
		t.Fatal(err)
	}
	return saved.Locale, saved.LanguagePreference
}

func TestNoSettingsAndAnEmptyLanguageFieldDefaultToAutomatic(t *testing.T) {
	languages := []string{"zh-HK"}
	empty := `{"version":1,"locale":"","defaultAdapterIPv4":"","theme":"aero"}`
	for _, body := range []*string{nil, &empty} {
		store, path := storeWithSystemLanguages(t, body, &languages)
		if got := store.current().Settings; got.LanguagePreference != languagePreferenceAuto || got.Locale != "zh-TW" {
			t.Fatalf("default = %+v", got)
		}
		if locale, preference := savedLanguageFields(t, path); locale != "zh-TW" || preference != languagePreferenceAuto {
			t.Fatalf("saved = %q, %q", locale, preference)
		}
	}
}

func TestAnExistingConcreteLanguageIsNotReinterpretedAsAutomatic(t *testing.T) {
	// A Build 12 file holds the language it seeded with no marker of how it got there, so it stays the user's language.
	body := `{"version":1,"locale":"zh-TW","defaultAdapterIPv4":"","theme":"aero"}`
	languages := []string{"ja-JP"}
	store, path := storeWithSystemLanguages(t, &body, &languages)
	if got := store.current().Settings; got.Locale != "zh-TW" || got.LanguagePreference != "" {
		t.Fatalf("existing language = %+v", got)
	}
	content, _ := os.ReadFile(path)
	if string(content) != body {
		t.Fatalf("a settled preference rewrote the file: %s", content)
	}
}

func TestSwitchingBetweenAutomaticAndAConcreteLanguage(t *testing.T) {
	languages := []string{"ko-KR"}
	store, path := storeWithSystemLanguages(t, nil, &languages)

	state, err := store.setLocale("ja")
	if err != nil || state.Settings.Locale != "ja" || state.Settings.LanguagePreference != "" {
		t.Fatalf("auto to ja = %+v, %v", state.Settings, err)
	}
	revision := state.LocaleRevision

	state, err = store.setLocale(languagePreferenceAuto)
	if err != nil || state.Settings.Locale != "ko" || state.Settings.LanguagePreference != languagePreferenceAuto {
		t.Fatalf("ja to auto = %+v, %v", state.Settings, err)
	}
	if state.LocaleRevision == revision {
		t.Fatal("a change of the resolved language must reach the windows")
	}
	if locale, preference := savedLanguageFields(t, path); locale != "ko" || preference != languagePreferenceAuto {
		t.Fatalf("saved after auto = %q, %q", locale, preference)
	}

	if state, err = store.setLocale("en"); err != nil || state.Settings.LanguagePreference != "" || state.Settings.Locale != "en" {
		t.Fatalf("auto to en = %+v, %v", state.Settings, err)
	}
	// Automatic resolves the language at the moment it is chosen.
	languages = []string{"zh-CN"}
	if state, err = store.setLocale(languagePreferenceAuto); err != nil || state.Settings.Locale != "zh-CN" || state.Settings.LanguagePreference != languagePreferenceAuto {
		t.Fatalf("en to auto = %+v, %v", state.Settings, err)
	}
}

func TestAutomaticSurvivesARestartAndResolvesEveryLanguage(t *testing.T) {
	cases := []struct {
		name      string
		languages []string
		want      string
	}{
		{"zh-TW", []string{"zh-TW"}, "zh-TW"},
		{"zh-HK", []string{"zh-HK"}, "zh-TW"},
		{"zh-MO", []string{"zh-MO"}, "zh-TW"},
		{"zh-CN", []string{"zh-CN"}, "zh-CN"},
		{"zh-SG", []string{"zh-SG"}, "zh-CN"},
		{"ja-JP", []string{"ja-JP"}, "ja"},
		{"ko-KR", []string{"ko-KR"}, "ko"},
		{"en-US", []string{"en-US"}, "en"},
		{"unsupported", []string{"de-DE"}, "en"},
		{"undecided zh then a later preference", []string{"zh", "ko-KR"}, "ko"},
		{"preference order", []string{"ja-JP", "en-US"}, "ja"},
	}
	for _, c := range cases {
		languages := []string{"en-US"}
		store, path := storeWithSystemLanguages(t, nil, &languages)
		if _, err := store.setLocale(languagePreferenceAuto); err != nil {
			t.Fatal(err)
		}
		reloaded := loadSettings(path, fullLocaleCatalog())
		reloaded.seedInitialLocale(c.languages)
		if got := reloaded.current().Settings; got.Locale != c.want || got.LanguagePreference != languagePreferenceAuto {
			t.Errorf("%s: %+v, want %s", c.name, got, c.want)
		}
	}
}

func TestAutomaticWithoutASavedChangeDoesNotRewriteTheFile(t *testing.T) {
	languages := []string{"ja-JP"}
	store, path := storeWithSystemLanguages(t, nil, &languages)
	_ = store
	before, _ := os.ReadFile(path)
	reloaded := loadSettings(path, fullLocaleCatalog())
	reloaded.seedInitialLocale(languages)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("an unchanged Automatic preference rewrote the file:\n%s\n%s", before, after)
	}
}

func TestLanguagePreferenceValidation(t *testing.T) {
	languages := []string{"ja-JP"}
	store, _ := storeWithSystemLanguages(t, nil, &languages)
	if _, err := store.setLocale("external-pack"); err == nil {
		t.Fatal("an unsupported locale was accepted")
	}
	// An unknown saved preference is ignored; the concrete language it came with stays.
	body := `{"version":1,"locale":"ko","languagePreference":"wiki","defaultAdapterIPv4":"","theme":"aero"}`
	other, _ := storeWithSystemLanguages(t, &body, &languages)
	if got := other.current().Settings; got.Locale != "ko" || got.LanguagePreference != "" {
		t.Fatalf("unknown preference = %+v", got)
	}
}

func TestDamagedSettingsStayOnAutomaticInMemoryOnly(t *testing.T) {
	languages := []string{"ko-KR"}
	body := `{`
	store, path := storeWithSystemLanguages(t, &body, &languages)
	if got := store.current().Settings; got.Locale != "ko" || got.LanguagePreference != languagePreferenceAuto {
		t.Fatalf("damaged settings = %+v", got)
	}
	content, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(content), `{`) || len(content) != 1 {
		t.Fatalf("damaged settings were overwritten: %s", content)
	}
}

func TestAutomaticIsMissingFromTheBootQueryAndTheResolvedLanguageIsSent(t *testing.T) {
	languages := []string{"ko-KR"}
	store, _ := storeWithSystemLanguages(t, nil, &languages)
	if got := bootLocaleQuery(store); got != "lang=ko" {
		t.Fatalf("boot query = %q", got)
	}
}
