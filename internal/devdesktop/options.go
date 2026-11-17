// Package devdesktop holds the Development capabilities of the desktop host: its command line (PCAP replay through
// the full host, automatic start, the live diagnostic spool, the Motion Showcase, explicit asset, font, Master and
// History inputs, UI-check simulations and WebView2 debugging). Only the Development build composition
// (cmd/desktop/compose_development.go) links it; a Public executable contains none of it.
package devdesktop

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/desktop"
)

// ParseOptions reads the Development Build flags. Besides the Public flags they allow an automatic start, a paced
// PCAP replay through the full host (for UI work without the game), the Development diagnostic PCAP, and showing the
// overlay at start.
func ParseOptions(arguments []string, output io.Writer) (desktop.Options, error) {
	flags := flag.NewFlagSet("WWlogs-dev", flag.ContinueOnError)
	flags.SetOutput(output)
	masterDatabase := flags.String("master-db", "", "Development: use an explicit read-only Master SQLite database for testing (default: embedded)")
	locale := flags.String("locale", "", "Initial global display locale; approved bundled locales only (overrides saved preference)")
	flags.Bool("version", false, "Print the WWlogs version and build identity")
	ip := flags.String("ip", "", "Local IPv4 address: preselects the capture interface and decides packet direction in replay")
	replay := flags.String("replay", "", "Development: replay this PCAP through the host instead of capturing live; starts automatically")
	pacing := flags.Float64("replay-pacing", 1, "Development: replay speed factor (1 = capture speed, 0 = as fast as possible)")
	autoStart := flags.Bool("autostart", false, "Development: start live capture on -ip when the windows open")
	diagnostics := flags.Bool("capture-diagnostics", true, "Development: record live PCAP and diagnostics to the local spool (default true; replay does not record)")
	showOverlay := flags.Bool("overlay", false, "Development: show the overlay window at start")
	debugPort := flags.Int("webview-debug-port", 0, "Development: open the WebView2 remote debugging port for automated UI checks")
	showcase := flags.Bool("showcase", false, "Development: open the Motion Showcase (mock data only) instead of the Desktop view")
	showcaseOverlays := flags.Bool("showcase-overlays", false, "Development: with -showcase, also open its transparent Overlay windows")
	webviewArgs := flags.String("webview-args", "", "Development: extra WebView2 browser arguments for rendering experiments, separated by spaces")
	assetRoot := flags.String("asset-root", "", "Development: explicitly override embedded game images with a directory that contains game/")
	historyDatabase := flags.String("history-db", "", "Development: record and show Combat History in this test file instead of the user's own; only this file records replays")
	fontRoot := flags.String("font-root", "", "Development: directory that contains local Showcase font candidates; nothing is served without it")
	simulateEnvironment := flags.String("simulate-capture-environment", "", "Development: comma-separated states, one per check, for UI checks; the last repeats and \"ready\" is the real check")
	npcapOffline := flags.Bool("simulate-npcap-offline", false, "Development: make the official Npcap sources unreachable, for UI checks")
	browserLog := flags.String("record-browser-url", "", "Development: append URLs to this file instead of opening the browser, for UI checks")
	simulatedLanguages := flags.String("simulate-ui-languages", "", "Development: comma-separated Windows display languages in preference order, for UI checks of a first launch")
	if err := flags.Parse(arguments); err != nil {
		return desktop.Options{}, err
	}
	environmentProbe, err := scriptedEnvironment(*simulateEnvironment)
	if err != nil {
		return desktop.Options{}, err
	}
	var npcapTransport http.RoundTripper
	if *npcapOffline {
		npcapTransport = offlineTransport{}
	}
	var openBrowser func(string) error
	if *browserLog != "" {
		openBrowser = func(address string) error { return appendLine(*browserLog, address) }
	}
	var systemLanguages func() []string
	if *simulatedLanguages != "" {
		languages := strings.Split(*simulatedLanguages, ",")
		systemLanguages = func() []string { return languages }
	}
	if *replay != "" && *diagnostics && flagsWereSet(flags, "capture-diagnostics") {
		return desktop.Options{}, errors.New("-capture-diagnostics applies to live capture only")
	}
	if *replay != "" {
		*diagnostics = false
	}
	if *showcaseOverlays && !*showcase {
		return desktop.Options{}, errors.New("-showcase-overlays requires -showcase")
	}
	if *fontRoot != "" && !*showcase {
		return desktop.Options{}, errors.New("-font-root requires -showcase")
	}
	return desktop.Options{
		MasterDatabase: *masterDatabase,
		Locale:         *locale,
		Session: capture.SessionConfig{
			LocalIPv4: *ip, ReplayPath: *replay, ReplayPacing: *pacing, Diagnostics: *diagnostics,
		},
		AutoStart:        *autoStart || *replay != "",
		ShowOverlay:      *showOverlay,
		Showcase:         *showcase,
		ShowcaseOverlays: *showcaseOverlays,
		GameAssetRoot:    *assetRoot,
		HistoryDatabase:  *historyDatabase,
		PreviewFontRoot:  *fontRoot,
		WebviewArguments: webviewArguments(*webviewArgs, *debugPort),
		EnvironmentProbe: environmentProbe,
		NpcapTransport:   npcapTransport,
		OpenBrowser:      openBrowser,
		SystemLanguages:  systemLanguages,
	}, nil
}

// scriptedEnvironment walks through the named states, one per check, so the real UI and host can be driven through every
// Npcap state on a machine where Npcap works. "ready" runs the real check, so a script can end in a usable capture.
func scriptedEnvironment(script string) (func() capture.CaptureEnvironment, error) {
	if script == "" {
		return nil, nil
	}
	var states []capture.EnvironmentState
	for _, name := range strings.Split(script, ",") {
		state := capture.EnvironmentState(name)
		switch state {
		case capture.EnvironmentReady, capture.EnvironmentNpcapMissing, capture.EnvironmentNpcapUnavailable, capture.EnvironmentNoInterfaces, capture.EnvironmentNoIPv4, capture.EnvironmentCheckFailed:
			states = append(states, state)
		default:
			return nil, fmt.Errorf("unknown capture environment state %q", name)
		}
	}
	var position atomic.Int64
	return func() capture.CaptureEnvironment {
		// A short delay makes the checking state and repeated presses observable.
		time.Sleep(300 * time.Millisecond)
		index := min(int(position.Add(1))-1, len(states)-1)
		if states[index] == capture.EnvironmentReady {
			return capture.SystemEnvironmentProbe().Check()
		}
		return capture.CaptureEnvironment{State: states[index], Adapters: []capture.Adapter{}}
	}, nil
}

// offlineTransport fails every request the way an unreachable network does.
type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("simulated offline network")
}

func appendLine(path, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(file, line)
	return errors.Join(writeErr, file.Close())
}

func flagsWereSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// webviewArguments are the extra WebView2 browser arguments: the requested ones, then the remote debugging port for
// automated UI checks when one is requested.
func webviewArguments(requested string, debugPort int) []string {
	arguments := strings.Fields(requested)
	if debugPort > 0 {
		arguments = append(arguments, fmt.Sprintf("--remote-debugging-port=%d", debugPort))
	}
	return arguments
}
