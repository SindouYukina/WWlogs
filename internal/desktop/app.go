// Package desktop is the Wails v3 application host: one Go process, one CaptureService, one Publisher, and two
// windows (Desktop and Overlay) that load the same frontend with different views. All capture and statistics logic
// stays in internal/capture and internal/service; this package only creates windows and exposes the Bridge.
package desktop

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
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
	// Showcase opens the Development Motion Showcase instead of the Desktop view. ShowcaseOverlays adds its real
	// transparent windows. Public flags cannot set these, and Public frontend builds do not contain the Showcase.
	Showcase         bool
	ShowcaseOverlays bool
	// GameAssetRoot is an explicitly supplied directory for game images. Empty means no images are served.
	GameAssetRoot string
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
	frontend, _, err := frontendFS()
	if err != nil {
		return err
	}
	var assetHandler http.Handler = application.AssetFileServerFS(frontend)
	if options.GameAssetRoot != "" {
		assetHandler = withGameAssets(assetHandler, os.DirFS(options.GameAssetRoot))
	}
	catalog, err := readLocaleCatalog(frontend)
	if err != nil {
		return err
	}
	settings := loadSettings(settingsPath(), catalog)
	if options.Locale != "" {
		locale := options.Locale
		// Preserve older desktop CLI aliases using only the bundled manifest's Master mapping.
		if !catalog.valid(locale) {
			for _, candidate := range catalog.SupportedLocales {
				language := catalog.Languages[candidate]
				if language.MasterLocale == locale {
					locale = candidate
					break
				}
			}
		}
		if _, err := settings.setLocale(locale); err != nil {
			return err
		}
	}
	masters, err := capture.LoadMasterData(options.MasterDatabase, catalog.Languages[settings.current().Settings.Locale].MasterLocale)
	if err != nil {
		return fmt.Errorf("load combat Master database: %w", err)
	}
	resolver, err := capture.OpenDisplayResolver(options.MasterDatabase)
	if err != nil {
		return err
	}
	defer resolver.Close()
	captureService := service.NewCaptureService(service.SessionOpener(masters))
	var overlay *application.WebviewWindow
	bridge := &Bridge{capture: captureService, defaults: options.Session, settings: settings, overlay: func() *application.WebviewWindow { return overlay }}
	bridge.catalog = catalog
	bridge.projection = &displayProjection{resolver: resolver}
	var app *application.App
	var flushOverlay func(bool)
	shutdown := newShutdownGate(func(ctx context.Context) error {
		err := captureService.Shutdown(ctx)
		if flushOverlay != nil {
			flushOverlay(true)
		}
		return err
	}, func() { app.Quit() })
	bridge.shutdown = shutdown

	app = application.New(application.Options{
		Name:        "WWlogs",
		Description: "Wuthering Waves combat analyzer",
		Services:    []application.Service{application.NewService(bridge)},
		Assets:      application.AssetOptions{Handler: assetHandler},
		ShouldQuit:  shutdown.shouldQuit,
		Windows: application.WindowsOptions{
			WebviewUserDataPath:   webviewUserDataPath(),
			AdditionalBrowserArgs: webviewArguments(options),
		},
	})

	desktopURL, desktopWidth, desktopHeight := "/?view=desktop", 1100, 720
	if options.Showcase {
		desktopURL, desktopWidth, desktopHeight = "/?view=showcase", 1360, 880
	}
	desktop := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             desktopWindowName,
		Title:            "WWlogs",
		Width:            desktopWidth,
		Height:           desktopHeight,
		MinWidth:         720,
		MinHeight:        480,
		URL:              desktopURL,
		BackgroundColour: application.NewRGB(15, 17, 23),
	})
	overlay = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:           overlayWindowName,
		Title:          "WWlogs Overlay",
		Width:          440,
		Height:         640,
		MinWidth:       320,
		MinHeight:      360,
		MaxWidth:       1200,
		MaxHeight:      1600,
		URL:            "/?view=overlay",
		Frameless:      true,
		AlwaysOnTop:    true,
		BackgroundType: application.BackgroundTypeTransparent,
		Hidden:         options.Showcase || !(options.ShowOverlay || settings.current().Settings.ShowOverlayOnStartup),
		// Mouse click-through is deliberately not enabled in this phase: the overlay takes mouse input.
		// Without the frameless decorations Windows keeps no caption, frame, shadow, or rounded corners around the
		// transparent area.
		Windows: application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true},
	})
	if options.Showcase {
		// The Showcase is a review surface: it must not rewrite the user's saved window placement.
		flushOverlay = func(bool) {}
		if options.ShowcaseOverlays {
			openShowcaseOverlays(app)
		}
	} else {
		flushOverlay = trackOverlay(app, overlay, settings)
	}
	// Cancel placement timers on startup failure as well; native teardown already persisted successful runs.
	defer flushOverlay(false)
	// Closing the Desktop window quits the application, including the hidden overlay.
	desktop.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !shutdown.ready.Load() {
			event.Cancel()
			shutdown.request()
		}
	})
	// A close request on the overlay (for example Alt+F4) only hides it, so the Desktop window can show it again.
	// Quit destroys windows directly and is not affected.
	overlay.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if shutdown.ready.Load() {
			return
		}
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
	err = app.Run()
	if shutdown.ready.Load() {
		return errors.Join(err, shutdown.err)
	}
	// Run may also return after a startup failure, before any native close request.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownWaitLimit)
	defer cancel()
	return errors.Join(err, captureService.Shutdown(ctx))
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
