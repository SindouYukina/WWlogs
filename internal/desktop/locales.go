package desktop

import (
	"encoding/json"
	"errors"
	"io/fs"
)

// localeCatalog is loaded only from the compiled frontend archive, never from user files.
type localeCatalog struct {
	FallbackLocale   string   `json:"fallbackLocale"`
	SupportedLocales []string `json:"supportedLocales"`
	Languages        map[string]struct {
		Label        string `json:"label"`
		MasterLocale string `json:"masterLocale"`
	} `json:"languages"`
}

func readLocaleCatalog(assets fs.FS) (localeCatalog, error) {
	var catalog localeCatalog
	raw, err := fs.ReadFile(assets, "locales/manifest.json")
	if err != nil {
		return catalog, errors.New("the bundled language manifest is missing")
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		return catalog, errors.New("the bundled language manifest is invalid")
	}
	seen := map[string]bool{}
	for _, locale := range catalog.SupportedLocales {
		if locale == "" || seen[locale] || catalog.Languages[locale].MasterLocale == "" {
			return catalog, errors.New("the bundled language manifest is inconsistent")
		}
		seen[locale] = true
	}
	if !seen[catalog.FallbackLocale] || len(seen) != len(catalog.Languages) {
		return catalog, errors.New("the bundled language fallback is invalid")
	}
	return catalog, nil
}

func (c localeCatalog) valid(locale string) bool {
	_, ok := c.Languages[locale]
	return ok
}
