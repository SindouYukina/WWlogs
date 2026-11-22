package desktop

import (
	"encoding/json"
	"io/fs"

	"wwlogs/internal/instance"
)

// startupMessageKeys name the texts, in the bundled startup namespace, of the launch outcomes that started nothing and
// must say why: no window exists to say it in. A launch that brought the running WWlogs back says nothing.
var startupMessageKeys = map[instance.Outcome]string{
	instance.RunningElsewhere: "alreadyRunningElsewhere",
	instance.NotResponding:    "notResponding",
	instance.CheckFailed:      "checkFailed",
}

// startupText returns a startup message in the language the application would have started in: the -locale option, or
// else the saved language preference, resolved as at launch, with the Windows display languages for Automatic. The
// settings file is only read: it belongs to the running instance. A text missing from that language comes from the
// bundled fallback; without bundled translations there is no text.
func startupText(assets fs.FS, key, requested, settingsFile string, systemLanguages []string) (string, bool) {
	catalog, err := readLocaleCatalog(assets)
	if err != nil {
		return "", false
	}
	locale := catalog.optionLocale(requested)
	if !catalog.valid(locale) {
		locale = loadSettings(settingsFile, catalog).launchLocale(systemLanguages)
	}
	for _, candidate := range []string{locale, catalog.FallbackLocale} {
		if text := startupString(assets, candidate, key); text != "" {
			return text, true
		}
	}
	return "", false
}

// startupString reads one text of a language's startup namespace, which the frontend build places in the archive for
// the host.
func startupString(assets fs.FS, locale, key string) string {
	raw, err := fs.ReadFile(assets, "locales/"+locale+"/startup.json")
	if err != nil {
		return ""
	}
	var texts map[string]string
	if json.Unmarshal(raw, &texts) != nil {
		return ""
	}
	return texts[key]
}
