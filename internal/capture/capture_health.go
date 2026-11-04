package capture

import (
	"strconv"
	"time"

	"github.com/google/gopacket/pcap"
)

// Loss is measured at separate layers, and the counts are never added together: a kernel drop is a packet Npcap could
// not hand over, a KCP gap is a missing sequence that may or may not have been one of those packets, and a dropped
// diagnostic record is lost output with no effect on the analysis.
//
//   - Npcap kernel counters (pcap_stats): packets received and dropped by the capture driver for this handle. Only a
//     live handle reports them; offline replay has none.
//   - Interface drops (ps_ifdrop): Npcap returns a field, but its meaning on Windows has not been verified, so it is
//     reported unavailable rather than as a number.
//   - Application drops: the capture loop reads packets one at a time with no packet queue, so it drops none by
//     design; the only application-side discard is the pending data of a KCP stream evicted from the bounded table.
//   - Diagnostic drops: development records discarded by a full or failed sink (development profile only).
//   - KCP gaps, recovery, and resynchronization: see TransportHealthSnapshot.

// Capture counter statuses.
const (
	CaptureCounterAvailable     = "available"
	CaptureCounterUnavailable   = "unavailable"
	CaptureCounterNotApplicable = "not_applicable"
)

// CaptureCounter is one loss counter with whether it can be trusted.
type CaptureCounter struct {
	Status string `json:"status"`
	Value  uint64 `json:"value"`
	Reason string `json:"reason,omitempty"`
}

// CaptureLayerHealth reports the capture-side counters.
type CaptureLayerHealth struct {
	Source             string         `json:"source"`
	PacketsRead        uint64         `json:"packetsRead"`
	KernelReceived     CaptureCounter `json:"kernelReceived"`
	KernelDropped      CaptureCounter `json:"kernelDropped"`
	InterfaceDropped   CaptureCounter `json:"interfaceDropped"`
	ApplicationDropped CaptureCounter `json:"applicationDropped"`
	DiagnosticDropped  CaptureCounter `json:"diagnosticDropped"`
	StatsSampledAt     time.Time      `json:"statsSampledAt,omitempty"`
}

// captureStatsSampleInterval spaces pcap_stats calls; the counters only need to be current to about a second.
const captureStatsSampleInterval = time.Second

type captureLayerState struct {
	live                bool
	packetsRead         uint64
	statsKnown          bool
	statsReason         string
	received, dropped   uint64
	sampledAt           time.Time
	evictedSegments     uint64
	diagnosticDropCount func() uint64
}

func (p *processor) observeCaptureSource(live bool) {
	p.captureLayer.live = live
	if !live {
		p.captureLayer.statsReason = "offline_replay_has_no_driver_counters"
	}
}

func (p *processor) observePacketRead() {
	p.captureLayer.packetsRead++
}

// sampleCaptureStats reads the Npcap counters at most once per captureStatsSampleInterval of wall-clock time.
func (p *processor) sampleCaptureStats(handle *pcap.Handle, now time.Time, force bool) {
	if !p.captureLayer.live || handle == nil {
		return
	}
	if !force && !p.captureLayer.sampledAt.IsZero() && now.Sub(p.captureLayer.sampledAt) < captureStatsSampleInterval {
		return
	}
	p.captureLayer.sampledAt = now
	stats, err := handle.Stats()
	if err != nil || stats == nil {
		p.captureLayer.statsKnown, p.captureLayer.statsReason = false, "pcap_stats_failed"
		return
	}
	p.captureLayer.statsKnown, p.captureLayer.statsReason = true, ""
	p.captureLayer.received, p.captureLayer.dropped = uint64(stats.PacketsReceived), uint64(stats.PacketsDropped)
}

func (p *processor) captureLayerHealth() CaptureLayerHealth {
	state := p.captureLayer
	health := CaptureLayerHealth{
		Source: "offline", PacketsRead: state.packetsRead,
		InterfaceDropped:   CaptureCounter{Status: CaptureCounterUnavailable, Reason: "interface_drop_semantics_not_verified"},
		ApplicationDropped: CaptureCounter{Status: CaptureCounterAvailable, Value: state.evictedSegments},
		DiagnosticDropped:  CaptureCounter{Status: CaptureCounterNotApplicable, Reason: "no_diagnostic_output_in_this_build"},
		StatsSampledAt:     state.sampledAt,
	}
	if state.live {
		health.Source = "live"
	}
	if state.statsKnown {
		health.KernelReceived = CaptureCounter{Status: CaptureCounterAvailable, Value: state.received}
		health.KernelDropped = CaptureCounter{Status: CaptureCounterAvailable, Value: state.dropped}
	} else {
		reason := state.statsReason
		if reason == "" {
			reason = "not_sampled_yet"
		}
		health.KernelReceived = CaptureCounter{Status: CaptureCounterUnavailable, Reason: reason}
		health.KernelDropped = CaptureCounter{Status: CaptureCounterUnavailable, Reason: reason}
	}
	if state.diagnosticDropCount != nil {
		health.DiagnosticDropped = CaptureCounter{Status: CaptureCounterAvailable, Value: state.diagnosticDropCount()}
	}
	return health
}

func formatCaptureCounter(counter CaptureCounter) string {
	if counter.Status != CaptureCounterAvailable {
		return counter.Status
	}
	return strconv.FormatUint(counter.Value, 10)
}
