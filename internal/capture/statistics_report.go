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
	EncounterState  EncounterState
	EncounterDPS    DPSResult
	Encounters      int
	ActiveEffects   int
	EffectApplies   uint64
	EffectRemoves   uint64
	EffectSnapshots EffectSnapshotStatus
	// Trust: transport delivery, local-player attribution context, the integrity of the headline encounter, and of
	// the interval totals. They are separate on purpose; none of them implies another.
	Transport          TransportState
	Context            ContextState
	EncounterIntegrity string
	DataIntegrity      IntegrityStatus
}

// currentStatisticsReport reads the statistics at the last observed capture frame time. Rolling windows
// are evaluated on the capture clock, which is the only clock their events were stamped with.
func (p *processor) currentStatisticsReport() statisticsReport {
	damage := p.damageStatisticsSnapshot()
	if observedAt := p.currentFrameTime; !observedAt.IsZero() {
		damage = p.damageStatisticsSnapshotAt(CaptureQueryTime{CaptureSessionID: damage.CaptureSessionID, Timestamp: observedAt})
	}
	effects := p.effectSnapshotAtLastFrame()
	trust := p.dataIntegritySnapshot(damage)
	encounterIntegrity := "none"
	if damage.Encounters.Current != nil {
		encounterIntegrity = string(damage.Encounters.Current.Integrity.Status)
	} else if n := len(damage.Encounters.Completed); n > 0 {
		encounterIntegrity = string(damage.Encounters.Completed[n-1].Integrity.Status)
	}
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
		EncounterState:  damage.Encounters.State,
		EncounterDPS:    damage.EncounterDPS,
		Encounters:      len(damage.Encounters.Completed) + int(damage.Encounters.CompletedDropped),
		ActiveEffects:   len(effects.Effects),
		EffectApplies:   effects.Counters.Applies,
		EffectRemoves:   effects.Counters.Removes,
		EffectSnapshots: effects.Status,
		Transport:       trust.Transport.State,
		Context:         trust.Context.State,
		DataIntegrity:   trust.Statistics.Status,

		EncounterIntegrity: encounterIntegrity,
	}
}

// String formats the report on one line with stable field names.
func (r statisticsReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "damage_dealt=%d damage_taken=%d outgoing_events=%d incoming_events=%d unattributed_events=%d players=%d",
		r.DamageDealt, r.DamageTaken, r.OutgoingEvents, r.IncomingEvents, r.Unattributed, r.Players)
	fmt.Fprintf(&b, " span_dps=%s rolling_5s_dps=%s rolling_10s_dps=%s", formatDPS(r.DamageSpanDPS), formatDPS(r.Rolling5sDPS), formatDPS(r.Rolling10sDPS))
	confidence := r.EncounterDPS.Confidence
	if confidence == "" {
		confidence = "none"
	}
	fmt.Fprintf(&b, " encounter_state=%s encounter_dps=%s encounter_confidence=%s encounters=%d",
		r.EncounterState, formatDPS(r.EncounterDPS), confidence, r.Encounters)
	fmt.Fprintf(&b, " active_effects=%d effect_applies=%d effect_removes=%d", r.ActiveEffects, r.EffectApplies, r.EffectRemoves)
	fmt.Fprintf(&b, " transport=%s context=%s encounter_integrity=%s data_integrity=%s",
		strings.ToLower(string(r.Transport)), strings.ToLower(string(r.Context)), strings.ToLower(r.EncounterIntegrity),
		strings.ToLower(string(r.DataIntegrity)))
	return b.String()
}

func formatDPS(result DPSResult) string {
	switch result.Status {
	case DPSStatusAvailable:
		return fmt.Sprintf("%.1f", result.Value)
	case DPSStatusLowerBound:
		// A lower bound is shown with its status so it cannot be read as a complete value.
		return fmt.Sprintf("lower_bound:%.1f", result.Value)
	}
	return string(result.Status)
}
