//go:build !wwdev

package main

import (
	"wwlogs/internal/capture"
	"wwlogs/internal/desktop"
)

// composition is the Public host: the product command line and build identity. No Development module is linked.
var composition = desktop.Composition{ParseOptions: desktop.ParseOptions, PrintVersion: capture.PrintVersion}
