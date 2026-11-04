package capture

import (
	"fmt"
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
	// DeliveryDelay is loop receive time minus the Npcap timestamp; ProcessedDelay is the time decoding of the packet
	// finished minus the Npcap timestamp. Both use the host clock and exist only for live capture.
	DeliveryDelay  CaptureLatency `json:"deliveryDelay"`
	ProcessedDelay CaptureLatency `json:"processedDelay"`
}

// CaptureLatency summarizes a delay histogram. Percentiles are bucket upper bounds (a value of 20 means at most
// 20 ms), so they never understate the delay.
type CaptureLatency struct {
	Status  string  `json:"status"`
	Samples uint64  `json:"samples"`
	P50ms   float64 `json:"p50AtMostMs"`
	P95ms   float64 `json:"p95AtMostMs"`
	P99ms   float64 `json:"p99AtMostMs"`
	MaxMs   float64 `json:"maxMs"`
}

// captureLatencyBounds are the histogram bucket upper bounds; the last bucket holds everything above 5 s.
var captureLatencyBounds = [...]time.Duration{
	time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond,
	50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond, time.Second,
	2 * time.Second, 5 * time.Second,
}

// latencyHistogram is a fixed-size histogram: recording a packet allocates nothing.
type latencyHistogram struct {
	counts [len(captureLatencyBounds) + 1]uint64
	total  uint64
	max    time.Duration
}

func (h *latencyHistogram) observe(delay time.Duration) {
	if delay < 0 {
		// The two timestamps come from different host clock reads; a tiny negative value means "no delay".
		delay = 0
	}
	index := len(captureLatencyBounds)
	for i, bound := range captureLatencyBounds {
		if delay <= bound {
			index = i
			break
		}
	}
	h.counts[index]++
	h.total++
	if delay > h.max {
		h.max = delay
	}
}

func (h latencyHistogram) summary(live bool) CaptureLatency {
	if !live {
		return CaptureLatency{Status: CaptureCounterNotApplicable}
	}
	if h.total == 0 {
		return CaptureLatency{Status: CaptureCounterUnavailable}
	}
	percentile := func(p float64) float64 {
		rank := uint64(p*float64(h.total) + 0.999999)
		var seen uint64
		for i, count := range h.counts {
			seen += count
			if seen >= rank {
				if i < len(captureLatencyBounds) {
					return float64(captureLatencyBounds[i]) / float64(time.Millisecond)
				}
				return float64(h.max) / float64(time.Millisecond)
			}
		}
		return float64(h.max) / float64(time.Millisecond)
	}
	return CaptureLatency{Status: CaptureCounterAvailable, Samples: h.total, P50ms: percentile(0.50), P95ms: percentile(0.95),
		P99ms: percentile(0.99), MaxMs: float64(h.max) / float64(time.Millisecond)}
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
	delivery, processed latencyHistogram
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

// observePacketTiming records, for live capture only, how long a packet waited between its Npcap timestamp and the
// loop (received) and until its decoding finished (processed). Replay timestamps are historical and are not used.
func (p *processor) observePacketTiming(captured, received, processed time.Time) {
	if !p.captureLayer.live || captured.IsZero() {
		return
	}
	p.captureLayer.delivery.observe(received.Sub(captured))
	if !processed.IsZero() {
		p.captureLayer.processed.observe(processed.Sub(captured))
	}
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
	health.DeliveryDelay = state.delivery.summary(state.live)
	health.ProcessedDelay = state.processed.summary(state.live)
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

// captureLatencyLine formats both delay summaries for development diagnostics.
func captureLatencyLine(health CaptureLayerHealth) string {
	format := func(latency CaptureLatency) string {
		if latency.Status != CaptureCounterAvailable {
			return latency.Status
		}
		return fmt.Sprintf("samples:%d/p50<=%gms/p95<=%gms/p99<=%gms/max:%.1fms", latency.Samples, latency.P50ms, latency.P95ms, latency.P99ms, latency.MaxMs)
	}
	return fmt.Sprintf("capture_latency source=%s delivery=%s processed=%s", health.Source, format(health.DeliveryDelay), format(health.ProcessedDelay))
}
