//go:build !wwdev

package main

import "wwlogs/internal/capture"

// runCapture is the Public capture command line: confirmed statistics only. No Development module is linked.
var runCapture = capture.Main
