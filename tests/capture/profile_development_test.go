//go:build wwdev

package capture

import (
	"io"
	"strings"
	"testing"
)

// diagnosticConsole reports whether this build profile prints per-event console diagnostics.
const diagnosticConsole = true

// The development build keeps the sinks that research needs.
func TestDevelopmentProcessorKeepsDiagnosticSinks(t *testing.T) {
	var p Processor
	p.Dev.encounterSummary = &strings.Builder{}
	if p.Dev.encounterSummary == nil {
		t.Fatal("development processor lost its diagnostic sink")
	}
}

// discardConsole drops the console lines of a test processor that does not read them.
func discardConsole(p *Processor) { p.Dev.stdout = io.Discard }
