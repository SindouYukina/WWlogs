//go:build !wwdev

package main

import "io"

// printVersionDetails prints nothing extra: public builds do not hash the executable at start-up.
func printVersionDetails(io.Writer) {}
