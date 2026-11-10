//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"wwlogs/internal/buildinfo"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// sessionDiagnostics records a live session's packets to the Development diagnostic PCAP in a new spool directory,
// through the same asynchronous pipeline, PCAP writer, and integrity record the CLI uses. The capture loop only
// queues records; a slow or failed writer never delays packet processing (diagnostic_pipeline_development.go).
type sessionDiagnostics struct {
	pipeline      *diagnosticPipeline
	pcap          *diagnosticPCAP
	finished      bool
	directory     string
	outputs       map[string]io.Writer
	files         map[*asyncDiagnosticWriter]*os.File
	release       func()
	processor     *processor
	effectDisplay *effectDisplayAuditWriter
}

func openSessionDiagnostics(requested bool, link layers.LinkType) (*sessionDiagnostics, error) {
	d, err := openDevelopmentSessionDiagnostics(requested, link)
	if err != nil {
		return nil, &diagnosticSetupError{err: err}
	}
	return d, nil
}

func openDevelopmentSessionDiagnostics(requested bool, link layers.LinkType) (*sessionDiagnostics, error) {
	if !requested {
		return &sessionDiagnostics{finished: true}, nil
	}
	now := time.Now()
	bundle, err := defaultLiveCaptureOutputBundle(os.Getenv("LOCALAPPDATA"), now)
	if err != nil {
		return nil, fmt.Errorf("create diagnostic session directory: %w", err)
	}
	release, err := reserveDiagnosticStorage(bundle.directory)
	if err != nil {
		_ = os.Remove(bundle.directory)
		return nil, err
	}
	pipeline := newDiagnosticPipeline(true)
	budget := &diagnosticByteBudget{remaining: diagnosticSessionBytes - 24}
	pcapWriter, err := openDiagnosticPCAP(bundle.pcap, link, newDiagnosticRunID(now), pipeline, now, func(w io.Writer) io.Writer { return &budgetedDiagnosticWriter{target: w, budget: budget} })
	if err != nil {
		pipeline.close(diagnosticFlushTimeout)
		release()
		return nil, fmt.Errorf("create diagnostic PCAP: %w", err)
	}
	d := &sessionDiagnostics{pipeline: pipeline, pcap: pcapWriter, directory: bundle.directory, release: release, outputs: make(map[string]io.Writer), files: make(map[*asyncDiagnosticWriter]*os.File)}
	for _, name := range []string{"console.log", "combat-events.jsonl", "buff-events.jsonl", "buff-snapshots.jsonl", "encounter-events.jsonl", "dps-effects.jsonl", "effect-display.jsonl", "effect-state-final.json"} {
		file, err := os.OpenFile(filepath.Join(bundle.directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			d.finish(StopReasonError, CaptureCounter{})
			return nil, fmt.Errorf("create diagnostic output: %w", err)
		}
		target := &budgetedDiagnosticWriter{target: file, budget: budget}
		sink := pipeline.wrap(name, target, nil, diagnosticSinkCapacityBytes).(*asyncDiagnosticWriter)
		d.outputs[name] = sink
		d.files[sink] = file
	}
	return d, nil
}

func (d *sessionDiagnostics) attach(p *processor) {
	if d.pipeline != nil {
		p.captureLayer.diagnosticDropCount = d.pipeline.droppedRecords
		d.processor = p
		p.stdout, p.diagnostic = d.outputs["console.log"], d.outputs["console.log"]
		p.combatEventsJSONL = d.outputs["combat-events.jsonl"]
		p.buffLifecycle, p.buffSnapshots = d.outputs["buff-events.jsonl"], d.outputs["buff-snapshots.jsonl"]
		p.encounterOut = d.outputs["encounter-events.jsonl"]
		d.effectDisplay = &effectDisplayAuditWriter{target: d.outputs["effect-display.jsonl"]}
	}
}

// observeEffectDisplay records why the Effects Overlay shows what it shows (effect_display_audit_development.go).
func (d *sessionDiagnostics) observeEffectDisplay(p *processor, now time.Time) {
	d.effectDisplay.observe(p, now)
}

func (d *sessionDiagnostics) packet(info gopacket.CaptureInfo, data []byte) {
	if d.pcap != nil {
		d.pcap.writePacket(info, data)
	}
}

// finish drains and closes the writers within the shared deadline and finalizes the PCAP integrity record. It runs
// once; later calls return an empty summary.
func (d *sessionDiagnostics) finish(stopReason string, kernelDropped CaptureCounter) string {
	if d.finished {
		return ""
	}
	d.finished = true
	if d.processor != nil {
		snapshot := d.processor.liveDiagnosticSnapshot(time.Now())
		snapshot.Final = true
		_ = writeLiveDiagnosticSnapshot(d.outputs["dps-effects.jsonl"], snapshot)
		_ = d.processor.writeEffectState(d.outputs["effect-state-final.json"])
	}
	d.pipeline.finishCapture()
	d.pipeline.close(diagnosticFlushTimeout)
	record := d.pcap.finish(time.Now(), stopReason, kernelDropped)
	allReleased := d.pipeline.released(d.pcap.sink.target)
	for sink, file := range d.files {
		if !sink.exited() {
			allReleased = false
			closedFile := file
			sink.whenReleased(func() { closedFile.Close() })
		} else {
			_ = file.Sync()
			_ = file.Close()
		}
	}
	stats := d.pipeline.stats()
	status := struct {
		Build      buildinfo.Record        `json:"build"`
		State      string                  `json:"state"`
		StopReason string                  `json:"stopReason"`
		PCAP       diagnosticPCAPIntegrity `json:"pcap"`
		Sinks      []diagnosticSinkStats   `json:"sinks"`
	}{buildinfo.CurrentRecord(), "closed", stopReason, record, stats}
	data, _ := json.MarshalIndent(status, "", "  ")
	statusErr := os.WriteFile(filepath.Join(d.directory, "status.json"), data, 0o600)
	if allReleased && d.release != nil {
		d.release()
	}
	summary := record.line() + "; " + diagnosticPipelineLine(record.RunID, stats)
	if statusErr != nil {
		summary += "; diagnostic status could not be saved"
	}
	return summary
}
