package capture

import (
	"io"
	"os"
)

// ExitIfVersionRequested prints the build identity with print and exits when -version is the request. It runs
// before any other start-up work, so asking for the version never touches files, the network, or the capture driver.
func ExitIfVersionRequested(print func(io.Writer)) {
	for _, argument := range os.Args[1:] {
		if argument == "-version" || argument == "--version" {
			print(os.Stdout)
			os.Exit(0)
		}
	}
}
