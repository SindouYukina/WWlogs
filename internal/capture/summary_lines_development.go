//go:build wwdev

package capture

import (
	"fmt"
	"time"
)

// protocolReport is the Development census and diagnostic view of the protocol sessions of a capture.
type protocolReport struct {
	Status            string `json:"status"`
	GameVersion       string `json:"gameVersion,omitempty"`
	PendingFrames     uint64 `json:"pendingFrames"`
	UnsupportedFrames uint64 `json:"unsupportedFrames"`
}

func protocolReportOf(s *protocolSession) protocolReport {
	status, version := s.status()
	return protocolReport{Status: status, GameVersion: version, PendingFrames: s.counters.pendingFrames, UnsupportedFrames: s.counters.unsupportedFrames}
}

// line formats the report for Development diagnostics. It carries counts and the reported major.minor only.
func (r protocolReport) line() string {
	version := r.GameVersion
	if version == "" {
		version = "none"
	}
	return fmt.Sprintf("protocol_session status=%s game_version=%s pending_frames=%d unsupported_frames=%d", r.Status, version, r.PendingFrames, r.UnsupportedFrames)
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

// damageNotifyLine formats the notified-result counters for development diagnostics.
func damageNotifyLine(s damageNotifyStats, sequenceGaps int) string {
	return fmt.Sprintf("damage_notify notifies=%d no_amount=%d malformed=%d keyless=%d negative_status=%d negative_status_local=%d credited_trigger=%d credited_owner=%d uncredited=%d status_triggers=%d status_triggers_forcing_nothing=%d settled_by_application=%d settled_across_targets=%d instant_status_executions=%d excluded_status_instances=%d server_sequence_gaps=%d",
		s.notifies, s.noAmount, s.malformed, s.keyless, s.negativeStatus, s.negativeStatusLocal, s.creditedTrigger, s.creditedOwner, s.uncredited, s.statusTriggers,
		s.statusTriggersForcingNothing, s.settledByApplication, s.settledAcrossTargets, s.instantStatusExecutions, s.excludedStatusInstances, sequenceGaps)
}

// rpcPairingLine formats the pairing counters for development diagnostics.
func rpcPairingLine(s RPCPairingSnapshot) string {
	return fmt.Sprintf("damage_rpc_pairing requests=%d responses=%d standalone_responses=%d paired=%d paired_out_of_order=%d max_out_of_order_wait_ms=%.1f unpaired_expired=%d unpaired_evicted=%d unpaired_session_change=%d unpaired_capture_end=%d rewound_entities=%d duplicate_responses=%d conflicting_responses=%d invalid_responses=%d pending_requests=%d evicted_requests=%d held_responses=%d reused_request_ids=%d superseded_requests=%d",
		s.Requests, s.Responses, s.StandaloneResponses, s.Paired, s.PairedOutOfOrder, s.MaxOutOfOrderWaitMs, s.UnpairedExpired, s.UnpairedEvicted,
		s.UnpairedSessionChange, s.UnpairedCaptureEnd, s.RewoundEntities, s.DuplicateResponses, s.ConflictingResponses, s.InvalidResponses, s.PendingRequests, s.EvictedRequests, s.HeldResponses,
		s.ReusedRequestIDs, s.SupersededRequests)
}

// kcpTransportHealth summarizes KCP delivery at one moment.
type kcpTransportHealth struct {
	Streams         int
	OpenGaps        int
	Recovering      int
	PendingSegments int
	PendingBytes    int
	OldestGapSince  time.Time
}

func summarizeKCPTransport(streams map[string]*stream) kcpTransportHealth {
	var health kcpTransportHealth
	for _, state := range streams {
		health.Streams++
		health.PendingSegments += len(state.pending)
		health.PendingBytes += state.pendingBytes
		if state.recovery.active {
			health.Recovering++
		}
		if !state.gapOpen {
			continue
		}
		health.OpenGaps++
		if health.OldestGapSince.IsZero() || state.gapSince.Before(health.OldestGapSince) {
			health.OldestGapSince = state.gapSince
		}
	}
	return health
}

// transportHealthLine formats KCP delivery counters for development diagnostics. It carries counts and one capture
// time only: no conversation IDs, sequences, or payload bytes.
func transportHealthLine(streams map[string]*stream, stats captureStats) string {
	health := summarizeKCPTransport(streams)
	oldest := "none"
	if !health.OldestGapSince.IsZero() {
		oldest = health.OldestGapSince.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("transport_health kcp_streams=%d kcp_open_gaps=%d kcp_oldest_open_gap=%s kcp_pending_segments=%d kcp_pending_bytes=%d kcp_pending_peak=%d kcp_pending_overflow=%d kcp_out_of_order=%d kcp_duplicates=%d kcp_gaps_opened=%d kcp_gaps_filled=%d kcp_max_gap_wait=%s kcp_gaps_skipped=%d kcp_segments_skipped=%d kcp_fragment_resets=%d kcp_fragment_tail_drops=%d kcp_fragments_discarded=%d kcp_streams_evicted=%d kcp_streams_retired=%d kcp_retired_pending_segments=%d kcp_retired_stragglers=%d frame_desyncs=%d resync_attempts=%d resync_successes=%d resync_failures=%d resync_recovering=%d resync_bytes_discarded=%d resync_candidates=%d resync_budget_stops=%d",
		health.Streams, health.OpenGaps, oldest, health.PendingSegments, health.PendingBytes, stats.kcpPendingPeak, stats.kcpPendingOverflow,
		stats.kcpOutOfOrder, stats.kcpDuplicates, stats.kcpGapsOpened, stats.kcpGapsFilled, stats.kcpMaxGapWait,
		stats.kcpGapsSkipped, stats.kcpSegmentsSkipped, stats.kcpFragmentResets, stats.kcpFragmentTailDrops, stats.kcpFragmentsDiscarded,
		stats.kcpStreamsEvicted, stats.kcpStreamsRetired, stats.kcpRetiredPendingSegments, stats.kcpRetiredStragglers, stats.frameDesyncs, stats.resyncAttempts, stats.resyncSuccesses, stats.resyncFailures, health.Recovering,
		stats.resyncBytesDiscarded, stats.resyncCandidatesChecked, stats.resyncBudgetStops)
}
