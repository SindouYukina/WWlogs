//go:build !wwdev

package buildinfo

import "testing"

// A build without the wwdev tag must never identify itself as a development build.
func TestProfileIsPublicWithoutTag(t *testing.T) {
	if profile != ProfilePublic || Current().Profile != ProfilePublic {
		t.Fatalf("profile = %q, want public", profile)
	}
}
