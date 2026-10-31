//go:build !wwdev

package capture

import "testing"

// diagnosticConsole reports whether this build profile prints per-event console diagnostics.
const diagnosticConsole = false

// A public build must not be able to hold a diagnostic sink: the sink type is a pointer that nothing can set.
func TestPublicDiagnosticSinksStayNil(t *testing.T) {
	p := newProcessor(nil, nopWriter{}, nopWriter{}, nil)
	if p.encounterSummary != nil {
		t.Fatal("a public processor has a non-nil diagnostic sink")
	}
	var unavailable diagWriter
	if unavailable != nil {
		t.Fatal("diagWriter has a non-nil zero value")
	}
	if _, err := (&unavailableWriter{}).Write([]byte("x")); err == nil {
		t.Fatal("the unavailable writer accepted data")
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
