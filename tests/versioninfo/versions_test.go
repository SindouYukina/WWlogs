package versioninfo_test

import (
	"strings"
	"testing"

	"wwlogs/internal/versioninfo"
)

func TestCurrentMetadataIsComplete(t *testing.T) {
	value := versioninfo.Current()
	if value.Product.DevelopmentLine != "v0.6.0" {
		t.Fatalf("unexpected product development line: %q", value.Product.DevelopmentLine)
	}
	if value.ObservedClient.GameVersion != "3.7.0" || value.ObservedClient.ResourceVersion != "3.7.17" {
		t.Fatalf("observed client metadata does not match the latest verified capture: %+v", value.ObservedClient)
	}
	if value.ObservedClient.Region == "" || value.ObservedClient.GameVersion == "" || value.ObservedClient.ResourceVersion == "" || value.ObservedClient.LoginBuild == "" || len(value.ObservedClient.CheckClientPushVersions) != 3 {
		t.Fatal("observed client metadata is incomplete")
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
