//go:build wwdev

package main

import (
	"strings"
	"testing"
)

// diagnosticConsole reports whether this build profile prints per-event console diagnostics.
const diagnosticConsole = true

// The development build keeps the sinks that research needs.
func TestDevelopmentProcessorKeepsDiagnosticSinks(t *testing.T) {
	var p processor
	p.encounterSummary = &strings.Builder{}
	if p.encounterSummary == nil {
		t.Fatal("development processor lost its diagnostic sink")
	}
}
