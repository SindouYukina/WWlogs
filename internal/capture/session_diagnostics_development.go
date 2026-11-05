//go:build wwdev

package capture

import (
	"fmt"
	"os"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// sessionDiagnostics records a live session's packets to the Development diagnostic PCAP in a new spool directory,
// through the same asynchronous pipeline, PCAP writer, and integrity record the CLI uses. The capture loop only
// queues records; a slow or failed writer never delays packet processing (diagnostic_pipeline_development.go).
type sessionDiagnostics struct {
	pipeline *diagnosticPipeline
	pcap     *diagnosticPCAP
	finished bool
}

func openSessionDiagnostics(requested bool, link layers.LinkType) (*sessionDiagnostics, error) {
	if !requested {
		return &sessionDiagnostics{finished: true}, nil
	}
	now := time.Now()
	bundle, err := defaultLiveCaptureOutputBundle(os.Getenv("LOCALAPPDATA"), now)
	if err != nil {
		return nil, fmt.Errorf("create diagnostic session directory: %w", err)
	}
	pipeline := newDiagnosticPipeline(true)
	pcapWriter, err := openDiagnosticPCAP(bundle.pcap, link, newDiagnosticRunID(now), pipeline, now, nil)
	if err != nil {
		pipeline.close(diagnosticFlushTimeout)
		return nil, fmt.Errorf("create diagnostic PCAP: %w", err)
	}
	return &sessionDiagnostics{pipeline: pipeline, pcap: pcapWriter}, nil
}

func (d *sessionDiagnostics) attach(p *processor) {
	if d.pipeline != nil {
		p.captureLayer.diagnosticDropCount = d.pipeline.droppedRecords
	}
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
	d.pipeline.finishCapture()
	d.pipeline.close(diagnosticFlushTimeout)
	record := d.pcap.finish(time.Now(), stopReason, kernelDropped)
	return record.line()
}
