package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestDesktopWindowFitsEveryCommonWorkArea(t *testing.T) {
	// 1080p, 1440p and 4K at 100, 125, 150 and 200% Windows scaling, each with a 48 DIP taskbar.
	for _, size := range [][2]int{{1920, 1080}, {2560, 1440}, {3840, 2160}} {
		for _, dpi := range []float64{1, 1.25, 1.5, 2} {
			area := application.Rect{Width: int(float64(size[0]) / dpi), Height: int(float64(size[1])/dpi) - 48}
			got := desktopShape.fit(area)
			if got.width > area.Width || got.height > area.Height || got.minWidth > got.width || got.minHeight > got.height {
				t.Fatalf("%dx%d at %.0f%%: %+v does not fit %+v", size[0], size[1], dpi*100, got, area)
			}
			// A work area that holds the designed window keeps it unchanged.
			if area.Width >= desktopShape.width && area.Height >= desktopShape.height && got != desktopShape {
				t.Fatalf("%dx%d at %.0f%%: %+v changed although the design fits", size[0], size[1], dpi*100, got)
			}
		}
	}
}

func TestDesktopWindowDesignShowsTheDashboardAt1080p(t *testing.T) {
	// A 1080p display at 100% with a 48 DIP taskbar holds the designed window, which is tall enough for the measured
	// Dashboard: 892 DIPs of content, a 51 DIP header, one 44 DIP notice row and a 37 DIP native frame.
	area := application.Rect{Width: 1920, Height: 1080 - 48}
	if got := desktopShape.fit(area); got != desktopShape {
		t.Fatalf("1080p at 100%%: %+v", got)
	}
	if need := 892 + 51 + 44 + 37; desktopShape.height < need {
		t.Fatalf("designed height %d is below the measured %d", desktopShape.height, need)
	}
}

func TestDesktopWindowShrinksOnlyWhereItMust(t *testing.T) {
	// 1080p at 150%: 1280 x 672 DIPs. The window fills the height instead of hanging below the taskbar.
	got := desktopShape.fit(application.Rect{Width: 1280, Height: 672})
	if got.width != 1280 || got.height != 672 || got.minWidth != 980 || got.minHeight != 620 {
		t.Fatalf("1080p at 150%%: %+v", got)
	}
	// 1080p at 200%: 960 x 492 DIPs, smaller than the minimum. The minimum follows the work area so the window can fit.
	got = desktopShape.fit(application.Rect{Width: 960, Height: 492})
	if got.width != 960 || got.height != 492 || got.minWidth != 960 || got.minHeight != 492 {
		t.Fatalf("1080p at 200%%: %+v", got)
	}
	if got := desktopShape.fit(application.Rect{}); got != desktopShape {
		t.Fatalf("no work area: %+v", got)
	}
}
