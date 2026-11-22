package versioninfo_test

import (
	"testing"

	"wwlogs/internal/versioninfo"
)

func TestCurrentMetadataIsComplete(t *testing.T) {
	value := versioninfo.Current()
	if value.Product.DevelopmentLine != "v1.0.0" {
		t.Fatalf("unexpected product development line: %q", value.Product.DevelopmentLine)
	}
}
