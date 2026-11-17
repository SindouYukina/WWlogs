//go:build !wwdev

package capture

import "io"

// processorDiagnostics is the Development state of a processor (processor_diagnostics_development.go). A Public build
// has no console lines, research outputs or raw diagnostic sinks, so it holds nothing and costs nothing.
type processorDiagnostics struct{}

func newProcessorDiagnostics(io.Writer, io.Writer) processorDiagnostics {
	return processorDiagnostics{}
}

func (*processorDiagnostics) beginProtocolSession() {
}
