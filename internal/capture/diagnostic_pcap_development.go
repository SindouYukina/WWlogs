//go:build wwdev

package capture

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"

	"wwlogs/internal/buildinfo"
)

// The development PCAP may lose records by design (a full queue drops instead of blocking capture), so every PCAP has a
// companion integrity record, <pcap>.integrity.json, that says whether the file holds everything it should. The PCAP
// format itself is unchanged.
//
// The record is written as in_progress when the PCAP is created and replaced when the run stops. A file still marked
// in_progress therefore belongs to a run that did not stop normally and must not be treated as complete. Each run has
// its own run ID; reusing a path overwrites the earlier record at creation, so a later run never inherits an earlier
// run's state.
//
// complete guarantees: every packet that the capture loop received from Npcap after the BPF filter, from the start of
// this run until it stopped, was written to the file in arrival order; no record was dropped, no write failed, the
// writer drained before the stop deadline, and the file size equals the header plus every record. It does not cover
// packets Npcap dropped before delivery (kernelDropped, recorded separately), packets outside the filter, or traffic
// before the run started or after it stopped.

const diagnosticPCAPIntegritySchemaVersion = 1

// PCAP integrity states.
const (
	pcapIntegrityInProgress = "in_progress"
	pcapIntegrityComplete   = "complete"
	pcapIntegrityIncomplete = "incomplete"
)

const pcapIntegrityScope = "complete: every packet the capture loop received from Npcap after the BPF filter, from the start of this run until it stopped, " +
	"was written in arrival order with no dropped record, no write error, no unfinished flush, and a matching file size; " +
	"it does not cover packets Npcap dropped before delivery (kernelDropped), packets outside the filter, or traffic outside the run"

type diagnosticPCAPIntegrity struct {
	SchemaVersion     int              `json:"schemaVersion"`
	RunID             string           `json:"runId"`
	PCAPFile          string           `json:"pcapFile"`
	State             string           `json:"state"`
	IncompleteReasons []string         `json:"incompleteReasons,omitempty"`
	Scope             string           `json:"scope"`
	Build             buildinfo.Record `json:"build"`
	StartedAt         time.Time        `json:"startedAt"`
	FinishedAt        time.Time        `json:"finishedAt,omitempty"`
	StopReason        string           `json:"stopReason,omitempty"`
	PacketsOffered    uint64           `json:"packetsOffered"`
	RecordsWritten    uint64           `json:"recordsWritten"`
	RecordsDropped    uint64           `json:"recordsDropped"`
	RecordsUnwritten  uint64           `json:"recordsUnwritten"`
	WriterError       string           `json:"writerError,omitempty"`
	Shutdown          string           `json:"shutdown,omitempty"`
	ExpectedBytes     uint64           `json:"expectedBytes"`
	FileBytes         int64            `json:"fileBytes"`
	KernelDropped     CaptureCounter   `json:"kernelDropped"`
}

// newDiagnosticRunID returns a run identifier unique to this process run.
func newDiagnosticRunID(now time.Time) string {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("%s-%d", now.UTC().Format("20060102T150405.000000000Z"), os.Getpid())
	}
	return fmt.Sprintf("%s-%d-%s", now.UTC().Format("20060102T150405Z"), os.Getpid(), hex.EncodeToString(random[:]))
}

// diagnosticPCAP is the development PCAP of one run.
type diagnosticPCAP struct {
	path       string
	recordPath string
	file       *os.File
	sink       *asyncDiagnosticWriter
	pipeline   *diagnosticPipeline
	integrity  diagnosticPCAPIntegrity
	offered    uint64
	headerSize uint64
}

func pcapIntegrityPath(pcapPath string) string {
	return pcapPath + ".integrity.json"
}

