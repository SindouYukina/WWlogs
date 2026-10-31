package versioninfo_test

import (
	"strings"
	"testing"

	"wwlogs/internal/versioninfo"
)

func TestCurrentMetadataIsComplete(t *testing.T) {
	value := versioninfo.Current()
	if value.Product.DevelopmentLine == "" {
		t.Fatal("product development line is empty")
	}
	if value.ObservedClient.Region == "" || value.ObservedClient.GameVersion == "" || value.ObservedClient.ResourceVersion == "" || value.ObservedClient.LoginBuild == "" || len(value.ObservedClient.CheckClientPushVersions) != 3 {
		t.Fatal("observed client metadata is incomplete")
	}
	for name, schema := range map[string]versioninfo.SchemaMetadata{
		"windows": value.Schemas.WindowsGlobal,
		"client": value.Schemas.clientGlobal,
	} {
		if schema.Label == "" || schema.Version == "" || schema.Compatibility == "" {
			t.Errorf("%s schema metadata is incomplete: %+v", name, schema)
		}
	}
	builder := value.MasterBuilder
	if builder.Region == "" || builder.GameVersion == "" || builder.ResourceVersion == "" || builder.SourceCommit == "" || builder.SourceURL == "" || builder.DefaultLocale == "" {
		t.Fatalf("master builder metadata is incomplete: %+v", builder)
	}
	if !strings.Contains(versioninfo.WindowsSchemaLabel(), versioninfo.WindowsSchemaVersion()) || !strings.Contains(versioninfo.clientSchemaLabel(), versioninfo.clientSchemaVersion()) {
		t.Fatal("schema labels do not include their registered version")
	}
}
