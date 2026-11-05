//go:build wwdev

package desktop

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"wwlogs/internal/capture"
)

// parseOptions reads the Development Build flags. Besides the Public flags they allow an automatic start, a paced
// PCAP replay through the full host (for UI work without the game), the Development diagnostic PCAP, and showing the
// overlay at start.
func parseOptions(arguments []string, output io.Writer) (Options, error) {
	flags := flag.NewFlagSet("WWlogs-dev", flag.ContinueOnError)
	flags.SetOutput(output)
	masterDatabase := flags.String("master-db", capture.DefaultMasterDatabasePath(), "Read-only combat ID Master SQLite database")
	locale := flags.String("locale", "", "Initial global display locale; approved bundled locales only (overrides saved preference)")
	flags.Bool("version", false, "Print the WWlogs version and build identity")
	ip := flags.String("ip", "", "Local IPv4 address: preselects the capture interface and decides packet direction in replay")
	replay := flags.String("replay", "", "Development: replay this PCAP through the host instead of capturing live; starts automatically")
	pacing := flags.Float64("replay-pacing", 1, "Development: replay speed factor (1 = capture speed, 0 = as fast as possible)")
	autoStart := flags.Bool("autostart", false, "Development: start live capture on -ip when the windows open")
	diagnostics := flags.Bool("capture-diagnostics", false, "Development: record each live session to the local diagnostic PCAP spool")
	showOverlay := flags.Bool("overlay", false, "Development: show the overlay window at start")
	debugPort := flags.Int("webview-debug-port", 0, "Development: open the WebView2 remote debugging port for automated UI checks")
	publishInterval := flags.Duration("publish-interval", capture.DefaultPublishInterval, "Development: minimum time between UI snapshots (at least 100ms)")
	if err := flags.Parse(arguments); err != nil {
		return Options{}, err
	}
	if *replay != "" && *diagnostics {
		return Options{}, errors.New("-capture-diagnostics applies to live capture only")
	}
	if *publishInterval < capture.MinPublishInterval {
		*publishInterval = capture.MinPublishInterval
	}
	return Options{
		MasterDatabase: *masterDatabase,
		Locale:         *locale,
		Session: capture.SessionConfig{
			LocalIPv4: *ip, ReplayPath: *replay, ReplayPacing: *pacing,
			PublishInterval: *publishInterval, Diagnostics: *diagnostics,
		},
		AutoStart:        *autoStart || *replay != "",
		ShowOverlay:      *showOverlay,
		WebviewDebugPort: *debugPort,
	}, nil
}

// webviewArguments opens the WebView2 remote debugging port when requested, for automated UI checks.
func webviewArguments(options Options) []string {
	if options.WebviewDebugPort <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("--remote-debugging-port=%d", options.WebviewDebugPort)}
}
