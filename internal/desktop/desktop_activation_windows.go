package desktop

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"

	"wwlogs/internal/instance"
)

var procFlashWindowEx = windows.NewLazySystemDLL("user32.dll").NewProc("FlashWindowEx")

// flashWindowInfo is FLASHWINFO.
type flashWindowInfo struct {
	size    uint32
	hwnd    uintptr
	flags   uint32
	count   uint32
	timeout uint32
}

// FLASHW_TRAY | FLASHW_TIMERNOFG: flash the taskbar button until the window comes to the foreground.
const flashTaskbarUntilForeground = 0x2 | 0xC

// windowVisible reports whether a window exists and is on screen. Reading the style needs no UI thread.
func windowVisible(window *application.WebviewWindow) bool {
	handle := window.NativeWindow()
	return handle != nil && w32.IsWindowVisible(w32.HWND(handle))
}

// bringWindowBack brings a window back the way a click on its taskbar button does, on the UI thread: a minimized window
// is restored where it was, maximized again if it was maximized; a hidden one is shown; then it asks Windows for the
// foreground. Its position, size and display stay as they are, and a window already in the foreground is left alone, so
// nothing flickers. When Windows keeps the foreground elsewhere, the taskbar button flashes until the user switches to
// the window. Nothing is made topmost and no input is simulated, and the Overlays are not touched.
func bringWindowBack(window *application.WebviewWindow) instance.Reply {
	reply := instance.Activated
	ran := onWindowThread(window, func(hwnd w32.HWND) {
		if !w32.IsWindowVisible(hwnd) {
			window.Show()
		}
		if window.IsMinimised() {
			window.UnMinimise()
		}
		if w32.GetForegroundWindow() == hwnd {
			return
		}
		if w32.SetForegroundWindow(hwnd) == 0 {
			info := flashWindowInfo{hwnd: uintptr(hwnd), flags: flashTaskbarUntilForeground}
			info.size = uint32(unsafe.Sizeof(info))
			procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
			reply = instance.Flashed
		}
	})
	if !ran {
		return instance.Pending
	}
	return reply
}
