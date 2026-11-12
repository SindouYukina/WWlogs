package versioninfo

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed versions.json
var versionsJSON []byte

// Metadata records the active product and protocol schema versions.
type Metadata struct {
	Product struct {
		DevelopmentLine string `json:"developmentLine"`
	} `json:"product"`
	Schemas struct {
		WindowsGlobal   SchemaMetadata `json:"windowsGlobal"`
		ReferenceGlobal SchemaMetadata `json:"referenceGlobal"`
	} `json:"schemas"`
}

// SchemaMetadata records a schema label, version, and compatibility status.
type SchemaMetadata struct {
	Label         string `json:"label"`
	Version       string `json:"version"`
	Compatibility string `json:"compatibility"`
}

var current = load()

func load() Metadata {
	var value Metadata
	if err := json.Unmarshal(versionsJSON, &value); err != nil {
		panic(fmt.Errorf("decode embedded version metadata: %w", err))
	}
	return value
}

// Current returns the embedded version registry used by production packages.
func Current() Metadata {
	return current
}

// WindowsSchemaLabel formats the registered Windows schema source and version.
func WindowsSchemaLabel() string {
	return schemaLabel(current.Schemas.WindowsGlobal)
}

// ReferenceSchemaLabel formats the registered reference schema and version.
func ReferenceSchemaLabel() string {
	return schemaLabel(current.Schemas.ReferenceGlobal)
}

func schemaLabel(value SchemaMetadata) string {
	return strings.TrimSpace(value.Label + " " + value.Version)
}

// WindowsSchemaVersion returns the registered Windows schema version.
func WindowsSchemaVersion() string {
	return current.Schemas.WindowsGlobal.Version
}

// ReferenceSchemaVersion returns the registered reference schema version.
func ReferenceSchemaVersion() string {
	return current.Schemas.ReferenceGlobal.Version
}
