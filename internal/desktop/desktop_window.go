package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// desktopWindowShape is a Desktop window's size and minimum size in DIPs.
type desktopWindowShape struct {
	width, height, minWidth, minHeight int
}

// The Desktop window's designed shape. 1024 DIPs show the whole Dashboard, with the tallest skill table and one notice
// row, without a vertical scrollbar (measured with real replays: up to 892 DIPs of content, a 51 DIP header, a 44 DIP
// notice and 37 DIPs of native frame); 1280 DIPs show every column without truncation. The minimum is unchanged.
var (
	desktopShape  = desktopWindowShape{width: 1280, height: 1024, minWidth: 980, minHeight: 620}
	showcaseShape = desktopWindowShape{width: 1360, height: 880, minWidth: 980, minHeight: 620}
)

// fit returns the shape for a work area in DIPs: never larger than the work area, so the window, title bar included,
// opens fully on screen, and with a minimum the work area can hold. A missing work area keeps the designed shape.
func (shape desktopWindowShape) fit(workArea application.Rect) desktopWindowShape {
	if workArea.Width <= 0 || workArea.Height <= 0 {
		return shape
	}
	shape.width, shape.height = min(shape.width, workArea.Width), min(shape.height, workArea.Height)
	shape.minWidth, shape.minHeight = min(shape.minWidth, shape.width), min(shape.minHeight, shape.height)
	return shape
}

// fitDesktopWindow shrinks the Desktop window to the primary work area when the designed shape does not fit it, and
// centers it there; on a large enough display it changes nothing. The Desktop window's size is not saved, so this runs at
// every launch and never replaces a size the user chose. The minimum is lowered before the size, because Windows would
// otherwise hold the window at the old minimum.
func fitDesktopWindow(window *application.WebviewWindow, screen *application.Screen, shape desktopWindowShape) {
	if window == nil || screen == nil {
		return
	}
	fitted := shape.fit(screen.WorkArea)
	if fitted == shape {
		return
	}
	window.SetMinSize(fitted.minWidth, fitted.minHeight)
	window.SetSize(fitted.width, fitted.height)
	window.Center()
}
