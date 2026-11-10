package desktop

import "strings"

// initialLocaleFor chooses the UI language of a first launch: the first of the user's preferred languages, in their
// order, that maps clearly to a bundled locale. It decides only the initial UI preference. The fallback it ends on is the
// catalog's own, and the engine's and Master's language rules are not involved.
func initialLocaleFor(preferred []string, catalog localeCatalog) string {
	for _, tag := range preferred {
		if locale, known := bundledLocaleForTag(tag); known && catalog.valid(locale) {
			return locale
		}
	}
	return catalog.FallbackLocale
}

// languageTagParts is the part of a BCP 47 tag the mapping needs, in lower case.
type languageTagParts struct {
	language, script, region string
}

// parseLanguageTag reads a BCP 47 tag, or a Windows locale name such as zh-TW_radstr. Everything after the first
// singleton subtag (extensions and private use) is ignored, as are variants.
func parseLanguageTag(tag string) languageTagParts {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(tag)), func(r rune) bool { return r == '-' || r == '_' })
	var parts languageTagParts
	if len(fields) == 0 {
		return parts
	}
	parts.language = fields[0]
	for _, field := range fields[1:] {
		if len(field) == 1 {
			break
		}
		switch {
		case len(field) == 4 && isAlphabetic(field) && parts.script == "":
			parts.script = field
		case (len(field) == 2 && isAlphabetic(field) || len(field) == 3 && !isAlphabetic(field)) && parts.region == "":
			parts.region = field
		case field == "chs" && parts.script == "":
			// Windows' legacy neutral names for the two Chinese scripts.
			parts.script = "hans"
		case field == "cht" && parts.script == "":
			parts.script = "hant"
		}
	}
	return parts
}

func isAlphabetic(value string) bool {
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return value != ""
}

// bundledLocaleForTag maps one language tag to a bundled locale. A Chinese tag needs a script or a region that decides
// between Traditional and Simplified; a bare "zh" decides nothing and the next preference is consulted instead.
func bundledLocaleForTag(tag string) (string, bool) {
	parts := parseLanguageTag(tag)
	switch parts.language {
	case "en", "ja", "ko":
		return parts.language, true
	case "zh":
		switch parts.script {
		case "hant":
			return "zh-TW", true
		case "hans":
			return "zh-CN", true
		}
		switch parts.region {
		case "tw", "hk", "mo":
			return "zh-TW", true
		case "cn", "sg", "my":
			return "zh-CN", true
		}
	}
	return "", false
}
