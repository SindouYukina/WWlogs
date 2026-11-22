package desktop

import (
	"golang.org/x/sys/windows"

	"wwlogs/internal/instance"
)

var startupMessageIcons = map[instance.Outcome]uint32{
	instance.RunningElsewhere: windows.MB_ICONINFORMATION,
	instance.NotResponding:    windows.MB_ICONWARNING,
	instance.CheckFailed:      windows.MB_ICONERROR,
}

// showStartupMessage tells the user, in a native message box, why WWlogs did not open. It shows nothing on a window
// station nobody can see, where the box could never be answered, or in an executable without bundled translations; the
// diagnostic on standard error remains in both cases. The text says what happened and what to do, never how the check
// works.
func showStartupMessage(outcome instance.Outcome, options Options) {
	key, known := startupMessageKeys[outcome]
	if !known || !instance.Interactive() {
		return
	}
	assets, _, err := frontendFS()
	if err != nil {
		return
	}
	systemLanguages := options.SystemLanguages
	if systemLanguages == nil {
		systemLanguages = userPreferredUILanguages
	}
	text, ok := startupText(assets, key, options.Locale, settingsPath(), systemLanguages())
	if !ok {
		return
	}
	textPointer, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	titlePointer, _ := windows.UTF16PtrFromString("WWlogs")
	windows.MessageBox(0, textPointer, titlePointer, windows.MB_OK|startupMessageIcons[outcome]|windows.MB_SETFOREGROUND)
}
