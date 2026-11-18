package devcapture

import (
	"fmt"
	"io"

	"wwlogs/internal/buildinfo"
	"wwlogs/internal/capture"
)

// PrintVersion prints the build identity of a Development executable: the Public lines, then the hash of the running
// executable, which Development builds record in their metadata.
func PrintVersion(w io.Writer) {
	capture.PrintVersion(w)
	fmt.Fprintf(w, "Executable SHA-256: %s\n", buildinfo.ExecutableSHA256())
}

// announceBuildIdentity prints the build identity once at start-up.
func announceBuildIdentity(w io.Writer) {
	identity := buildinfo.Current()
	fmt.Fprintf(w, "build_identity version=%s build_number=%s profile=%s commit=%s dirty=%s executable_sha256=%s\n",
		identity.Version, identity.BuildLabel(), identity.Profile, identity.Commit, identity.Dirty, buildinfo.ExecutableSHA256())
}
