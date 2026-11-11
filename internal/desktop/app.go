// Package desktop is the Wails v3 application host: one Go process, one CaptureService, one Publisher, and three
// windows (Desktop, Combat Overlay and Effects Overlay) that load the same frontend with different views. All capture and
// statistics logic stays in internal/capture and internal/service; this package only creates windows and exposes the
// Bridge.
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

// desktopWindowName names the main window; the frontend selects its view from the URL, not from window names.
const desktopWindowName = "desktop"

// Options configure the host. Profile-specific flags fill them (options_public.go, options_development.go).
type Options struct {
	MasterDatabase string
	Locale         string
	Session        capture.SessionConfig
	// AutoStart starts a capture with Session as soon as the windows exist.
	AutoStart bool
	// ShowOverlay shows both Overlays at start; otherwise each restores its saved visibility.
	ShowOverlay bool
	// WebviewDebugPort opens the WebView2 remote debugging port. Only Development builds can set it or contain the
	// browser argument (webviewArguments in options_development.go).
	WebviewDebugPort int
	// Showcase opens the Development Motion Showcase instead of the Desktop view. ShowcaseOverlays adds its real
	// transparent windows. Public flags cannot set these, and Public frontend builds do not contain the Showcase.
	Showcase         bool
	ShowcaseOverlays bool
	// GameAssetRoot is an explicitly supplied directory for game images. Empty means the images embedded in the
	// executable, if any. Only Development flags can set it.
	GameAssetRoot string
	// HistoryDatabase is an explicitly supplied Combat History file for tests; empty uses the user's own. Only it records
	// replays, so a replay never enters the user's own history. Only Development flags can set it.
	HistoryDatabase string
	// PreviewFontRoot is an explicitly supplied directory for Development Showcase font candidates.
	PreviewFontRoot string
	// WebviewArguments are extra browser arguments for rendering experiments; only Development flags can set them.
	WebviewArguments []string
	// SystemLanguages replaces the Windows display-language query for controlled UI checks. Only Development sets it.
	SystemLanguages func() []string
	// EnvironmentProbe, NpcapTransport and OpenBrowser replace system access for controlled UI checks. Only Development
	// flags can set them; nil means the real system.
	EnvironmentProbe func() capture.CaptureEnvironment
	NpcapTransport   http.RoundTripper
	OpenBrowser      func(string) error
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
	assetHandler := withGameAssets(application.AssetFileServerFS(frontend), gameAssetSource(options.GameAssetRoot, gameAssetArchive))
	if options.PreviewFontRoot != "" {
		// A wrong path is reported here and shows as Missing in the Showcase; it never stops the application.
		if info, err := os.Stat(options.PreviewFontRoot); err != nil || !info.IsDir() {
			fmt.Fprintln(os.Stderr, "WWlogs desktop: the -font-root directory was not found; font candidates will show as missing")
		}
		assetHandler = withPreviewFonts(assetHandler, os.DirFS(options.PreviewFontRoot))
	}
	catalog, err := readLocaleCatalog(frontend)
	if err != nil {
		return err
	}
	settings := loadSettings(settingsPath(), catalog)
	systemLanguages := options.SystemLanguages
	if systemLanguages == nil {
		systemLanguages = userPreferredUILanguages
	}
	settings.useSystemLanguages(systemLanguages)
	settings.seedInitialLocale(systemLanguages())
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
	defer masters.Close()
	resolver := masters.DisplayResolver()
	historyStore := openHistory(options.HistoryDatabase, masters)
	// Deferred after masters.Close, so it runs first: the capture has stopped and finished its history by then.
	defer historyStore.Close()
	captureService := service.NewCaptureService(service.SessionOpener(masters))
	captureService.SetPublishInterval(effectiveUpdateInterval(settings.current().Settings))
	captureService.SetHistory(historyStarter(historyStore, options.HistoryDatabase != ""))
	bridge := &Bridge{capture: captureService, defaults: options.Session, settings: settings, history: historyStore, masters: masters}
	bridge.catalog = catalog
	bridge.projection = &displayProjection{resolver: resolver}
	environmentProbe := options.EnvironmentProbe
	if environmentProbe == nil {
		environmentProbe = capture.SystemEnvironmentProbe().Check
	}
	bridge.environment = newEnvironmentChecker(environmentProbe)
	bridge.npcap = newNpcapDownloadResolver(options.NpcapTransport)
	// The process list is read only while a capture is running; nothing polls while the application is idle.
	bridge.process = &service.GameProcessWatcher{}
	watcherContext, stopWatcher := context.WithCancel(context.Background())
	defer stopWatcher()
	go bridge.process.Run(watcherContext, func() bool { return captureService.Status().State == service.StateRunning })
	var app *application.App
	var keeper *desktopWindowKeeper
	shutdown := newShutdownGate(func(ctx context.Context) error {
		err := captureService.Shutdown(ctx)
		keeper.close(true)
		bridge.overlays.close(true)
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
			WndProcInterceptor:    keepForeground,
		},
	})
	bridge.openBrowser = options.OpenBrowser
	if bridge.openBrowser == nil {
		bridge.openBrowser = app.Browser.OpenURL
	}

	desktopURL, width, height := "/?view=desktop&"+bootLocaleQuery(settings), desktopShape.width, desktopShape.height
	if options.Showcase {
		desktopURL = "/?view=showcase"
	} else {
		keeper = newDesktopWindowKeeper(app, settings, desktopShape)
		bridge.desktopWindow = keeper
		width, height = keeper.size()
	}
	desktop := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             desktopWindowName,
		Title:            "WWlogs",
		Width:            width,
		Height:           height,
		MinWidth:         desktopShape.minWidth,
		MinHeight:        desktopShape.minHeight,
		URL:              desktopURL,
		BackgroundColour: application.NewRGB(15, 17, 23),
		InitialPosition:  application.WindowCentered,
		Hidden:           keeper != nil,
	})
	if keeper != nil {
		// The window opens where the user left it: the keeper places and shows it once its runtime is ready.
		keeper.attach(desktop)
		defer keeper.close(false)
	} else {
		// The Showcase review window keeps its default and saves nothing. Wails reads the displays only when the
		// application runs, so a work area smaller than the default is handled then.
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			fitDesktopWindow(desktop, app.Screen.GetPrimary(), desktopShape)
		})
	}
	if options.Showcase {
		// The Showcase is a review surface: it must not rewrite the user's saved windows or settings.
		if options.ShowcaseOverlays {
			openShowcaseOverlays(app)
		}
	} else {
		bridge.overlays = newOverlayManager(app, settings, bridge.shell.fire, shutdown.requested.Load)
		// Overlays are created on first use, so they are opened only after the application loop runs.
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			go bridge.overlays.start(options.ShowOverlay)
		})
		// Cancel placement timers on startup failure as well; native teardown already persisted successful runs.
		defer bridge.overlays.close(false)
	}
	// Closing the Desktop window quits the application, including any hidden Overlay.
	desktop.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !shutdown.ready.Load() {
			event.Cancel()
			shutdown.request()
		}
	})
	if options.AutoStart {
		// Development -autostart and -replay start their own configured session; the saved preference does not add one.
		bridge.startup.claim()
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			go func() {
				if err := captureService.Start(options.Session); err != nil {
					fmt.Fprintf(os.Stderr, "capture start failed: %v\n", err)
				}
			}()
		})
	} else if !options.Showcase {
		// The saved "start capture when WWlogs launches" preference, evaluated once per run.
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			go func() {
				outcome, err := bridge.startAtLaunch(watcherContext)
				if err != nil {
					fmt.Fprintf(os.Stderr, "automatic capture start failed (%s): %v\n", outcome, err)
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
