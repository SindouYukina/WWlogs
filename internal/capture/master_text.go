package capture

import "strings"

// Official text of the Master's locale tables. The client shows no text for a localized string that is empty or begins
// with the "dnt/" placeholder, and the Master keeps such rows exactly as the client data has them (914 of them in the
// Global 3.8.12 Master), so every product name lookup asks masterOfficialText before it shows one: a placeholder is never
// a name, and the lookup then falls back as it would for a missing row. The rows themselves are not changed.

// masterOfficialText reports whether a Master locale text is an official display text. It allocates nothing: the loader
// asks it for every name it reads.
func masterOfficialText(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed != "" && !(len(trimmed) >= 4 && strings.EqualFold(trimmed[:4], "dnt/"))
}

// masterPlainText is a Master text without the client's rich-text markup (richTextTag: <word>…</word>, ruby
// annotations), as the client displays it.
func masterPlainText(text string) string {
	return strings.TrimSpace(richTextTag.ReplaceAllString(text, ""))
}
