//go:build wwdev

package capture

import (
	"fmt"
	"io"

	"wwlogs/internal/buildinfo"
)

// printVersionDetails adds the hash of the running executable, which development builds record in their metadata.
func printVersionDetails(w io.Writer) {
	fmt.Fprintf(w, "Executable SHA-256: %s\n", buildinfo.ExecutableSHA256())
}

// announceBuildIdentity prints the build identity once at start-up.
func announceBuildIdentity(w io.Writer) {
	identity := buildinfo.Current()
	fmt.Fprintf(w, "build_identity version=%s build_number=%s profile=%s commit=%s dirty=%s executable_sha256=%s\n",
		identity.Version, identity.BuildLabel(), identity.Profile, identity.Commit, identity.Dirty, buildinfo.ExecutableSHA256())
}
