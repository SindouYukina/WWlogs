package capture

import (
	"fmt"
	"io"

	"wwlogs/internal/buildinfo"
)

// printVersion prints the version and the build identity that needs no file access.
func printVersion(w io.Writer) {
	identity := buildinfo.Current()
	fmt.Fprintf(w, "WWlogs version: %s\n", identity.Version)
	fmt.Fprintf(w, "Build number: %s\n", identity.BuildLabel())
	fmt.Fprintf(w, "Build profile: %s\n", identity.Profile)
	fmt.Fprintf(w, "Commit: %s\n", identity.Commit)
	fmt.Fprintf(w, "Working tree modified: %s\n", identity.Dirty)
	printVersionDetails(w)
}

// PrintVersion prints the build identity for another executable of this product, such as the desktop host.
func PrintVersion(w io.Writer) {
	printVersion(w)
}
