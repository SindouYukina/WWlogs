//go:build wwdev

package capture

import (
	"bufio"
	"io"
)

// perfWrapDiagnostics routes the measured sinks through the development diagnostic pipeline in replay (waiting)
// mode, as the development executable does, and returns the function that drains it.
func perfWrapDiagnostics(p *processor, console *bufio.Writer) func() {
	pipeline := newDiagnosticPipeline(false)
	restore := wrapProcessorDiagnostics(p, pipeline, map[io.Writer]func() error{console: console.Flush})
	return func() {
		pipeline.finishCapture()
		pipeline.close(diagnosticFlushTimeout)
		restore()
	}
}
