package capture

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// A Development diagnostic row is written beside the core processing of a message, never instead of it. A row that
// encoding/json rejects (a NaN or infinite duration, for example) or a sink that refuses the write is counted per sink
// and reported; the message's effect, damage and identity processing continues unchanged, and the failure is never a
// parse error. A public build has no diagnostic sinks (diagWriter cannot be set there), so none of this runs in it.

// writeDiagnosticRow encodes one row to a diagnostic sink.
func (p *processor) writeDiagnosticRow(sink diagWriter, name string, row any) {
	if sink == nil {
		return
	}
	if err := json.NewEncoder(sink).Encode(row); err != nil {
		p.noteDiagnosticFailure(name, err)
	}
}

// noteDiagnosticFailure counts one failed diagnostic write. The first failure of each sink is also written to the
// diagnostic stream, so a failing sink is visible without flooding the console.
func (p *processor) noteDiagnosticFailure(name string, err error) {
	if p.diagnosticFailures == nil {
		p.diagnosticFailures = make(map[string]int)
	}
	p.diagnosticFailures[name]++
	if p.diagnosticFailures[name] == 1 && p.diagnostic != nil {
		fmt.Fprintf(p.diagnostic, "diagnostic_output_error sink=%s error=%v\n", name, err)
	}
}

// diagnosticFailureLine summarizes the failed diagnostic writes of the capture by sink, or returns "" when there were none.
func (p *processor) diagnosticFailureLine() string {
	if len(p.diagnosticFailures) == 0 {
		return ""
	}
	names := make([]string, 0, len(p.diagnosticFailures))
	for name := range p.diagnosticFailures {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, p.diagnosticFailures[name]))
	}
	return "diagnostic_output_errors " + strings.Join(parts, " ")
}
