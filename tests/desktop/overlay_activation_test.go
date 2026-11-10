package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/w32"
)

func TestOverlayRefusesActivationExceptInEditMode(t *testing.T) {
	const other = uint32(w32.WS_EX_LAYERED | w32.WS_EX_TRANSPARENT | w32.WS_EX_TOOLWINDOW | w32.WS_EX_TOPMOST)
	normal := overlayActivationStyle(other, false)
	if normal&w32.WS_EX_NOACTIVATE == 0 {
		t.Fatal("a click in Normal Mode can take the foreground")
	}
	editing := overlayActivationStyle(normal, true)
	if editing&w32.WS_EX_NOACTIVATE != 0 {
		t.Fatal("Edit Mode cannot be activated by the mouse")
	}
	// Click-through, topmost and the taskbar style belong to other settings and must survive both directions.
	if normal&^w32.WS_EX_NOACTIVATE != other || editing != other {
		t.Fatalf("other styles changed: normal %#x editing %#x", normal, editing)
	}
	if overlayActivationStyle(normal, false) != normal {
		t.Fatal("repeating Normal Mode changed the style")
	}
}

func TestOnlyTheForegroundWindowHandlesFocus(t *testing.T) {
	const window, other = uintptr(0x10), uintptr(0x20)
	if !mayTakeFocusMessage(window, window) {
		t.Fatal("the foreground window lost its focus handling")
	}
	// Wails answers this message with SetForegroundWindow, which would take the foreground from the game.
	if mayTakeFocusMessage(window, other) {
		t.Fatal("a background window can promote itself to the foreground")
	}
	if mayTakeFocusMessage(window, 0) {
		t.Fatal("a window can promote itself while nothing holds the foreground")
	}
}

func TestFocusMessagesAreTheOnlyOnesConsumed(t *testing.T) {
	for _, msg := range []uint32{w32.WM_ACTIVATE, w32.WM_KILLFOCUS, w32.WM_SIZE, w32.WM_WINDOWPOSCHANGING} {
		if code, handled := keepForeground(0x10, msg, 0, 0); handled || code != 0 {
			t.Fatalf("message %#x was consumed", msg)
		}
	}
}
