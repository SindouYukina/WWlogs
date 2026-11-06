//go:build !wwdev

package desktop

import (
	"flag"
	"io"
)

// parseOptions reads the Public Build flags: the display locale only. The Master database is embedded. Replay,
// automatic start, and diagnostics are Development features and do not exist here.
func parseOptions(arguments []string, output io.Writer) (Options, error) {
	flags := flag.NewFlagSet("WWlogs", flag.ContinueOnError)
	flags.SetOutput(output)
	locale := flags.String("locale", "", "Initial global display locale; approved bundled locales only (overrides saved preference)")
	flags.Bool("version", false, "Print the WWlogs version and build identity")
	if err := flags.Parse(arguments); err != nil {
		return Options{}, err
	}
	return Options{Locale: *locale}, nil
}

// webviewArguments adds no browser arguments in Public builds: WebView2 debugging does not exist there.
func webviewArguments(Options) []string {
	return nil
}
