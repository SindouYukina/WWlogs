//go:build wwdev

package main

import (
	"wwlogs/internal/capture"
	"wwlogs/internal/desktop"
	"wwlogs/internal/devdesktop"
)

// composition is the Development host: the shared host with the Development command line of
// wwlogs/internal/devdesktop.
var composition = desktop.Composition{ParseOptions: devdesktop.ParseOptions, PrintVersion: capture.PrintVersion}
