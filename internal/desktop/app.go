// Package desktop is the Wails v3 application host: one Go process, one CaptureService, one Publisher, and two
// windows (Desktop and Overlay) that load the same frontend with different views. All capture and statistics logic
// stays in internal/capture and internal/service; this package only creates windows and exposes the Bridge.
package desktop

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

// Window names; the frontend selects its view from the URL, not from these names.
const (
	desktopWindowName = "desktop"
	overlayWindowName = "overlay"
)

// Options configure the host. Profile-specific flags fill them (options_public.go, options_development.go).
type Options struct {
	MasterDatabase string
	Locale         string
	Session        capture.SessionConfig
	// AutoStart starts a capture with Session as soon as the windows exist.
	AutoStart bool
	// ShowOverlay shows the overlay at start; it is hidden otherwise.
	ShowOverlay bool
	// WebviewDebugPort opens the WebView2 remote debugging port. Only Development builds can set it or contain the
	// browser argument (webviewArguments in options_development.go).
	WebviewDebugPort int
}

// Main parses the command line and runs the host until the Desktop window closes.
func Main() {
	for _, argument := range os.Args[1:] {
		if argument == "-version" || argument == "--version" {
			capture.PrintVersion(os.Stdout)
			return
		}
	}
	options, err := parseOptions(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "WWlogs desktop: %v\n", err)
		os.Exit(2)
	}
	if err := Run(options); err != nil {
		fmt.Fprintf(os.Stderr, "WWlogs desktop: %v\n", err)
		os.Exit(1)
	}
}

// Run starts the application and blocks until it quits. The capture is stopped and its resources released before
// Run returns.
func Run(options Options) error {
	masters, err := capture.LoadMasterData(options.MasterDatabase, options.Locale)
	if err != nil {
		return fmt.Errorf("load combat Master database: %w", err)
	}
	assets, _, err := frontendFS()
	if err != nil {
		return err
	}
	captureService := service.NewCaptureService(service.SessionOpener(masters))
	var overlay *application.WebviewWindow
	bridge := &Bridge{capture: captureService, defaults: options.Session, overlay: func() *application.WebviewWindow { return overlay }}

	app := application.New(application.Options{
		Name:        "WWlogs",
		Description: "Wuthering Waves combat analyzer",
		Services:    []application.Service{application.NewService(bridge)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Windows: application.WindowsOptions{
			WebviewUserDataPath:   webviewUserDataPath(),
			AdditionalBrowserArgs: webviewArguments(options),
		},
	})

	desktop := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             desktopWindowName,
		Title:            "WWlogs",
		Width:            1100,
		Height:           720,
		MinWidth:         720,
		MinHeight:        480,
		URL:              "/?view=desktop",
		BackgroundColour: application.NewRGB(15, 17, 23),
	})
	overlay = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:           overlayWindowName,
		Title:          "WWlogs Overlay",
		Width:          380,
		Height:         240,
		URL:            "/?view=overlay",
		Frameless:      true,
		AlwaysOnTop:    true,
		BackgroundType: application.BackgroundTypeTransparent,
		Hidden:         !options.ShowOverlay,
		// Mouse click-through is deliberately not enabled in this phase: the overlay takes mouse input.
		// Without the frameless decorations Windows keeps no caption, frame, shadow, or rounded corners around the
		// transparent area.
		Windows: application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true},
	})
	// Closing the Desktop window quits the application, including the hidden overlay.
	desktop.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		app.Quit()
	})
	// A close request on the overlay (for example Alt+F4) only hides it, so the Desktop window can show it again.
	// Quit destroys windows directly and is not affected.
	overlay.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		overlay.Hide()
		event.Cancel()
	})
	if options.AutoStart {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			go func() {
				if err := captureService.Start(options.Session); err != nil {
					fmt.Fprintf(os.Stderr, "capture start failed: %v\n", err)
				}
			}()
		})
	}
	return app.Run()
}

// webviewUserDataPath keeps WebView2 profile data under %LOCALAPPDATA%\WWlogs instead of beside the executable.
func webviewUserDataPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return ""
		}
	}
	return filepath.Join(base, "WWlogs", "webview2")
}
