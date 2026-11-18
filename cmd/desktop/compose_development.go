//go:build wwdev

package main

import (
	"wwlogs/internal/desktop"
	"wwlogs/internal/devcapture"
	"wwlogs/internal/devdesktop"
)

// composition is the Development host: the shared host with the Development command line of
// wwlogs/internal/devdesktop (which also opens the live session diagnostics of wwlogs/internal/devcapture) and the
// Development build identity.
var composition = desktop.Composition{ParseOptions: devdesktop.ParseOptions, PrintVersion: devcapture.PrintVersion}
