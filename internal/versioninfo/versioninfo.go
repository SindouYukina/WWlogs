package versioninfo

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed versions.json
var versionsJSON []byte

// Metadata records the active product development line.
type Metadata struct {
	Product struct {
		DevelopmentLine string `json:"developmentLine"`
	} `json:"product"`
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
