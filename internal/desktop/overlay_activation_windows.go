package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// overlayActivationStyle returns the extended window style for an Overlay. Outside Edit Mode a click on an Overlay must not
// activate it, because the foreground window is normally the game. Edit Mode is the one state where it is used with the
// mouse, so there it activates like any window.
func overlayActivationStyle(style uint32, editing bool) uint32 {
	if editing {
		return style &^ w32.WS_EX_NOACTIVATE
	}
	return style | w32.WS_EX_NOACTIVATE
}

// onWindowThread runs a native call on the window's thread and reports whether it ran. It does nothing for a window
// without a native handle.
func onWindowThread(window *application.WebviewWindow, call func(hwnd w32.HWND)) bool {
	handle := window.NativeWindow()
	if handle == nil {
		return false
	}
	hwnd := w32.HWND(handle)
	application.InvokeSync(func() { call(hwnd) })
	return true
}

func setOverlayActivation(window *application.WebviewWindow, editing bool) {
	onWindowThread(window, func(hwnd w32.HWND) {
		current := uint32(w32.GetWindowLong(hwnd, w32.GWL_EXSTYLE))
		if next := overlayActivationStyle(current, editing); next != current {
			w32.SetWindowLong(hwnd, w32.GWL_EXSTYLE, next)
		}
	})
}

// showOverlayInactive makes a hidden window visible without activating it. The SW_SHOW in Wails' Show activates a hidden
// window whatever its style, but it does nothing to a window that is already visible. Show is still needed for Wails' own
// state and for WebView2's visibility.
func showOverlayInactive(window *application.WebviewWindow) {
	onWindowThread(window, func(hwnd w32.HWND) { w32.ShowWindow(hwnd, w32.SW_SHOWNA) })
}

// setOverlayTopmost replaces Wails' SetAlwaysOnTop, whose SetWindowPos omits SWP_NOACTIVATE.
func setOverlayTopmost(window *application.WebviewWindow, topmost bool) {
	insertAfter := w32.HWND_NOTOPMOST
	if topmost {
		insertAfter = w32.HWND_TOPMOST
	}
	onWindowThread(window, func(hwnd w32.HWND) {
		w32.SetWindowPos(hwnd, insertAfter, 0, 0, 0, 0, w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOACTIVATE)
	})
}

// foregroundWindow returns the window that holds the foreground now, which may belong to another process.
func foregroundWindow() uintptr {
	return uintptr(w32.GetForegroundWindow())
}

// returnForeground undoes the one activation that cannot be prevented. While Wails creates a frameless window it calls
// SetWindowPos without SWP_NOACTIVATE. That only makes the new window active inside this thread, unless this thread holds
// the foreground, which means the Desktop window had it. Then the Desktop window gets it back; no other process is involved.
func returnForeground(window *application.WebviewWindow, previous uintptr) {
	onWindowThread(window, func(hwnd w32.HWND) {
		if previous != 0 && previous != uintptr(hwnd) && w32.GetForegroundWindow() == hwnd {
			w32.SetForegroundWindow(w32.HWND(previous))
		}
	})
}

// mayTakeFocusMessage reports whether a window may handle WM_SETFOCUS. Wails answers that message by calling
// SetForegroundWindow, which turns a change inside this thread into taking the foreground from another process. This covers
// every window of the application: hiding an Overlay can also move this thread's active window to the Desktop window.
func mayTakeFocusMessage(hwnd, foreground uintptr) bool {
	return hwnd == foreground
}

// keepForeground sees the messages of this application's own windows only.
func keepForeground(hwnd uintptr, msg uint32, _, _ uintptr) (uintptr, bool) {
	if msg == w32.WM_SETFOCUS && !mayTakeFocusMessage(hwnd, uintptr(w32.GetForegroundWindow())) {
		return 0, true
	}
	return 0, false
}
