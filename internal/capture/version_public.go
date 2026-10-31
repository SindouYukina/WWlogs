//go:build !wwdev

package capture

import "io"

// printVersionDetails prints nothing extra: public builds do not hash the executable at start-up.
func printVersionDetails(io.Writer) {}
