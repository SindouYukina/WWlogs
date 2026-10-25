package main

import "os"

// exitIfVersionRequested prints the build identity and exits when -version is the request. It runs before
// any other start-up work, so asking for the version never touches files, the network, or the capture driver.
func exitIfVersionRequested() {
	for _, argument := range os.Args[1:] {
		if argument == "-version" || argument == "--version" {
			printVersion(os.Stdout)
			os.Exit(0)
		}
	}
}