// openDiagnosticPCAP creates the PCAP, writes its header synchronously, records the run as in_progress, and puts packet
// records behind the pipeline. wrapTarget, when not nil, wraps the file as the sink target for fault injection.
func openDiagnosticPCAP(path string, link layers.LinkType, runID string, pipeline *diagnosticPipeline, now time.Time, wrapTarget func(io.Writer) io.Writer) (*diagnosticPCAP, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create PCAP directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create PCAP: %w", err)
	}
	if err := pcapgo.NewWriter(file).WriteFileHeader(liveCaptureSnapLen, link); err != nil {
		file.Close()
		return nil, fmt.Errorf("write PCAP header: %w", err)
	}
	d := &diagnosticPCAP{
		path: path, recordPath: pcapIntegrityPath(path), file: file, pipeline: pipeline, headerSize: 24,
		integrity: diagnosticPCAPIntegrity{
			SchemaVersion: diagnosticPCAPIntegritySchemaVersion, RunID: runID, PCAPFile: filepath.Base(path),
			State: pcapIntegrityInProgress, Scope: pcapIntegrityScope, Build: buildinfo.CurrentRecord(), StartedAt: now,
		},
	}
	if err := d.writeRecord(); err != nil {
		file.Close()
		return nil, err
	}
	var target io.Writer = file
	if wrapTarget != nil {
		target = wrapTarget(file)
	}
	d.sink = pipeline.wrap("pcap", target, nil, diagnosticPCAPCapacityBytes).(*asyncDiagnosticWriter)
	return d, nil
}

// writePacket queues one packet record. It never blocks the capture loop in live mode.
func (d *diagnosticPCAP) writePacket(info gopacket.CaptureInfo, data []byte) {
	d.offered++
	_, _ = d.sink.Write(pcapPacketRecord(info, data))
}

// finish is called after the pipeline has been closed. It closes the file if its writer released it, checks the
// file size, and replaces the in_progress record with the final state.
func (d *diagnosticPCAP) finish(now time.Time, stopReason string, kernelDropped CaptureCounter) diagnosticPCAPIntegrity {
	stats := d.sink.snapshot()
	record := &d.integrity
	record.FinishedAt, record.StopReason, record.KernelDropped = now, stopReason, kernelDropped
	record.PacketsOffered, record.RecordsWritten, record.RecordsDropped, record.RecordsUnwritten = d.offered, stats.Records, stats.Dropped, stats.Unwritten
	record.WriterError, record.Shutdown = stats.FirstError, stats.shutdown()
	record.ExpectedBytes = d.headerSize + stats.Bytes
	var reasons []string
	if stats.Dropped > 0 {
		reasons = append(reasons, "records_dropped")
	}
	if stats.WriteErrors > 0 {
		reasons = append(reasons, "writer_error")
	}
	if stats.FlushTimedOut || stats.Unwritten > 0 {
		reasons = append(reasons, "flush_timed_out")
	}
	if stats.Records+stats.Dropped+stats.Unwritten != d.offered {
		reasons = append(reasons, "record_count_mismatch")
	}
	if d.pipeline.released(d.sink.target) {
		// The writer has exited, so the file is ours again.
		if err := d.file.Sync(); err != nil {
			reasons = append(reasons, "sync_failed")
		}
		if err := d.file.Close(); err != nil {
			reasons = append(reasons, "close_failed")
		}
		if info, err := os.Stat(d.path); err == nil {
			record.FileBytes = info.Size()
			if uint64(info.Size()) != record.ExpectedBytes {
				reasons = append(reasons, "file_size_mismatch")
			}
		} else {
			record.FileBytes = -1
			reasons = append(reasons, "file_unreadable")
		}
	} else {
		// An abandoned writer may still be inside a write: the file is neither touched nor measured here. The writer
		// closes it itself once that call returns.
		record.FileBytes = -1
		reasons = append(reasons, "writer_still_running")
		file := d.file
		d.sink.whenReleased(func() { file.Close() })
	}
	record.IncompleteReasons = reasons
	if len(reasons) == 0 {
		record.State = pcapIntegrityComplete
	} else {
		record.State = pcapIntegrityIncomplete
	}
	if err := d.writeRecord(); err != nil {
		fmt.Fprintf(os.Stderr, "pcap_integrity_write_error=%v\n", err)
	}
	return *record
}

// writeRecord replaces the integrity file atomically.
func (d *diagnosticPCAP) writeRecord() error {
	data, err := json.MarshalIndent(d.integrity, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal PCAP integrity: %w", err)
	}
	temporary := d.recordPath + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write PCAP integrity: %w", err)
	}
	if err := os.Rename(temporary, d.recordPath); err != nil {
		return fmt.Errorf("replace PCAP integrity: %w", err)
	}
	return nil
}

func (r diagnosticPCAPIntegrity) line() string {
	return fmt.Sprintf("pcap_integrity run_id=%s state=%s reasons=%v offered=%d written=%d dropped=%d unwritten=%d shutdown=%s",
		r.RunID, r.State, r.IncompleteReasons, r.PacketsOffered, r.RecordsWritten, r.RecordsDropped, r.RecordsUnwritten, r.Shutdown)
}
