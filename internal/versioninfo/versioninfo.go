package versioninfo

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed versions.json
var versionsJSON []byte

// Metadata records the active client, schema, and product data version roles.
type Metadata struct {
	Product struct {
		DevelopmentLine string `json:"developmentLine"`
	} `json:"product"`
	ObservedClient struct {
		Region                  string   `json:"region"`
		GameVersion             string   `json:"gameVersion"`
		ResourceVersion         string   `json:"resourceVersion"`
		LoginBuild              string   `json:"loginBuild"`
		CheckClientPushVersions []string `json:"checkClientPushVersions"`
	} `json:"observedClient"`
	Schemas struct {
		WindowsGlobal SchemaMetadata `json:"windowsGlobal"`
		clientGlobal  SchemaMetadata `json:"clientGlobal"`
	} `json:"schemas"`
	MasterBuilder struct {
		Region          string `json:"region"`
		GameVersion     string `json:"gameVersion"`
		ResourceVersion string `json:"resourceVersion"`
		SourceCommit    string `json:"sourceCommit"`
		SourceURL       string `json:"sourceURL"`
		DefaultLocale   string `json:"defaultLocale"`
	} `json:"masterBuilder"`
}

// SchemaMetadata keeps a schema's source label and compatibility evidence together.
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

// clientSchemaLabel formats the registered client schema source and version.
func clientSchemaLabel() string {
	return schemaLabel(current.Schemas.clientGlobal)
}

func schemaLabel(value SchemaMetadata) string {
	return strings.TrimSpace(value.Label + " " + value.Version)
}

// WindowsSchemaVersion returns the registered Windows schema version.
func WindowsSchemaVersion() string {
	return current.Schemas.WindowsGlobal.Version
}

// clientSchemaVersion returns the registered client schema version.
func clientSchemaVersion() string {
	return current.Schemas.clientGlobal.Version
}
