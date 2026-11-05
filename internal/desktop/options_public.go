//go:build !wwdev

package desktop

import (
	"flag"
	"io"

	"wwlogs/internal/capture"
)

// parseOptions reads the Public Build flags: the Master database and the display locale only. Replay, automatic
// start, and diagnostics are Development features and do not exist here.
func parseOptions(arguments []string, output io.Writer) (Options, error) {
	flags := flag.NewFlagSet("WWlogs", flag.ContinueOnError)
	flags.SetOutput(output)
	masterDatabase := flags.String("master-db", capture.DefaultMasterDatabasePath(), "Read-only combat ID Master SQLite database")
	locale := flags.String("locale", "zh-TW", "Locale for Master display names: zh-TW, zh-CN, en-US, ja-JP, or ko-KR")
	flags.Bool("version", false, "Print the WWlogs version and build identity")
	if err := flags.Parse(arguments); err != nil {
		return Options{}, err
	}
	return Options{MasterDatabase: *masterDatabase, Locale: *locale}, nil
}

// webviewArguments adds no browser arguments in Public builds: WebView2 debugging does not exist there.
func webviewArguments(Options) []string {
	return nil
}
