//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// A Development diagnostic row is written beside the core processing of a message, never instead of it. A row that
// encoding/json rejects (a NaN or infinite duration, for example) or a sink that refuses the write is counted per sink
// and reported; the message's effect, damage and identity processing continues unchanged, and the failure is never a
// parse error. A public build has no diagnostic sinks at all (processor_diagnostics_public.go), so none of this exists in it.

// writeDiagnosticRow encodes one row to a diagnostic sink. Callers check the sink first: passing a row converts it to an
// interface value, which allocates, and must not happen on the core path of a run without diagnostic outputs.
func (p *processor) writeDiagnosticRow(sink io.Writer, name string, row any) {
	if sink == nil {
		return
	}
	if err := json.NewEncoder(sink).Encode(row); err != nil {
		p.noteDiagnosticFailure(name, err)
	}
}

// writeResearchRecord writes one record of a research output (frame archive, schema decode, role config, reference,
// state and push outputs) as a JSON line, and counts a record that could not be encoded or written in that output's
// error counter. It reports whether the record could be encoded.
func writeResearchRecord(sink io.Writer, record any, failures *int) bool {
	line, err := json.Marshal(record)
	if err != nil {
		*failures++
		return false
	}
	if _, err := sink.Write(append(line, '\n')); err != nil {
		*failures++
	}
	return true
}

// noteDiagnosticFailure counts one failed diagnostic write. The first failure of each sink is also written to the
// diagnostic stream, so a failing sink is visible without flooding the console.
func (p *processor) noteDiagnosticFailure(name string, err error) {
	if p.dev.diagnosticFailures == nil {
		p.dev.diagnosticFailures = make(map[string]int)
	}
	p.dev.diagnosticFailures[name]++
	if p.dev.diagnosticFailures[name] == 1 && p.diagnostic != nil {
		fmt.Fprintf(p.diagnostic, "diagnostic_output_error sink=%s error=%v\n", name, err)
	}
}

// diagnosticFailureLine summarizes the failed diagnostic writes of the capture by sink, or returns "" when there were none.
func (p *processor) diagnosticFailureLine() string {
	if len(p.dev.diagnosticFailures) == 0 {
		return ""
	}
	names := make([]string, 0, len(p.dev.diagnosticFailures))
	for name := range p.dev.diagnosticFailures {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, p.dev.diagnosticFailures[name]))
	}
	return "diagnostic_output_errors " + strings.Join(parts, " ")
}

// writeBuffLifecycleRow writes one decoded Buff lifecycle row to the Buff event output (-buff-event-out) before the core
// observes it. The row becomes an interface value only when the sink exists, so a run without the output pays nothing.
func (p *processor) writeBuffLifecycleRow(row *buffLifecycleAuditRow) {
	if p.dev.buffLifecycle != nil {
		p.writeDiagnosticRow(p.dev.buffLifecycle, "buff-events", *row)
	}
}
