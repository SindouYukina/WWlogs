// Package desktop is the Wails v3 application host: one Go process, one CaptureService, one Publisher, and three
// windows (Desktop, Combat Overlay and Effects Overlay) that load the same frontend with different views. All capture and
// statistics logic stays in internal/capture and internal/service; this package only creates windows and exposes the
// Bridge.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"wwlogs/internal/assets"
	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

// desktopWindowName names the main window; the frontend selects its view from the URL, not from window names.
const desktopWindowName = "desktop"

// Options configure the host. The command line of the composed build profile fills them: ParseOptions in a Public
// build, the Development flags (wwlogs/internal/devdesktop) in a Development build.
type Options struct {
	MasterDatabase string
	Locale         string
	Session        capture.SessionConfig
	// AutoStart starts a capture with Session as soon as the windows exist.
	AutoStart bool
	// ShowOverlay shows both Overlays at start; otherwise each restores its saved visibility.
	ShowOverlay bool
	// Review opens a review surface instead of the Desktop view; nil runs the product. Only the Development composition
	// supplies one (the Motion Showcase of wwlogs/internal/devdesktop); Public flags cannot set it, and the host holds no
	// review content of its own.
	Review *ReviewSurface
	// GameAssetRoot is an explicitly supplied directory for game images. Empty means the images embedded in the
	// executable, if any. Only Development flags can set it.
	GameAssetRoot string
	// HistoryDatabase is an explicitly supplied Combat History file for tests; empty uses the user's own. Only it records
	// replays, so a replay never enters the user's own history. Only Development flags can set it.
	HistoryDatabase string
	// WebviewArguments are extra WebView2 browser arguments (rendering experiments, the remote debugging port for
	// automated UI checks); only Development flags can set them.
	WebviewArguments []string
	// SystemLanguages replaces the Windows display-language query for controlled UI checks. Only Development sets it.
	SystemLanguages func() []string
	// EnvironmentProbe, NpcapTransport and OpenBrowser replace system access for controlled UI checks. Only Development
	// flags can set them; nil means the real system.
	EnvironmentProbe func() capture.CaptureEnvironment
	NpcapTransport   http.RoundTripper
	OpenBrowser      func(string) error
}

// ReviewSurface is a review window the host opens instead of the Desktop view: it loads Route with the default window
// shape, keeps and saves no window state, and has no Overlay manager and no saved automatic capture start. A Development
// composition supplies its content (the Motion Showcase, wwlogs/internal/devdesktop); the host only opens it.
type ReviewSurface struct {
	// Route is the frontend route of the review window.
	Route string
	// Assets, when set, wraps the asset handler, for example to serve explicitly supplied files.
	Assets func(http.Handler) http.Handler
	// Open, when set, opens the surface's further windows before the application runs.
	Open func(app *application.App)
}

// Composition is what a build profile composes into the host. The build composition boundary of the executable
// (cmd/desktop/compose_public.go, compose_development.go) supplies it, so the host itself has no profile-specific code.
type Composition struct {
	// ParseOptions reads the command line: ParseOptions for Public, the Development flags of
	// wwlogs/internal/devdesktop for Development.
	ParseOptions func(arguments []string, output io.Writer) (Options, error)
	// PrintVersion prints the build identity for -version.
	PrintVersion func(io.Writer)
}

// Main parses the command line of the composed profile and runs the host until the Desktop window closes. Only one WWlogs
// runs for each Windows user: a launch while one runs brings that one's window back and exits (launcher.launch).
func Main(composition Composition) {
	os.Exit(systemLauncher.launch(composition, os.Args[1:], os.Stdout, os.Stderr))
}

// run starts the application and blocks until it quits. The capture is stopped and its resources released before run
// returns. activation, when set, is given the window a later launch brings back.
func run(options Options, activation *instanceActivation) error {
	frontend, _, err := frontendFS()
	if err != nil {
		return err
	}
	images := gameAssetSource(options.GameAssetRoot, gameAssetArchive)
	assetHandler := withGameAssets(application.AssetFileServerFS(frontend), images)
	if options.Review != nil && options.Review.Assets != nil {
		assetHandler = options.Review.Assets(assetHandler)
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
		if _, err := settings.setLocale(catalog.optionLocale(options.Locale)); err != nil {
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
	recorders := &recorderTracker{}
	captureService.SetHistory(historyStarter(historyStore, options.HistoryDatabase != "", recorders))
	captureService.SetHistoryRecording(!settings.current().Settings.HistoryRecordingDisabled)
	bridge := &Bridge{capture: captureService, defaults: options.Session, settings: settings, history: historyStore, masters: masters, recorders: recorders, images: assets.NewResolver(images)}
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
			AdditionalBrowserArgs: options.WebviewArguments,
			WndProcInterceptor:    keepForeground,
		},
	})
	bridge.openBrowser = options.OpenBrowser
	if bridge.openBrowser == nil {
		bridge.openBrowser = app.Browser.OpenURL
	}

	desktopURL, width, height := "/?view=desktop&"+bootLocaleQuery(settings), desktopShape.width, desktopShape.height
	if options.Review != nil {
		desktopURL = options.Review.Route
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
	// A later launch brings this window back, the Desktop window or the review window alike, and waits while it closes.
	activation.attach(desktop, shutdown.requested.Load)
	if keeper != nil {
		// The window opens where the user left it: the keeper places and shows it once its runtime is ready.
		keeper.attach(desktop)
		defer keeper.close(false)
	} else {
		// A review window keeps its default and saves nothing. Wails reads the displays only when the application runs,
		// so a work area smaller than the default is handled then.
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			fitDesktopWindow(desktop, app.Screen.GetPrimary(), desktopShape)
		})
	}
	if options.Review != nil {
		// A review surface must not rewrite the user's saved windows or settings, so it has no Overlay manager.
		if options.Review.Open != nil {
			options.Review.Open(app)
		}
	} else {
		bridge.overlays = newOverlayManager(app, settings, bridge.shell.fire, shutdown.requested.Load, captureService.Publisher().Next)
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
	} else if options.Review == nil {
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
