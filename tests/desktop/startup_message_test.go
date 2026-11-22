package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"testing/fstest"

	"wwlogs/internal/instance"
)

const startupTestManifest = `{"fallbackLocale":"en","supportedLocales":["zh-TW","en","ja"],"languages":{` +
	`"zh-TW":{"label":"繁體中文","masterLocale":"zh-TW"},"en":{"label":"English","masterLocale":"en-US"},"ja":{"label":"日本語","masterLocale":"ja-JP"}}}`

func startupAssets(texts map[string]string) fstest.MapFS {
	assets := fstest.MapFS{"locales/manifest.json": &fstest.MapFile{Data: []byte(startupTestManifest)}}
	for locale, text := range texts {
		assets["locales/"+locale+"/startup.json"] = &fstest.MapFile{Data: []byte(text)}
	}
	return assets
}

var startupTestTexts = map[string]string{
	"en":    `{"notResponding":"en not responding","checkFailed":"en check failed"}`,
	"ja":    `{"notResponding":"ja not responding"}`,
	"zh-TW": `{"notResponding":"zh-TW not responding"}`,
}

// The message follows the language the application would start in, and reading it leaves the running instance's
// settings exactly as they were.
func TestStartupTextFollowsTheLaunchLanguage(t *testing.T) {
	assets := startupAssets(startupTestTexts)
	dir := t.TempDir()
	saved := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	chosenJapanese := saved("chosen.json", `{"version":1,"locale":"ja","defaultAdapterIPv4":""}`)
	automatic := saved("automatic.json", `{"version":1,"locale":"en","languagePreference":"auto","defaultAdapterIPv4":""}`)
	cases := []struct {
		name, requested, settings string
		system                    []string
		want                      string
	}{
		{"a chosen language", "", chosenJapanese, []string{"zh-Hant-TW"}, "ja not responding"},
		{"Automatic follows Windows", "", automatic, []string{"zh-Hant-TW", "en-US"}, "zh-TW not responding"},
		{"no settings yet", "", filepath.Join(dir, "missing.json"), []string{"ja-JP"}, "ja not responding"},
		{"the -locale option", "ja", automatic, []string{"zh-TW"}, "ja not responding"},
		{"a Master locale for -locale", "ja-JP", automatic, []string{"zh-TW"}, "ja not responding"},
		{"no bundled language", "", automatic, []string{"fr-FR"}, "en not responding"},
	}
	before := map[string][]byte{}
	for _, path := range []string{chosenJapanese, automatic} {
		before[path], _ = os.ReadFile(path)
	}
	for _, c := range cases {
		text, ok := startupText(assets, "notResponding", c.requested, c.settings, c.system)
		if !ok || text != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, text, ok, c.want)
		}
	}
	// A text the language lacks comes from the fallback.
	if text, ok := startupText(assets, "checkFailed", "", chosenJapanese, nil); !ok || text != "en check failed" {
		t.Errorf("missing Japanese text: %q %v", text, ok)
	}
	for path, content := range before {
		if now, _ := os.ReadFile(path); !bytes.Equal(now, content) {
			t.Errorf("%s changed", filepath.Base(path))
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("the settings directory holds %d entries, want the 2 test files only", len(entries))
	}
}

// An executable built without translations shows no message box rather than a text that is not in the registry; the
// diagnostic on standard error remains.
func TestNoStartupTextWithoutBundledTranslations(t *testing.T) {
	if _, ok := startupText(startupAssets(nil), "notResponding", "", "", []string{"en-US"}); ok {
		t.Fatal("a text without the startup namespace")
	}
	if _, ok := startupText(fstest.MapFS{}, "notResponding", "", "", []string{"en-US"}); ok {
		t.Fatal("a text without a manifest")
	}
	if _, ok := startupText(startupAssets(startupTestTexts), "unknownKey", "", "", []string{"en-US"}); ok {
		t.Fatal("a text for an unknown key")
	}
}

// Every outcome that shows a message has its text in every bundled language, and no text explains the check itself.
func TestStartupTextsAreInEveryBundledLanguage(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "locales")
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SupportedLocales []string `json:"supportedLocales"`
		Namespaces       []string `json:"namespaces"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	internals := regexp.MustCompile(`(?i)mutex|pipe|\bSID\b|kernel|handle|security descriptor`)
	for _, locale := range manifest.SupportedLocales {
		raw, err := os.ReadFile(filepath.Join(root, locale, "startup.json"))
		if err != nil {
			t.Fatal(err)
		}
		var texts map[string]string
		if err := json.Unmarshal(raw, &texts); err != nil {
			t.Fatalf("%s: %v", locale, err)
		}
		for outcome, key := range startupMessageKeys {
			if texts[key] == "" {
				t.Errorf("%s: no text for %s (%s)", locale, key, outcome)
			}
			if internals.MatchString(texts[key]) {
				t.Errorf("%s/%s names how the check works: %q", locale, key, texts[key])
			}
		}
		if len(texts) != len(startupMessageKeys) {
			t.Errorf("%s: %d startup texts, want %d", locale, len(texts), len(startupMessageKeys))
		}
	}
	for _, outcome := range []instance.Outcome{instance.Primary, instance.HandedOff} {
		if _, shown := startupMessageKeys[outcome]; shown {
			t.Errorf("%s shows a message", outcome)
		}
	}
}
