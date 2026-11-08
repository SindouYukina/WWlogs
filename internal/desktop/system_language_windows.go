package desktop

import "golang.org/x/sys/windows"

// userPreferredUILanguages returns the current user's Windows display languages in preference order. This is the display
// language, not the regional format, so a Japanese Windows set to the Taiwan region still reports Japanese first.
func userPreferredUILanguages() []string {
	languages, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return languages
}
