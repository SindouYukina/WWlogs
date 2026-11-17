package devcapture

import (
	"io"
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

// The Development processor keeps the sinks that research needs, and is the observer of its core.
func TestDevelopmentProcessorKeepsDiagnosticSinks(t *testing.T) {
	p := newProcessor(nil, io.Discard, io.Discard, nil)
	p.dev.encounterSummary = &strings.Builder{}
	if p.dev.encounterSummary == nil {
		t.Fatal("development processor lost its diagnostic sink")
	}
	var core capture.Observer = p
	if core == nil {
		t.Fatal("the Development processor is not an observer")
	}
}

// discardConsole drops the console lines of a test processor that does not read them.
func discardConsole(p *processor) { p.dev.stdout = io.Discard }

// observed attaches a hand-built Development processor to its core as the observer, as newProcessor does.
func observed(p *processor) *processor {
	p.Processor.Observe(p)
	return p
}
