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
func (p *Processor) writeDiagnosticRow(sink io.Writer, name string, row any) {
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

// researchOutputErrorLines reports, for every research output the run wrote, the records that could not be decoded,
// encoded or written ("<output> errors=<n>"), so a failing research output is visible at the end of the run. The frame
// archive, schema decode and part reference outputs report theirs in their own summary lines.
func (p *Processor) researchOutputErrorLines() []string {
	var lines []string
	for _, output := range []struct {
		name   string
		sink   io.Writer
		errors int
	}{
		{"role_config", p.Dev.roleConfigOut, p.Dev.roleConfigErrors},
		{"shield_reference", p.Dev.shieldRefOut, p.Dev.shieldRefErrors},
		{"bullet_reference", p.Dev.bulletRefOut, p.Dev.bulletRefErrors},
		{"skill_reference", p.Dev.skillRefOut, p.Dev.skillRefErrors},
		{"client_push", p.Dev.clientPushOut, p.Dev.clientPushErrors},
		{"summon_owner", p.Dev.summonOwnerOut, p.Dev.summonOwnerErrors},
		{"damage_push", p.Dev.damagePushOut, p.Dev.damagePushErrors},
		{"hp_state", p.Dev.hpStateOut, p.Dev.hpStateErrors},
		{"combat_state", p.Dev.combatStateOut, p.Dev.combatStateErrors},
	} {
		if output.sink != nil {
			lines = append(lines, fmt.Sprintf("%s errors=%d", output.name, output.errors))
		}
	}
	return lines
}

// noteDiagnosticFailure counts one failed diagnostic write. The first failure of each sink is also written to the
// diagnostic stream, so a failing sink is visible without flooding the console.
func (p *Processor) noteDiagnosticFailure(name string, err error) {
	if p.Dev.diagnosticFailures == nil {
		p.Dev.diagnosticFailures = make(map[string]int)
	}
	p.Dev.diagnosticFailures[name]++
	if p.Dev.diagnosticFailures[name] == 1 && p.Diagnostic != nil {
		fmt.Fprintf(p.Diagnostic, "diagnostic_output_error sink=%s error=%v\n", name, err)
	}
}

// diagnosticFailureLine summarizes the failed diagnostic writes of the capture by sink, or returns "" when there were none.
func (p *Processor) diagnosticFailureLine() string {
	if len(p.Dev.diagnosticFailures) == 0 {
		return ""
	}
	names := make([]string, 0, len(p.Dev.diagnosticFailures))
	for name := range p.Dev.diagnosticFailures {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, p.Dev.diagnosticFailures[name]))
	}
	return "diagnostic_output_errors " + strings.Join(parts, " ")
}

// WriteBuffLifecycleRow writes one decoded Buff lifecycle row to the Buff event output (-buff-event-out) before the core
// observes it. The row becomes an interface value only when the sink exists, so a run without the output pays nothing.
func (p *Processor) WriteBuffLifecycleRow(row *BuffLifecycleAuditRow) {
	if p.Dev.buffLifecycle != nil {
		p.writeDiagnosticRow(p.Dev.buffLifecycle, "buff-events", withMessagePath(*row))
	}
}
