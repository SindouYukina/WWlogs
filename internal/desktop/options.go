package desktop

import (
	"flag"
	"io"
)

// ParseOptions reads the Public command line: the display locale only. The Master database is embedded. Replay,
// automatic start, diagnostics and UI-check simulations are Development capabilities (wwlogs/internal/devdesktop) that
// a Public build does not compose, so they do not exist here.
func ParseOptions(arguments []string, output io.Writer) (Options, error) {
	flags := flag.NewFlagSet("WWlogs", flag.ContinueOnError)
	flags.SetOutput(output)
	locale := flags.String("locale", "", "Initial global display locale; approved bundled locales only (overrides saved preference)")
	flags.Bool("version", false, "Print the WWlogs version and build identity")
	if err := flags.Parse(arguments); err != nil {
		return Options{}, err
	}
	return Options{Locale: *locale}, nil
}
