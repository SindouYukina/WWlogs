package capture

import (
	"fmt"
	"strings"
)

// statisticsReport is the console view of confirmed statistics. It carries aggregate values only:
// no raw Entity, Handle, or RPC identifiers and no per-event data, so it is safe to show to end users.
type statisticsReport struct {
	DamageDealt     int64
	DamageTaken     int64
	OutgoingEvents  uint64
	IncomingEvents  uint64
	Unattributed    uint64
	Players         int
	DamageSpanDPS   DPSResult
	Rolling5sDPS    DPSResult
	Rolling10sDPS   DPSResult
	ActiveEffects   int
	EffectApplies   uint64
	EffectRemoves   uint64
	EffectSnapshots EffectSnapshotStatus
}

// currentStatisticsReport reads the statistics at the last observed capture frame time. Rolling windows
// are evaluated on the capture clock, which is the only clock their events were stamped with.
func (p *processor) currentStatisticsReport() statisticsReport {
	damage := p.damageStatisticsSnapshot()
	if observedAt := p.currentFrameTime; !observedAt.IsZero() {
		damage = p.damageStatisticsSnapshotAt(CaptureQueryTime{CaptureSessionID: damage.CaptureSessionID, Timestamp: observedAt})
	}
	effects := p.effectSnapshotAtLastFrame()
	return statisticsReport{
		DamageDealt:     damage.DamageDealt,
		DamageTaken:     damage.DamageTaken,
		OutgoingEvents:  damage.OutgoingEvents,
		IncomingEvents:  damage.IncomingEvents,
		Unattributed:    damage.UnattributedOutgoingEvents + damage.UnattributedIncomingEvents,
		Players:         len(damage.ByPlayerEntity),
		DamageSpanDPS:   damage.DamageSpanDPS,
		Rolling5sDPS:    damage.Rolling5sDPS,
		Rolling10sDPS:   damage.Rolling10sDPS,
		ActiveEffects:   len(effects.Effects),
		EffectApplies:   effects.Counters.Applies,
		EffectRemoves:   effects.Counters.Removes,
		EffectSnapshots: effects.Status,
	}
}

// String formats the report on one line with stable field names.
func (r statisticsReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "damage_dealt=%d damage_taken=%d outgoing_events=%d incoming_events=%d unattributed_events=%d players=%d",
		r.DamageDealt, r.DamageTaken, r.OutgoingEvents, r.IncomingEvents, r.Unattributed, r.Players)
	fmt.Fprintf(&b, " span_dps=%s rolling_5s_dps=%s rolling_10s_dps=%s", formatDPS(r.DamageSpanDPS), formatDPS(r.Rolling5sDPS), formatDPS(r.Rolling10sDPS))
	fmt.Fprintf(&b, " active_effects=%d effect_applies=%d effect_removes=%d", r.ActiveEffects, r.EffectApplies, r.EffectRemoves)
	return b.String()
}

func formatDPS(result DPSResult) string {
	if result.Status != DPSStatusAvailable {
		return string(result.Status)
	}
	return fmt.Sprintf("%.1f", result.Value)
}
