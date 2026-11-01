//go:build wwdev

package buildinfo

import "testing"

func TestProfileIsDevelopmentWithTag(t *testing.T) {
	if profile != ProfileDevelopment || Current().Profile != ProfileDevelopment {
		t.Fatalf("profile = %q, want development", profile)
	}
}
