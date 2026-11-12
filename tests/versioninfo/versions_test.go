package versioninfo_test

import (
	"strings"
	"testing"

	"wwlogs/internal/versioninfo"
)

func TestCurrentMetadataIsComplete(t *testing.T) {
	value := versioninfo.Current()
	if value.Product.DevelopmentLine != "v0.7.0" {
		t.Fatalf("unexpected product development line: %q", value.Product.DevelopmentLine)
	}
	for name, schema := range map[string]versioninfo.SchemaMetadata{
		"windows":   value.Schemas.WindowsGlobal,
		"reference": value.Schemas.ReferenceGlobal,
	} {
		if schema.Label == "" || schema.Version == "" || schema.Compatibility == "" {
			t.Errorf("%s schema metadata is incomplete: %+v", name, schema)
		}
	}
	if !strings.Contains(versioninfo.WindowsSchemaLabel(), versioninfo.WindowsSchemaVersion()) || !strings.Contains(versioninfo.ReferenceSchemaLabel(), versioninfo.ReferenceSchemaVersion()) {
		t.Fatal("schema labels do not include their registered version")
	}
}
